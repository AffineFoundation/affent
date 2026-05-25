package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	maxBrowserFindQueryBytes = 256
	defaultBrowserFindLimit  = 8
	maxBrowserFindLimit      = 25
	maxBrowserFindSnippet    = 220
)

// FindTool returns `browser_find`. It searches the current rendered
// DOM directly and returns compact matching snippets, so the agent can
// look for labels like "market cap" or "price" without repeated
// scroll/snapshot calls. Unlike browser_snapshot, it is not limited by
// the formatted snapshot's text-block display cap.
func FindTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["query"],
        "properties": {
            "query": {
                "type": "string",
                "minLength": 1,
                "maxLength": %d,
                "description": "Case-insensitive text to find on the current rendered page."
            },
            "max_results": {
                "type": "integer",
                "minimum": 1,
                "maximum": %d,
                "default": %d,
                "description": "Maximum matching snippets to return."
            }
        }
    }`, maxBrowserFindQueryBytes, maxBrowserFindLimit, defaultBrowserFindLimit))
	return &agent.Tool{
		Name:        "browser_find",
		Description: "Search the current rendered page for text and return compact matching snippets plus link refs. Use before repeated scrolling when looking for specific facts such as price, market cap, docs links, dates, or names.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Query      string `json:"query"`
				MaxResults int    `json:"max_results"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_find with only documented fields: query and max_results"); err != nil {
				return "", err
			}
			query := strings.TrimSpace(args.Query)
			if query == "" {
				return "", browserInvalidArgs("query is required", "retry browser_find with a visible word or label from the current page")
			}
			if len(query) > maxBrowserFindQueryBytes {
				return "", browserInvalidArgs(fmt.Sprintf("query is %d bytes; browser_find supports queries up to %d bytes", len(query), maxBrowserFindQueryBytes), "retry with a shorter distinctive phrase")
			}
			limit := args.MaxResults
			if limit <= 0 {
				limit = defaultBrowserFindLimit
			}
			if limit > maxBrowserFindLimit {
				return "", browserInvalidArgs(fmt.Sprintf("max_results must be between 1 and %d", maxBrowserFindLimit), "omit max_results to use the default, or retry with a smaller value")
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			result, err := s.FindText(ctx, query, limit)
			if err != nil {
				return "", fmt.Errorf("find: %w", err)
			}
			return formatBrowserFindResults(result, query, limit), nil
		},
	}
}

type BrowserFindResult struct {
	SnapshotID  int64                `json:"snapshot_id"`
	URL         string               `json:"url"`
	Title       string               `json:"title"`
	Interactive []InteractiveElement `json:"interactive"`
	TextBlocks  []TextBlock          `json:"text_blocks"`
}

// FindText searches the rendered DOM and stamps fresh data-affent-ref
// ids on interactive elements. Those refs remain valid until the next
// snapshot/find/navigation changes them.
func (s *Session) FindText(ctx context.Context, query string, limit int) (*BrowserFindResult, error) {
	if s.page == nil {
		return nil, ErrNoPage
	}
	if limit <= 0 {
		limit = defaultBrowserFindLimit
	}
	page := s.withContext(ctx)
	result, err := page.Eval(browserFindJS(query, limit))
	if err != nil {
		return nil, fmt.Errorf("eval find js: %w", err)
	}
	raw, err := result.Value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("re-marshal find result: %w", err)
	}
	var out BrowserFindResult
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("decode find result: %w (raw=%s)", err, string(raw))
	}
	out.SnapshotID = s.newSnapshotID()
	return &out, nil
}

func browserFindJS(query string, limit int) string {
	rawQuery, _ := json.Marshal(query)
	return fmt.Sprintf(`() => {
  const query = %s;
  const maxResults = %d;
  document.querySelectorAll('[data-affent-ref]').forEach(el => el.removeAttribute('data-affent-ref'));

  function clean(s) {
    return (s || '').toString().trim().replace(/\s+/g, ' ');
  }
  function norm(s) {
    return clean(s).toLowerCase();
  }
  function isVisible(el) {
    if (!el || !el.getBoundingClientRect) return false;
    const rect = el.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return false;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') return false;
    if (parseFloat(cs.opacity || '1') === 0) return false;
    return true;
  }
  function accessibleName(el) {
    const ariaLabel = el.getAttribute && el.getAttribute('aria-label');
    if (ariaLabel) return clean(ariaLabel).slice(0, 200);
    const ariaLabelledBy = el.getAttribute && el.getAttribute('aria-labelledby');
    if (ariaLabelledBy) {
      const ref = document.getElementById(ariaLabelledBy);
      if (ref) return clean(ref.textContent).slice(0, 200);
    }
    const alt = el.getAttribute && el.getAttribute('alt');
    if (alt) return clean(alt).slice(0, 200);
    const title = el.getAttribute && el.getAttribute('title');
    if (title) return clean(title).slice(0, 200);
    if (el.tagName === 'INPUT' && el.placeholder) return clean(el.placeholder).slice(0, 200);
    return clean(el.textContent).slice(0, 200);
  }
  function roleOf(el) {
    const explicit = el.getAttribute && el.getAttribute('role');
    if (explicit) return explicit;
    const tag = el.tagName.toLowerCase();
    if (tag === 'a' && el.hasAttribute && el.hasAttribute('href')) return 'link';
    if (tag === 'button') return 'button';
    if (tag === 'input') {
      const t = ((el.type || 'text') + '').toLowerCase();
      if (t === 'checkbox') return 'checkbox';
      if (t === 'radio') return 'radio';
      if (t === 'submit' || t === 'button' || t === 'reset') return 'button';
      return 'textbox';
    }
    if (tag === 'textarea') return 'textbox';
    if (tag === 'select') return 'combobox';
    if (tag === 'summary') return 'button';
    if (el.isContentEditable) return 'textbox';
    return tag;
  }
  function directText(el) {
    let out = '';
    for (const node of el.childNodes) {
      if (node.nodeType === 3) out += node.textContent;
    }
    return clean(out);
  }
  function visibleText(el) {
    if (!el) return '';
    const parts = [];
    for (const node of el.childNodes) {
      if (node.nodeType === 3) {
        const text = clean(node.textContent);
        if (text) parts.push(text);
        continue;
      }
      if (node.nodeType !== 1) continue;
      const child = node;
      if (!isVisible(child)) continue;
      const text = visibleText(child);
      if (text) parts.push(text);
    }
    return clean(parts.join(' '));
  }
  function contextualText(el, fallback) {
    let best = clean(fallback);
    for (let cur = el, depth = 0; cur && cur !== document.body && depth < 4; cur = cur.parentElement, depth++) {
      const full = visibleText(cur);
      if (!full || !norm(full).includes(needle)) continue;
      if (full.length <= 600 && full.length > best.length) {
        best = full;
      }
    }
    return best;
  }
  function around(text) {
    text = clean(text);
    const lower = text.toLowerCase();
    const needle = norm(query);
    const max = 600;
    if (text.length <= max) return text;
    const idx = lower.indexOf(needle);
    if (idx < 0) return text.slice(0, max);
    let start = idx - Math.floor((max - needle.length) / 2);
    if (start < 0) start = 0;
    if (start + max > text.length) start = Math.max(0, text.length - max);
    const end = Math.min(text.length, start + max);
    return (start > 0 ? '... ' : '') + text.slice(start, end).trim() + (end < text.length ? ' ...' : '');
  }

  const needle = norm(query);
  const interactive = [];
  const textBlocks = [];
  const interactiveSelectors = [
    'a[href]', 'button', 'summary',
    'input:not([type=hidden])', 'textarea', 'select',
    '[role=button]', '[role=link]', '[role=menuitem]', '[role=tab]',
    '[role=checkbox]', '[role=switch]', '[role=combobox]', '[role=textbox]',
    '[role=radio]', '[role=option]',
    '[contenteditable]:not([contenteditable=false])',
    '[onclick]',
    '[tabindex]:not([tabindex="-1"])',
  ].join(',');
  let nextRef = 0;
  document.querySelectorAll(interactiveSelectors).forEach(el => {
    if (!isVisible(el)) return;
    nextRef++;
    el.setAttribute('data-affent-ref', String(nextRef));
    if (interactive.length >= maxResults) return;
    const info = { ref: nextRef, role: roleOf(el), name: accessibleName(el) };
    if (el.tagName === 'A' && el.getAttribute('href')) info.href = el.getAttribute('href');
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT') {
      info.value = ((el.value || '') + '').slice(0, 200);
    }
    if (el.tagName === 'INPUT' && (el.type === 'checkbox' || el.type === 'radio')) {
      info.checked = !!el.checked;
    }
    if (norm([info.role, info.name, info.href || '', info.value || ''].join(' ')).includes(needle)) {
      interactive.push(info);
    }
  });
  const remaining = () => maxResults - interactive.length - textBlocks.length;
  const addText = (el, type, text) => {
    if (remaining() <= 0) return;
    const context = contextualText(el, text);
    if (!context || !norm(context).includes(needle)) return;
    textBlocks.push({ type, text: around(context) });
  };
  const namedBlocks = 'h1,h2,h3,h4,h5,h6,p,li,td,blockquote,pre';
  document.querySelectorAll(namedBlocks).forEach(el => {
    if (remaining() <= 0 || !isVisible(el)) return;
    addText(el, el.tagName.toLowerCase(), visibleText(el));
  });
  const candidates = ['div', 'span', 'section', 'article'];
  document.querySelectorAll(candidates.join(',')).forEach(el => {
    if (remaining() <= 0 || !isVisible(el)) return;
    addText(el, el.tagName.toLowerCase(), directText(el));
  });
  return {
    url: location.href,
    title: document.title,
    interactive: interactive,
    text_blocks: textBlocks,
  };
}`, string(rawQuery), limit)
}

func formatBrowserFindResults(result *BrowserFindResult, query string, limit int) string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\n", result.URL)
	if result.Title != "" {
		fmt.Fprintf(&b, "TITLE: %s\n", result.Title)
	}
	fmt.Fprintf(&b, "SNAPSHOT_ID: %d\n", result.SnapshotID)
	fmt.Fprintf(&b, "QUERY: %q\n\n", query)

	matches := browserFindMatches(result, query, limit)
	if len(matches) == 0 {
		b.WriteString("MATCHES: none\n")
		b.WriteString("Next: retry browser_find with a shorter or different visible phrase, call browser_snapshot to inspect current text, scroll once if the desired section is likely off-screen, or continue from existing evidence.\n")
		return b.String()
	}
	b.WriteString("MATCHES:\n")
	for _, m := range matches {
		b.WriteString(m)
		b.WriteByte('\n')
	}
	return b.String()
}

func browserFindMatches(result *BrowserFindResult, query string, limit int) []string {
	needle := normalizedSnapshotText(query)
	if needle == "" || limit <= 0 {
		return nil
	}
	var out []string
	add := func(line string) bool {
		out = append(out, line)
		return len(out) >= limit
	}
	for _, el := range result.Interactive {
		hay := normalizedSnapshotText(strings.Join([]string{el.Role, el.Name, el.Href, el.Value}, " "))
		if !strings.Contains(hay, needle) {
			continue
		}
		if add(fmt.Sprintf("[interactive ref=%d] %s", el.Ref, formatInteractive(el))) {
			return out
		}
	}
	for _, tb := range result.TextBlocks {
		text := strings.TrimSpace(tb.Text)
		if text == "" || !strings.Contains(normalizedSnapshotText(text), needle) {
			continue
		}
		typ := strings.TrimSpace(tb.Type)
		if typ == "" {
			typ = "text"
		}
		if add(fmt.Sprintf("[text %s] %s", typ, snippetAround(text, query, maxBrowserFindSnippet))) {
			return out
		}
	}
	return out
}

func snippetAround(text, query string, limit int) string {
	text = strings.Join(strings.Fields(strings.TrimSpace(text)), " ")
	if limit <= 0 || len(text) <= limit {
		return text
	}
	lowerText := strings.ToLower(text)
	lowerQuery := strings.ToLower(strings.TrimSpace(query))
	idx := strings.Index(lowerText, lowerQuery)
	if idx < 0 {
		return truncateSnapshotField(text, limit)
	}
	start := idx - (limit-len(lowerQuery))/2
	if start < 0 {
		start = 0
	}
	if start+limit > len(text) {
		start = len(text) - limit
	}
	if start < 0 {
		start = 0
	}
	end := start + limit
	prefix, suffix := "", ""
	if start > 0 {
		prefix = "... "
	}
	if end < len(text) {
		suffix = " ..."
	}
	return prefix + strings.TrimSpace(text[start:end]) + suffix
}
