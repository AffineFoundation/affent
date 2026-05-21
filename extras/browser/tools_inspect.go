package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"

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

// maxInlinePNGBytes is the largest PNG payload we'll inline as base64.
// affent caps tool results at MaxToolResultBytesInContext (8 KiB) before
// feeding them to the model; base64 expands ~4/3, so any PNG above this
// bound would be truncated mid-encoding into an unparseable string.
// Real-world Chromium screenshots almost always exceed this — the
// `save_path` arg is the practical path for screenshots beyond a blank
// page.
const maxInlinePNGBytes = (affent.MaxToolResultBytesInContext - 64) * 3 / 4

// ScreenshotTool returns `browser_screenshot`. Captures a PNG of the
// current viewport (or the full page). With `save_path` set, writes
// the bytes to disk and returns the path. Without it, returns a base64
// data URL — but only if small enough to fit inside affent's
// MaxToolResultBytesInContext budget; oversize inline returns are
// refused with an error instead of returning truncated base64 that
// decodes to nothing.
//
// Opt-in via Options.IncludeScreenshot so text-only deployments don't
// expose a tool whose output they cannot consume.
func ScreenshotTool(s *Session) *affent.Tool {
	schema := json.RawMessage(`{
        "type": "object",
        "properties": {
            "full_page": {
                "type": "boolean",
                "description": "If true, capture the whole scrollable document, not just the viewport."
            },
            "save_path": {
                "type": "string",
                "description": "Filesystem path to write the PNG bytes to. Returns the path on success. Recommended for any non-blank page: inline base64 of a real screenshot exceeds the tool-result context budget."
            }
        }
    }`)
	return &affent.Tool{
		Name: "browser_screenshot",
		Description: "Capture a PNG screenshot of the current page (viewport by default; " +
			"set full_page=true for the whole document). With save_path set, writes the " +
			"bytes to that path and returns the path. Without save_path, returns a base64 " +
			"data URL — only viable for very small (near-blank) captures because affent's " +
			"tool-result context budget is 8 KiB. Text-only models should prefer " +
			"browser_snapshot for structured page content.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				FullPage bool   `json:"full_page"`
				SavePath string `json:"save_path"`
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
			if args.SavePath != "" {
				if err := os.WriteFile(args.SavePath, png, 0o644); err != nil {
					return "", fmt.Errorf("write screenshot to %s: %w", args.SavePath, err)
				}
				return fmt.Sprintf("screenshot saved to %s (%d bytes, png)", args.SavePath, len(png)), nil
			}
			if len(png) > maxInlinePNGBytes {
				return "", fmt.Errorf(
					"screenshot is %d bytes (png); inline base64 would exceed affent's %d-byte tool-result context budget and be truncated to unparseable noise. Re-run with save_path=\"<path>.png\" to write the image to disk",
					len(png), affent.MaxToolResultBytesInContext,
				)
			}
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
		},
	}
}
