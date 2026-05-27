//go:build browser_smoke

package browser

import (
	"context"
	"net/http"
	"net/http/httptest"
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

func TestSession_SnapshotPicksUpOpenShadowDOM(t *testing.T) {
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
        <h1>Host document</h1>
        <metric-card id="metric"></metric-card>
        <script>
          const root = document.getElementById('metric').attachShadow({mode: 'open'});
          root.innerHTML = '<h2>Shadow metric panel</h2><p>Shadow market cap 201.04K T</p><button>Reveal shadow status</button><div id="status">shadow idle</div>';
          root.querySelector('button').addEventListener('click', () => {
            root.getElementById('status').textContent = 'shadow clicked';
          });
        </script>
    </body></html>`

	out, err := runNavigate(ctx, sess, dataURL(body), "")
	if err != nil {
		t.Fatalf("runNavigate: %v", err)
	}
	for _, want := range []string{
		"h2: Shadow metric panel",
		"p: Shadow market cap 201.04K T",
		"button",
		"Reveal shadow status",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("shadow DOM snapshot missing %q:\n%s", want, out)
		}
	}
	ref := findFirstRef(out, "button")
	if ref == 0 {
		t.Fatalf("shadow DOM button did not get a ref:\n%s", out)
	}
	result, err := ClickTool(sess).Execute(ctx, []byte(`{"ref":`+intStr(ref)+`}`))
	if err != nil {
		t.Fatalf("click shadow DOM button: %v", err)
	}
	if !strings.Contains(result, "shadow clicked") {
		t.Fatalf("post-click shadow snapshot missing updated status:\n%s", result)
	}

	findOut, err := FindTool(sess).Execute(ctx, []byte(`{"query":"shadow market cap","max_results":3}`))
	if err != nil {
		t.Fatalf("browser_find shadow DOM: %v", err)
	}
	for _, want := range []string{
		`QUERY: "shadow market cap"`,
		`[text p] Shadow market cap 201.04K T`,
	} {
		if !strings.Contains(findOut, want) {
			t.Fatalf("shadow DOM browser_find missing %q:\n%s", want, findOut)
		}
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

func TestSession_ClickToolUpdatesSnapshot(t *testing.T) {
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
        <button onclick="document.getElementById('status').textContent='clicked'">Open tab</button>
        <div id="status">idle</div>
    </body></html>`
	out, err := runNavigate(ctx, sess, dataURL(body), "")
	if err != nil {
		t.Fatalf("runNavigate: %v", err)
	}
	ref := findFirstRef(out, "button")
	if ref == 0 {
		t.Fatalf("snapshot didn't expose a button ref:\n%s", out)
	}

	result, err := ClickTool(sess).Execute(ctx, []byte(`{"ref":`+intStr(ref)+`}`))
	if err != nil {
		t.Fatalf("click tool: %v", err)
	}
	if !strings.Contains(result, "clicked") {
		t.Fatalf("post-click snapshot missing updated text:\n%s", result)
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

func TestSession_NetworkEvidenceCapturesDashboardXHR(t *testing.T) {
	bin := findChromium(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = w.Write([]byte(`<!doctype html>
<html>
<body>
  <h1>Affine SN120 dashboard</h1>
  <dl>
    <dt>Market Cap</dt><dd id="market-cap">loading <number-flow-react style="display:inline-block;width:12px;height:12px"></number-flow-react></dd>
    <dt>24h Volume</dt><dd id="volume">loading</dd>
  </dl>
  <div id="status">booting</div>
  <script>
    fetch('/api/metrics')
      .then((r) => r.json())
      .then(() => {
        document.getElementById('market-cap').textContent = 'loaded from API';
        document.getElementById('volume').textContent = 'loaded from API';
        document.getElementById('status').textContent = 'loaded';
      });
  </script>
</body>
</html>`))
		case "/api/metrics":
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			_, _ = w.Write([]byte(`{"subnet":"Affine","netuid":120,"market_cap":"201.04K T","volume_24h":"18.7K T"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()

	sess, err := NewSession(SessionConfig{
		BinaryPath: bin,
		NoSandbox:  true,
		Intercept:  InterceptConfig{AllowPrivateNetwork: true},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	out, err := runNavigate(ctx, sess, srv.URL+"/", "networkidle")
	if err != nil {
		t.Fatalf("runNavigate: %v", err)
	}
	for _, want := range []string{"Affine SN120 dashboard", "Market Cap", "loaded from API"} {
		if !strings.Contains(out, want) {
			t.Fatalf("snapshot output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "201.04K T") || strings.Contains(out, "18.7K T") {
		t.Fatalf("fixture should not expose metric values in rendered text:\n%s", out)
	}

	findOut, err := FindTool(sess).Execute(ctx, []byte(`{"query":"201.04K T","max_results":3}`))
	if err != nil {
		t.Fatalf("browser_find exact hidden value: %v", err)
	}
	if !strings.Contains(findOut, "MATCHES: none") {
		t.Fatalf("hidden API value should not be visible to browser_find:\n%s", findOut)
	}
	labelFindOut, err := FindTool(sess).Execute(ctx, []byte(`{"query":"Market Cap","max_results":3}`))
	if err != nil {
		t.Fatalf("browser_find visible metric label: %v", err)
	}
	for _, want := range []string{
		"page_text_below=partial_dynamic_page_evidence",
		"empty_dynamic_metric_widgets:",
		"[text dt] Market Cap",
	} {
		if !strings.Contains(labelFindOut, want) {
			t.Fatalf("browser_find metric label output missing %q:\n%s", want, labelFindOut)
		}
	}
	if strings.Contains(labelFindOut, "page_text_below=verified_page_evidence") {
		t.Fatalf("browser_find must not mark empty widget label as verified evidence:\n%s", labelFindOut)
	}

	searchOut := waitForNetworkEvidence(t, ctx, sess, "market_cap")
	ref := firstNetworkRef(searchOut)
	if ref == "" {
		t.Fatalf("browser_network search did not expose a network ref:\n%s", searchOut)
	}
	readOut, err := NetworkReadTool(sess).Execute(ctx, []byte(`{"ref":"`+ref+`","max_bytes":512}`))
	if err != nil {
		t.Fatalf("browser_network_read: %v", err)
	}
	for _, want := range []string{
		"SourceAccess: browser_network_url=" + srv.URL + "/api/metrics",
		"ref=" + ref,
		"source_method=network_xhr_fetch",
		`"market_cap":"201.04K T"`,
		`"volume_24h":"18.7K T"`,
	} {
		if !strings.Contains(readOut, want) {
			t.Fatalf("browser_network_read output missing %q:\n%s", want, readOut)
		}
	}
}

func TestSession_RelaxDomainBlockingKeepsBrowserUsable(t *testing.T) {
	bin := findChromium(t)
	sess, err := NewSession(SessionConfig{
		BinaryPath: bin,
		NoSandbox:  true,
		Intercept: InterceptConfig{
			BlockedDomains: []string{"example.com"},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer sess.Close()

	if err := sess.relaxDomainBlocking(); err != nil {
		t.Fatalf("relaxDomainBlocking: %v", err)
	}
	if !sess.cfg.Intercept.AllowAllDomains {
		t.Fatal("relaxDomainBlocking should flip AllowAllDomains on the live session")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	out, err := runNavigate(ctx, sess, dataURL(`<html><body><h1>relaxed</h1></body></html>`), "")
	if err != nil {
		t.Fatalf("runNavigate after relax: %v", err)
	}
	if !strings.Contains(out, "relaxed") {
		t.Fatalf("navigate after relax should still work, got:\n%s", out)
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

func waitForNetworkEvidence(t *testing.T, ctx context.Context, sess *Session, query string) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	tool := NetworkSearchTool(sess)
	args := []byte(`{"query":"` + query + `","max_results":5}`)
	var last string
	for time.Now().Before(deadline) {
		out, err := tool.Execute(ctx, args)
		if err != nil {
			t.Fatalf("browser_network: %v", err)
		}
		last = out
		if strings.Contains(out, "MATCHES:") && !strings.Contains(out, "MATCHES: none") {
			return out
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for browser_network evidence for %q; last output:\n%s", query, last)
	return ""
}

func firstNetworkRef(out string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[0] == "-" && strings.HasPrefix(fields[1], "n") {
			return fields[1]
		}
	}
	return ""
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
