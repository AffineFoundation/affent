package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/go-rod/rod"
)

// Snapshot is the structured page representation handed back to the
// LLM. JSON-marshaled to the wire on tool replies and re-rendered by
// Format() into the text form the model actually reads.
type Snapshot struct {
	// SnapshotID rises monotonically per session per snapshot. Stored
	// here so tests / clients can reason about staleness; the JSON
	// tools embed it in their result envelope.
	SnapshotID int64 `json:"snapshot_id"`

	URL   string `json:"url"`
	Title string `json:"title"`

	// Interactive elements found on the page, in DOM order. Each
	// carries a stable ref id valid until the next Snapshot() call;
	// click_, type_ etc. take this ref.
	Interactive []InteractiveElement `json:"interactive"`

	// TextBlocks are passive page content (headings, paragraphs, list
	// items, table cells, blockquotes, preformatted blocks) in DOM
	// order. Each capped at 400 chars per block; total blocks capped
	// at 200.
	TextBlocks []TextBlock `json:"text_blocks"`

	// TruncatedBlocks reports whether the page had more text blocks
	// than the 200-block cap allowed.
	TruncatedBlocks bool `json:"truncated_blocks,omitempty"`
}

// InteractiveElement describes one ref-addressable element.
type InteractiveElement struct {
	Ref     int    `json:"ref"`
	Role    string `json:"role"`
	Name    string `json:"name,omitempty"`
	Href    string `json:"href,omitempty"`
	Value   string `json:"value,omitempty"`
	Checked *bool  `json:"checked,omitempty"`
}

// TextBlock is one structural text node.
type TextBlock struct {
	Type string `json:"type"` // h1..h6, p, li, td, blockquote, pre
	Text string `json:"text"`
}

const (
	maxFormattedTextBlocks     = 120
	maxFormattedInteractive    = 120
	maxFormattedInteractiveURL = 240
	maxFormattedValue          = 160
	maxGroupedTextLine         = 220
)

// snapshotJS runs in the page's JS realm. It:
//
//   - clears prior data-affent-ref attributes (so refs are always
//     scoped to "most recent snapshot");
//   - enumerates interactive elements, assigns sequential ref ids,
//     stamps each with data-affent-ref="N" so click/type tools can
//     look them up later via that attribute selector;
//   - collects passive text blocks in DOM order with hard caps.
//
// Output is a single JSON object matching Snapshot's fields (minus
// SnapshotID, which the Go side fills in).
const snapshotJS = `() => {
  document.querySelectorAll('[data-affent-ref]').forEach(el => el.removeAttribute('data-affent-ref'));

  function isVisible(el) {
    if (!el || !el.getBoundingClientRect) return false;
    const rect = el.getBoundingClientRect();
    if (rect.width === 0 || rect.height === 0) return false;
    const cs = getComputedStyle(el);
    if (cs.visibility === 'hidden' || cs.display === 'none') return false;
    if (parseFloat(cs.opacity || '1') === 0) return false;
    return true;
  }

  function clean(s) {
    return (s || '').toString().trim().replace(/\s+/g, ' ');
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
  const interactive = [];
  document.querySelectorAll(interactiveSelectors).forEach(el => {
    if (!isVisible(el)) return;
    nextRef++;
    el.setAttribute('data-affent-ref', String(nextRef));
    const info = { ref: nextRef, role: roleOf(el), name: accessibleName(el) };
    if (el.tagName === 'A' && el.getAttribute('href')) info.href = el.getAttribute('href');
    if (el.tagName === 'INPUT' || el.tagName === 'TEXTAREA' || el.tagName === 'SELECT') {
      info.value = ((el.value || '') + '').slice(0, 200);
    }
    if (el.tagName === 'INPUT' && (el.type === 'checkbox' || el.type === 'radio')) {
      info.checked = !!el.checked;
    }
    interactive.push(info);
  });

  const MAX_BLOCKS = 200;
  const MAX_BLOCK_CHARS = 400;
  const blocks = [];
  let blockCount = 0, truncated = false;
  // Headings, paragraphs, list items, table cells, blockquote, pre
  // are unambiguous text blocks. Divs and spans get included only
  // when they contain a non-empty direct text node — that catches
  // leaf-text-divs (the common idiom on modern sites) without
  // duplicating the same text under every ancestor wrapper.
  const namedBlocks = 'h1,h2,h3,h4,h5,h6,p,li,td,blockquote,pre';
  const candidates = ['div', 'span', 'section', 'article'];

  function hasDirectText(el) {
    for (const node of el.childNodes) {
      if (node.nodeType === 3 /* TEXT_NODE */ && (node.textContent || '').trim() !== '') {
        return true;
      }
    }
    return false;
  }

  document.querySelectorAll(namedBlocks).forEach(el => {
    if (truncated) return;
    if (!isVisible(el)) return;
    const text = clean(el.textContent);
    if (!text) return;
    if (blockCount >= MAX_BLOCKS) { truncated = true; return; }
    blocks.push({ type: el.tagName.toLowerCase(), text: text.slice(0, MAX_BLOCK_CHARS) });
    blockCount++;
  });
  document.querySelectorAll(candidates.join(',')).forEach(el => {
    if (truncated) return;
    if (!isVisible(el)) return;
    if (!hasDirectText(el)) return;
    // Only emit the direct text node content, not descendant text
    // (which already got emitted by its own block selector or by a
    // deeper hasDirectText match).
    let direct = '';
    for (const node of el.childNodes) {
      if (node.nodeType === 3) direct += node.textContent;
    }
    const text = clean(direct);
    if (!text) return;
    if (blockCount >= MAX_BLOCKS) { truncated = true; return; }
    blocks.push({ type: el.tagName.toLowerCase(), text: text.slice(0, MAX_BLOCK_CHARS) });
    blockCount++;
  });

  return {
    url: location.href,
    title: document.title,
    interactive: interactive,
    text_blocks: blocks,
    truncated_blocks: truncated,
  };
}`

// TakeSnapshot runs the snapshot JS in the page, decodes the result
// into a Snapshot, and stamps a fresh snapshot id.
func (s *Session) TakeSnapshot(ctx context.Context) (*Snapshot, error) {
	if s.page == nil {
		return nil, ErrNoPage
	}
	page := s.withContext(ctx)
	result, err := page.Eval(snapshotJS)
	if err != nil {
		return nil, fmt.Errorf("eval snapshot js: %w", err)
	}
	raw, err := result.Value.MarshalJSON()
	if err != nil {
		return nil, fmt.Errorf("re-marshal snapshot result: %w", err)
	}
	var snap Snapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		return nil, fmt.Errorf("decode snapshot: %w (raw=%s)", err, string(raw))
	}
	snap.SnapshotID = s.newSnapshotID()
	return &snap, nil
}

// elementByRef locates the DOM element associated with a ref id from
// the most recent snapshot. Returns ErrStaleRef if the ref is not found
// — either it was never stamped, or a subsequent snapshot replaced the
// attribute on different elements.
func (s *Session) elementByRef(ctx context.Context, ref int) (*rod.Element, error) {
	page := s.withContext(ctx)
	selector := fmt.Sprintf(`[data-affent-ref="%d"]`, ref)
	el, err := page.Element(selector)
	if err != nil {
		return nil, &StaleRefError{Ref: ref, Cause: err}
	}
	return el, nil
}

// StaleRefError is returned when click/type/etc. cannot resolve a ref.
type StaleRefError struct {
	Ref   int
	Cause error
}

func (e *StaleRefError) Error() string {
	return fmt.Sprintf(
		"ref %d not found on page (most likely the page changed since the last browser_snapshot)\n"+
			"Failure: kind=stale_ref\n"+
			"Next: call browser_snapshot, inspect the current refs, then retry with a fresh ref",
		e.Ref,
	)
}

func (e *StaleRefError) Unwrap() error { return e.Cause }

// Format renders a Snapshot into the text form the LLM reads. Compact,
// scannable, ref ids in square brackets.
func (snap *Snapshot) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "URL: %s\n", snap.URL)
	if snap.Title != "" {
		fmt.Fprintf(&b, "TITLE: %s\n", snap.Title)
	}
	fmt.Fprintf(&b, "SNAPSHOT_ID: %d\n", snap.SnapshotID)
	b.WriteString("\n")

	interactiveNames := map[string]struct{}{}
	if len(snap.Interactive) > 0 {
		b.WriteString("INTERACTIVE ELEMENTS:\n")
		limit := minInt(len(snap.Interactive), maxFormattedInteractive)
		for _, el := range snap.Interactive[:limit] {
			if key := normalizedSnapshotText(el.Name); key != "" {
				interactiveNames[key] = struct{}{}
			}
			line := formatInteractive(el)
			fmt.Fprintf(&b, "[%d] %s\n", el.Ref, line)
		}
		if len(snap.Interactive) > limit {
			fmt.Fprintf(&b, "[... %d interactive elements omitted; use search/filter, scroll, or navigate directly when possible ...]\n", len(snap.Interactive)-limit)
		}
		b.WriteString("\n")
	} else {
		b.WriteString("(no interactive elements detected)\n\n")
	}

	if len(snap.TextBlocks) > 0 {
		b.WriteString("PAGE TEXT:\n")
		writeTextBlocks(&b, snap.TextBlocks, interactiveNames)
		if snap.TruncatedBlocks {
			b.WriteString("[... text blocks truncated (200-block cap) ...]\n")
		}
		b.WriteString("\n")
	}

	return b.String()
}

func writeTextBlocks(b *strings.Builder, blocks []TextBlock, skip map[string]struct{}) {
	written := 0
	omittedDuplicates := 0
	omittedLimit := 0
	var groupType string
	var group []string
	groupLen := 0

	flush := func() {
		if len(group) == 0 {
			return
		}
		fmt.Fprintf(b, "%s: %s\n", groupType, strings.Join(group, " | "))
		groupType = ""
		group = nil
		groupLen = 0
	}

	for _, tb := range blocks {
		text := strings.TrimSpace(tb.Text)
		if text == "" {
			continue
		}
		if _, ok := skip[normalizedSnapshotText(text)]; ok {
			omittedDuplicates++
			continue
		}
		if written >= maxFormattedTextBlocks {
			omittedLimit++
			continue
		}
		typ := strings.TrimSpace(tb.Type)
		if typ == "" {
			typ = "text"
		}
		if shouldGroupTextBlock(typ, text) {
			nextLen := groupLen + len(text)
			if len(group) > 0 {
				nextLen += 3 // " | "
			}
			if groupType == typ && nextLen <= maxGroupedTextLine {
				group = append(group, text)
				groupLen = nextLen
				written++
				continue
			}
			flush()
			groupType = typ
			group = []string{text}
			groupLen = len(text)
			written++
			continue
		}
		flush()
		fmt.Fprintf(b, "%s: %s\n", typ, text)
		written++
	}
	flush()
	if omittedDuplicates > 0 {
		fmt.Fprintf(b, "[... %d text blocks omitted because they duplicate interactive element names ...]\n", omittedDuplicates)
	}
	if omittedLimit > 0 {
		fmt.Fprintf(b, "[... %d text blocks omitted from formatted snapshot (%d-block display cap) ...]\n", omittedLimit, maxFormattedTextBlocks)
	}
}

func shouldGroupTextBlock(typ, text string) bool {
	switch typ {
	case "p", "li", "td", "div", "span":
		return len(text) <= 80
	default:
		return false
	}
}

func formatInteractive(el InteractiveElement) string {
	var parts []string
	parts = append(parts, el.Role)
	if el.Name != "" {
		parts = append(parts, fmt.Sprintf("%q", el.Name))
	}
	if el.Href != "" {
		parts = append(parts, "→ "+truncateSnapshotField(el.Href, maxFormattedInteractiveURL))
	}
	if el.Value != "" {
		parts = append(parts, fmt.Sprintf("(value: %q)", truncateSnapshotField(el.Value, maxFormattedValue)))
	}
	if el.Checked != nil {
		if *el.Checked {
			parts = append(parts, "(checked)")
		} else {
			parts = append(parts, "(unchecked)")
		}
	}
	return strings.Join(parts, " ")
}

func normalizedSnapshotText(s string) string {
	return strings.ToLower(strings.Join(strings.Fields(strings.TrimSpace(s)), " "))
}

func truncateSnapshotField(s string, limit int) string {
	if limit <= 0 || len(s) <= limit {
		return s
	}
	if limit <= len("...(truncated)") {
		return s[:limit]
	}
	return strings.TrimSpace(s[:limit-len("...(truncated)")]) + "...(truncated)"
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
