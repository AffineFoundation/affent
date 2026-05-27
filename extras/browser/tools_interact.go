package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/input"
	"github.com/go-rod/rod/lib/proto"
)

// interactableTimeout caps how long Click / Type will wait for the
// targeted element to become visible + not-covered + clickable. Set
// short so a confused LLM gets a fast retry signal; a longer wait
// just delays the next reasoning step without buying anything (the
// element is either interactable now or has a layout problem the
// agent needs to surface).
const (
	interactableTimeout        = 2 * time.Second
	browserClickTimeout        = 12 * time.Second
	maxBrowserTypeTextBytes    = 4096
	defaultBrowserScrollAmount = 600
	maxBrowserScrollAmount     = 5000
)

func browserRefRequiredError(tool string) error {
	return browserInvalidArgs("ref must be a positive integer", fmt.Sprintf("call browser_snapshot to get current ref ids, then retry %s with one of those refs", tool))
}

func browserNotInteractableError(ref int, err error) error {
	return fmt.Errorf(
		"ref %d not interactable (hidden, disabled, or covered by another element): %w\n"+
			"Failure: kind=not_interactable\n"+
			"Next: call browser_snapshot to inspect the current page; if needed scroll, close the covering element, or choose a different visible ref",
		ref,
		err,
	)
}

func browserClickTimeoutError(ref int, timeout time.Duration, err error) error {
	return fmt.Errorf(
		"browser_click ref %d timed out after %s: %w\n"+
			"Failure: kind=timeout\n"+
			"Next: call browser_snapshot or browser_find to inspect the current page; retry this click only if the page changed or the target ref is still essential, otherwise navigate directly to a canonical URL or answer from verified evidence",
		ref,
		timeout,
		err,
	)
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx == nil {
		return context.Background()
	}
	return ctx
}

// waitInteractable wraps rod.Element.WaitInteractable with our bounded
// timeout and a friendlier error string. Returns a helpful message
// when the element is hidden / covered so the LLM can act (close the
// modal, scroll, re-snapshot).
func waitInteractable(ctx context.Context, el *rod.Element, ref int) error {
	innerCtx, cancel := context.WithTimeout(ctx, interactableTimeout)
	defer cancel()
	if _, err := el.Context(innerCtx).WaitInteractable(); err != nil {
		return browserNotInteractableError(ref, err)
	}
	return nil
}

// ClickTool returns `browser_click`. Looks up the element by ref id
// from the most recent snapshot and clicks it; takes a fresh snapshot
// afterward (since the click may trigger navigation, DOM mutation, or
// modal opening).
func ClickTool(s *Session) *agent.Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["ref"],
        "properties": {
            "ref": {
                "type": "integer",
                "minimum": 1,
                "description": "Ref id from the most recent browser_snapshot/browser_navigate result."
            }
        }
    }`)
	return &agent.Tool{
		Name: "browser_click",
		Description: "Click an element identified by its ref id from the most recent " +
			"snapshot. Returns a fresh snapshot after the click. If the page navigated, " +
			"the new URL appears in the snapshot's URL line.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Ref int `json:"ref"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_click with only the documented field: ref"); err != nil {
				return "", err
			}
			if args.Ref <= 0 {
				return "", browserRefRequiredError("browser_click")
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			return executeClickWithTimeout(ctx, s, args.Ref, browserClickTimeout, runClick)
		},
	}
}

type clickRunner func(context.Context, *Session, int) (string, error)

type clickResult struct {
	out string
	err error
}

func executeClickWithTimeout(ctx context.Context, s *Session, ref int, timeout time.Duration, run clickRunner) (string, error) {
	parent := nonNilContext(ctx)
	clickCtx, cancel := context.WithCancel(parent)
	defer cancel()
	done := make(chan clickResult, 1)
	go func() {
		out, err := run(clickCtx, s, ref)
		done <- clickResult{out: out, err: err}
	}()

	timer := time.NewTimer(timeout)
	defer timer.Stop()

	select {
	case result := <-done:
		if result.err != nil {
			if errors.Is(clickCtx.Err(), context.DeadlineExceeded) || errors.Is(result.err, context.DeadlineExceeded) {
				return "", browserClickTimeoutError(ref, timeout, result.err)
			}
			return "", result.err
		}
		return result.out, nil
	case <-timer.C:
		cancel()
		return "", browserClickTimeoutError(ref, timeout, context.DeadlineExceeded)
	case <-parent.Done():
		cancel()
		return "", browserClickTimeoutError(ref, timeout, parent.Err())
	}
}

func runClick(ctx context.Context, s *Session, ref int) (string, error) {
	el, err := s.elementByRef(ctx, ref)
	if err != nil {
		return "", err
	}
	el = el.Context(ctx)
	if err := el.ScrollIntoView(); err != nil {
		// Non-fatal; click may still work if already in viewport.
		_ = err
	}
	if err := waitInteractable(ctx, el, ref); err != nil {
		return "", err
	}
	if err := el.Click(proto.InputMouseButtonLeft, 1); err != nil {
		return "", fmt.Errorf("click ref %d: %w", ref, err)
	}
	// Briefly wait for any resulting load/navigation. Soft wait —
	// most clicks don't navigate.
	_ = waitForLoad(ctx, s, "load", 5*time.Second)
	snap, err := s.TakeSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("post-click snapshot: %w", err)
	}
	return formatSnapshotResult(snap)
}

// TypeTool returns `browser_type`. Focuses an element by ref, clears
// existing value (for input/textarea), and types the given text. If
// submit=true the input is followed by Enter, useful for search boxes.
func TypeTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["ref", "text"],
        "properties": {
            "ref": {
                "type": "integer",
                "minimum": 1,
                "description": "Ref id from the most recent snapshot. Must be an input, textarea, or contenteditable element."
            },
            "text": {
                "type": "string",
                "maxLength": %d,
                "description": "Text to type. Existing value is cleared first."
            },
            "submit": {
                "type": "boolean",
                "description": "If true, press Enter after typing — submits the form / triggers search."
            }
        }
    }`, maxBrowserTypeTextBytes))
	return &agent.Tool{
		Name: "browser_type",
		Description: "Type text into a form field identified by ref id from the most recent snapshot. " +
			"Clears any existing value first. Set submit=true to press Enter after typing (e.g. for a search box).",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Ref    int    `json:"ref"`
				Text   string `json:"text"`
				Submit bool   `json:"submit"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_type with only documented fields: ref, text, and submit"); err != nil {
				return "", err
			}
			if args.Ref <= 0 {
				return "", browserRefRequiredError("browser_type")
			}
			if len(args.Text) > maxBrowserTypeTextBytes {
				return "", browserInvalidArgs(fmt.Sprintf("text is %d bytes; browser_type supports text up to %d bytes", len(args.Text), maxBrowserTypeTextBytes), "retry browser_type with shorter text, or paste large content through a file/shell workflow instead")
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			el, err := s.elementByRef(ctx, args.Ref)
			if err != nil {
				return "", err
			}
			if err := el.ScrollIntoView(); err != nil {
				_ = err
			}
			if err := waitInteractable(ctx, el, args.Ref); err != nil {
				return "", err
			}
			if err := el.Focus(); err != nil {
				return "", fmt.Errorf("focus ref %d: %w", args.Ref, err)
			}
			// Clear existing value: select all + delete. SelectAllText
			// works on inputs/textareas/contenteditable.
			if err := el.SelectAllText(); err == nil {
				// SelectAllText succeeded → press Delete.
				page := s.withContext(ctx)
				_ = page.Keyboard.Press(input.Delete)
			}
			if err := el.Input(args.Text); err != nil {
				return "", fmt.Errorf("type into ref %d: %w", args.Ref, err)
			}
			if args.Submit {
				page := s.withContext(ctx)
				if err := page.Keyboard.Press(input.Enter); err != nil {
					return "", fmt.Errorf("press enter: %w", err)
				}
				_ = waitForLoad(ctx, s, "load", 5*time.Second)
			}
			snap, err := s.TakeSnapshot(ctx)
			if err != nil {
				return "", fmt.Errorf("post-type snapshot: %w", err)
			}
			return formatSnapshotResult(snap)
		},
	}
}

// ScrollTool returns `browser_scroll`. Scrolls the viewport up, down,
// or by a page. The amount parameter (CSS pixels) is honored for
// up/down; page_up/page_down ignore it.
func ScrollTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["direction"],
        "properties": {
            "direction": {
                "type": "string",
                "minLength": 1,
                "enum": ["up", "down", "page_up", "page_down", "top", "bottom"],
                "description": "Scroll direction."
            },
            "amount": {
                "type": "integer",
                "minimum": 1,
                "maximum": %d,
                "default": %d,
                "description": "CSS pixels for up/down (ignored otherwise). Default 600."
            }
        }
    }`, maxBrowserScrollAmount, defaultBrowserScrollAmount))
	return &agent.Tool{
		Name:        "browser_scroll",
		Description: "Scroll the viewport. Use 'page_down'/'page_up' for one viewport's worth, 'top'/'bottom' to jump to extremes, or 'up'/'down' with an 'amount' in pixels. Returns a fresh snapshot of what's now visible.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				Direction string `json:"direction"`
				Amount    int    `json:"amount"`
			}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_scroll with only documented fields: direction and amount"); err != nil {
				return "", err
			}
			args.Direction = strings.TrimSpace(args.Direction)
			amount := args.Amount
			if amount <= 0 {
				amount = defaultBrowserScrollAmount
			}
			if amount > maxBrowserScrollAmount {
				return "", browserInvalidArgs(fmt.Sprintf("amount must be between 1 and %d CSS pixels", maxBrowserScrollAmount), "omit amount to use the default, use page_down/page_up, or retry with a smaller amount")
			}
			var js string
			switch args.Direction {
			case "up":
				js = fmt.Sprintf("() => window.scrollBy(0, -%d)", amount)
			case "down":
				js = fmt.Sprintf("() => window.scrollBy(0, %d)", amount)
			case "page_up":
				js = "() => window.scrollBy(0, -window.innerHeight)"
			case "page_down":
				js = "() => window.scrollBy(0, window.innerHeight)"
			case "top":
				js = "() => window.scrollTo(0, 0)"
			case "bottom":
				js = "() => window.scrollTo(0, document.body.scrollHeight)"
			case "":
				return "", browserInvalidArgs("'direction' is required", "retry with one of up, down, page_up, page_down, top, or bottom")
			default:
				return "", browserInvalidArgs(fmt.Sprintf("unknown direction %q", args.Direction), "retry with one of up, down, page_up, page_down, top, or bottom")
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			page := s.withContext(ctx)
			result, err := page.Eval(browserScrollJS(args.Direction, amount, js))
			if err != nil {
				return "", fmt.Errorf("scroll: %w", err)
			}
			scrollRaw, err := result.Value.MarshalJSON()
			if err != nil {
				return "", fmt.Errorf("re-marshal scroll result: %w", err)
			}
			var telemetry browserScrollTelemetry
			if err := json.Unmarshal(scrollRaw, &telemetry); err != nil {
				return "", fmt.Errorf("decode scroll result: %w (raw=%s)", err, string(scrollRaw))
			}
			// Give lazy-load handlers a beat to fire.
			time.Sleep(150 * time.Millisecond)
			snap, err := s.TakeSnapshot(ctx)
			if err != nil {
				return "", fmt.Errorf("post-scroll snapshot: %w", err)
			}
			return formatScrollSnapshotResult(snap, telemetry)
		},
	}
}

type browserScrollTelemetry struct {
	Direction string `json:"direction"`
	BeforeY   int    `json:"before_y"`
	AfterY    int    `json:"after_y"`
	MaxY      int    `json:"max_y"`
}

func browserScrollJS(direction string, amount int, action string) string {
	return fmt.Sprintf(`() => {
  const root = document.scrollingElement || document.documentElement || document.body;
  const readY = () => Math.round(window.scrollY || (root && root.scrollTop) || 0);
  const readMaxY = () => Math.max(0, Math.round(((root && root.scrollHeight) || (document.body && document.body.scrollHeight) || 0) - window.innerHeight));
  const beforeY = readY();
  (%s)();
  const afterY = readY();
  return {direction: %q, before_y: beforeY, after_y: afterY, max_y: readMaxY()};
}`, action, direction)
}

func formatScrollSnapshotResult(snap *Snapshot, telemetry browserScrollTelemetry) (string, error) {
	out, err := formatSnapshotResult(snap)
	scrollLine := formatScrollTelemetry(telemetry)
	if scrollLine == "" {
		return out, err
	}
	if out != "" && !strings.HasSuffix(out, "\n") {
		out += "\n"
	}
	out += scrollLine
	return out, err
}

func formatScrollTelemetry(t browserScrollTelemetry) string {
	if t.Direction == "" {
		return ""
	}
	movement := "moved"
	if absInt(t.AfterY-t.BeforeY) <= 1 {
		movement = "none"
	}
	boundary := ""
	switch {
	case t.AfterY <= 1:
		boundary = "top"
	case t.MaxY > 0 && t.AfterY >= t.MaxY-1:
		boundary = "bottom"
	}
	line := fmt.Sprintf("SCROLL: direction=%s before_y=%d after_y=%d max_y=%d movement=%s", t.Direction, t.BeforeY, t.AfterY, t.MaxY, movement)
	if boundary != "" {
		line += " boundary=" + boundary
	}
	line += "\n"
	if movement == "none" {
		line += "Next: scrolling did not move the page; use browser_find/browser_snapshot for visible text, browser_network/browser_network_read for hidden XHR/fetch data, interact with a visible pagination/tab control, or mark the field unavailable.\n"
	}
	return line
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
