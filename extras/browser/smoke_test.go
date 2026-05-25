//go:build browser_smoke

package browser

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

// findChromium probes the standard installation paths. If nothing is
// found, the smoke tests skip — we do NOT trigger rod's auto-download
// in tests, which would silently fetch ~150MB on first run and is
// inappropriate for CI.
func findChromium(t *testing.T) string {
	t.Helper()
	// Honor an explicit override for environments that pin the binary.
	if p := os.Getenv("AFFENT_BROWSER_BINARY"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
		t.Skipf("AFFENT_BROWSER_BINARY=%s not found on disk", p)
	}
	for _, name := range []string{
		"chromium-browser", "chromium", "google-chrome", "google-chrome-stable",
	} {
		if p, err := exec.LookPath(name); err == nil {
			return p
		}
	}
	t.Skip("no chromium binary found on PATH; skipping browser smoke test")
	return ""
}

// dataURL builds a `data:text/html;base64,...` URL with the given body.
// We avoid raw `data:text/html,` to skirt edge cases in URL parsing.
func dataURL(body string) string {
	// Keep it simple — Chromium accepts un-encoded data: HTML for
	// our small fixtures. Browser Use / Playwright tests use the
	// same shortcut.
	return "data:text/html;charset=utf-8," + body
}

func TestSession_SnapshotPicksUpInteractiveAndText(t *testing.T) {
	bin := findChromium(t)
	sess, err := NewSession(SessionConfig{
		BinaryPath: bin,
		NoSandbox:  true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	const body = `<html><body>
        <h1>Hello world</h1>
        <p>Paragraph text.</p>
        <a href="/info">More info</a>
        <button>Click me</button>
        <input type="text" placeholder="search" />
    </body></html>`

	out, err := runNavigate(ctx, sess, dataURL(body), "")
	if err != nil {
		t.Fatalf("runNavigate: %v", err)
	}
	for _, want := range []string{
		"h1: Hello world",
		"p: Paragraph text.",
		"link",
		"More info",
		"button",
		"Click me",
		"textbox",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("navigate output missing %q\n---\n%s\n---", want, out)
		}
	}
	// Snapshot must include a SNAPSHOT_ID line.
	if !strings.Contains(out, "SNAPSHOT_ID: 1") {
		t.Errorf("expected SNAPSHOT_ID: 1, got:\n%s", out)
	}
}

func TestSession_ClickStaleRef(t *testing.T) {
	bin := findChromium(t)
	sess, err := NewSession(SessionConfig{
		BinaryPath: bin,
		NoSandbox:  true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// No navigation yet → no refs stamped → clicking ref=999 must
	// surface a StaleRefError.
	_, err = sess.elementByRef(ctx, 999)
	if err == nil {
		t.Fatalf("expected StaleRefError for unknown ref, got nil")
	}
	if _, ok := err.(*StaleRefError); !ok {
		t.Errorf("expected *StaleRefError, got %T (%v)", err, err)
	}
}

func TestSession_FindToolSearchesRenderedPage(t *testing.T) {
	bin := findChromium(t)
	sess, err := NewSession(SessionConfig{
		BinaryPath: bin,
		NoSandbox:  true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	var body strings.Builder
	body.WriteString(`<html><body><h1>Affine metrics</h1>`)
	for i := 0; i < 240; i++ {
		body.WriteString(`<p>filler row `)
		body.WriteString(strconv.Itoa(i))
		body.WriteString(`</p>`)
	}
	body.WriteString(`<div class="metric-row"><span>Market cap</span><span>$55.4M</span><span>Liquidity $44.8M</span></div><a href="/market">Market details</a></body></html>`)
	if _, err := runNavigate(ctx, sess, dataURL(body.String()), ""); err != nil {
		t.Fatalf("runNavigate: %v", err)
	}
	out, err := FindTool(sess).Execute(ctx, []byte(`{"query":"market","max_results":3}`))
	if err != nil {
		t.Fatalf("browser_find: %v", err)
	}
	for _, want := range []string{`QUERY: "market"`, `[interactive ref=`, `Market details`, `Market cap $55.4M Liquidity $44.8M`} {
		if !strings.Contains(out, want) {
			t.Fatalf("browser_find output missing %q:\n%s", want, out)
		}
	}
}

func TestSession_TypeAndSubmitFlow(t *testing.T) {
	bin := findChromium(t)
	sess, err := NewSession(SessionConfig{
		BinaryPath: bin,
		NoSandbox:  true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// A form whose submission updates the page via JS — keeps the
	// test offline and deterministic.
	const body = `<html><body>
        <form onsubmit="event.preventDefault();
            document.getElementById('result').textContent = 'q=' + document.querySelector('input').value;
            return false;">
            <input type="text" placeholder="search" />
            <button type="submit">Go</button>
        </form>
        <div id="result"></div>
    </body></html>`

	out, err := runNavigate(ctx, sess, dataURL(body), "")
	if err != nil {
		t.Fatalf("runNavigate: %v", err)
	}
	// Find the textbox ref. Snapshot output puts it on a line like:
	//   [N] textbox "search" ...
	// We pull N out by string scanning.
	ref := findFirstRef(out, "textbox")
	if ref == 0 {
		t.Fatalf("snapshot didn't expose a textbox ref:\n%s", out)
	}

	tool := TypeTool(sess)
	args := []byte(`{"ref":` + intStr(ref) + `,"text":"hello","submit":true}`)
	result, err := tool.Execute(ctx, args)
	if err != nil {
		t.Fatalf("type tool: %v", err)
	}
	if !strings.Contains(result, "q=hello") {
		t.Errorf("submit didn't update page; result:\n%s", result)
	}
}

// findFirstRef scans `out` for the first line starting with [N] that
// contains `role`, returning N. Returns 0 if not found.
func findFirstRef(out, role string) int {
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "[") {
			continue
		}
		if !strings.Contains(line, role) {
			continue
		}
		end := strings.Index(line, "]")
		if end <= 1 {
			continue
		}
		n := 0
		for _, c := range line[1:end] {
			if c < '0' || c > '9' {
				return 0
			}
			n = n*10 + int(c-'0')
		}
		return n
	}
	return 0
}

func intStr(n int) string {
	if n == 0 {
		return "0"
	}
	digits := []byte{}
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
