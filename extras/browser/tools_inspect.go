package browser

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/go-rod/rod/lib/proto"
)

// resolveSavePath validates a screenshot save_path against an optional
// workspace sandbox. Mirrors agent.safeWorkspacePath: relative joins
// onto workspaceDir, absolute must stay inside it. workspaceDir == ""
// disables enforcement and returns the input path unchanged (back-
// compat for callers that gate the tool another way).
//
// Without this, `save_path=/etc/cron.d/anything.png` would happily
// land an attacker-controlled file at an arbitrary location. The
// regular write_file tool already enforces via safeWorkspacePath;
// screenshot save_path was the inconsistency.
func resolveSavePath(workspaceDir, savePath string) (string, error) {
	savePath = strings.TrimSpace(savePath)
	if workspaceDir == "" {
		return savePath, nil
	}
	absWS, err := filepath.Abs(workspaceDir)
	if err != nil {
		return "", err
	}
	var target string
	if filepath.IsAbs(savePath) {
		target = filepath.Clean(savePath)
	} else {
		target = filepath.Clean(filepath.Join(absWS, savePath))
	}
	rel, err := filepath.Rel(absWS, target)
	// Reject paths that escape upward. The check must distinguish the
	// literal ".." path component from filenames that *start with*
	// ".." (e.g. "..backup.png"). HasPrefix(rel, "..") would falsely
	// reject the latter. Same shape as agent.safeWorkspacePath's
	// escape check.
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("save_path %q escapes workspace %q", savePath, absWS)
	}
	return target, nil
}

// SnapshotTool returns `browser_snapshot`. Re-takes the snapshot of
// the current page; the model uses this after dynamic content changes
// (e.g. an XHR finished after the last navigation).
func SnapshotTool(s *Session) *agent.Tool {
	schema := json.RawMessage(`{"type":"object","properties":{}}`)
	return &agent.Tool{
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
const (
	maxInlinePNGBytes          = (agent.MaxToolResultBytesInContext - 64) * 3 / 4
	maxScreenshotSavePathBytes = 4096
)

// ScreenshotTool returns `browser_screenshot`. Captures a PNG of the
// current viewport (or the full page). With `save_path` set, writes
// the bytes to disk and returns the path. Without it, returns a base64
// data URL — but only if small enough to fit inside agent runtime's
// MaxToolResultBytesInContext budget; oversize inline returns are
// refused with an error instead of returning truncated base64 that
// decodes to nothing.
//
// Opt-in via Options.IncludeScreenshot so text-only deployments don't
// expose a tool whose output they cannot consume.
func ScreenshotTool(s *Session) *agent.Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "properties": {
            "full_page": {
                "type": "boolean",
                "description": "If true, capture the whole scrollable document, not just the viewport."
            },
            "save_path": {
                "type": "string",
                "maxLength": %d,
                "description": "Filesystem path to write the PNG bytes to. Returns the path on success. Recommended for any non-blank page: inline base64 of a real screenshot exceeds the tool-result context budget."
            }
        }
    }`, maxScreenshotSavePathBytes))
	return &agent.Tool{
		Name: "browser_screenshot",
		Description: "Capture a PNG screenshot of the current page (viewport by default; " +
			"set full_page=true for the whole document). With save_path set, writes the " +
			"bytes to that path and returns the path. Without save_path, returns a base64 " +
			"data URL — only viable for very small (near-blank) captures because agent runtime's " +
			"tool-result context budget is 8 KiB. Text-only models should prefer " +
			"browser_snapshot for structured page content.",
		Schema: schema,
		Execute: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var args struct {
				FullPage bool    `json:"full_page"`
				SavePath *string `json:"save_path"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &args); err != nil {
					return "", fmt.Errorf("decode args: %w", err)
				}
			}
			var savePath string
			if args.SavePath != nil {
				savePath = strings.TrimSpace(*args.SavePath)
			}
			if len(savePath) > maxScreenshotSavePathBytes {
				return "", fmt.Errorf("save_path is %d bytes; browser_screenshot supports save_path up to %d bytes\nNext: retry with a shorter workspace-relative PNG path, for example screenshots/page.png", len(savePath), maxScreenshotSavePathBytes)
			}
			if args.SavePath != nil && savePath == "" {
				return "", errors.New("save_path must not be blank when provided\nNext: omit save_path for inline output, or retry with a workspace-relative PNG path")
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
			if savePath != "" {
				resolved, err := resolveSavePath(s.cfg.WorkspaceDir, savePath)
				if err != nil {
					return "", err
				}
				// Match agent.writeFileTool's behavior: auto-create the
				// parent dir so the model can drop screenshots into a
				// fresh subdirectory ("./screenshots/page1.png") without
				// having to first chain a shell mkdir. resolveSavePath
				// already guarantees the path stays inside WorkspaceDir,
				// so MkdirAll can't escape the sandbox.
				if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
					return "", fmt.Errorf("mkdir for screenshot %s: %w", resolved, err)
				}
				if err := os.WriteFile(resolved, png, 0o644); err != nil {
					return "", fmt.Errorf("write screenshot to %s: %w", resolved, err)
				}
				return fmt.Sprintf("screenshot saved to %s (%d bytes, png)", resolved, len(png)), nil
			}
			if len(png) > maxInlinePNGBytes {
				return "", fmt.Errorf(
					"screenshot is %d bytes (png); inline base64 would exceed agent runtime's %d-byte tool-result context budget and be truncated to unparseable noise.\nNext: re-run browser_screenshot with save_path=\"<path>.png\" to write the image to disk",
					len(png), agent.MaxToolResultBytesInContext,
				)
			}
			return "data:image/png;base64," + base64.StdEncoding.EncodeToString(png), nil
		},
	}
}
