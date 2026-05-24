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

const (
	maxBrowserURLBytes      = 4096
	maxBrowserWaitTextBytes = 2048
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
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["url"],
        "properties": {
            "url": {
                "type": "string",
                "minLength": 1,
                "maxLength": %d,
                "description": "The fully-qualified URL to open (http:// or https://)."
            },
            "wait_until": {
                "type": "string",
                "enum": ["load", "domcontentloaded", "networkidle"],
                "default": "load",
                "description": "What event ends the navigation. Default 'load'. Use 'networkidle' for SPAs whose content arrives via XHR after load."
            }
        }
    }`, maxBrowserURLBytes))

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
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_navigate with only documented fields: url and wait_until"); err != nil {
				return "", err
			}
			args.URL = strings.TrimSpace(args.URL)
			if args.URL == "" {
				return "", errors.New("url is required\nNext: retry browser_navigate with a fully-qualified http:// or https:// URL")
			}
			if len(args.URL) > maxBrowserURLBytes {
				return "", fmt.Errorf("url is %d bytes; browser_navigate supports URLs up to %d bytes\nNext: retry browser_navigate with the canonical page URL, or use web_search to find a shorter result URL", len(args.URL), maxBrowserURLBytes)
			}
			if !strings.HasPrefix(args.URL, "http://") && !strings.HasPrefix(args.URL, "https://") {
				return "", fmt.Errorf("url must start with http:// or https:// (got %q)\nNext: retry browser_navigate with the full URL including the http:// or https:// scheme", args.URL)
			}
			waitUntil, err := normalizeBrowserLoadWait(args.WaitUntil, "wait_until")
			if err != nil {
				return "", err
			}
			return runNavigate(ctx, s, args.URL, waitUntil)
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
	waitUntil, err := normalizeBrowserLoadWait(waitUntil, "wait_until")
	if err != nil {
		return err
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
	}
	return nil
}

func normalizeBrowserLoadWait(waitUntil, argName string) (string, error) {
	waitUntil = strings.TrimSpace(waitUntil)
	if waitUntil == "" {
		return "load", nil
	}
	switch waitUntil {
	case "load", "domcontentloaded", "networkidle":
		return waitUntil, nil
	default:
		return "", fmt.Errorf("%s %q is not supported\nNext: retry with one of load, domcontentloaded, or networkidle", argName, waitUntil)
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
	schema := json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{}}`)
	return &agent.Tool{
		Name:        "browser_back",
		Description: "Navigate one step back in the browser history. Returns the resulting page snapshot. No-op if there is no prior history entry.",
		Schema:      schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct{}
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_back with an empty JSON object"); err != nil {
				return "", err
			}
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
        "additionalProperties": false,
        "required": ["for"],
        "properties": {
            "for": {
                "type": "string",
                "minLength": 1,
                "enum": ["load", "domcontentloaded", "networkidle", "text"],
                "description": "What to wait for. 'text' polls until the page body contains the substring given in 'value'."
            },
            "value": {
                "type": "string",
                "minLength": 1,
                "maxLength": %d,
                "description": "Required when 'for' is 'text'; the substring to wait for in the page body."
            },
            "timeout_ms": {
                "type": "integer",
                "minimum": %d,
                "maximum": %d,
                "default": %d,
                "description": "Max time to wait in milliseconds. Default %d."
            }
        }
    }`, maxBrowserWaitTextBytes, minBrowserWaitTimeoutMS, maxBrowserWaitTimeoutMS, defaultBrowserWaitTimeoutMS, defaultBrowserWaitTimeoutMS))
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
			if err := decodeBrowserToolArgs(raw, &args, "retry browser_wait with only documented fields: for, value, and timeout_ms"); err != nil {
				return "", err
			}
			args.For = strings.TrimSpace(args.For)
			args.Value = strings.TrimSpace(args.Value)
			if args.For == "" {
				return "", errors.New("'for' is required. Next: retry with one of load, domcontentloaded, networkidle, or text")
			}
			waitFor, err := normalizeBrowserWaitFor(args.For)
			if err != nil {
				return "", err
			}
			timeout, err := resolveBrowserWaitTimeout(args.TimeoutMS)
			if err != nil {
				return "", fmt.Errorf("%w\nNext: omit timeout_ms to use the default, or retry with a value between %d and %d", err, minBrowserWaitTimeoutMS, maxBrowserWaitTimeoutMS)
			}
			if waitFor == "text" {
				if args.Value == "" {
					return "", errors.New("'value' is required when 'for'='text'. Next: retry with the exact short substring you expect to appear")
				}
				if len(args.Value) > maxBrowserWaitTextBytes {
					return "", fmt.Errorf("'value' is %d bytes; browser_wait text supports values up to %d bytes\nNext: retry browser_wait with a shorter exact substring that appears on the page", len(args.Value), maxBrowserWaitTextBytes)
				}
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			if waitFor == "text" {
				if err := waitForText(ctx, s, args.Value, timeout); err != nil {
					return "", err
				}
			} else {
				if err := waitForLoad(ctx, s, waitFor, timeout); err != nil {
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

func normalizeBrowserWaitFor(waitFor string) (string, error) {
	waitFor = strings.TrimSpace(waitFor)
	switch waitFor {
	case "load", "domcontentloaded", "networkidle", "text":
		return waitFor, nil
	default:
		return "", fmt.Errorf("'for' value %q is not supported\nNext: retry browser_wait with for=load, for=domcontentloaded, for=networkidle, or for=text", waitFor)
	}
}

func browserWaitTextTimeoutError(timeout time.Duration, substr string) error {
	return fmt.Errorf("timed out after %s waiting for text %q\nNext: call browser_snapshot to inspect current text, retry with a shorter visible substring, or continue if the page already contains enough evidence", timeout, substr)
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
			return browserWaitTextTimeoutError(timeout, substr)
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
