package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

func newTestConv(t *testing.T) *Conversation {
	t.Helper()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	c, err := OpenConversationAt(path)
	if err != nil {
		t.Fatalf("open conversation: %v", err)
	}
	return c
}

// newTestStore returns a FileMemoryStore wired to a temp dir with
// tight caps suitable for loop-side tests. The internal/memory
// package has its own copy with more knobs; this is the minimal
// helper for the root package's tests.
func newTestStore(t *testing.T) *memory.FileMemoryStore {
	t.Helper()
	dir := t.TempDir()
	s := memory.NewFileMemoryStore(dir)
	s.UserPath = filepath.Join(dir, "USER.md")
	return s
}

func TestDefaultSystemPromptReflectsRuntimeBudgets(t *testing.T) {
	for _, want := range []string{
		fmt.Sprintf("~%d tool calls", DefaultMaxTurnSteps),
		fmt.Sprintf("After %d tool calls", DefaultMaxTurnSteps/2),
		fmt.Sprintf("past %d calls", DefaultMaxTurnSteps*4/5),
		fmt.Sprintf("~%dKB", MaxToolResultBytesInContext/1024),
	} {
		if !strings.Contains(DefaultSystemPrompt, want) {
			t.Fatalf("DefaultSystemPrompt missing %q:\n%s", want, DefaultSystemPrompt)
		}
	}
}

func TestBaseSystemPromptForSurface(t *testing.T) {
	if got := BaseSystemPromptForSurface(SystemPromptSurface{Builtins: true}); got != DefaultSystemPrompt {
		t.Fatal("builtins surface should use default workspace prompt")
	}
	if got := BaseSystemPromptForSurface(SystemPromptSurface{Memory: true}); got != MemoryOnlySystemPrompt {
		t.Fatal("memory-only surface should use memory-only prompt")
	}
	got := BaseSystemPromptForSurface(SystemPromptSurface{Memory: true, OtherTools: true})
	if got != LimitedToolSystemPrompt {
		t.Fatal("mixed non-builtin surface should use limited-tool prompt")
	}
	if got := BaseSystemPromptForSurface(SystemPromptSurface{}); got != LimitedToolSystemPrompt {
		t.Fatal("empty tool surface should use limited-tool prompt")
	}
}

func TestWithMemorySystemGuidance_AppendsOnce(t *testing.T) {
	base := "be helpful"
	once := WithMemorySystemGuidance(base)
	for _, want := range []string{"Memory retrieval:", "action=list", "action=search", "target=user", "target=memory", "topic=core"} {
		if !strings.Contains(once, want) {
			t.Fatalf("memory guidance missing %q:\n%s", want, once)
		}
	}
	if twice := WithMemorySystemGuidance(once); twice != once {
		t.Fatal("memory guidance should be idempotent")
	}
	if got := WithMemorySystemGuidance(""); !strings.Contains(got, DefaultSystemPrompt) || !strings.Contains(got, "Memory retrieval:") {
		t.Fatalf("empty prompt should fall back to default + memory guidance:\n%s", got)
	}
}

func TestWithExternalResearchSystemGuidance_AppendsOnce(t *testing.T) {
	base := "be helpful"
	surface := externalResearchToolSurface{WebSearch: true, WebFetch: true, Browser: true}
	once := WithExternalResearchSystemGuidance(base, surface)
	for _, want := range []string{"External research:", "web_search", "authoritative", "browser_navigate", "social posts", "dates/freshness"} {
		if !strings.Contains(once, want) {
			t.Fatalf("external research guidance missing %q:\n%s", want, once)
		}
	}
	if twice := WithExternalResearchSystemGuidance(once, surface); twice != once {
		t.Fatal("external research guidance should be idempotent")
	}
	if got := WithExternalResearchSystemGuidance("", surface); !strings.Contains(got, DefaultSystemPrompt) || !strings.Contains(got, "External research:") {
		t.Fatalf("empty prompt should fall back to default + external research guidance:\n%s", got)
	}

	browserOnly := WithExternalResearchSystemGuidance("be helpful", externalResearchToolSurface{Browser: true})
	for _, forbidden := range []string{"web_search", "web_fetch"} {
		if strings.Contains(browserOnly, forbidden) {
			t.Fatalf("browser-only guidance should not mention unavailable %q:\n%s", forbidden, browserOnly)
		}
	}
	for _, want := range []string{"browser_navigate", "browser_snapshot", "unavailable discovery tools"} {
		if !strings.Contains(browserOnly, want) {
			t.Fatalf("browser-only guidance missing %q:\n%s", want, browserOnly)
		}
	}

	webOnly := WithExternalResearchSystemGuidance("be helpful", externalResearchToolSurface{WebSearch: true, WebFetch: true})
	if strings.Contains(webOnly, "browser_navigate") || strings.Contains(webOnly, "browser_snapshot") {
		t.Fatalf("web-only guidance should not mention browser tools:\n%s", webOnly)
	}
}

func TestRegistrySystemPromptComposition(t *testing.T) {
	reg := NewRegistry()
	reg.Add(&Tool{Name: MemoryToolName})
	if got := BaseSystemPromptForRegistry(reg); got != MemoryOnlySystemPrompt {
		t.Fatal("memory-only registry should use memory-only base prompt")
	}
	prompt := WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	if !strings.Contains(prompt, "Memory retrieval:") {
		t.Fatalf("memory registry prompt missing memory guidance:\n%s", prompt)
	}
	for _, forbidden := range []string{"'shell' tool", "read_file", "Session history retrieval:", "External research:", "Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:"} {
		if strings.Contains(prompt, forbidden) {
			t.Fatalf("memory-only registry prompt should not include %q:\n%s", forbidden, prompt)
		}
	}
	emptyPrompt := WithRegistrySystemGuidance("", reg)
	if !strings.Contains(emptyPrompt, "only tool is 'memory'") || !strings.Contains(emptyPrompt, "Memory retrieval:") {
		t.Fatalf("empty prompt should compose memory-only base + guidance:\n%s", emptyPrompt)
	}
	if strings.Contains(emptyPrompt, "'shell' tool") || strings.Contains(emptyPrompt, "read_file") {
		t.Fatalf("empty memory-only prompt should not fall back to default workspace prompt:\n%s", emptyPrompt)
	}

	reg.Add(&Tool{Name: PlanToolName})
	reg.Add(&Tool{Name: SubagentToolName})
	reg.Add(&Tool{Name: FocusedTaskToolName})
	reg.Add(&Tool{Name: SessionSearchToolName})
	reg.Add(&Tool{Name: "web_search"})
	prompt = WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	for _, want := range []string{"Memory retrieval:", "Session history retrieval:", "External research:", "Subagent delegation:", "Focused tasks (run_task):", "Affent plan tool guidance:"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("registry prompt missing %q:\n%s", want, prompt)
		}
	}
	if strings.Count(WithRegistrySystemGuidance(prompt, reg), "Memory retrieval:") != 1 {
		t.Fatal("registry guidance should be idempotent")
	}
	if strings.Count(WithRegistrySystemGuidance(prompt, reg), "Session history retrieval:") != 1 {
		t.Fatal("session search guidance should be idempotent")
	}
	if strings.Count(WithRegistrySystemGuidance(prompt, reg), "External research:") != 1 {
		t.Fatal("external research guidance should be idempotent")
	}
	if strings.Contains(prompt, "Subagent browser delegation:") {
		t.Fatalf("registry prompt without browser tools should not include browser-specific subagent guidance:\n%s", prompt)
	}

	reg.Add(&Tool{Name: "browser_navigate"})
	prompt = WithRegistrySystemGuidance(BaseSystemPromptForRegistry(reg), reg)
	if !strings.Contains(prompt, "Subagent browser delegation:") {
		t.Fatalf("registry prompt with subagent and browser should include browser delegation guidance:\n%s", prompt)
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: MemoryToolName})
	reg.Add(&Tool{Name: "read_file"})
	if got := BaseSystemPromptForRegistry(reg); got != LimitedToolSystemPrompt {
		t.Fatal("memory plus partial file tools must not use the memory-only prompt")
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: SessionSearchToolName})
	emptyPrompt = WithRegistrySystemGuidance("", reg)
	if !strings.Contains(emptyPrompt, "limited-tool runtime") || !strings.Contains(emptyPrompt, "Session history retrieval:") {
		t.Fatalf("empty session-search prompt should compose limited base + guidance:\n%s", emptyPrompt)
	}
	if strings.Contains(emptyPrompt, "'shell' tool") || strings.Contains(emptyPrompt, "read_file") {
		t.Fatalf("empty session-search prompt should not fall back to default workspace prompt:\n%s", emptyPrompt)
	}

	reg = NewRegistry()
	reg.Add(&Tool{Name: "browser_navigate"})
	emptyPrompt = WithRegistrySystemGuidance("", reg)
	if !strings.Contains(emptyPrompt, "limited-tool runtime") || !strings.Contains(emptyPrompt, "External research:") {
		t.Fatalf("empty browser prompt should compose limited base + external research guidance:\n%s", emptyPrompt)
	}
	for _, forbidden := range []string{"web_search", "web_fetch"} {
		if strings.Contains(emptyPrompt, forbidden) {
			t.Fatalf("browser-only registry prompt should not mention unavailable %q:\n%s", forbidden, emptyPrompt)
		}
	}
}

func TestLoopTurnOptionsOverrideToolSurfaceAndPolicies(t *testing.T) {
	baseTools := NewRegistry()
	baseTools.Add(&Tool{Name: "shell"})
	baseTools.Add(&Tool{Name: PlanToolName})
	planOnlyTools := NewRegistry()
	planOnlyTools.Add(&Tool{Name: PlanToolName})

	loop := &Loop{
		Tools:                  baseTools,
		FirstToolPolicy:        &FirstToolPolicy{ToolName: "shell"},
		MaxToolCalls:           8,
		FinalNoToolsOnMaxTurns: false,
	}
	opts := TurnOptions{
		Tools:                  planOnlyTools,
		FirstToolPolicy:        PlanFirstToolPolicy(),
		MaxToolCalls:           2,
		FinalNoToolsOnMaxTurns: true,
	}

	defs := loop.toolDefs(opts)
	if len(defs) != 1 || defs[0].Function.Name != PlanToolName {
		t.Fatalf("turn tool defs = %+v, want only plan", defs)
	}
	if got := loop.activeFirstToolPolicy("draft a plan", opts); got == nil || got.ToolName != PlanToolName {
		t.Fatalf("turn first-tool policy = %+v, want plan", got)
	}
	if got := loop.maxToolCallsForTurn(opts); got != 2 {
		t.Fatalf("turn max tool calls = %d, want 2", got)
	}
	if !loop.finalNoToolsOnMaxTurnsForTurn(opts) {
		t.Fatal("turn should request final no-tool answer on max turns")
	}

	baseDefs := loop.toolDefs(TurnOptions{})
	if len(baseDefs) != 2 {
		t.Fatalf("base tool defs changed = %+v, want original two tools", baseDefs)
	}
	if got := loop.activeFirstToolPolicy("draft a plan", TurnOptions{}); got == nil || got.ToolName != "shell" {
		t.Fatalf("base first-tool policy changed = %+v, want shell", got)
	}
}

func TestEnsureSystemPrompt_EmptyConv_NoMemory(t *testing.T) {
	conv := newTestConv(t)
	l := &Loop{Conv: conv}
	if err := l.EnsureSystemPrompt("custom prompt"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != "custom prompt" {
		t.Fatalf("system message wrong: %+v", msgs[0])
	}
}

func TestConsumeAndPersist_ReasoningOnlyTerminalEmitsMessageDone(t *testing.T) {
	conv := newTestConv(t)
	events := make(chan sse.Event, 8)
	l := &Loop{Conv: conv, Events: events, Log: zerolog.Nop()}

	stream := make(chan StreamEvent, 1)
	stream <- StreamEvent{Finish: &FinishInfo{
		Reason: "stop",
		Final: ChatMessage{
			Role:             "assistant",
			ReasoningContent: "  final answer from reasoning channel  ",
		},
	}}
	close(stream)

	finish, sawText, err := l.consumeAndPersist(context.Background(), "turn-1", stream)
	if err != nil {
		t.Fatal(err)
	}
	if sawText {
		t.Fatal("reasoning-only output must not count as streamed visible text")
	}
	if finish.Final.Content != "final answer from reasoning channel" {
		t.Fatalf("reasoning fallback did not populate visible content: %+v", finish.Final)
	}

	var gotMessageDone string
	var gotThinkingDone string
	for i := 0; i < 2; i++ {
		select {
		case ev := <-events:
			switch ev.Type {
			case sse.TypeMessageDone:
				var p sse.MessageDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatal(err)
				}
				gotMessageDone = p.Text
			case sse.TypeThinkingDone:
				var p sse.ThinkingDonePayload
				if err := json.Unmarshal(ev.Data, &p); err != nil {
					t.Fatal(err)
				}
				gotThinkingDone = p.Text
			}
		default:
			t.Fatal("expected thinking.done and message.done events")
		}
	}
	if gotThinkingDone != "  final answer from reasoning channel  " {
		t.Fatalf("thinking.done changed reasoning payload: %q", gotThinkingDone)
	}
	if gotMessageDone != "final answer from reasoning channel" {
		t.Fatalf("message.done fallback = %q", gotMessageDone)
	}

	msgs := conv.Snapshot()
	if len(msgs) != 1 || msgs[0].Content != "final answer from reasoning channel" {
		t.Fatalf("conversation did not persist fallback visible content: %+v", msgs)
	}
}

func TestEnsureSystemPrompt_EmptyConv_WithMemory(t *testing.T) {
	conv := newTestConv(t)
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "User uses Go 1.22 + sqlc"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("base prompt"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected exactly 1 system message, got %d", len(msgs))
	}
	c := msgs[0].Content
	if !strings.HasPrefix(c, "base prompt") {
		t.Fatalf("system message should start with base prompt: %q", c)
	}
	if !strings.Contains(c, "User uses Go 1.22") {
		t.Fatalf("system message should contain memory entry: %q", c)
	}
	if !strings.Contains(c, "MEMORY") {
		t.Fatalf("system message should contain memory header: %q", c)
	}
}

func TestEnsureSystemPrompt_ResumedConv_RewritesCurrentRuntimePrompt(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old prompt\n\nSubagent delegation:\nstale guidance\n\nAffent plan tool guidance:\nstale plan guidance"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv}
	if err := l.EnsureSystemPrompt("new prompt without disabled feature guidance"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("resumed conv must not gain a message, got %d", len(msgs))
	}
	if msgs[0].Content != "new prompt without disabled feature guidance" {
		t.Fatalf("resumed conv must rewrite system msg to current runtime prompt, got %q", msgs[0].Content)
	}
	if strings.Contains(msgs[0].Content, "Subagent delegation:") || strings.Contains(msgs[0].Content, "Affent plan tool guidance:") {
		t.Fatalf("disabled feature guidance leaked after prompt rewrite:\n%s", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Fatalf("user message must survive rewrite, got %+v", msgs[1])
	}
}

func TestEnsureSystemPrompt_ResumedConv_WithMemory_Rewritten(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old base + old memory block"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "assistant", Content: "hello"}); err != nil {
		t.Fatal(err)
	}

	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "Fresh fact for this session"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("fresh base"); err != nil {
		t.Fatal(err)
	}

	msgs := conv.Snapshot()
	if len(msgs) != 3 {
		t.Fatalf("message count must be preserved, got %d", len(msgs))
	}
	if msgs[0].Role != "system" {
		t.Fatalf("first message must remain a system message, got role=%q", msgs[0].Role)
	}
	if !strings.HasPrefix(msgs[0].Content, "fresh base") {
		t.Fatalf("system msg must start with new base prompt, got %q", msgs[0].Content)
	}
	if !strings.Contains(msgs[0].Content, "Fresh fact for this session") {
		t.Fatalf("system msg must include current memory entry, got %q", msgs[0].Content)
	}
	if msgs[1].Role != "user" || msgs[1].Content != "hi" {
		t.Fatalf("user message must survive rewrite, got %+v", msgs[1])
	}
	if msgs[2].Role != "assistant" {
		t.Fatalf("assistant message must survive rewrite, got %+v", msgs[2])
	}
}

func TestEnsureSystemPrompt_ResumedConv_WithMemory_AlreadyEqual_NoOp(t *testing.T) {
	conv := newTestConv(t)
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "stable fact"); err != nil {
		t.Fatal(err)
	}
	// Compute what EnsureSystemPrompt would produce and pre-seed the
	// conversation with exactly that.
	want := "base" + "\n\n" + mem.Snapshot()
	if err := conv.Append(ChatMessage{Role: "system", Content: want}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "earlier"}); err != nil {
		t.Fatal(err)
	}

	// Capture file mtime to assert no Replace happened.
	path := conv.path
	st1, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}

	l := &Loop{Conv: conv, Memory: mem}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	st2, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Fatalf("expected no-op when system msg already equals composition; file was rewritten")
	}
	msgs := conv.Snapshot()
	if msgs[0].Content != want {
		t.Fatalf("system message changed unexpectedly")
	}
}

func TestEnsureSystemPrompt_ProjectContext_EmptyConv(t *testing.T) {
	conv := newTestConv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("Project uses Go 1.22"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	c := msgs[0].Content
	if !strings.HasPrefix(c, "base") {
		t.Fatalf("system msg should start with base: %q", c)
	}
	if !strings.Contains(c, "PROJECT CONTEXT") || !strings.Contains(c, "Project uses Go 1.22") {
		t.Fatalf("project context missing:\n%s", c)
	}
}

func TestEnsureSystemPrompt_ProjectContextPlusMemory_Order(t *testing.T) {
	conv := newTestConv(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("user-authored fact"), 0o644); err != nil {
		t.Fatal(err)
	}
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "agent-authored fact"); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir, Memory: mem}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	c := conv.Snapshot()[0].Content

	basePos := strings.Index(c, "base")
	projPos := strings.Index(c, "user-authored fact")
	memPos := strings.Index(c, "agent-authored fact")
	if basePos < 0 || projPos < 0 || memPos < 0 {
		t.Fatalf("missing pieces in composed prompt:\n%s", c)
	}
	if !(basePos < projPos && projPos < memPos) {
		t.Fatalf("expected order base → project-context → memory; got positions %d %d %d",
			basePos, projPos, memPos)
	}
}

func TestEnsureSystemPrompt_ProjectContext_ResumeRewrites(t *testing.T) {
	conv := newTestConv(t)
	if err := conv.Append(ChatMessage{Role: "system", Content: "old prompt without project context"}); err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "hi"}); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte("freshly added project rule"), 0o644); err != nil {
		t.Fatal(err)
	}
	l := &Loop{Conv: conv, ProjectContextDir: dir}
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages preserved, got %d", len(msgs))
	}
	if !strings.Contains(msgs[0].Content, "freshly added project rule") {
		t.Fatalf("project context not refreshed on resume:\n%s", msgs[0].Content)
	}
}

func TestEnsureSystemPrompt_ProjectContext_DirEmptyOrMissing_NoOp(t *testing.T) {
	conv := newTestConv(t)
	l := &Loop{Conv: conv, ProjectContextDir: t.TempDir()} // dir exists but no files
	if err := l.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if got := conv.Snapshot()[0].Content; got != "base" {
		t.Fatalf("with no project files, system msg should equal base, got %q", got)
	}
}

func TestEnsureSystemPrompt_SnapshotLiveAcrossSessions(t *testing.T) {
	// One store, two sessions: each session's system message reflects
	// store state at that session's start.
	mem := newTestStore(t)
	if _, err := mem.Add(memory.TargetMemory, "", "session-1 fact"); err != nil {
		t.Fatal(err)
	}

	conv1 := newTestConv(t)
	l1 := &Loop{Conv: conv1, Memory: mem}
	if err := l1.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(conv1.Snapshot()[0].Content, "session-1 fact") {
		t.Fatalf("session 1 system msg missing the fact")
	}

	if _, err := mem.Add(memory.TargetMemory, "", "session-2 fact"); err != nil {
		t.Fatal(err)
	}
	conv2 := newTestConv(t)
	l2 := &Loop{Conv: conv2, Memory: mem}
	if err := l2.EnsureSystemPrompt("base"); err != nil {
		t.Fatal(err)
	}
	sys2 := conv2.Snapshot()[0].Content
	if !strings.Contains(sys2, "session-1 fact") || !strings.Contains(sys2, "session-2 fact") {
		t.Fatalf("session 2 system msg must reflect current store state, got %q", sys2)
	}

	// And session 1's prompt must NOT have been retroactively changed.
	if strings.Contains(conv1.Snapshot()[0].Content, "session-2 fact") {
		t.Fatalf("session 1 prompt must not see session-2 fact retroactively")
	}
}

// TestTruncateForContext_UTF8Safe verifies the helper that clamps
// oversized tool results to the in-context budget doesn't split a
// multi-byte UTF-8 rune. Before the fix it byte-sliced the input at
// the raw `max` offset; if that offset landed inside a Cyrillic /
// Greek / emoji rune the model received invalid UTF-8.
func TestTruncateForContext_UTF8Safe(t *testing.T) {
	// Each Cyrillic rune is 2 UTF-8 bytes. Sweeping all sub-rune
	// offsets exercises both the "lands mid-rune" and "lands on
	// boundary" paths.
	in := "приветприветпривет"
	for n := 1; n < len(in); n++ {
		out := truncateForContext(in, n)
		// truncateForContext appends a banner starting with "\n\n[...";
		// the prefix is everything before that.
		prefix := strings.SplitN(out, "\n\n[", 2)[0]
		if !utf8.ValidString(prefix) {
			t.Fatalf("truncateForContext(_, %d) produced invalid UTF-8 prefix: %q", n, prefix)
		}
	}
}

// TestPublish_NilEventsIsSilent pins the no-allocation, no-log path
// when an caller opts out of the event stream by leaving
// Loop.Events nil. Pre-fix the publish call hit `case nil <- ev:
// default:` which never proceeds, so every event triggered a
// misleading "event channel full" warning.
func TestPublish_NilEventsIsSilent(t *testing.T) {
	var buf strings.Builder
	loop := &Loop{
		Log:    zerolog.New(&buf),
		Events: nil,
	}
	// Spam a batch of varied events; none of them should log or panic.
	for i := 0; i < 50; i++ {
		loop.publish("message.delta", map[string]any{"delta": "x"})
		loop.publish("turn.end", map[string]any{"reason": "completed"})
	}
	if strings.Contains(buf.String(), "channel full") {
		t.Fatalf("nil Events must not produce \"channel full\" logs: %s", buf.String())
	}
	if buf.Len() != 0 {
		t.Fatalf("nil Events must produce no log output, got %q", buf.String())
	}
}

// TestPreviewN_UTF8Safe covers the event-bus preview path the same way.
func TestPreviewN_UTF8Safe(t *testing.T) {
	in := "héllo wörld" // 'é' and 'ö' are each 2 bytes
	for n := 1; n < len(in); n++ {
		out := previewN(in, n)
		cut := strings.TrimSuffix(out, "...")
		if !utf8.ValidString(cut) {
			t.Fatalf("previewN(%q, %d) produced invalid UTF-8 prefix: %q", in, n, cut)
		}
	}
}

func TestLoopToolResultContextCapsByTool(t *testing.T) {
	loop := &Loop{}
	cases := map[string]int{
		"read_file":           12 * 1024,
		"shell":               6 * 1024,
		MemoryToolName:        4 * 1024,
		SessionSearchToolName: 4 * 1024,
		"list_files":          4 * 1024,
		"edit_file":           2 * 1024,
		"unknown":             MaxToolResultBytesInContext,
	}
	for tool, want := range cases {
		if got := loop.toolResultMaxBytesInContextFor(tool); got != want {
			t.Fatalf("%s cap = %d, want %d", tool, got, want)
		}
	}
	loop.ToolResultMaxBytesInContext = 123
	if got := loop.toolResultMaxBytesInContextFor("read_file"); got != 123 {
		t.Fatalf("explicit cap should win, got %d", got)
	}
}
