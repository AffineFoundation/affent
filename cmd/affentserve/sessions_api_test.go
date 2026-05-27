package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/rs/zerolog"
)

func TestHandleSessionCreate_ExplicitIDAndDetail(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	pool.cfg.EnableMemory = true
	pool.cfg.EnableWeb = true
	pool.cfg.EnableSubagent = true
	pool.cfg.SubagentMaxDepth = 3
	pool.cfg.EnableFocusedTasks = true
	h := handleSessionsCollection(pool)

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"client-one"}`))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", got, w.Body.String())
	}
	var created sessionCreateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.Session.ID != "client-one" || !created.Session.Active || !created.Session.Durable {
		t.Fatalf("created session = %+v, want active durable client-one", created.Session)
	}
	if created.Session.WorkspacePath == "" {
		t.Fatalf("created session missing workspace path: %+v", created.Session)
	}
	if created.Session.WorkspaceLabel != filepath.Base(created.Session.WorkspacePath) {
		t.Fatalf("created workspace label = %q, want basename of %q", created.Session.WorkspaceLabel, created.Session.WorkspacePath)
	}
	assertSessionCapabilities(t, created.Session.Capabilities, sessionCapabilities{
		Builtins:         true,
		WorkspaceTools:   []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", agent.SymbolContextToolName, "repo_search"},
		SkillInstall:     true,
		Plan:             true,
		Memory:           true,
		SessionSearch:    true,
		SymbolContext:    true,
		RepoSearch:       true,
		Web:              true,
		Subagent:         true,
		SubagentMaxDepth: 3,
		FocusedTasks:     true,
		FocusedTaskProfiles: []string{
			"recall",
			"explore",
			"web_extract",
			"research",
			"verify",
			"review",
		},
	})

	r = httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"client-one"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("duplicate create status = %d, want 200; body=%s", got, w.Body.String())
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/client-one", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", got, w.Body.String())
	}
	var detail sessionDetailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode detail: %v", err)
	}
	if detail.Session.ID != "client-one" || !detail.Session.Active || !detail.Session.Durable {
		t.Fatalf("detail session = %+v, want active durable client-one", detail.Session)
	}
	if detail.Session.WorkspacePath != created.Session.WorkspacePath || detail.Session.WorkspaceLabel != created.Session.WorkspaceLabel {
		t.Fatalf("detail workspace = %q/%q, want %q/%q", detail.Session.WorkspacePath, detail.Session.WorkspaceLabel, created.Session.WorkspacePath, created.Session.WorkspaceLabel)
	}
	assertSessionCapabilities(t, detail.Session.Capabilities, *created.Session.Capabilities)
}

func TestHandleSessionCreate_GeneratedID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", http.NoBody)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", got, w.Body.String())
	}
	var resp sessionCreateResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Session.ID == "" || !resp.Session.Active || !resp.Session.Durable {
		t.Fatalf("generated session = %+v, want id active durable", resp.Session)
	}
}

func TestHandleSessionCreate_RejectsInvalidID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions", bytes.NewBufferString(`{"session_id":"../escape"}`))
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionList_MergesActiveAndDurableSessions(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	active, err := pool.GetOrCreate("active")
	if err != nil {
		t.Fatalf("GetOrCreate active: %v", err)
	}
	if err := active.conv.Append(agent.ChatMessage{Role: "user", Content: "inspect the webui session list"}); err != nil {
		t.Fatalf("append active conversation: %v", err)
	}
	createDurableSessionDir(t, pool, "archived")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("archived"), "conversation.jsonl"), []byte(
		`{"role":"system","content":"test"}`+"\n"+
			`{"role":"user","content":"old task"}`+"\n"+
			`{"role":"assistant","content":"done"}`+"\n"+
			`{"role":"user","content":"resume archived work"}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write archived conversation: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("sessions = %+v, want 2", resp.Sessions)
	}
	if resp.Sessions[0].ID != "active" || !resp.Sessions[0].Active || !resp.Sessions[0].Durable {
		t.Fatalf("first session = %+v, want active durable active", resp.Sessions[0])
	}
	if resp.Sessions[0].LatestUserMessage != "inspect the webui session list" {
		t.Fatalf("active latest user = %q", resp.Sessions[0].LatestUserMessage)
	}
	if resp.Sessions[0].TopicUserMessage != "inspect the webui session list" {
		t.Fatalf("active topic user = %q", resp.Sessions[0].TopicUserMessage)
	}
	if resp.Sessions[1].ID != "archived" || resp.Sessions[1].Active || !resp.Sessions[1].Durable {
		t.Fatalf("second session = %+v, want durable-only archived", resp.Sessions[1])
	}
	if resp.Sessions[1].LatestUserMessage != "resume archived work" {
		t.Fatalf("archived latest user = %q", resp.Sessions[1].LatestUserMessage)
	}
	if resp.Sessions[1].TopicUserMessage != "old task" {
		t.Fatalf("archived topic user = %q, want old task", resp.Sessions[1].TopicUserMessage)
	}
}

func TestMergeSessionSummariesKeepsActiveLatestUserMessage(t *testing.T) {
	context := &sessionContextSummary{MessageCount: 96, CompactTrigger: 120, CompactPercent: 80, MessagesUntilCompact: 24}
	got := mergeSessionSummaries(
		sessionSummary{
			ID:                "active",
			Active:            true,
			LatestUserMessage: "new in-memory task",
			TopicUserMessage:  "stable active topic",
		},
		sessionSummary{
			ID:                "active",
			Durable:           true,
			LatestUserMessage: "older durable task",
			TopicUserMessage:  "older durable topic",
			Context:           context,
		},
	)
	if got.LatestUserMessage != "new in-memory task" {
		t.Fatalf("latest_user_message = %q, want active in-memory value", got.LatestUserMessage)
	}
	if got.TopicUserMessage != "stable active topic" {
		t.Fatalf("topic_user_message = %q, want active in-memory value", got.TopicUserMessage)
	}
	if !got.Active || !got.Durable {
		t.Fatalf("merged active/durable flags = %+v", got)
	}
	if got.Context != context {
		t.Fatalf("context summary = %+v, want durable context carried over", got.Context)
	}
	got = mergeSessionSummaries(
		sessionSummary{ID: "active", Active: true, WorkspacePath: "/tmp/ws", WorkspaceLabel: "ws"},
		sessionSummary{ID: "active", Durable: true, LastAgentCWD: "subdir"},
	)
	if got.WorkspacePath != "/tmp/ws" || got.WorkspaceLabel != "ws" || got.LastAgentCWD != "subdir" {
		t.Fatalf("merged workspace evidence = %+v", got)
	}
	compactions := &sessionContextCompactionSummary{Count: 1, Reactive: 1, RemovedMessages: 32, SummaryBytes: 2048, LatestReason: "context_overflow", LatestReactive: true}
	got = mergeSessionSummaries(sessionSummary{ID: "active", Active: true}, sessionSummary{ID: "active", Durable: true, ContextCompactions: compactions})
	if got.ContextCompactions != compactions {
		t.Fatalf("context compactions = %+v, want durable compactions carried over", got.ContextCompactions)
	}
}

func TestSummarizeDurableSessionIncludesLatestShellCWD(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "cwd-evidence")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("cwd-evidence"), "events.jsonl"), []byte(
		`{"id":1,"type":"turn.start","data":{"turn_id":"t1"}}`+"\n"+
			`{"id":2,"type":"tool.request","data":{"turn_id":"t1","call_id":"one","tool":"shell","args":{"command":"npm test","cwd":"extras/webui"},"args_truncated":false,"args_bytes":52,"args_omitted_bytes":0,"args_cap_bytes":65536}}`+"\n"+
			`{"id":3,"type":"tool.request","data":{"turn_id":"t1","call_id":"two","tool":"shell","args":{"command":"npm run build","cwd":"."},"args_truncated":false,"args_bytes":48,"args_omitted_bytes":0,"args_cap_bytes":65536}}`+"\n",
	), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "cwd-evidence")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("summarizeDurableSession found=false")
	}
	if summary.LastAgentCWD != "." {
		t.Fatalf("last_agent_cwd = %q, want latest shell cwd '.'", summary.LastAgentCWD)
	}
}

func TestSessionContextSnapshotUsesCompactionTrigger(t *testing.T) {
	got := sessionContextSnapshot(96, Config{CompactTrigger: 120})
	if got.MessageCount != 96 || got.CompactTrigger != 120 || got.CompactPercent != 80 || got.MessagesUntilCompact != 24 {
		t.Fatalf("context snapshot = %+v, want 96/120 at 80%% with 24 remaining", got)
	}
	over := sessionContextSnapshot(130, Config{CompactTrigger: 120})
	if over.CompactPercent != 108 || over.MessagesUntilCompact != 0 {
		t.Fatalf("over-trigger snapshot = %+v, want 108%% and no remaining messages", over)
	}
	def := sessionContextSnapshot(1, Config{})
	if def.CompactTrigger != agent.DefaultSummaryTriggerMsgs {
		t.Fatalf("default trigger = %d, want %d", def.CompactTrigger, agent.DefaultSummaryTriggerMsgs)
	}
}

func TestLatestUserMessageFromConversationFileSkipsOversizedLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	hugeUser := `{"role":"user","content":"` + strings.Repeat("x", maxSessionSummaryLineBytes+1024) + `"}` + "\n"
	body := hugeUser +
		`{"role":"assistant","content":"ignored"}` + "\n" +
		`{"role":"user","content":"final resumable task"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := latestUserMessageFromConversationFile(path)
	if err != nil {
		t.Fatalf("latestUserMessageFromConversationFile: %v", err)
	}
	if got != "final resumable task" {
		t.Fatalf("latest user = %q, want final resumable task", got)
	}
}

func TestLatestUserMessageFromConversationFileReadsBoundedTail(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	body := `{"role":"user","content":"old task outside tail"}` + "\n" +
		`{"role":"assistant","content":"` + strings.Repeat("x", maxSessionSummaryTailBytes+1024) + `"}` + "\n" +
		`{"role":"user","content":"recent task in tail"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := latestUserMessageFromConversationFile(path)
	if err != nil {
		t.Fatalf("latestUserMessageFromConversationFile: %v", err)
	}
	if got != "recent task in tail" {
		t.Fatalf("latest user = %q, want recent task in tail", got)
	}
}

func TestUserMessageSummariesFromConversationFileKeepStableTopic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	body := `{"role":"user","content":"affine 是 Bittensor 的一个子网，请收集信息"}` + "\n" +
		`{"role":"assistant","content":"需要更多步骤"}` + "\n" +
		`{"role":"user","content":"请继续同一个任务。基于已有证据输出报告"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	latest, topic, err := userMessageSummariesFromConversationFile(path)
	if err != nil {
		t.Fatalf("userMessageSummariesFromConversationFile: %v", err)
	}
	if latest != "请继续同一个任务。基于已有证据输出报告" {
		t.Fatalf("latest user = %q, want continuation prompt", latest)
	}
	if topic != "affine 是 Bittensor 的一个子网，请收集信息" {
		t.Fatalf("topic user = %q, want original task", topic)
	}
}

func TestUserMessageSummariesFromMessagesKeepStableTopic(t *testing.T) {
	latest, topic := userMessageSummariesFromMessages([]agent.ChatMessage{
		{Role: "user", Content: "research affine"},
		{Role: "assistant", Content: "not enough steps"},
		{Role: "user", Content: "continue and summarize"},
		{Role: "assistant", Content: "partial report"},
		{Role: "user", Content: "不要再调用任何工具。直接基于本 session 前两轮结果输出最终报告。"},
	})

	if latest != "不要再调用任何工具。直接基于本 session 前两轮结果输出最终报告。" {
		t.Fatalf("latest user = %q, want finalization prompt", latest)
	}
	if topic != "research affine" {
		t.Fatalf("topic user = %q, want original task", topic)
	}
}

func TestSummarizeDurableSessionRestoresTopicFromEventsAfterCompaction(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "compacted-topic")
	dir := pool.sessionDirPath("compacted-topic")

	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(
		`{"role":"system","content":"base"}`+"\n"+
			`{"role":"assistant","content":"Previous conversation summary: researched Affine subnet."}`+"\n"+
			`{"role":"user","content":"请继续同一个任务。基于已有证据输出报告"}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}
	toolRecovery := "file missing\nNext: run rg --files config before retrying\nFailure: kind=not_found"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{TurnID: "t1", Text: "affine 是 Bittensor 的一个子网，请收集信息并向我介绍"})+
			sessionEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{TurnID: "t1", BeforeMessages: 48, AfterMessages: 12, RemovedMessages: 36, Reactive: true, Reason: "context_overflow", SummaryPresent: true, SummaryBytes: 1024})+
			sessionEventLine(t, sse.TypeContextCompact, map[string]any{"turn_id": "t2", "before_messages": 44, "after_messages": 18, "removed_messages": 26, "reactive": false, "reason": "proactive_threshold", "summary_present": false})+
			sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t2", CallID: "c1", ExitCode: 1, ResultSummary: toolRecovery, Result: toolRecovery})+
			sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t2", CallID: "mem1", ResultSummary: `{"ok":true}`, Result: `{"ok":true}`, MemoryUpdate: &sse.MemoryUpdateMeta{
				Action:      "replace",
				Target:      "memory",
				Topic:       "markets",
				Location:    "memory:markets",
				Preview:     "old dashboard rule -> prefer browser network evidence",
				NextPreview: "prefer browser network evidence",
			}})+
			sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{TurnID: "t2", Text: "请继续同一个任务。基于已有证据输出报告"}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "compacted-topic")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestUserMessage != "请继续同一个任务。基于已有证据输出报告" {
		t.Fatalf("latest_user_message = %q, want continuation prompt", summary.LatestUserMessage)
	}
	if summary.TopicUserMessage != "affine 是 Bittensor 的一个子网，请收集信息并向我介绍" {
		t.Fatalf("topic_user_message = %q, want original event-stream task", summary.TopicUserMessage)
	}
	if summary.SummaryTitle != "Affine（Bittensor 子网）" {
		t.Fatalf("summary_title = %q, want original task title", summary.SummaryTitle)
	}
	if summary.LatestRecoveryHint != "run rg --files config before retrying" {
		t.Fatalf("latest_recovery_hint = %q, want actionable tool recovery hint", summary.LatestRecoveryHint)
	}
	if summary.LatestMemoryUpdate == nil ||
		summary.LatestMemoryUpdate.Action != "replace" ||
		summary.LatestMemoryUpdate.Location != "memory:markets" ||
		summary.LatestMemoryUpdate.Preview != "old dashboard rule -> prefer browser network evidence" {
		t.Fatalf("latest_memory_update = %+v, want durable memory preview", summary.LatestMemoryUpdate)
	}
	if summary.ContextCompactions == nil {
		t.Fatal("context_compactions should be summarized from durable events")
	}
	if summary.ContextCompactions.Count != 2 ||
		summary.ContextCompactions.Reactive != 1 ||
		summary.ContextCompactions.RemovedMessages != 62 ||
		summary.ContextCompactions.SummaryBytes != 1024 ||
		summary.ContextCompactions.SummaryMissing != 1 ||
		summary.ContextCompactions.LatestReason != "proactive_threshold" ||
		summary.ContextCompactions.LatestReactive ||
		summary.ContextCompactions.LatestSummaryState != "missing" {
		t.Fatalf("context_compactions = %+v, want durable compaction summary", summary.ContextCompactions)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromConversation(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "resume-repair")
	dir := pool.sessionDirPath("resume-repair")

	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(
		`{"role":"system","content":"base"}`+"\n"+
			`{"role":"user","content":"continue recovered task"}`+"\n"+
			`{"role":"tool","tool_call_id":"c2","name":"web_fetch","content":"(tool result missing on resume; process likely crashed mid-turn)\nFailure: kind=resume_missing_tool_result\nNext: do not assume the tool succeeded; continue from available context and rerun the missing tool only if its result is still essential and safe to repeat."}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "resume-repair")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "do not assume the tool succeeded; continue from available context and rerun the missing tool only if its result is still essential and safe to repeat." {
		t.Fatalf("latest_recovery_hint = %q, want conversation repair hint", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromConversationRepairNote(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "resume-duplicate-repair")
	dir := pool.sessionDirPath("resume-duplicate-repair")

	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(
		`{"role":"system","content":"base"}`+"\n"+
			`{"role":"user","content":"continue recovered task"}`+"\n"+
			`{"role":"user","content":"Recovered invalid tool result during resume.\nFailure: kind=resume_duplicate_tool_result\nNext: use the first matching tool result already in this conversation; do not replay the duplicate unless its evidence is still essential.\nToolCallID: c2\nTool: read_file\nRecoveredPreview: duplicate content"}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "resume-duplicate-repair")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "use the first matching tool result already in this conversation; do not replay the duplicate unless its evidence is still essential." {
		t.Fatalf("latest_recovery_hint = %q, want duplicate repair hint", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromConversationRepairedEvent(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "resume-repair-event")
	dir := pool.sessionDirPath("resume-repair-event")

	toolRecovery := "file missing\nNext: inspect the workspace before retrying\nFailure: kind=not_found"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t1", CallID: "c1", ExitCode: 1, ResultSummary: toolRecovery, Result: toolRecovery})+
			sessionEventLine(t, sse.TypeConversationRepaired, sse.ConversationRepairedPayload{
				SessionID:             "resume-repair-event",
				DuplicateToolResults:  2,
				UnexpectedToolResults: 1,
				FailureKind:           "resume_duplicate_tool_result",
				Next:                  "use the first matching tool result already in this conversation; do not replay the duplicate unless its evidence is still essential.",
			}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "resume-repair-event")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "use the first matching tool result already in this conversation; do not replay the duplicate unless its evidence is still essential." {
		t.Fatalf("latest_recovery_hint = %q, want conversation.repaired hint", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromVisibleLoopDecision(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-decision-hint")
	dir := pool.sessionDirPath("loop-decision-hint")
	hidden := false
	visible := true

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeLoopDecision, sse.LoopDecisionPayload{
			TurnID:         "t1",
			DecisionID:     "hidden",
			Kind:           "evidence_quality",
			Decision:       "defer",
			RequiredAction: "this hidden action should not be surfaced",
			VisibleInUI:    &hidden,
		})+
			sessionEventLine(t, sse.TypeLoopDecision, sse.LoopDecisionPayload{
				TurnID:         "t1",
				DecisionID:     "continue",
				Kind:           "evidence_quality",
				Decision:       "continue",
				RequiredAction: "this non-blocking action should not be surfaced",
				VisibleInUI:    &visible,
			})+
			sessionEventLine(t, sse.TypeLoopDecision, sse.LoopDecisionPayload{
				TurnID:         "t2",
				DecisionID:     "evidence-quality-dynamic-partial",
				Kind:           "evidence_quality",
				Trigger:        "source_access_dynamic_partial",
				Decision:       "defer",
				RequiredAction: "Read browser network responses or an official API/source before citing dynamic page metrics.",
				VisibleInUI:    &visible,
			}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "loop-decision-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "Read browser network responses or an official API/source before citing dynamic page metrics." {
		t.Fatalf("latest_recovery_hint = %q, want visible loop decision required action", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromMaxTurns(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "max-turns-hint")
	dir := pool.sessionDirPath("max-turns-hint")

	toolRecovery := "blocked\nNext: use a different source before retrying\nFailure: kind=blocked"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t1", CallID: "c1", ExitCode: 1, ResultSummary: toolRecovery, Result: toolRecovery})+
			sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
				TurnID: "t1",
				Reason: sse.TurnEndMaxTurns,
				ToolStats: &sse.ToolRuntimeStats{
					ToolRequests:           10,
					ToolErrors:             2,
					LoopGuardInterventions: 1,
					ToolContextTruncated:   1,
				},
			}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "max-turns-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{
		"turn reached the tool-step budget",
		"change strategy",
		"inspect artifacts",
	} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromSessionSearchRecentAnchors(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "recall-miss-anchors")
	dir := pool.sessionDirPath("recall-miss-anchors")

	result := `{"query":"missing marker","total":0,"results":[],"message":"no results. Next: retry with fewer or different keywords, include outcome words like passed/final/decision, or use recent_sessions as anchors for a narrower query.","recent_sessions":[{"session_id":"market-alpha","mod_time":"2026-05-27T12:00:00Z","latest_user":"Analyze Alpha Coast stock recovery","latest_assistant":"final marker HIST-STOCK-44"},{"session_id":"subnet-120","latest_user":"Analyze Bittensor subnet 120"}]}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t1", CallID: "search-1", ExitCode: 0, ResultSummary: result, Result: result}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "recall-miss-anchors")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"session recall found no direct hits", "retry from recent session market-alpha", "Alpha Coast stock recovery"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestRecoveryHintFromConversationSessionSearchRecentAnchors(t *testing.T) {
	result := `{"query":"missing marker","total":0,"results":[],"recent_sessions":[{"session_id":"recent-a","latest_assistant":"final marker HIST-STOCK-44"}]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"session recall found no direct hits", "retry from recent session recent-a", "HIST-STOCK-44"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation recovery hint missing %q: %q", want, got)
		}
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromMemorySearchTopicAnchors(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "memory-miss-anchors")
	dir := pool.sessionDirPath("memory-miss-anchors")

	result := `{"ok":true,"message":"no entries matched. Next: retry with fewer/different keywords, search a specific topic from topics, or use action=list for full topic discovery.","target":"memory","results":[],"topics":[{"topic":"deploy","entries":2,"chars":120},{"topic":"auth","entries":1,"chars":80}]}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t1", CallID: "memory-1", ExitCode: 0, ResultSummary: result, Result: result}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "memory-miss-anchors")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"memory search found no direct hits", "target=memory", "specific topic such as deploy", "available topics: deploy, auth"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestRecoveryHintFromConversationMemorySearchTopicAnchors(t *testing.T) {
	result := `{"ok":true,"message":"no entries matched. Next: retry with fewer/different keywords, search a specific topic from topics, or use action=list for full topic discovery.","target":"memory","results":[],"topics":[{"topic":"markets","entries":2}]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"memory search found no direct hits", "specific topic such as markets"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation recovery hint missing %q: %q", want, got)
		}
	}
}

func TestSummarizeDurableSessionKeepsSpecificRuntimeErrorRecoveryHint(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "runtime-error-hint")
	dir := pool.sessionDirPath("runtime-error-hint")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeError, sse.ErrorPayload{TurnID: "t1", Code: "llm_timeout", Message: "upstream provider timed out while streaming", FailureKind: "llm_timeout", Recoverable: true})+
			sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: "t1", Reason: sse.TurnEndError}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "runtime-error-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if !strings.Contains(summary.LatestRecoveryHint, "upstream provider timed out while streaming") ||
		!strings.Contains(summary.LatestRecoveryHint, "retry or continue from persisted state") {
		t.Fatalf("latest_recovery_hint = %q, want specific runtime error recovery hint", summary.LatestRecoveryHint)
	}
}

func TestMergeSessionSummariesLetsDurableTopicRepairActiveContinuation(t *testing.T) {
	got := mergeSessionSummaries(
		sessionSummary{
			ID:                "active",
			Active:            true,
			LatestUserMessage: "请继续同一个任务。基于已有证据输出报告",
			TopicUserMessage:  "请继续同一个任务。基于已有证据输出报告",
		},
		sessionSummary{
			ID:               "active",
			Durable:          true,
			TopicUserMessage: "affine 是 Bittensor 的一个子网，请收集信息并向我介绍",
		},
	)
	if got.TopicUserMessage != "affine 是 Bittensor 的一个子网，请收集信息并向我介绍" {
		t.Fatalf("topic_user_message = %q, want durable original task", got.TopicUserMessage)
	}
	if got.LatestUserMessage != "请继续同一个任务。基于已有证据输出报告" {
		t.Fatalf("latest_user_message = %q, want active latest prompt", got.LatestUserMessage)
	}
}

func sessionEventLine(t *testing.T, typ string, payload any) string {
	t.Helper()
	ev, err := sse.NewEvent(typ, payload)
	if err != nil {
		t.Fatalf("build event %s: %v", typ, err)
	}
	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal event %s: %v", typ, err)
	}
	return string(raw) + "\n"
}

func TestUserMessageSummariesPreferDisplayText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	body := sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID:      "t1",
		Text:        "internal loop setup prompt with detailed tool instructions",
		DisplayText: "Set up loop: market monitor",
	})
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	latest, topic, err := userMessageSummariesFromEventsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if latest != "Set up loop: market monitor" || topic != "Set up loop: market monitor" {
		t.Fatalf("latest/topic = %q/%q, want display text", latest, topic)
	}
}

func TestSummarizeSessionTitleFromUserMessage(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "chinese subnet research",
			in:   "affine 是 Bittensor 的一个子网，请收集信息并向我介绍",
			want: "Affine（Bittensor 子网）",
		},
		{
			name: "title feedback",
			in:   "会话的标题最好是经过总结的，而不是把第一句话的输入当做标题",
			want: "会话标题摘要",
		},
		{
			name: "focus phrase",
			in:   "理解当前项目，重点关注webui的设计",
			want: "WebUI 设计",
		},
		{
			name: "english review",
			in:   "please review the WebUI session list behavior",
			want: "WebUI session list behavior",
		},
		{
			name: "question topic",
			in:   "bittensor是什么",
			want: "Bittensor",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeSessionTitleFromUserMessage(tc.in); got != tc.want {
				t.Fatalf("title = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSummarizeDurableSessionIncludesGeneratedTitle(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "titled")
	convPath := filepath.Join(pool.sessionDirPath("titled"), "conversation.jsonl")
	if err := os.WriteFile(convPath, []byte(
		`{"role":"user","content":"affine 是 Bittensor 的一个子网，请收集信息并向我介绍"}`+"\n",
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "titled")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.SummaryTitle != "Affine（Bittensor 子网）" {
		t.Fatalf("summary_title = %q, want generated topic title", summary.SummaryTitle)
	}
}

func TestLatestUserMessageFromConversationFileDoesNotScanWholeHugeLog(t *testing.T) {
	path := filepath.Join(t.TempDir(), "conversation.jsonl")
	body := `{"role":"user","content":"old task outside bounded tail"}` + "\n" +
		`{"role":"assistant","content":"` + strings.Repeat("x", maxSessionSummaryTailBytes+1024) + `"}` + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := latestUserMessageFromConversationFile(path)
	if err != nil {
		t.Fatalf("latestUserMessageFromConversationFile: %v", err)
	}
	if got != "" {
		t.Fatalf("latest user = %q, want empty because only old user is outside bounded tail", got)
	}
}

func TestSummarizeLatestUserMessageCollapsesWhitespaceAndTruncatesRunes(t *testing.T) {
	got := summarizeLatestUserMessage("  第一行\n\t第二行  " + strings.Repeat("界", maxSessionTaskSummaryChars))
	if strings.ContainsAny(got, "\n\t") {
		t.Fatalf("summary should be single-line, got %q", got)
	}
	if !strings.HasSuffix(got, "...") {
		t.Fatalf("summary should be truncated with ellipsis, got %q", got)
	}
	if !strings.Contains(got, "第一行 第二行") {
		t.Fatalf("summary lost leading content: %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("summary must remain valid UTF-8 text: %q", got)
	}
}

func TestSummarizeLatestUserMessageUnwrapsPlanModePrompts(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want string
	}{
		{
			name: "plan only",
			in:   agent.PlanOnlyUserPrompt("draft the migration plan"),
			want: "draft the migration plan",
		},
		{
			name: "execute plan",
			in:   sessionExecutePlanPrompt("ship the next step", "plan:1/2:active"),
			want: "ship the next step",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := summarizeLatestUserMessage(tc.in); got != tc.want {
				t.Fatalf("summary = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestTruncateSessionTitleUsesRuneSafePreview(t *testing.T) {
	got := truncateSessionTitle("Affine 子网研究任务", 8)
	if !strings.HasPrefix(got, "Affine ") {
		t.Fatalf("title truncation should preserve safe prefix, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("title truncation produced invalid UTF-8: %q", got)
	}
}

func TestHandleSessionList_PaginatesBySessionID(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	for _, id := range []string{"alpha", "bravo", "charlie"} {
		createDurableSessionDir(t, pool, id)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions?limit=2", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	var page1 sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page1); err != nil {
		t.Fatalf("decode page1: %v body=%s", err, w.Body.String())
	}
	if got, want := sessionIDs(page1.Sessions), []string{"alpha", "bravo"}; !sameStrings(got, want) {
		t.Fatalf("page1 ids = %v, want %v", got, want)
	}
	if !page1.HasMore || page1.NextAfter != "bravo" {
		t.Fatalf("page1 cursor = has_more:%v next:%q, want true/bravo", page1.HasMore, page1.NextAfter)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions?limit=2&after=bravo", nil)
	w = httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	var page2 sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &page2); err != nil {
		t.Fatalf("decode page2: %v body=%s", err, w.Body.String())
	}
	if got, want := sessionIDs(page2.Sessions), []string{"charlie"}; !sameStrings(got, want) {
		t.Fatalf("page2 ids = %v, want %v", got, want)
	}
	if page2.HasMore || page2.NextAfter != "" {
		t.Fatalf("page2 cursor = has_more:%v next:%q, want false/empty", page2.HasMore, page2.NextAfter)
	}
}

func TestHandleSessionListSkipsHiddenSystemDirs(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "visible")
	if err := os.MkdirAll(filepath.Join(memRoot, ".affentserve", "account-skills"), 0o755); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if got, want := sessionIDs(resp.Sessions), []string{"visible"}; !sameStrings(got, want) {
		t.Fatalf("session ids = %v, want %v", got, want)
	}
}

func TestHandleSessionDetail_ReadsDurableSessionAfterRestart(t *testing.T) {
	memRoot := t.TempDir()
	pool1 := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool1, "restartable")
	if err := os.WriteFile(filepath.Join(pool1.sessionDirPath("restartable"), "plan.json"), []byte(`{"version":1,"steps":[{"text":"inspect","status":"completed"},{"text":"resume work","status":"in_progress"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}
	pool1.Shutdown()

	pool2 := newPoolWithMemoryRoot(t, memRoot)
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/restartable", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool2).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionDetailResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Session.ID != "restartable" || resp.Session.Active || !resp.Session.Durable || !resp.Session.HasConversation {
		t.Fatalf("session = %+v, want durable-only restartable with conversation", resp.Session)
	}
	if !resp.Session.HasPlan {
		t.Fatalf("session = %+v, want durable-only restartable with plan", resp.Session)
	}
	if resp.Session.PlanSummary == nil || resp.Session.PlanSummary.Label != "plan:1/2:active" || resp.Session.PlanSummary.CompletedSteps != 1 || resp.Session.PlanSummary.TotalSteps != 2 || !resp.Session.PlanSummary.Active || resp.Session.PlanSummary.CurrentStep != "resume work" || resp.Session.PlanSummary.CurrentStepIndex != 2 || resp.Session.PlanSummary.CurrentStepStatus != "in_progress" || resp.Session.PlanSummary.LastCompletedStep != "inspect" || resp.Session.PlanSummary.LastCompletedStepIndex != 1 {
		t.Fatalf("plan summary = %+v, want active 1/2", resp.Session.PlanSummary)
	}
	if resp.Session.Capabilities != nil {
		t.Fatalf("durable-only session should not report active capabilities, got %+v", resp.Session.Capabilities)
	}
}

func TestHandleSessionList_ReportsPlanSummary(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "planned-list")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("planned-list"), "plan.json"), []byte(`{"version":1,"steps":[{"text":"done","status":"completed"},{"text":"blocked","status":"blocked"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want one session", resp.Sessions)
	}
	summary := resp.Sessions[0].PlanSummary
	if summary == nil || summary.Label != "plan:1/2:blocked" || summary.CompletedSteps != 1 || summary.TotalSteps != 2 || !summary.Blocked || summary.CurrentStep != "blocked" || summary.CurrentStepIndex != 2 || summary.CurrentStepStatus != "blocked" || summary.LastCompletedStep != "done" || summary.LastCompletedStepIndex != 1 || summary.BlockedStep != "blocked" || summary.BlockedStepIndex != 2 {
		t.Fatalf("plan summary = %+v, want blocked 1/2", summary)
	}
}

func TestHandleSessionList_ReportsScheduleSummary(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "scheduled-list")
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	file := sessionSchedulesFile{
		Version: 1,
		Schedules: []sessionSchedule{
			{
				ID:        "sched_later",
				Prompt:    "review project progress",
				Enabled:   true,
				NextRunAt: now.Add(2 * time.Hour).Format(time.RFC3339),
				CreatedAt: now.Format(time.RFC3339),
				UpdatedAt: now.Format(time.RFC3339),
			},
			{
				ID:          "sched_next",
				Kind:        sessionScheduleKindLoopTick,
				Prompt:      "ask clarifying questions and update LOOP.md",
				DisplayText: "Loop every 30m: scheduled-list",
				Enabled:     true,
				NextRunAt:   now.Add(time.Hour).Format(time.RFC3339),
				CreatedAt:   now.Format(time.RFC3339),
				UpdatedAt:   now.Format(time.RFC3339),
			},
			{
				ID:        "sched_paused",
				Prompt:    "paused task",
				Enabled:   false,
				NextRunAt: now.Add(30 * time.Minute).Format(time.RFC3339),
				CreatedAt: now.Format(time.RFC3339),
				UpdatedAt: now.Add(10 * time.Minute).Format(time.RFC3339),
				LastError: "LOOP.md not running; answer calibration first",
			},
		},
	}
	if err := writeSessionSchedulesFile(sessionSchedulesPath(pool, "scheduled-list"), file); err != nil {
		t.Fatalf("write schedules: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions", nil)
	w := httptest.NewRecorder()
	handleSessionsCollection(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 1 {
		t.Fatalf("sessions = %+v, want one session", resp.Sessions)
	}
	summary := resp.Sessions[0].Schedules
	if !resp.Sessions[0].HasSchedules || summary == nil {
		t.Fatalf("session = %+v, want schedule summary", resp.Sessions[0])
	}
	if summary.Count != 3 || summary.Enabled != 2 || summary.EnabledLoopTicks != 1 || summary.PendingLoopTicks != 1 || summary.NextScheduleID != "sched_next" || summary.NextScheduleKind != sessionScheduleKindLoopTick || summary.NextRunAt != now.Add(time.Hour).Format(time.RFC3339) || summary.NextPromptPreview != "Loop every 30m: scheduled-list" {
		t.Fatalf("schedule summary = %+v, want next enabled schedule", summary)
	}
	if summary.ErrorCount != 1 || summary.LastError != "LOOP.md not running; answer calibration first" {
		t.Fatalf("schedule summary = %+v, want latest timer error", summary)
	}
}

func TestSummarizeDurableSessionReportsBadPlanSummaryWithoutFailing(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "bad-plan")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("bad-plan"), "plan.json"), []byte(`{`), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "bad-plan")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if !summary.HasPlan || summary.PlanSummary == nil || !summary.PlanSummary.Error || summary.PlanSummary.Label != "plan:error" {
		t.Fatalf("bad plan summary = %+v", summary)
	}
}

func TestHandleSessionSchedules_CreateListDeleteWithoutReopening(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "scheduled")
	nextRunAt := time.Date(2026, 5, 27, 13, 30, 0, 0, time.UTC).Format(time.RFC3339)

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/scheduled/schedules", bytes.NewBufferString(`{"kind":"loop_tick","prompt":"Ask the user two focused questions before enabling loop.","display_text":"Loop every hour: scheduled","next_run_at":"`+nextRunAt+`","repeat_interval_seconds":3600}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusCreated {
		t.Fatalf("create status = %d, want 201; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "scheduled") != nil {
		t.Fatal("POST schedules must not reopen an inactive durable session")
	}
	var created sessionSchedulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create: %v", err)
	}
	if created.SessionID != "scheduled" || len(created.Schedules) != 1 {
		t.Fatalf("created = %+v, want one schedule", created)
	}
	schedule := created.Schedules[0]
	if schedule.ID == "" || schedule.Kind != sessionScheduleKindLoopTick || schedule.Prompt != "Ask the user two focused questions before enabling loop." || schedule.DisplayText != "Loop every hour: scheduled" || !schedule.Enabled || schedule.NextRunAt != nextRunAt || schedule.RepeatIntervalSeconds != 3600 {
		t.Fatalf("schedule = %+v, want persisted request fields", schedule)
	}
	if created.Summary == nil || created.Summary.Count != 1 || created.Summary.Enabled != 1 || created.Summary.EnabledLoopTicks != 1 || created.Summary.PendingLoopTicks != 1 || created.Summary.NextScheduleID != schedule.ID {
		t.Fatalf("summary = %+v, want one enabled schedule", created.Summary)
	}

	r = httptest.NewRequest(http.MethodGet, "/v1/sessions/scheduled/schedules", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "scheduled") != nil {
		t.Fatal("GET schedules must not reopen an inactive durable session")
	}
	var listed sessionSchedulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(listed.Schedules) != 1 || listed.Schedules[0].ID != schedule.ID {
		t.Fatalf("listed = %+v, want created schedule", listed)
	}

	r = httptest.NewRequest(http.MethodPatch, "/v1/sessions/scheduled/schedules/"+schedule.ID, strings.NewReader(`{"enabled":false}`))
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("pause status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "scheduled") != nil {
		t.Fatal("PATCH schedule must not reopen an inactive durable session")
	}
	var paused sessionSchedulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &paused); err != nil {
		t.Fatalf("decode pause: %v", err)
	}
	if len(paused.Schedules) != 1 || paused.Schedules[0].Enabled || paused.Summary == nil || paused.Summary.Enabled != 0 {
		t.Fatalf("paused = %+v, want disabled schedule", paused)
	}

	r = httptest.NewRequest(http.MethodPatch, "/v1/sessions/scheduled/schedules/"+schedule.ID, strings.NewReader(`{"enabled":true}`))
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusConflict {
		t.Fatalf("uncalibrated resume status = %d, want 409; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "activate LOOP.md") {
		t.Fatalf("uncalibrated resume body = %s, want activation guidance", w.Body.String())
	}
	if activeSessionByID(pool, "scheduled") != nil {
		t.Fatal("rejected PATCH schedule must not reopen an inactive durable session")
	}

	writeLoopProtocolStatusFixture(t, pool, "scheduled", "running")
	if err := loopstate.WriteState(sessionLoopStatePath(pool, "scheduled"), loopstate.State{
		Version: 1,
		LoopID:  "scheduled",
		Status:  "running",
	}); err != nil {
		t.Fatalf("write running loop state: %v", err)
	}
	r = httptest.NewRequest(http.MethodPatch, "/v1/sessions/scheduled/schedules/"+schedule.ID, strings.NewReader(`{"enabled":true}`))
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("calibrated resume status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resumed sessionSchedulesResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resumed); err != nil {
		t.Fatalf("decode resume: %v", err)
	}
	if len(resumed.Schedules) != 1 || !resumed.Schedules[0].Enabled || resumed.Summary == nil || resumed.Summary.Enabled != 1 {
		t.Fatalf("resumed = %+v, want enabled schedule", resumed)
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/sessions/scheduled/schedules/"+schedule.ID, nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("delete status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "scheduled") != nil {
		t.Fatal("DELETE schedule must not reopen an inactive durable session")
	}
	var deleted sessionScheduleDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &deleted); err != nil {
		t.Fatalf("decode delete: %v", err)
	}
	if !deleted.Cleared || deleted.ScheduleID != schedule.ID || deleted.Summary == nil || deleted.Summary.Count != 0 {
		t.Fatalf("deleted = %+v, want cleared with empty summary", deleted)
	}
}

func TestHandleSessionSchedules_ValidatesRequest(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cases := []struct {
		name string
		body string
	}{
		{name: "empty prompt", body: `{"prompt":" ","next_run_at":"2026-05-27T13:30:00Z"}`},
		{name: "missing next run", body: `{"prompt":"work"}`},
		{name: "display too large", body: `{"prompt":"work","display_text":"` + strings.Repeat("x", maxSessionScheduleDisplay+1) + `","next_run_at":"2026-05-27T13:30:00Z"}`},
		{name: "too fast repeat", body: `{"prompt":"work","next_run_at":"2026-05-27T13:30:00Z","repeat_interval_seconds":1}`},
		{name: "bad kind", body: `{"kind":"forever","prompt":"work","next_run_at":"2026-05-27T13:30:00Z"}`},
		{name: "unknown field", body: `{"prompt":"work","next_run_at":"2026-05-27T13:30:00Z","cron":"*"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/bad-schedule/schedules", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handleSessionRoutes(pool).ServeHTTP(w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
		})
	}
}

func TestHandleSessionScheduleUpdate_ValidatesRequest(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "bad-schedule-update")
	now := time.Date(2026, 5, 27, 12, 0, 0, 0, time.UTC)
	if err := writeSessionSchedulesFile(sessionSchedulesPath(pool, "bad-schedule-update"), sessionSchedulesFile{
		Version: 1,
		Schedules: []sessionSchedule{{
			ID:        "sched_update",
			Prompt:    "work",
			Enabled:   true,
			NextRunAt: now.Add(time.Hour).Format(time.RFC3339),
			CreatedAt: now.Format(time.RFC3339),
			UpdatedAt: now.Format(time.RFC3339),
		}},
	}); err != nil {
		t.Fatalf("write schedules: %v", err)
	}
	cases := []struct {
		name string
		body string
	}{
		{name: "missing enabled", body: `{}`},
		{name: "unknown field", body: `{"enabled":false,"next_run_at":"2026-05-27T13:30:00Z"}`},
		{name: "multiple", body: `{"enabled":false} {"enabled":true}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPatch, "/v1/sessions/bad-schedule-update/schedules/sched_update", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handleSessionRoutes(pool).ServeHTTP(w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
		})
	}
}

func TestHandleSessionPlan_ReadsDurablePlanWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "planned")
	planJSON := `{"version":1,"steps":[{"text":"resume work","status":"in_progress"}]}`
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("planned"), "plan.json"), []byte(planJSON+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/planned/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "planned") != nil {
		t.Fatal("GET plan must not reopen an inactive durable session")
	}
	var resp sessionPlanResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "planned" {
		t.Fatalf("session_id = %q, want planned", resp.SessionID)
	}
	if resp.Summary == nil || resp.Summary.Label != "plan:0/1:active" || resp.Summary.TotalSteps != 1 || !resp.Summary.Active || resp.Summary.CurrentStep != "resume work" || resp.Summary.CurrentStepIndex != 1 || resp.Summary.CurrentStepStatus != "in_progress" {
		t.Fatalf("summary = %+v, want active 0/1", resp.Summary)
	}
	var plan struct {
		Steps []struct {
			Text   string `json:"text"`
			Status string `json:"status"`
		} `json:"steps"`
	}
	if err := json.Unmarshal(resp.Plan, &plan); err != nil {
		t.Fatalf("decode plan: %v", err)
	}
	if len(plan.Steps) != 1 || plan.Steps[0].Text != "resume work" || plan.Steps[0].Status != "in_progress" {
		t.Fatalf("plan = %+v", plan)
	}
}

func TestHandleSessionPlan_Returns404WhenNoPlan(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "no-plan")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/no-plan/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionLoopProtocol_ReadsDurableProtocolWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "looped")
	protocolPath := sessionLoopProtocolPath(pool, "looped")
	if err := os.MkdirAll(filepath.Dir(protocolPath), 0o755); err != nil {
		t.Fatal(err)
	}
	protocol := `# Loop Protocol: Loop Test

## 0. Metadata

- loop_id: looped
- owner_session: looped
- status: running

## 1. North Star

Keep long-run evidence recoverable.`
	if err := os.WriteFile(protocolPath, []byte(protocol+"\n"), 0o644); err != nil {
		t.Fatalf("write loop protocol: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/looped/loop-protocol", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "looped") != nil {
		t.Fatal("GET loop-protocol must not reopen an inactive durable session")
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "looped" || !strings.Contains(resp.Protocol, "Keep long-run evidence recoverable.") {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Summary == nil ||
		resp.Summary.Path != loopstate.ProtocolRelPath("looped") ||
		resp.Summary.LoopID != "looped" ||
		resp.Summary.OwnerSession != "looped" ||
		resp.Summary.Status != "running" ||
		resp.Summary.UpdatedAt == "" ||
		!strings.Contains(resp.Summary.Preview, "Keep long-run evidence recoverable.") {
		t.Fatalf("summary = %+v", resp.Summary)
	}

	summary, found, err := summarizeDurableSession(pool, "looped")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasLoopProtocol || summary.LoopProtocol == nil || summary.LoopProtocol.Status != "running" {
		t.Fatalf("durable summary = %+v", summary)
	}
}

func TestHandleSessionLoopProtocol_Returns404WhenMissing(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "no-loop")

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/no-loop/loop-protocol", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", got, w.Body.String())
	}
}

func TestHandleSessionLoopProtocolUpdate_WritesDurableProtocolWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	protocol := `# Loop Protocol: API

## 0. Metadata

- loop_id: api-loop
- owner_session: api-loop
- status: draft

## 1. North Star

Keep API-created loop state durable.`

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/api-loop/loop-protocol", strings.NewReader(`{"protocol":`+strconv.Quote(protocol)+`,"reason":"initial long-run protocol","sections_changed":["Metadata","North Star"]}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "api-loop") != nil {
		t.Fatal("POST loop-protocol must not reopen an inactive durable session")
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "api-loop" || resp.Protocol != protocol {
		t.Fatalf("response = %+v", resp)
	}
	if resp.Summary == nil || resp.Summary.Status != "draft" || resp.Summary.Path != loopstate.ProtocolRelPath("api-loop") {
		t.Fatalf("summary = %+v", resp.Summary)
	}
	if resp.State == nil || resp.State.LoopID != "api-loop" || resp.State.Status != "draft" || resp.State.ProtocolUpdates != 1 || resp.State.EventCount != 1 {
		t.Fatalf("state = %+v", resp.State)
	}
	if len(resp.Events) != 1 || resp.Events[0].Type != "loop.protocol_update" || resp.Events[0].Reason != "initial long-run protocol" || resp.Events[0].Seq != 1 {
		t.Fatalf("events = %+v", resp.Events)
	}
	raw, err := os.ReadFile(sessionLoopProtocolPath(pool, "api-loop"))
	if err != nil {
		t.Fatalf("read written protocol: %v", err)
	}
	if string(raw) != protocol+"\n" {
		t.Fatalf("written protocol = %q", string(raw))
	}
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, "api-loop"))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.LastEventType != "loop.protocol_update" || state.LastEventSummary == "" {
		t.Fatalf("persisted state = %+v", state)
	}
	events, found, err := loopstate.ReadRecentEvents(sessionLoopEventsPath(pool, "api-loop"), 10)
	if err != nil || !found || len(events) != 1 {
		t.Fatalf("ReadRecentEvents found=%v len=%d err=%v", found, len(events), err)
	}
}

func TestHandleSessionLoopProtocolUpdate_RejectsRunningTransitionWithoutActivation(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	protocol := `# Loop Protocol: API

## 0. Metadata

- loop_id: api-loop-bypass
- owner_session: api-loop-bypass
- status: running

## 1. North Star

Keep API-created loop state durable.`

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/api-loop-bypass/loop-protocol", strings.NewReader(`{"protocol":`+strconv.Quote(protocol)+`,"reason":"bypass activation"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "requires activate=true") {
		t.Fatalf("response missing activation guidance: %s", w.Body.String())
	}
	if _, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "api-loop-bypass")); err != nil || found {
		t.Fatalf("rejected update must not write protocol found=%v err=%v", found, err)
	}
}

func TestHandleSessionLoopProtocolUpdate_AllowsRunningProtocolMaintenance(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "api-loop-running")
	writeLoopProtocolStatusFixture(t, pool, "api-loop-running", "running")
	protocol := `# Loop Protocol: API

## 0. Metadata

- loop_id: api-loop-running
- owner_session: api-loop-running
- status: running

## 1. North Star

Maintain active loop recovery anchors.`

	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/api-loop-running/loop-protocol", strings.NewReader(`{"protocol":`+strconv.Quote(protocol)+`,"reason":"maintain active loop"}`))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Summary == nil || resp.Summary.Status != "running" || resp.State == nil || resp.State.Status != "running" {
		t.Fatalf("response = %+v, want running protocol maintenance", resp)
	}
	if !resp.State.NeedsFullProtocolFeed {
		t.Fatalf("running protocol maintenance should force next full feed: state=%+v", resp.State)
	}
	if !strings.Contains(resp.Protocol, "Maintain active loop recovery anchors.") {
		t.Fatalf("protocol not updated:\n%s", resp.Protocol)
	}
}

func TestHandleSessionLoopProtocolUpdate_ActivatesDraftTemplateWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	body := `{"activate":true,"goal":"Understand the user's long-running market analysis intent."}`
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/api-loop-draft/loop-protocol", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "api-loop-draft") != nil {
		t.Fatal("POST activate loop-protocol must not reopen an inactive durable session")
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State == nil || resp.State.Status != "draft" || resp.State.InitialGoalPreview != "Understand the user's long-running market analysis intent." {
		t.Fatalf("state = %+v", resp.State)
	}
	for _, want := range []string{
		"- status: draft",
		"Understand the user's long-running market analysis intent.",
		"Operational stop conditions:",
	} {
		if !strings.Contains(resp.Protocol, want) {
			t.Fatalf("protocol missing %q:\n%s", want, resp.Protocol)
		}
	}
	if len(resp.Events) != 1 || resp.Events[0].Type != "loop.protocol_init" {
		t.Fatalf("events = %+v", resp.Events)
	}
}

func TestHandleSessionLoopProtocolUpdate_RejectsPrematureActivation(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	protocol := loopstate.DefaultProtocolTemplate(loopstate.ProtocolTemplateOptions{
		LoopID:       "api-loop-premature",
		OwnerSession: "api-loop-premature",
		Goal:         "Understand the user's long-running market analysis intent.",
		Status:       "running",
	})
	body := `{"activate":true,"protocol":` + strconv.Quote(protocol) + `,"reason":"premature"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/api-loop-premature/loop-protocol", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unresolved activation placeholder") {
		t.Fatalf("response missing activation readiness error: %s", w.Body.String())
	}
}

func TestHandleSessionLoopProtocolUpdate_RejectsActivationBeforeCalibrationAnswer(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	if _, _, _, _, err := writeSessionLoopProtocol(pool, "api-loop-uncalibrated", sessionLoopProtocolUpdateRequest{
		Activate: true,
		Goal:     "Understand the user's long-running market analysis intent.",
	}); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "api-loop-uncalibrated"))
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "- status: draft", "- status: running", 1)
	protocol = strings.Replace(protocol, "- hard constraints:", "- hard constraints: cite evidence and stop on unclear user intent", 1)
	protocol = strings.Replace(protocol, "- known evidence:", "- known evidence: user wants market analysis", 1)
	protocol = strings.Replace(protocol, "- current risk or blocker:", "- current risk or blocker: live source quality unknown", 1)
	protocol = strings.Replace(protocol, "- important artifacts:", "- important artifacts: none yet", 1)
	protocol = strings.Replace(protocol, "- important trace spans:", "- important trace spans: loop draft", 1)
	protocol = strings.Replace(protocol, "- last known recovery note:", "- last known recovery note: reload LOOP.md and plan state", 1)
	body := `{"activate":true,"protocol":` + strconv.Quote(protocol) + `,"reason":"uncalibrated"}`
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/api-loop-uncalibrated/loop-protocol", strings.NewReader(body))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "requires a user calibration answer") {
		t.Fatalf("response missing calibration readiness error: %s", w.Body.String())
	}
	stillDraft, found, err := loopstate.ReadProtocol(sessionLoopProtocolPath(pool, "api-loop-uncalibrated"))
	if err != nil || !found {
		t.Fatalf("ReadProtocol after rejection found=%v err=%v", found, err)
	}
	if !strings.Contains(stillDraft, "- status: draft") {
		t.Fatalf("rejected activation must not overwrite draft protocol:\n%s", stillDraft)
	}
}

func TestHandleSessionLoopProtocolUpdate_RejectsBlankAndUnknownFields(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	cases := []struct {
		name string
		body string
	}{
		{name: "blank", body: `{"protocol":" \n\t"}`},
		{name: "unknown", body: `{"protocol":"# Loop","extra":true}`},
		{name: "multiple", body: `{"protocol":"# Loop"} {"protocol":"# Other"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/v1/sessions/bad-loop/loop-protocol", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			handleSessionRoutes(pool).ServeHTTP(w, r)
			if got := w.Result().StatusCode; got != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
			}
		})
	}
}

func TestHandleSessionLoopProtocolDelete_RemovesDurableProtocolWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "clear-loop")
	path := sessionLoopProtocolPath(pool, "clear-loop")
	if err := loopstate.WriteProtocol(path, "# Loop\n\nstatus: paused"); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-loop/loop-protocol", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "clear-loop") != nil {
		t.Fatal("DELETE loop-protocol must not reopen an inactive durable session")
	}
	var resp sessionLoopProtocolDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "clear-loop" || !resp.Cleared {
		t.Fatalf("delete response = %+v, want cleared clear-loop", resp)
	}
	if resp.State == nil || resp.State.Status != "disabled" || resp.State.LastEventType != "loop.protocol_delete" {
		t.Fatalf("delete state = %+v", resp.State)
	}
	if len(resp.Events) != 1 || resp.Events[0].Type != "loop.protocol_delete" {
		t.Fatalf("delete events = %+v", resp.Events)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("loop protocol path after delete err = %v, want not exists", err)
	}
	summary, found, err := summarizeDurableSession(pool, "clear-loop")
	if err != nil || !found {
		t.Fatalf("summarize after delete found=%v err=%v", found, err)
	}
	if summary.HasLoopProtocol || !summary.HasLoopState || summary.LoopState == nil || summary.LoopState.Status != "disabled" {
		t.Fatalf("loop state should remain visible after protocol delete: %+v", summary)
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-loop/loop-protocol", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", got, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if resp.Cleared {
		t.Fatalf("second delete response = %+v, want cleared=false", resp)
	}
	if resp.State == nil || resp.State.Status != "disabled" || resp.State.EventCount != 2 {
		t.Fatalf("second delete state = %+v", resp.State)
	}
}

func TestReadSessionLoopProtocolReturnsStateAndRecentEvents(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	protocol := `# Loop

- status: paused`
	req := sessionLoopProtocolUpdateRequest{Protocol: protocol, Reason: "first"}
	if _, _, _, _, err := writeSessionLoopProtocol(pool, "loop-read", req); err != nil {
		t.Fatalf("first write: %v", err)
	}
	req.Reason = "second"
	if _, _, _, _, err := writeSessionLoopProtocol(pool, "loop-read", req); err != nil {
		t.Fatalf("second write: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/loop-read/loop-protocol", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State == nil || resp.State.ProtocolUpdates != 2 || resp.State.EventCount != 2 || resp.State.Status != "paused" {
		t.Fatalf("state = %+v", resp.State)
	}
	if len(resp.Events) != 2 || resp.Events[0].Reason != "first" || resp.Events[1].Reason != "second" {
		t.Fatalf("events = %+v", resp.Events)
	}

	summary, found, err := summarizeDurableSession(pool, "loop-read")
	if err != nil || !found {
		t.Fatalf("summarizeDurableSession found=%v err=%v", found, err)
	}
	if !summary.HasLoopProtocol || summary.LoopProtocol == nil || summary.LoopProtocol.State == nil || summary.LoopProtocol.State.EventCount != 2 {
		t.Fatalf("durable loop summary = %+v", summary.LoopProtocol)
	}
}

func TestHandleSessionLoopProtocolReturnsRuntimeSidecarCheckpoints(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	protocol := `# Loop

- status: running`
	protocolPath := sessionLoopProtocolPath(pool, "loop-visible")
	if err := loopstate.WriteProtocol(protocolPath, protocol); err != nil {
		t.Fatalf("write protocol: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolUpdate(protocolPath, "activate", nil); err != nil {
		t.Fatalf("record protocol update: %v", err)
	}
	if _, _, err := loopstate.RecordMemoryUpdate(protocolPath, loopstate.MemoryUpdateCheckpoint{
		TurnID:          "turn_mem",
		CallID:          "memory-1",
		Action:          "replace",
		Target:          "memory",
		Topic:           "markets",
		Location:        "memory:markets",
		Preview:         "old dashboard rule -> prefer browser network evidence",
		PreviousPreview: "old dashboard rule",
		NextPreview:     "prefer browser network evidence",
	}); err != nil {
		t.Fatalf("RecordMemoryUpdate: %v", err)
	}
	if _, _, err := loopstate.RecordDecision(protocolPath, loopstate.DecisionCheckpoint{
		DecisionID:     "evidence-quality-dynamic-partial",
		Kind:           "evidence_quality",
		Trigger:        "source_access_dynamic_partial",
		Decision:       "defer",
		Confidence:     "high",
		Reason:         "dynamic widgets lacked text",
		RequiredAction: "read browser network responses",
	}); err != nil {
		t.Fatalf("RecordDecision: %v", err)
	}
	if _, _, err := loopstate.RecordTurnCheckpoint(protocolPath, loopstate.TurnCheckpoint{
		TurnID:             "turn_done",
		EndReason:          sse.TurnEndCompleted,
		InputTokens:        123,
		OutputTokens:       45,
		ToolRequests:       2,
		MemoryUpdates:      1,
		MemorySearchCalls:  3,
		MemorySearchMisses: 2,
		SessionSearchCalls: 1,
	}); err != nil {
		t.Fatalf("RecordTurnCheckpoint: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/loop-visible/loop-protocol", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State == nil ||
		resp.State.MemoryUpdateEvents != 1 ||
		resp.State.LastMemoryUpdateLoc != "memory:markets" ||
		resp.State.LastMemoryUpdate != "old dashboard rule -> prefer browser network evidence" ||
		resp.State.LoopDecisions != 1 ||
		resp.State.LastDecisionKind != "evidence_quality" ||
		resp.State.LastDecisionAction != "read browser network responses" ||
		resp.State.TurnCheckpoints != 1 ||
		resp.State.LastTurnID != "turn_done" ||
		resp.State.LastTurnMemoryUpdates != 1 ||
		resp.State.LastTurnMemorySearches != 3 ||
		resp.State.LastTurnMemoryMisses != 2 ||
		resp.State.LastTurnSessionSearch != 1 {
		t.Fatalf("state should expose runtime checkpoints for WebUI: %+v", resp.State)
	}
	if len(resp.Events) != 4 {
		t.Fatalf("events len = %d, want 4: %+v", len(resp.Events), resp.Events)
	}
	if resp.Events[1].Type != "loop.memory_update" ||
		resp.Events[1].MemoryLocation != "memory:markets" ||
		resp.Events[1].NextPreview != "prefer browser network evidence" {
		t.Fatalf("memory update event = %+v", resp.Events[1])
	}
	if resp.Events[2].Type != "loop.decision" ||
		resp.Events[2].DecisionKind != "evidence_quality" ||
		resp.Events[2].RequiredAction != "read browser network responses" {
		t.Fatalf("decision event = %+v", resp.Events[2])
	}
	if resp.Events[3].Type != "loop.turn_checkpoint" ||
		resp.Events[3].TurnID != "turn_done" ||
		resp.Events[3].MemoryUpdates != 1 ||
		resp.Events[3].MemorySearches != 3 ||
		resp.Events[3].MemoryMisses != 2 ||
		resp.Events[3].SessionSearch != 1 {
		t.Fatalf("turn checkpoint event = %+v", resp.Events[3])
	}

	summary, found, err := summarizeDurableSession(pool, "loop-visible")
	if err != nil || !found {
		t.Fatalf("summarizeDurableSession found=%v err=%v", found, err)
	}
	if !summary.HasLoopState ||
		summary.LoopState == nil ||
		summary.LoopState.LastMemoryUpdateLoc != "memory:markets" ||
		summary.LoopState.LastDecisionKind != "evidence_quality" ||
		summary.LoopState.LastTurnID != "turn_done" ||
		summary.LoopState.LastTurnMemorySearches != 3 ||
		summary.LoopState.LastTurnMemoryMisses != 2 {
		t.Fatalf("durable session summary loop_state = %+v", summary.LoopState)
	}
}

func TestHandleSessionLoopProtocolDelete_RejectsSymlinkProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "clear-link-loop")
	outside := filepath.Join(t.TempDir(), "outside-loop.md")
	if err := os.WriteFile(outside, []byte("# outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	path := sessionLoopProtocolPath(pool, "clear-link-loop")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-link-loop/loop-protocol", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", got, w.Body.String())
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside protocol should remain: %v", err)
	}
}

func TestHandleSessionPlanDelete_RemovesDurablePlanWithoutReopeningSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "clear-plan")
	planPath := filepath.Join(pool.sessionDirPath("clear-plan"), "plan.json")
	if err := os.WriteFile(planPath, []byte(`{"version":1,"steps":[{"text":"stale"}]}`+"\n"), 0o644); err != nil {
		t.Fatalf("write plan: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-plan/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, "clear-plan") != nil {
		t.Fatal("DELETE plan must not reopen an inactive durable session")
	}
	var resp sessionPlanDeleteResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.SessionID != "clear-plan" || !resp.Cleared {
		t.Fatalf("delete response = %+v, want cleared clear-plan", resp)
	}
	if _, err := os.Lstat(planPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("plan path after delete err = %v, want not exists", err)
	}

	r = httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-plan/plan", nil)
	w = httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("second status = %d, want 200; body=%s", got, w.Body.String())
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode second: %v", err)
	}
	if resp.Cleared {
		t.Fatalf("second delete response = %+v, want cleared=false", resp)
	}
}

func TestHandleSessionPlanDelete_RejectsSymlinkPlan(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "clear-link-plan")
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(pool.sessionDirPath("clear-link-plan"), "plan.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	r := httptest.NewRequest(http.MethodDelete, "/v1/sessions/clear-link-plan/plan", nil)
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500; body=%s", got, w.Body.String())
	}
	if _, err := os.Lstat(link); err != nil {
		t.Fatalf("symlink plan should remain for operator inspection: %v", err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside plan should remain: %v", err)
	}
}

func TestHandleSessionPlanRejectsSymlinkPlan(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "link-plan")
	outside := filepath.Join(t.TempDir(), "outside-plan.json")
	if err := os.WriteFile(outside, []byte(`{"version":1}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pool.sessionDirPath("link-plan"), "plan.json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, found, err := readSessionPlan(pool, "link-plan")
	if err == nil || found {
		t.Fatalf("readSessionPlan symlink = found:%v err:%v, want error", found, err)
	}
	if !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("error = %v, want symlink", err)
	}
	summary, foundSummary, err := summarizeDurableSession(pool, "link-plan")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !foundSummary {
		t.Fatal("durable session should still be found")
	}
	if summary.HasPlan {
		t.Fatalf("symlink plan must not set has_plan: %+v", summary)
	}
}

func TestSummarizeDurableSessionIgnoresSymlinkConversation(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "link-conversation")
	convPath := filepath.Join(pool.sessionDirPath("link-conversation"), "conversation.jsonl")
	if err := os.Remove(convPath); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside-conversation.jsonl")
	if err := os.WriteFile(outside, []byte(`{"role":"user","content":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, convPath); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "link-conversation")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should still be found")
	}
	if summary.HasConversation {
		t.Fatalf("symlink conversation must not set has_conversation: %+v", summary)
	}
}

func TestSummarizeDurableSessionIgnoresSymlinkSessionDir(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "conversation.jsonl"), []byte(`{"role":"user","content":"outside"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, pool.sessionDirPath("link-dir")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "link-dir")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if found || summary.ID != "" {
		t.Fatalf("symlink session dir should not be durable: found=%v summary=%+v", found, summary)
	}
	if sessionKnown(pool, "link-dir") {
		t.Fatal("sessionKnown must not follow symlink session dirs")
	}
}

func TestSummarizeDurableSessionIgnoresSymlinkDurableStateMarkers(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "link-state")
	dir := pool.sessionDirPath("link-state")

	artifactDir := filepath.Join(dir, filepath.FromSlash(artifactPathPrefix))
	if err := os.MkdirAll(filepath.Dir(artifactDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideArtifacts := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideArtifacts, "artifact.txt"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideArtifacts, artifactDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	skillDir := agent.DefaultWorkspaceSkillDir(dir)
	if err := os.MkdirAll(filepath.Dir(skillDir), 0o755); err != nil {
		t.Fatal(err)
	}
	outsideSkills := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideSkills, "skill.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideSkills, skillDir); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	topics := filepath.Join(dir, "topics")
	if err := os.RemoveAll(topics); err != nil {
		t.Fatal(err)
	}
	outsideTopics := t.TempDir()
	if err := os.WriteFile(filepath.Join(outsideTopics, "topic.md"), []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideTopics, topics); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "link-state")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should still be found")
	}
	if summary.HasArtifacts || summary.HasRuntimeSkills || summary.HasMemory {
		t.Fatalf("symlink state markers must not be reported: %+v", summary)
	}
}

func TestSummarizeDurableSessionSharedUserMemoryDoesNotRefreshSession(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	pool.cfg.SharedUserMemory = true
	createDurableSessionDir(t, pool, "shared-user-old-session")
	dir := pool.sessionDirPath("shared-user-old-session")
	if err := os.RemoveAll(filepath.Join(dir, "topics")); err != nil {
		t.Fatalf("remove topics: %v", err)
	}
	oldTime := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	newTime := time.Date(2026, 1, 3, 3, 4, 5, 0, time.UTC)
	for _, path := range []string{dir, filepath.Join(dir, "conversation.jsonl"), filepath.Join(dir, "events.jsonl")} {
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}
	sharedUserPath := pool.sharedUserMemoryPath()
	if err := os.WriteFile(sharedUserPath, []byte("- shared preference\n"), 0o644); err != nil {
		t.Fatalf("write shared user memory: %v", err)
	}
	if err := os.Chtimes(sharedUserPath, newTime, newTime); err != nil {
		t.Fatalf("chtimes shared user memory: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "shared-user-old-session")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasMemory {
		t.Fatalf("shared user memory should mark the session as memory-capable: found=%v summary=%+v", found, summary)
	}
	if summary.LastUsedAt != formatTime(oldTime) {
		t.Fatalf("LastUsedAt = %q, want session-local time %q", summary.LastUsedAt, formatTime(oldTime))
	}
}

func TestSessionCapabilitiesReflectActualRegisteredTools(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = false
	pool.cfg.EnableMemory = true
	pool.cfg.EnableSubagent = true
	pool.cfg.EnableFocusedTasks = true
	s, err := pool.GetOrCreate("tool-light")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, pool.cfg)
	if caps.Builtins || caps.SkillInstall || caps.Plan || caps.RepoSearch {
		t.Fatalf("tool-light session should not report builtin-only tools: %+v", caps)
	}
	if !caps.Memory || !caps.SessionSearch || !caps.Subagent || !caps.FocusedTasks {
		t.Fatalf("tool-light session should report actually registered non-builtin tools: %+v", caps)
	}
}

func TestSessionCapabilitiesReportsWebSearchBackend(t *testing.T) {
	t.Setenv("AFFENT_WEB_SEARCH_PROVIDER", "google")
	t.Setenv("GOOGLE_API_KEY", "google-key")
	t.Setenv("GOOGLE_SEARCH_ENGINE_ID", "google-cx")
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableWeb = true
	pool.cfg.EnableWebSearch = true
	s, err := pool.GetOrCreate("web-search-google")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, pool.cfg)
	if !caps.WebSearch || caps.WebSearchBackend != "google" {
		t.Fatalf("capabilities should report google web_search backend: %+v", caps)
	}
}

// TestSessionCapabilities_IncludesFocusedTaskProfiles pins the
// per-session API contract: when run_task is registered, the response
// must enumerate the actual task_type values the model can request.
// Without this, an API client (WebUI, eval driver, custom dashboard)
// has to either parse the run_task tool schema themselves OR call
// affentctl doctor — both worse than reading a typed list off the
// session detail response.
func TestSessionCapabilities_IncludesFocusedTaskProfiles(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	pool.cfg.EnableMemory = true
	pool.cfg.EnableFocusedTasks = true
	// EnableWeb and EnableBrowser stay false: web_extract and research
	// must be filtered out of the reported profile list.
	s, err := pool.GetOrCreate("with-focused-tasks")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, pool.cfg)
	if !caps.FocusedTasks {
		t.Fatal("focused tasks should be registered")
	}
	want := []string{"recall", "explore", "verify", "review"}
	if !reflect.DeepEqual(caps.FocusedTaskProfiles, want) {
		t.Fatalf("FocusedTaskProfiles = %+v, want %+v", caps.FocusedTaskProfiles, want)
	}
	// Defensive: web_extract and research must NOT appear when both
	// external lookup surfaces are off; if they did, the model would
	// see task_type values it can't fulfill.
	for _, k := range caps.FocusedTaskProfiles {
		if k == "research" || k == "web_extract" {
			t.Errorf("web_extract/research must be filtered out without --web/--browser: %+v", caps.FocusedTaskProfiles)
		}
	}
}

func TestSessionCapabilities_IncludesFocusedResearchWithBrowserOnly(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{Name: agent.FocusedTaskToolName})
	reg.Add(&agent.Tool{Name: "browser_navigate"})
	caps := summarizeActiveCapabilities(&Session{registry: reg}, Config{
		EnableFocusedTasks: true,
		EnableBrowser:      true,
		EnableWeb:          false,
	})
	want := []string{"recall", "explore", "web_extract", "research", "verify", "review"}
	if !reflect.DeepEqual(caps.FocusedTaskProfiles, want) {
		t.Fatalf("FocusedTaskProfiles = %+v, want %+v", caps.FocusedTaskProfiles, want)
	}
}

func TestSessionCapabilities_TreatsBrowserFindAsBrowserSurface(t *testing.T) {
	reg := agent.NewRegistry()
	reg.Add(&agent.Tool{Name: "browser_find"})
	caps := summarizeActiveCapabilities(&Session{registry: reg}, Config{})
	if !caps.Browser {
		t.Fatalf("browser_find should report browser capability: %+v", caps)
	}
}

// TestSessionCapabilities_OmitsFocusedTaskProfilesWhenDisabled pins
// the absent-when-off contract via omitempty. The JSON wire form
// should not carry an empty array misleading clients into thinking
// the server is wired for focused tasks but configured to expose
// nothing.
func TestSessionCapabilities_OmitsFocusedTaskProfilesWhenDisabled(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	pool.cfg.EnableBuiltins = true
	pool.cfg.EnableMemory = true
	pool.cfg.EnableFocusedTasks = false
	s, err := pool.GetOrCreate("no-focused-tasks")
	if err != nil {
		t.Fatal(err)
	}
	caps := summarizeActiveCapabilities(s, pool.cfg)
	if caps.FocusedTasks {
		t.Fatal("FocusedTasks must be false when feature disabled")
	}
	if caps.FocusedTaskProfiles != nil {
		t.Errorf("FocusedTaskProfiles must be nil (omitempty) when feature disabled: %+v", caps.FocusedTaskProfiles)
	}
	// Wire-level check: marshaled JSON must NOT contain the key.
	raw, err := json.Marshal(caps)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"focused_task_profiles"`) {
		t.Errorf("wire form should omit focused_task_profiles entirely when feature disabled:\n%s", raw)
	}
}

func TestHandleSessionDetail_RejectsUnsafeID(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/..", nil)
	w := httptest.NewRecorder()
	handleSessionDetail(pool, "..", w, r)
	if got := w.Result().StatusCode; got != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", got, w.Body.String())
	}
}

func newPoolWithMemoryRoot(t *testing.T, memRoot string) *SessionPool {
	t.Helper()
	cfg := Config{
		Listen:         "127.0.0.1:0",
		MaxSessions:    8,
		SessionIdleTTL: "5m",
		WorkspaceRoot:  t.TempDir(),
		MemoryRoot:     memRoot,
		BaseURL:        "http://127.0.0.1:0",
		APIKey:         "test",
		Model:          "fake",
	}
	pool, err := NewSessionPool(cfg, zerolog.New(io.Discard))
	if err != nil {
		t.Fatalf("NewSessionPool: %v", err)
	}
	t.Cleanup(pool.Shutdown)
	return pool
}

func createDurableSessionDir(t *testing.T, pool *SessionPool, id string) {
	t.Helper()
	dir := pool.sessionDirPath(id)
	if err := os.MkdirAll(filepath.Join(dir, "topics"), 0o755); err != nil {
		t.Fatalf("mkdir session dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "conversation.jsonl"), []byte(`{"role":"system","content":"test"}`+"\n"), 0o644); err != nil {
		t.Fatalf("write conversation: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte("{}\n"), 0o644); err != nil {
		t.Fatalf("write events: %v", err)
	}
}

func sessionIDs(sessions []sessionSummary) []string {
	out := make([]string, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, s.ID)
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertSessionCapabilities(t *testing.T, got *sessionCapabilities, want sessionCapabilities) {
	t.Helper()
	if got == nil {
		t.Fatalf("capabilities = nil, want %+v", want)
	}
	// Compare struct-wise; the slice field disqualifies `==`, so we
	// reflect.DeepEqual here. This keeps focused_task_profiles exact
	// while preserving the older scalar-field comparisons.
	if !reflect.DeepEqual(*got, want) {
		t.Fatalf("capabilities = %+v, want %+v", *got, want)
	}
}
