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
)

// NavigateTool returns the `browser_navigate` tool.
//
// Behavior:
//   - Loads the requested URL in the session's current page.
//   - Waits up to navigationLoadTimeout for the document load event.
//   - Returns the post-navigation snapshot text. Status / final URL
//     after redirects are reflected in the snapshot's URL line.
//
// Failures (invalid scheme, network error, navigation timeout) are
// returned as the tool result string prefixed with "Error: ..." so the
// LLM sees them and can recover, matching the rest of affent's
// builtin-tool conventions.
func NavigateTool(s *Session) *agent.Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "required": ["url"],
        "properties": {
            "url": {
                "type": "string",
                "minLength": 1,
                "description": "The fully-qualified URL to open (http:// or https://)."
            },
            "wait_until": {
                "type": "string",
                "enum": ["load", "domcontentloaded", "networkidle"],
                "description": "What event ends the navigation. Default 'load'. Use 'networkidle' for SPAs whose content arrives via XHR after load."
            }
        }
    }`)

	return &agent.Tool{
		Name: "browser_navigate",
		Description: "Open a URL in the browser. Returns a structured snapshot of " +
			"the loaded page: text content, plus interactive elements with stable " +
			"ref ids (use those refs with browser_click, browser_type, etc.).",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				URL       string `json:"url"`
				WaitUntil string `json:"wait_until"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			args.URL = strings.TrimSpace(args.URL)
			if args.URL == "" {
				return "", errors.New("url is required")
			}
			if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
				return "", fmt.Errorf("url must start with http:// or https:// (got %q)", args.URL)
			}
			return runNavigate(ctx, s, args.URL, args.WaitUntil)
		},
	}
}

const (
	navigationLoadTimeout       = 30 * time.Second
	minBrowserWaitTimeoutMS     = 100
	defaultBrowserWaitTimeoutMS = 10000
	maxBrowserWaitTimeoutMS     = 60000
)

func runNavigate(ctx context.Context, s *Session, url, waitUntil string) (string, error) {
	if s.page == nil {
		return "", ErrNoPage
	}
	page := s.withContext(ctx)
	if err := page.Navigate(url); err != nil {
		return "", fmt.Errorf("navigate: %w", err)
	}
	if err := waitForLoad(ctx, s, waitUntil, navigationLoadTimeout); err != nil {
		// Soft failure: the page may still be usable even if the wait
		// condition didn't fire (slow-loading analytics, SPA pre-render,
		// etc.). Report it inline but proceed with the snapshot.
		snap, snapErr := s.TakeSnapshot(ctx)
		if snapErr != nil {
			return "", fmt.Errorf("post-navigate snapshot: %w (wait error: %v)", snapErr, err)
		}
		return fmt.Sprintf("(navigation wait timed out: %v)\n\n%s", err, snap.Format()), nil
	}
	snap, err := s.TakeSnapshot(ctx)
	if err != nil {
		return "", fmt.Errorf("snapshot: %w", err)
	}
	return snap.Format(), nil
}

func waitForLoad(ctx context.Context, s *Session, waitUntil string, timeout time.Duration) error {
	if waitUntil == "" {
		waitUntil = "load"
	}
	innerCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	page := s.withContext(innerCtx)
	switch waitUntil {
	case "load":
		return page.WaitLoad()
	case "domcontentloaded":
		// Map to the web standard: document.readyState reaches
		// "interactive" or "complete" — i.e. HTML parsed, sub-
		// resources may still be loading. An earlier version called
		// WaitDOMStable here, which waits for DOM *mutations* to
		// stop. That can hang indefinitely on SPAs that keep
		// mutating the DOM during their boot phase, even though
		// DOMContentLoaded already fired. readyState polling
		// matches what the agent (and the browser spec) means by
		// the term.
		return waitDOMContentLoaded(innerCtx, page, timeout)
	case "networkidle":
		return page.WaitIdle(timeout)
	default:
		return fmt.Errorf("unknown wait_until %q", waitUntil)
	}
}

// waitDOMContentLoaded polls document.readyState until it reaches the
// post-parse states ("interactive" = DOMContentLoaded fired,
// "complete" = the load event also fired). 100ms poll cadence keeps
// the overhead negligible while staying responsive.
func waitDOMContentLoaded(ctx context.Context, page *rod.Page, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for DOMContentLoaded", timeout)
		}
		result, err := page.Eval(`() => document.readyState`)
		if err == nil && result != nil {
			state := result.Value.Str()
			if state == "interactive" || state == "complete" {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// BackTool returns the `browser_back` tool — navigates one entry back
// in history and returns the new page snapshot.
func BackTool(s *Session) *agent.Tool {
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	return &agent.Tool{
		Name:        "browser_back",
		Description: "Navigate one step back in the browser history. Returns the resulting page snapshot. No-op if there is no prior history entry.",
		Schema:      schema,
		Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if s.page == nil {
				return "", ErrNoPage
			}
			page := s.withContext(ctx)
			if err := page.NavigateBack(); err != nil {
				return "", fmt.Errorf("navigate back: %w", err)
			}
			_ = waitForLoad(ctx, s, "load", navigationLoadTimeout)
			snap, err := s.TakeSnapshot(ctx)
			if err != nil {
				return "", fmt.Errorf("snapshot: %w", err)
			}
			return snap.Format(), nil
		},
	}
}

// WaitTool returns `browser_wait`. Lets the LLM explicitly wait for a
// dynamic page condition before taking a snapshot.
func WaitTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "required": ["for"],
        "properties": {
            "for": {
                "type": "string",
                "enum": ["load", "domcontentloaded", "networkidle", "text"],
                "description": "What to wait for. 'text' polls until the page body contains the substring given in 'value'."
            },
            "value": {
                "type": "string",
                "description": "Required when 'for' is 'text'; the substring to wait for in the page body."
            },
            "timeout_ms": {
                "type": "integer",
                "minimum": %d,
                "maximum": %d,
                "description": "Max time to wait in milliseconds. Default %d."
            }
        }
    }`, minBrowserWaitTimeoutMS, maxBrowserWaitTimeoutMS, defaultBrowserWaitTimeoutMS))
	return &agent.Tool{
		Name: "browser_wait",
		Description: "Explicitly wait for a page condition (load event, DOM stable, network idle, or a substring appearing) before taking a snapshot. " +
			"Use when content is injected asynchronously and the previous snapshot showed it missing.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				For       string `json:"for"`
				Value     string `json:"value"`
				TimeoutMS int    `json:"timeout_ms"`
			}
			if err := json.Unmarshal(raw, &args); err != nil {
				return "", fmt.Errorf("decode args: %w", err)
			}
			if args.For == "" {
				return "", errors.New("'for' is required")
			}
			timeout, err := resolveBrowserWaitTimeout(args.TimeoutMS)
			if err != nil {
				return "", err
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			if args.For == "text" {
				if args.Value == "" {
					return "", errors.New("'value' is required when 'for'='text'")
				}
				if err := waitForText(ctx, s, args.Value, timeout); err != nil {
					return "", err
				}
			} else {
				if err := waitForLoad(ctx, s, args.For, timeout); err != nil {
					return "", err
				}
			}
			snap, err := s.TakeSnapshot(ctx)
			if err != nil {
				return "", fmt.Errorf("snapshot: %w", err)
			}
			return snap.Format(), nil
		},
	}
}

func resolveBrowserWaitTimeout(timeoutMS int) (time.Duration, error) {
	if timeoutMS == 0 {
		return time.Duration(defaultBrowserWaitTimeoutMS) * time.Millisecond, nil
	}
	if timeoutMS < minBrowserWaitTimeoutMS || timeoutMS > maxBrowserWaitTimeoutMS {
		return 0, fmt.Errorf("timeout_ms must be between %d and %d milliseconds", minBrowserWaitTimeoutMS, maxBrowserWaitTimeoutMS)
	}
	return time.Duration(timeoutMS) * time.Millisecond, nil
}

func waitForText(ctx context.Context, s *Session, substr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	page := s.withContext(ctx)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("timed out after %s waiting for text %q", timeout, substr)
		}
		// Eval'd as JS so the page's runtime checks; cheaper than
		// shipping the body to Go on every poll.
		js := `(s) => (document.body && document.body.innerText && document.body.innerText.includes(s)) ? true : false`
		result, err := page.Eval(js, substr)
		if err == nil && result.Value.Bool() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
}
