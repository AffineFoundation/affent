package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/affinefoundation/affent"
	"github.com/go-rod/rod/lib/proto"
)

// SnapshotTool returns `browser_snapshot`. Re-takes the snapshot of
// the current page; the model uses this after dynamic content changes
// (e.g. an XHR finished after the last navigation).
func SnapshotTool(s *Session) *affent.Tool {
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	return &affent.Tool{
		Name: "browser_snapshot",
		Description: "Re-read the current page and return a fresh snapshot: text content " +
			"plus interactive elements with current ref ids. Use whenever the page may " +
			"have changed since the last action (XHR completion, modal open, etc.).",
		Schema: schema,
		Execute: func(ctx context.Context, _ json.RawMessage) (string, error) {
			if s.page == nil {
				return "", ErrNoPage
			}
			snap, err := s.TakeSnapshot(ctx)
			if err != nil {
				return "", fmt.Errorf("snapshot: %w", err)
			}
			return snap.Format(), nil
		},
	}
}

// ScreenshotTool returns `browser_screenshot`. Captures a PNG of the
// current viewport (or the full page) and returns it as a base64
// string the embedder can surface to a multimodal model. Text-only
// LLMs typically won't call this; it stays opt-in via the Options
// struct.
//
// Note: returning base64 in a tool result string is functional but
// heavyweight. The result is capped at the standard tool-result
// truncation (8 KiB in-context). For real multimodal use, register
// this tool through a wrapper that delegates the bytes to a side
// channel (image gallery, evidence store) and returns a short pointer
// to the model.
func ScreenshotTool(s *Session) *affent.Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "properties": {
            "full_page": {
                "type": "boolean",
                "description": "If true, capture the whole scrollable document, not just the viewport."
            }
        }
    }`)
	return &affent.Tool{
		Name: "browser_screenshot",
		Description: "Capture a PNG screenshot of the current page (viewport by default; " +
			"set full_page=true for the whole document). Returns a base64 data URL. " +
			"Most useful when paired with a multimodal model — text-only models should " +
			"prefer browser_snapshot, which already gives them the structured page text.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				FullPage bool `json:"full_page"`
			}
			if len(raw) > 0 {
				_ = json.Unmarshal(raw, &args)
			}
			if s.page == nil {
				return "", ErrNoPage
			}
			page := s.withContext(ctx)
			opts := &proto.PageCaptureScreenshot{
				Format:                proto.PageCaptureScreenshotFormatPng,
				CaptureBeyondViewport: args.FullPage,
			}
			png, err := page.Screenshot(args.FullPage, opts)
			if err != nil {
				return "", fmt.Errorf("screenshot: %w", err)
			}
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
		},
	}
}
