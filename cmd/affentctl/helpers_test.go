package main

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/eventlog"
	"github.com/affinefoundation/affent/internal/executor"
	"github.com/affinefoundation/affent/internal/sse"
)

// TestScanLog_CountAndPreview pins the JSONL scan that feeds
// `affentctl sessions`: each line is a message, the FIRST user
// message becomes the preview, malformed lines are skipped (not
// fatal). A regression that mis-counts or picks the wrong line
// would show stale / confusing data in the sessions list.
func TestScanLog_CountAndPreview(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	lines := []string{
		`{"role":"system","content":"be helpful"}`,
		`{"role":"user","content":"first user message"}`,
		`{this is malformed json — must be skipped, not crash}`,
		`{"role":"assistant","content":"reply"}`,
		`{"role":"user","content":"second user, ignored for preview"}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	count, preview := scanLog(path)
	// Count = total lines scanned (Scanner skips empty lines + the
	// malformed one is still ONE line as far as Scanner is concerned).
	// The function increments on every Scan, so 5 lines = 5.
	if count != 5 {
		t.Errorf("count = %d, want 5", count)
	}
	if !strings.Contains(preview, "first user message") {
		t.Errorf("preview = %q, want 'first user message'", preview)
	}
}

// TestScanLog_MissingFileReturnsZero pins the silent-fail-on-open
// behavior — sessionsCmd renders the listing best-effort and
// shouldn't crash when a file disappears between ReadDir and scan.
func TestScanLog_MissingFileReturnsZero(t *testing.T) {
	count, preview := scanLog("/no/such/path.jsonl")
	if count != 0 || preview != "" {
		t.Errorf("missing file: got (%d, %q), want (0, \"\")", count, preview)
	}
}

// TestOneLine_TrimAndTruncate pins three behaviors of the preview
// formatter: newlines collapse to spaces (so multi-line user input
// stays on one tabular row), trailing whitespace stripped, max-
// length adds an ellipsis at a UTF-8-safe boundary.
func TestOneLine_TrimAndTruncate(t *testing.T) {
	cases := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"short stays untouched", "hello", 80, "hello"},
		{"newlines become spaces", "line1\nline2\nline3", 80, "line1 line2 line3"},
		{"leading/trailing space stripped", "   hello   ", 80, "hello"},
		{"over max gets ellipsis", "12345678901234567890", 10, "123456789…"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := oneLine(c.in, c.max); got != c.want {
				t.Errorf("oneLine(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
		})
	}
}

// TestMostRecentSession_PicksByMtime pins the --continue resolution.
// Multiple .jsonl files exist; the one with the newest mtime wins.
// Non-jsonl files are ignored.
func TestMostRecentSession_PicksByMtime(t *testing.T) {
	dir := t.TempDir()

	// Three session files, plus one decoy non-jsonl.
	mk := func(name string, ageHours int) {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		when := time.Now().Add(-time.Duration(ageHours) * time.Hour)
		if err := os.Chtimes(p, when, when); err != nil {
			t.Fatal(err)
		}
	}
	mk("old.jsonl", 48)
	mk("middle.jsonl", 24)
	mk("newest.jsonl", 1)
	mk("ignored.txt", 0) // not .jsonl — must be skipped

	got, err := mostRecentSession(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got != "newest" {
		t.Errorf("got %q, want newest", got)
	}
}

// TestMostRecentSession_EmptyDir pins the no-sessions case: returns
// ("", nil) without erroring. sessionsCmd / --continue handle empty
// by starting fresh.
func TestMostRecentSession_EmptyDir(t *testing.T) {
	got, err := mostRecentSession(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("empty dir should return empty sid; got %q", got)
	}
}

// TestMostRecentSession_NonexistentDir pins the os.IsNotExist branch:
// the convDir hasn't been created yet (first run in a workspace),
// return ("", nil) cleanly.
func TestMostRecentSession_NonexistentDir(t *testing.T) {
	got, err := mostRecentSession(filepath.Join(t.TempDir(), "never-created"))
	if err != nil {
		t.Fatal(err)
	}
	if got != "" {
		t.Errorf("missing dir should return empty sid; got %q", got)
	}
}

// TestOpenTrace_SpecialSpecs pins the two reserved trace specs: ""
// → stderr, "-" → stdout. Both are unbuffered destinations; the
// returned close func must be a no-op (closing stderr/stdout would
// silently break the rest of the program).
func TestOpenTrace_SpecialSpecs(t *testing.T) {
	for _, spec := range []string{"", "-"} {
		w, closer, err := openTrace(spec, false)
		if err != nil {
			t.Fatalf("openTrace(%q) err: %v", spec, err)
		}
		if w == nil {
			t.Fatalf("openTrace(%q) writer is nil", spec)
		}
		// Closer must be safe to call and idempotent.
		if err := closer(); err != nil {
			t.Errorf("openTrace(%q) closer err: %v", spec, err)
		}
	}
}

// TestOpenTrace_FileAppendVsTruncate pins the resume contract:
// fresh sessions truncate the trace file (so re-running on the
// same workspace doesn't mix old + new JSONL); resumed sessions
// append so the trace keeps growing alongside the conv log.
func TestOpenTrace_FileAppendVsTruncate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "trace.jsonl")
	if err := os.WriteFile(path, []byte("existing\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Truncate mode (resume=false).
	w, closer, err := openTrace(path, false)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "fresh\n")
	_ = closer()
	got, _ := os.ReadFile(path)
	if string(got) != "fresh\n" {
		t.Errorf("truncate mode should overwrite; got %q", got)
	}

	// Append mode (resume=true).
	w, closer, err = openTrace(path, true)
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(w, "more\n")
	_ = closer()
	got, _ = os.ReadFile(path)
	if string(got) != "fresh\nmore\n" {
		t.Errorf("append mode should preserve prior content; got %q", got)
	}
}

func TestWriteTraceMeta(t *testing.T) {
	var buf bytes.Buffer
	if err := eventlog.NewRecorder(&buf, eventlog.Options{}).WriteMeta(); err != nil {
		t.Fatal(err)
	}
	var ev sse.Event
	if err := json.Unmarshal(buf.Bytes(), &ev); err != nil {
		t.Fatalf("decode trace meta: %v\n%s", err, buf.String())
	}
	if ev.Type != sse.TypeTraceMeta {
		t.Fatalf("Type = %q, want %q", ev.Type, sse.TypeTraceMeta)
	}
	var payload sse.TraceMetaPayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		t.Fatalf("decode trace meta payload: %v", err)
	}
	if payload.SchemaVersion != sse.TraceSchemaVersion {
		t.Fatalf("SchemaVersion = %d, want %d", payload.SchemaVersion, sse.TraceSchemaVersion)
	}
}

// TestSummarizeArgs_PrefersInformativeKeys pins the chat REPL's
// per-tool preview formatter. command / path / name / id are
// surfaced before generic JSON because they're the most-revealing
// piece of context for shell / read_file / write_file / etc.
func TestSummarizeArgs_PrefersInformativeKeys(t *testing.T) {
	cases := []struct {
		name string
		args map[string]any
		want string
	}{
		{"empty → {}", nil, "{}"},
		{"command wins", map[string]any{"command": "ls -la", "cwd": "/tmp"}, `command="ls -la"`},
		{"path wins when no command", map[string]any{"path": "/etc/hosts"}, `path="/etc/hosts"`},
		{"name wins next", map[string]any{"name": "myfunc"}, `name="myfunc"`},
		{"id wins last", map[string]any{"id": "abc123"}, `id="abc123"`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := summarizeArgs(c.args); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}
}

// TestSummarizeArgs_FallsBackToJSON pins the catch-all: when no
// informative key is present, the whole map is marshaled.
func TestSummarizeArgs_FallsBackToJSON(t *testing.T) {
	got := summarizeArgs(map[string]any{"action": "add", "topic": "ops"})
	if !strings.Contains(got, "action") || !strings.Contains(got, "add") {
		t.Errorf("fallback should include all keys; got %q", got)
	}
	if !strings.HasPrefix(got, "{") {
		t.Errorf("fallback should be JSON-shaped; got %q", got)
	}
}

// TestSummarizeArgs_TruncatesLongCommand pins the 80-char cap.
// A typical shell command can be hundreds of chars; the chat REPL
// truncates so the display line stays readable.
func TestSummarizeArgs_TruncatesLongCommand(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := summarizeArgs(map[string]any{"command": long})
	if !strings.HasSuffix(got, `..."`) {
		t.Errorf("long command should end with truncation marker + closing quote; got %q", got)
	}
	if len(got) > 95 {
		t.Errorf("truncated output should be ~85 chars max; got %d", len(got))
	}
}

// TestHandleSlashUsageReportsAccumulatedTokens pins the /usage
// command's contract: it must read the bundle's running totals
// (drainInteractive accumulates them on every TypeUsage event)
// and print a single human-readable line including session id,
// turn count, input/output, and the computed total. A regression
// where the print drops a field would silently hide spend from
// the user inside the REPL.
func TestHandleSlashUsageReportsAccumulatedTokens(t *testing.T) {
	// Capture os.Stderr to inspect the printed line. The /usage path
	// prints to Stderr via fmt.Fprintf, so we swap the global for the
	// duration of the call.
	r, w, _ := os.Pipe()
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	b := &loopBundle{
		loop:         &agent.Loop{},
		sessionID:    "sess_usage_test",
		turnsSeen:    3,
		inputTokens:  1000,
		outputTokens: 250,
	}
	cont, exit := handleSlash("/usage", b)
	if !cont || exit != 0 {
		t.Errorf("/usage should keep REPL alive with exit=0; got (%v, %d)", cont, exit)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	got := string(out)

	for _, want := range []string{
		"sess_usage_test",
		"3 turn(s)",
		"input=1000",
		"output=250",
		"total=1250",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("/usage output missing %q:\n%s", want, got)
		}
	}
}

func TestHandleSlashPlanPrintsCurrentSessionPlan(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, "sess_plan_test"), []byte(`{"version":1,"updated_at":"2026-05-23T00:00:00Z","steps":[{"text":"ship plan","status":"in_progress","evidence":["cmd/affentctl/cmd_chat.go"],"note":"keep it short"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	b := &loopBundle{
		loop:      &agent.Loop{},
		sessionID: "sess_plan_test",
		workspace: workspace,
	}
	cont, exit := handleSlash("/plan", b)
	if !cont || exit != 0 {
		t.Errorf("/plan should keep REPL alive with exit=0; got (%v, %d)", cont, exit)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	got := string(out)
	for _, want := range []string{
		"plan for session sess_plan_test",
		"(updated 2026-05-23T00:00:00Z)",
		"1. [in_progress] ship plan",
		"evidence: cmd/affentctl/cmd_chat.go",
		"note: keep it short",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("/plan output missing %q:\n%s", want, got)
		}
	}
}

func TestHandleSlashPlanReportsMissingPlan(t *testing.T) {
	workspace := t.TempDir()
	r, w, _ := os.Pipe()
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	b := &loopBundle{
		loop:      &agent.Loop{},
		sessionID: "sess_no_plan",
		workspace: workspace,
	}
	cont, exit := handleSlash("/plan", b)
	if !cont || exit != 0 {
		t.Errorf("/plan should keep REPL alive with exit=0; got (%v, %d)", cont, exit)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if got := string(out); !strings.Contains(got, "no active plan for session sess_no_plan") {
		t.Fatalf("/plan missing output = %s", got)
	}
}

func TestHandleSlashPlanClearRemovesCurrentSessionPlan(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := localSessionPlanPath(convDir, "sess_clear_plan")
	if err := os.WriteFile(path, []byte(`{"version":1,"steps":[{"text":"stale"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	r, w, _ := os.Pipe()
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	b := &loopBundle{
		loop:      &agent.Loop{},
		sessionID: "sess_clear_plan",
		workspace: workspace,
	}
	cont, exit := handleSlash("/plan clear", b)
	if !cont || exit != 0 {
		t.Errorf("/plan clear should keep REPL alive with exit=0; got (%v, %d)", cont, exit)
	}
	_ = w.Close()
	out, _ := io.ReadAll(r)
	if got := string(out); !strings.Contains(got, "cleared plan for session sess_clear_plan") {
		t.Fatalf("/plan clear output = %s", got)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("plan file err = %v, want removed", err)
	}
}

func TestFormatSessionPlanForChatFallsBackToRawJSON(t *testing.T) {
	raw := json.RawMessage(`{"version":2,"items":[{"title":"future schema"}]}`)
	got := formatSessionPlanForChat("sess_future", raw)
	if got != string(raw) {
		t.Fatalf("fallback = %s, want raw JSON", got)
	}
}

func TestEmitPlanChangeShowsUpdatedPlanSummary(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b := &loopBundle{
		sessionID: "sess_plan_delta",
		workspace: workspace,
	}
	before := currentSessionPlanSummary(b)
	if err := os.WriteFile(localSessionPlanPath(convDir, b.sessionID), []byte(`{"version":1,"steps":[{"text":"inspect runtime behavior","status":"in_progress"},{"text":"patch feedback"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	after := currentSessionPlanSummary(b)

	out := captureStderr(t, func() {
		emitPlanChange(before, after)
	})
	if !strings.Contains(out, "[plan] plan:0/2:active - step 1: inspect runtime behavior") {
		t.Fatalf("plan change output = %s", out)
	}

	out = captureStderr(t, func() {
		emitPlanChange(after, after)
	})
	if out != "" {
		t.Fatalf("unchanged plan should be quiet, got %s", out)
	}
}

func TestFormatPlanChangeLineReportsClearedPlan(t *testing.T) {
	if got := formatPlanChangeLine(currentSessionPlanSummary(&loopBundle{workspace: t.TempDir(), sessionID: "missing"})); got != "[plan] cleared" {
		t.Fatalf("cleared line = %q", got)
	}
}

func TestPrintStartupPlanSummaryShowsExistingPlan(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b := &loopBundle{
		sessionID: "sess_resume_plan",
		workspace: workspace,
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, b.sessionID), []byte(`{"version":1,"steps":[{"text":"continue implementation","status":"in_progress"},{"text":"run tests"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStderr(t, func() {
		printStartupPlanSummary(b)
	})
	if !strings.Contains(out, "[plan] plan:0/2:active - step 1: continue implementation") {
		t.Fatalf("startup plan output = %s", out)
	}
}

func TestFormatExistingPlanLineSkipsMissingPlan(t *testing.T) {
	if got := formatExistingPlanLine(currentSessionPlanSummary(&loopBundle{workspace: t.TempDir(), sessionID: "missing"})); got != "" {
		t.Fatalf("missing startup plan line = %q, want quiet", got)
	}
}

func TestFormatExistingPlanLineReportsDonePlan(t *testing.T) {
	workspace := t.TempDir()
	convDir := filepath.Join(workspace, ".affentctl")
	if err := os.MkdirAll(convDir, 0o755); err != nil {
		t.Fatal(err)
	}
	b := &loopBundle{
		sessionID: "sess_done_plan",
		workspace: workspace,
	}
	if err := os.WriteFile(localSessionPlanPath(convDir, b.sessionID), []byte(`{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"commit","status":"completed"}]}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := formatExistingPlanLine(currentSessionPlanSummary(b)); got != "[plan] plan:2/2:done" {
		t.Fatalf("done plan line = %q", got)
	}
}

// TestHandleSlash pins the REPL slash-command dispatcher. /exit and
// its aliases must return (continue=false, exit=0); /help / /sid /
// /plan / /plan clear / /cancel / unknown must keep the REPL alive. Casing and trailing
// whitespace must be tolerated (operators often type "  /Exit "
// without thinking). A regression that breaks any of these
// strands the user in a session they can't quit cleanly.
func TestHandleSlash(t *testing.T) {
	b := &loopBundle{loop: &agent.Loop{}, sessionID: "sess_test"}
	cases := []struct {
		in     string
		want   bool // continue
		wantEx int
	}{
		{"/exit", false, 0},
		{"/quit", false, 0},
		{"/q", false, 0},
		{"/EXIT", false, 0}, // case-insensitive
		{"  /exit  ", false, 0},
		{"/help", true, 0},
		{"/h", true, 0},
		{"/?", true, 0},
		{"/sid", true, 0},
		{"/plan", true, 0},       // current persisted plan, if any
		{"/plan clear", true, 0}, // remove current persisted plan, if any
		{"/cancel", true, 0},     // Loop with nil cancelFn → Cancel is a no-op
		{"/usage", true, 0},      // running totals; reads loopBundle counters
		{"/bogus", true, 0},      // unknown still keeps REPL alive
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			got, ex := handleSlash(c.in, b)
			if got != c.want || ex != c.wantEx {
				t.Errorf("handleSlash(%q) = (%v, %d), want (%v, %d)", c.in, got, ex, c.want, c.wantEx)
			}
		})
	}
}

// TestResolveSessionID pins the three branches that decide which
// session a `run` / `chat` invocation operates on:
//   - explicit --session-id existing → reuse + resumed=true
//   - explicit --session-id new → reuse the name + resumed=false
//   - --continue with no prior session → error pointing at the dir
//   - --continue with prior sessions → most-recent + resumed=true
//   - default (no flags) → fresh "run_<uuid>" + resumed=false
func TestResolveSessionID(t *testing.T) {
	t.Run("explicit existing", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "named.jsonl")
		if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		sid, resumed, err := resolveSessionID(dir, "named", false)
		if err != nil || sid != "named" || !resumed {
			t.Errorf("got (%q, %v, %v), want (named, true, nil)", sid, resumed, err)
		}
	})
	t.Run("explicit new", func(t *testing.T) {
		dir := t.TempDir()
		sid, resumed, err := resolveSessionID(dir, "fresh", false)
		if err != nil || sid != "fresh" || resumed {
			t.Errorf("got (%q, %v, %v), want (fresh, false, nil)", sid, resumed, err)
		}
	})
	t.Run("continueLast with no priors", func(t *testing.T) {
		dir := t.TempDir()
		_, _, err := resolveSessionID(dir, "", true)
		if err == nil || !strings.Contains(err.Error(), "--continue") {
			t.Errorf("expected --continue error, got %v", err)
		}
	})
	t.Run("continueLast picks most recent", func(t *testing.T) {
		dir := t.TempDir()
		older := filepath.Join(dir, "old.jsonl")
		newer := filepath.Join(dir, "new.jsonl")
		for _, p := range []string{older, newer} {
			if err := os.WriteFile(p, []byte("{}"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		past := time.Now().Add(-1 * time.Hour)
		if err := os.Chtimes(older, past, past); err != nil {
			t.Fatal(err)
		}
		sid, resumed, err := resolveSessionID(dir, "", true)
		if err != nil || sid != "new" || !resumed {
			t.Errorf("got (%q, %v, %v), want (new, true, nil)", sid, resumed, err)
		}
	})
	t.Run("default fresh", func(t *testing.T) {
		dir := t.TempDir()
		sid, resumed, err := resolveSessionID(dir, "", false)
		if err != nil || resumed {
			t.Errorf("got (%q, %v, %v); want non-empty, false, nil", sid, resumed, err)
		}
		if !strings.HasPrefix(sid, "run_") {
			t.Errorf("default sid should start with run_; got %q", sid)
		}
	})
}

// TestBuildExecutor pins the post-sandbox --executor shapes:
// empty / "local" → LocalExecutor; "docker:<cid>" →
// DockerExecExecutor; anything else (including bare "docker:")
// fails so a typo doesn't silently fall back to LocalExecutor on
// a host where the operator deliberately wanted container-only
// shell.
func TestBuildExecutor(t *testing.T) {
	t.Run("empty → local", func(t *testing.T) {
		got, err := buildExecutor("", "sess", "/tmp")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*executor.LocalExecutor); !ok {
			t.Errorf("empty spec should produce LocalExecutor; got %T", got)
		}
	})
	t.Run("local → local", func(t *testing.T) {
		got, err := buildExecutor("local", "sess", "/tmp")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := got.(*executor.LocalExecutor); !ok {
			t.Errorf("'local' should produce LocalExecutor; got %T", got)
		}
	})
	t.Run("docker:<cid> → docker", func(t *testing.T) {
		got, err := buildExecutor("docker:abc123", "sess", "/tmp")
		if err != nil {
			t.Fatal(err)
		}
		dockerExec, ok := got.(*executor.DockerExecExecutor)
		if !ok {
			t.Fatalf("'docker:abc123' should produce DockerExecExecutor; got %T", got)
		}
		if dockerExec.DefaultCwd != "/tmp" {
			t.Errorf("DockerExecExecutor.DefaultCwd = %q, want /tmp", dockerExec.DefaultCwd)
		}
	})
	t.Run("bare docker: errors", func(t *testing.T) {
		_, err := buildExecutor("docker:", "sess", "/tmp")
		if err == nil || !strings.Contains(err.Error(), "container id") {
			t.Errorf("'docker:' must error mentioning container id; got %v", err)
		}
	})
	t.Run("invalid docker container name errors", func(t *testing.T) {
		_, err := buildExecutor("docker:bad/name", "sess", "/tmp")
		if err == nil || !strings.Contains(err.Error(), "--executor docker may contain only") {
			t.Errorf("invalid docker executor should error before Docker use; got %v", err)
		}
	})
	t.Run("unknown spec errors", func(t *testing.T) {
		_, err := buildExecutor("kata:foo", "sess", "/tmp")
		if err == nil || !strings.Contains(err.Error(), "unknown") || !strings.Contains(err.Error(), "sandbox") {
			t.Errorf("unknown spec should error with valid executor values; got %v", err)
		}
	})
}
