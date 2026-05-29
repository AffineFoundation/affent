package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/memory"
	"github.com/affinefoundation/affent/internal/sessionstate"
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
		Builtins:              true,
		WorkspaceTools:        []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", agent.SymbolContextToolName, "repo_search"},
		SkillInstall:          true,
		Plan:                  true,
		SessionSchedule:       true,
		SessionScheduleRunner: true,
		Memory:                true,
		SessionSearch:         true,
		SymbolContext:         true,
		RepoSearch:            true,
		Web:                   true,
		Subagent:              true,
		SubagentMaxDepth:      3,
		FocusedTasks:          true,
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

func TestSummarizeDurableSessionReadsMetadataWorkspace(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "archived-workspace")
	workspace := "/workspace/sessions/archived-workspace-123"
	if err := sessionstate.WriteMetadata(pool.sessionDirPath("archived-workspace"), sessionstate.Metadata{
		SessionID:     "archived-workspace",
		WorkspacePath: workspace,
	}); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	summary, found, err := summarizeDurableSession(pool, "archived-workspace")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session not found")
	}
	if summary.WorkspacePath != workspace || summary.WorkspaceLabel != filepath.Base(workspace) {
		t.Fatalf("workspace summary = %q/%q, want %q/%q", summary.WorkspacePath, summary.WorkspaceLabel, workspace, filepath.Base(workspace))
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

func TestSummarizeActiveSessionUsesMainSessionEventsForRecoveryHintAndCWD(t *testing.T) {
	dir := t.TempDir()
	conv, err := agent.OpenConversationAt(filepath.Join(dir, "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(agent.ChatMessage{Role: "user", Content: "continue live dashboard evidence"}); err != nil {
		t.Fatal(err)
	}
	toolRecovery := "BROWSER NETWORK EVIDENCE\n" +
		"CURRENT_PAGE: https://taostats.io/subnets/120\n" +
		"query: \"market_cap\"\n" +
		"EVIDENCE_STATUS: refs_only_not_citable; read_required=true\n" +
		"MATCHES:\n" +
		"- n1 status=200 resource=fetch content_type=application/json url=https://taostats.io/api/subnets/120\n" +
		"Next: call browser_network_read with the most relevant ref and json_path before citing values.\n"
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "shell-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "go test ./...", "cwd": "cmd/affentserve"},
		})+
			sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
				TurnID:        "t1",
				CallID:        "net-1",
				ResultSummary: toolRecovery,
				Result:        toolRecovery,
			}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary := summarizeActiveSession(&Session{
		ID:         "active-main-events",
		conv:       conv,
		registry:   agent.NewRegistry(),
		sessionDir: dir,
		workspace:  "/tmp/workspace",
		createdAt:  time.Now(),
		lastUsed:   time.Now(),
	}, Config{})
	if summary.LatestRecoveryHint != "call browser_network_read with the most relevant ref and json_path before citing values." {
		t.Fatalf("latest_recovery_hint = %q, want main session event tool guidance", summary.LatestRecoveryHint)
	}
	if summary.LastAgentCWD != "cmd/affentserve" {
		t.Fatalf("last_agent_cwd = %q, want main session shell cwd", summary.LastAgentCWD)
	}
}

func TestSessionContextSnapshotUsesCompactionTrigger(t *testing.T) {
	got := sessionContextSnapshot(96, 16*1024, 1024, Config{CompactTrigger: 120})
	if got.MessageCount != 96 || got.CompactTrigger != 120 || got.CompactPercent != 80 || got.MessageCompactPercent != 80 || got.MessagesUntilCompact != 24 {
		t.Fatalf("context snapshot = %+v, want 96/120 at 80%% with 24 remaining", got)
	}
	over := sessionContextSnapshot(130, 16*1024, 1024, Config{CompactTrigger: 120})
	if over.CompactPercent != 108 || over.MessagesUntilCompact != 0 {
		t.Fatalf("over-trigger snapshot = %+v, want 108%% and no remaining messages", over)
	}
	def := sessionContextSnapshot(1, 1024, 256, Config{})
	if def.CompactTrigger != agent.DefaultSummaryTriggerMsgs {
		t.Fatalf("default trigger = %d, want %d", def.CompactTrigger, agent.DefaultSummaryTriggerMsgs)
	}
	if def.CompactTriggerBytes != agent.DefaultSummaryTriggerBytes {
		t.Fatalf("default byte trigger = %d, want %d", def.CompactTriggerBytes, agent.DefaultSummaryTriggerBytes)
	}
	if def.CompactTriggerInputTokens != agent.DefaultSummaryTriggerInputTokens {
		t.Fatalf("default input-token trigger = %d, want %d", def.CompactTriggerInputTokens, agent.DefaultSummaryTriggerInputTokens)
	}
}

func TestSessionContextSnapshotUsesBytePressure(t *testing.T) {
	contextBytes := agent.DefaultSummaryTriggerBytes + agent.DefaultSummaryTriggerBytes/4
	got := sessionContextSnapshot(12, contextBytes, 1024, Config{CompactTrigger: 240})
	if got.CompactPercent != 125 || got.ByteCompactPercent != 125 || got.MessageCompactPercent != 5 {
		t.Fatalf("context snapshot = %+v, want byte pressure to dominate at 125%%", got)
	}
	if got.BytesUntilCompact != 0 {
		t.Fatalf("bytes_until_compact = %d, want 0", got.BytesUntilCompact)
	}
	if got.ContextBytes != contextBytes {
		t.Fatalf("context_bytes = %d, want %d", got.ContextBytes, contextBytes)
	}
}

func TestSessionContextSnapshotUsesRequestInputPressure(t *testing.T) {
	inputTokens := agent.DefaultSummaryTriggerInputTokens + agent.DefaultSummaryTriggerInputTokens/2
	got := sessionContextSnapshot(12, 16*1024, inputTokens, Config{CompactTrigger: 240})
	if got.CompactPercent != 150 || got.RequestInputCompactPercent != 150 || got.MessageCompactPercent != 5 {
		t.Fatalf("context snapshot = %+v, want request-input pressure to dominate at 150%%", got)
	}
	if got.RequestInputTokensUntilCompact != 0 {
		t.Fatalf("request_input_tokens_until_compact = %d, want 0", got.RequestInputTokensUntilCompact)
	}
	if got.EstimatedRequestInputTokens != inputTokens {
		t.Fatalf("estimated_request_input_tokens = %d, want %d", got.EstimatedRequestInputTokens, inputTokens)
	}
}

func TestSessionContextSnapshotUsesConfiguredRequestInputTrigger(t *testing.T) {
	got := sessionContextSnapshot(12, 16*1024, 750, Config{CompactTrigger: 240, CompactTriggerInputTokens: 1000})
	if got.CompactTriggerInputTokens != 1000 ||
		got.RequestInputCompactPercent != 75 ||
		got.RequestInputTokensUntilCompact != 250 {
		t.Fatalf("context snapshot = %+v, want configured request-input trigger at 75%% with 250 remaining", got)
	}

	disabled := sessionContextSnapshot(12, 16*1024, 750, Config{CompactTrigger: 240, CompactTriggerInputTokens: -1})
	if disabled.CompactTriggerInputTokens != 0 ||
		disabled.RequestInputCompactPercent != 0 ||
		disabled.RequestInputTokensUntilCompact != 0 {
		t.Fatalf("disabled request-input snapshot = %+v, want no request-input pressure", disabled)
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

func TestSummarizeDurableSessionRestoresRecoveryHintFromLoopProtocolFeed(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-feed-hint")
	dir := pool.sessionDirPath("loop-feed-hint")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeLoopProtocolFeed, sse.LoopProtocolFeedPayload{
			TurnID:                     "t2",
			LoopID:                     "longrun",
			Mode:                       "digest",
			FeedNumber:                 8,
			PlanLabel:                  "plan:2/5:active",
			PlanCurrentStep:            "continue RECOVERY-FEED-88 through browser_network_read evidence",
			LastTurnEndReason:          sse.TurnEndMaxTurns,
			LastTurnLoopGuards:         2,
			LastTurnToolErrors:         1,
			LastTurnForcedNoTools:      1,
			LastTurnMemorySearchMisses: 1,
			LastTurnSessionSearchCalls: 1,
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "loop-feed-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{
		"loop feed recovery",
		"RECOVERY-FEED-88",
		"browser_network",
		"end=max_turns",
		"guards=2",
		"tool_errors=1",
		"forced_no_tools=1",
		"mem_miss=1",
		"sess_search=1",
	} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestSummarizeDurableSessionIgnoresRoutineLoopProtocolFeedHint(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-feed-routine")
	dir := pool.sessionDirPath("loop-feed-routine")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeLoopProtocolFeed, sse.LoopProtocolFeedPayload{
			TurnID:          "t1",
			LoopID:          "longrun",
			Mode:            "digest",
			FeedNumber:      3,
			PlanCurrentStep: "continue normal task",
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "loop-feed-routine")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "" {
		t.Fatalf("latest_recovery_hint = %q, want no routine loop feed hint", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromLoopTurnCheckpoint(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-turn-checkpoint-hint")
	dir := pool.sessionDirPath("loop-turn-checkpoint-hint")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeLoopTurnCheckpoint, sse.LoopTurnCheckpointPayload{
			TurnID:             "turn-checkpoint",
			LoopID:             "longrun",
			Status:             "running",
			EndReason:          sse.TurnEndMaxTurns,
			ToolErrors:         2,
			LoopGuards:         1,
			ForcedNoTools:      1,
			MemoryMisses:       1,
			SessionSearchCalls: 1,
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "loop-turn-checkpoint-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{
		"loop turn checkpoint recovery",
		"end=max_turns",
		"guards=1",
		"tool_errors=2",
		"forced_no_tools=1",
		"mem_miss=1",
		"sess_search=1",
		"loop=longrun",
		"inspect LOOP/plan",
	} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestSummarizeDurableSessionIgnoresRoutineLoopTurnCheckpointHint(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "loop-turn-checkpoint-routine")
	dir := pool.sessionDirPath("loop-turn-checkpoint-routine")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeLoopTurnCheckpoint, sse.LoopTurnCheckpointPayload{
			TurnID:    "turn-checkpoint",
			LoopID:    "longrun",
			Status:    "running",
			EndReason: sse.TurnEndCompleted,
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "loop-turn-checkpoint-routine")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "" {
		t.Fatalf("latest_recovery_hint = %q, want no routine checkpoint hint", summary.LatestRecoveryHint)
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

func TestSummarizeDurableSessionRestoresRecoveryHintFromToolRepairFailure(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "tool-repair-failed")
	dir := pool.sessionDirPath("tool-repair-failed")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
			ToolStats: &sse.ToolRuntimeStats{
				ToolErrors:          1,
				ToolRepairFailed:    1,
				ToolRepairByKind:    map[string]int{"malformed_json": 1},
				ToolRepairSucceeded: 2,
			},
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "tool-repair-failed")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"tool-call repair failed", "tool repair failed=1", "kind=malformed_json:1", "tool_errors=1"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestSummarizeDurableSessionIgnoresSuccessfulToolRepairHint(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "tool-repair-success")
	dir := pool.sessionDirPath("tool-repair-success")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
			ToolStats: &sse.ToolRuntimeStats{
				ToolRepairCalls:     2,
				ToolRepairSucceeded: 2,
				ToolRepairNotes:     2,
				ToolRepairByKind:    map[string]int{"alias_rename": 2},
			},
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "tool-repair-success")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.LatestRecoveryHint != "" {
		t.Fatalf("latest_recovery_hint = %q, want no successful repair hint", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromTruncatedArtifact(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "artifact-hint")
	dir := pool.sessionDirPath("artifact-hint")

	artifactRel := path.Join(artifactPathPrefix, "000001-c1.txt")
	artifactFull := filepath.Join(dir, filepath.FromSlash(artifactRel))
	if err := os.MkdirAll(filepath.Dir(artifactFull), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(artifactFull, []byte(strings.Repeat("large output\n", 256)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:              "t1",
			CallID:              "c1",
			ExitCode:            0,
			ResultSummary:       "large output preview",
			Result:              "large output preview",
			ResultTruncated:     true,
			ResultOmittedBytes:  4096,
			ContextOmittedBytes: 8192,
			ResultArtifactPath:  artifactRel,
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "artifact-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if !summary.HasArtifacts {
		t.Fatal("summary should report saved artifacts")
	}
	if summary.Artifacts == nil ||
		summary.Artifacts.Count != 1 ||
		summary.Artifacts.TotalBytes != int64(len(strings.Repeat("large output\n", 256))) ||
		summary.Artifacts.LatestPath != artifactRel ||
		summary.Artifacts.LatestModTime == "" {
		t.Fatalf("artifacts summary = %+v, want count/bytes/latest path", summary.Artifacts)
	}
	for _, want := range []string{"truncated tool output", artifactRel, "result omitted 4096 bytes", "context omitted 8192 bytes"} {
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

func TestRecoveryHintFromConversationSessionSearchRecentPlanAnchor(t *testing.T) {
	result := `{"query":"missing marker","total":0,"results":[],"recent_sessions":[{"session_id":"recent-plan","plan":"plan_status: plan:1/2:active current_step: 2 [in_progress] Continue Bittensor subnet 120 validator inventory"}]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"session recall found no direct hits", "retry from recent session recent-plan", "current_step", "Bittens"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation recovery hint missing %q: %q", want, got)
		}
	}
}

func TestRecoveryHintFromConversationSessionSearchRecentLoopAnchor(t *testing.T) {
	result := `{"query":"missing marker","total":0,"results":[],"recent_sessions":[{"session_id":"recent-loop","loop":"loop_status: running current_situation: Continue Alpha Coast market recovery with primary filings"}]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"session recall found no direct hits", "retry from recent session recent-loop", "current_situation", "Alpha Coast"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation recovery hint missing %q: %q", want, got)
		}
	}
}

func TestRecoveryHintFromConversationSessionSearchPrefersRecoveryAnchors(t *testing.T) {
	result := `{"query":"missing marker","total":0,"results":[],"recent_sessions":[{"session_id":"recent-loop","latest_user":"Continue the previous task","loop":"recent_loop_events: event: type=loop.protocol_feed mode=digest feed=4","recovery":"turn_end: reason=max_turns"}]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"session recall found no direct hits", "retry from recent session recent-loop", "recovery=turn_end", "loop=recent_loop_events", "loop.protocol_feed"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation recovery hint missing %q: %q", want, got)
		}
	}
}

func TestRecoveryHintFromConversationSessionSearchRecentRecoveryAnchor(t *testing.T) {
	result := `{"query":"missing marker","total":0,"results":[],"recent_sessions":[{"session_id":"recent-recovery","recovery":"turn_end: reason=max_turns; top_failure=loop_guard_no_new_evidence:2; loop_guards=2"}]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"session recall found no direct hits", "retry from recent session recent-recovery", "reason=max_turns", "loop_guard_no_new_evidence"} {
		if !strings.Contains(got, want) {
			t.Fatalf("conversation recovery hint missing %q: %q", want, got)
		}
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromWeakSessionSearchHits(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "recall-weak-hits")
	dir := pool.sessionDirPath("recall-weak-hits")

	result := `{"query":"alpha coast decision","total":2,"results":[{"session_id":"market-alpha","turn_idx":4,"message_idx":8,"role":"assistant","snippet":"final marker HIST-STOCK-44","matched_terms":["alpha"],"context_included":false},{"session_id":"market-beta","turn_idx":2,"message_idx":5,"role":"user","snippet":"alpha coast","matched_terms":["alpha","coast"],"context_included":false}]}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t1", CallID: "search-1", ExitCode: 0, ResultSummary: result, Result: result}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "recall-weak-hits")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"session recall hits lack adjacent context", "market-alpha", "turn=4", "message=8"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestRecoveryHintFromSessionSearchHitsAllowsPlanAnchor(t *testing.T) {
	result := `{"query":"loop protocol","total":1,"results":[{"session_id":"looped","role":"plan","snippet":"current_step: verify loop protocol","matched_terms":["loop","protocol"],"context_included":false}]}`
	if got := recoveryHintFromSessionSearchResult(result); got != "" {
		t.Fatalf("hint = %q, want no weak-hit warning for plan anchor", got)
	}
}

func TestRecoveryHintFromSessionSearchHitsAllowsLoopAnchor(t *testing.T) {
	result := `{"query":"loop protocol","total":1,"results":[{"session_id":"looped","role":"loop","snippet":"current_situation: verify loop protocol","matched_terms":["loop","protocol"],"context_included":false}]}`
	if got := recoveryHintFromSessionSearchResult(result); got != "" {
		t.Fatalf("hint = %q, want no weak-hit warning for loop anchor", got)
	}
}

func TestRecoveryHintFromSessionSearchHitsAllowsEventAnchor(t *testing.T) {
	result := `{"query":"loop guard max turns","total":1,"results":[{"session_id":"stalled","role":"event","snippet":"turn_end: reason=max_turns; top_failure=loop_guard_no_new_evidence:2","matched_terms":["loop","guard"],"context_included":false}]}`
	if got := recoveryHintFromSessionSearchResult(result); got != "" {
		t.Fatalf("hint = %q, want no weak-hit warning for recovery event anchor", got)
	}
}

func TestRecoveryHintFromSessionSearchDoesNotTreatOtherResultJSONAsWeakHits(t *testing.T) {
	result := `{"ok":true,"target":"memory","results":[{"topic":"markets","entry":"alpha coast decision"}]}`
	if got := recoveryHintFromSessionSearchResult(result); got != "" {
		t.Fatalf("hint = %q, want no session_search hint for non-session result JSON", got)
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

func TestSummarizeDurableSessionIncludesCompactMemorySummary(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "memory-summary")
	dir := pool.sessionDirPath("memory-summary")
	store := memory.NewFileMemoryStore("")
	store.MemoryDir = dir
	store.UserPath = pool.userMemoryPath(dir)
	if resp, err := store.Add(memory.TargetMemory, "markets", "prefer source-led market reports"); err != nil || !resp.OK {
		t.Fatalf("add markets memory resp=%+v err=%v", resp, err)
	}
	if resp, err := store.Add(memory.TargetMemory, memory.CoreTopic, "always preserve browser network evidence"); err != nil || !resp.OK {
		t.Fatalf("add core memory resp=%+v err=%v", resp, err)
	}

	summary, found, err := summarizeDurableSession(pool, "memory-summary")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasMemory || summary.Memory == nil {
		t.Fatalf("memory summary missing: found=%v summary=%+v", found, summary)
	}
	if summary.Memory.BucketCount != 2 ||
		summary.Memory.EntryCount != 2 ||
		summary.Memory.CharsUsed == 0 ||
		summary.Memory.LatestTarget != "memory" ||
		summary.Memory.LatestTopic == "" ||
		summary.Memory.LatestAt == "" {
		t.Fatalf("memory summary = %+v, want compact bucket totals and latest stamped topic", summary.Memory)
	}
}

func TestSummarizeDurableSessionRestoresLatestMemoryUpdateFromLoopState(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "memory-loop-state")
	dir := pool.sessionDirPath("memory-loop-state")
	statePath := sessionLoopStatePath(pool, "memory-loop-state")

	if err := loopstate.WriteState(statePath, loopstate.State{
		Version:                1,
		LoopID:                 "memory-loop-state",
		OwnerSession:           "memory-loop-state",
		Status:                 "running",
		MemoryUpdateEvents:     1,
		LastMemoryUpdateAction: "replace",
		LastMemoryUpdateTarget: "memory",
		LastMemoryUpdateTopic:  "markets",
		LastMemoryUpdateLoc:    "memory:markets",
		LastMemoryUpdatePrev:   "old dashboard rule",
		LastMemoryUpdateNext:   "prefer browser network evidence",
		LastMemoryUpdate:       "old dashboard rule -> prefer browser network evidence",
	}); err != nil {
		t.Fatalf("write loop state: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: "t1", Reason: sse.TurnEndCompleted}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "memory-loop-state")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasLoopState {
		t.Fatalf("loop state summary missing: found=%v summary=%+v", found, summary)
	}
	if summary.LatestMemoryUpdate == nil ||
		summary.LatestMemoryUpdate.Action != "replace" ||
		summary.LatestMemoryUpdate.Target != "memory" ||
		summary.LatestMemoryUpdate.Topic != "markets" ||
		summary.LatestMemoryUpdate.Location != "memory:markets" ||
		summary.LatestMemoryUpdate.Preview != "old dashboard rule -> prefer browser network evidence" ||
		summary.LatestMemoryUpdate.PreviousPreview != "old dashboard rule" ||
		summary.LatestMemoryUpdate.NextPreview != "prefer browser network evidence" {
		t.Fatalf("latest_memory_update = %+v, want loop-state memory checkpoint", summary.LatestMemoryUpdate)
	}
}

func TestSummarizeDurableSessionRestoresTaskStateFromRuntimeEvents(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "task-state-events")
	dir := pool.sessionDirPath("task-state-events")

	body := sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID:       "t1",
		Text:         "Fix clamp behavior, verify it, and push the code",
		DisplayText:  "Scheduled fix clamp behavior",
		Mode:         "execute_plan",
		Source:       "schedule",
		ScheduleID:   "sched_clamp",
		ScheduleKind: "checkin",
	}) +
		sessionEventLine(t, sse.TypeRuntimeSurface, sse.RuntimeSurfacePayload{
			TurnID:    "t1",
			ToolCount: 3,
			Tools: []sse.RuntimeSurfaceTool{
				{Name: "read_file", Group: "Workspace"},
				{Name: "memory", Group: "Memory"},
				{Name: "session_search", Group: "History"},
			},
			Capabilities: sse.RuntimeCapabilities{
				WorkspaceTools: []string{"read_file"},
				Memory:         true,
				SessionSearch:  true,
			},
			Workspace: &sse.RuntimeWorkspace{
				DefaultCWD: "workspace_root",
				PathMode:   "workspace_relative",
				RootEntries: []sse.RuntimeWorkspaceEntry{
					{Name: "remote.git", Kind: "dir"},
					{Name: "README.md", Kind: "file"},
				},
			},
		}) +
		sessionEventLine(t, sse.TypeContextInjected, sse.ContextInjectedPayload{
			TurnID:  "t1",
			Source:  "runtime_workspace",
			Summary: "Workspace tools resolve relative paths from the session workspace root.",
		}) +
		sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "read-1",
			Tool:   "read_file",
			Args:   map[string]any{"path": "app/mathutil/clamp.go"},
		}) +
		sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "edit-1",
			Tool:   "edit_file",
			Args:   map[string]any{"path": "app/mathutil/clamp.go"},
		}) +
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "edit-1",
			ExitCode:      0,
			ResultSummary: "replaced 1 occurrence",
		}) +
		sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "test-fail-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "go test ./..."},
		}) +
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "test-fail-1",
			ExitCode:      1,
			FailureKind:   "test_failed",
			ResultSummary: "FAIL ./...",
			Result:        "FAIL ./...\nNext: inspect clamp bounds then rerun go test\nFailure: kind=test_failed",
		}) +
		sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
			TurnID: "t1",
			CallID: "test-1",
			Tool:   "shell",
			Args:   map[string]any{"command": "go test ./..."},
		}) +
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
			TurnID:        "t1",
			CallID:        "test-1",
			ExitCode:      0,
			ResultSummary: "ok",
			Result:        "ok",
		}) +
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
			TurnID: "t1",
			Reason: sse.TurnEndCompleted,
		})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "task-state-events")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.TaskState == nil {
		t.Fatalf("task_state missing: found=%v summary=%+v", found, summary)
	}
	task := summary.TaskState
	if task.Objective != "Scheduled fix clamp behavior" || task.Status != "completed" {
		t.Fatalf("task_state objective/status = %q/%q, want completed clamp task", task.Objective, task.Status)
	}
	if task.RequestMode != "execute_plan" || task.RequestSource != "schedule" || task.ScheduleID != "sched_clamp" || task.ScheduleKind != "checkin" {
		t.Fatalf("request fields = mode:%q source:%q schedule:%q kind:%q, want scheduled execute_plan", task.RequestMode, task.RequestSource, task.ScheduleID, task.ScheduleKind)
	}
	if task.VerificationState != "last_shell_passed" {
		t.Fatalf("verification_state = %q, want last_shell_passed", task.VerificationState)
	}
	if task.NextStep != "" {
		t.Fatalf("next_step = %q, want empty after completed task with passing verification", task.NextStep)
	}
	if len(task.FailedActions) != 1 || task.FailedActions[0].Tool != "shell" || !stringSliceContains(task.FailedActions[0].Kinds, "test_failed") {
		t.Fatalf("failed_actions = %+v, want historical failed shell evidence", task.FailedActions)
	}
	if task.FailedActions[0].Next != "inspect clamp bounds then rerun go test" {
		t.Fatalf("failed_actions[0].next = %q, want recovery hint", task.FailedActions[0].Next)
	}
	if !stringSliceContains(task.Constraints, "workspace path mode: workspace_relative") {
		t.Fatalf("constraints = %+v, want workspace path mode", task.Constraints)
	}
	if !stringSliceContains(task.Constraints, "unavailable capabilities: live sources, nested work, skills, loop protocol, schedules, schedule runner") {
		t.Fatalf("constraints = %+v, want runtime capability gaps", task.Constraints)
	}
	if !stringSliceContains(task.KnownFacts, "available capabilities: workspace, memory/history") {
		t.Fatalf("known_facts = %+v, want available runtime capabilities", task.KnownFacts)
	}
	if !stringSliceContains(task.KnownFacts, "latest request mode: execute_plan") ||
		!stringSliceContains(task.KnownFacts, "latest request source: schedule checkin sched_clamp") {
		t.Fatalf("known_facts = %+v, want request provenance", task.KnownFacts)
	}
	if !stringSliceContains(task.Sources, "runtime_surface") || !stringSliceContains(task.Sources, "runtime_workspace") || !stringSliceContains(task.Sources, "schedule") {
		t.Fatalf("sources = %+v, want runtime workspace and schedule sources", task.Sources)
	}
	if len(task.ChangedFiles) != 1 || task.ChangedFiles[0].Path != "app/mathutil/clamp.go" || task.ChangedFiles[0].Action != "edit" {
		t.Fatalf("changed_files = %+v, want edited clamp.go", task.ChangedFiles)
	}
	if !taskActionsContain(task.AttemptedActions, "shell", "go test ./...") {
		t.Fatalf("attempted_actions = %+v, want shell verification", task.AttemptedActions)
	}
	if !taskEvidenceContains(task.Evidence, "shell", "go test ./...") ||
		!taskEvidenceContains(task.Evidence, "runtime_workspace", "Workspace tools resolve relative paths") {
		t.Fatalf("evidence = %+v, want workspace and shell evidence", task.Evidence)
	}
}

func TestSummarizeDurableSessionDefaultsTaskRequestSourceToUser(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "task-state-user-source")
	dir := pool.sessionDirPath("task-state-user-source")
	body := sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Inspect the repository and report risks",
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t1",
		Reason: sse.TurnEndCompleted,
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "task-state-user-source")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.TaskState == nil {
		t.Fatalf("task_state missing: found=%v summary=%+v", found, summary)
	}
	task := summary.TaskState
	if task.RequestMode != "normal" || task.RequestSource != "user" {
		t.Fatalf("request fields = mode:%q source:%q, want normal/user", task.RequestMode, task.RequestSource)
	}
	if !stringSliceContains(task.KnownFacts, "latest request source: user") {
		t.Fatalf("known_facts = %+v, want user request source", task.KnownFacts)
	}
	if !stringSliceContains(task.Sources, "user") {
		t.Fatalf("sources = %+v, want user source", task.Sources)
	}
}

func TestSummarizeDurableSessionKeepsTaskObjectiveAcrossScheduledTicks(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "task-state-scheduled-objective")
	dir := pool.sessionDirPath("task-state-scheduled-objective")
	body := sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Build a release notes generator and keep iterating until tests pass.",
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t1",
		Reason: sse.TurnEndCompleted,
	}) + sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID:       "t2",
		Text:         "Scheduled loop tick for release notes generator",
		DisplayText:  "Loop tick: continue release notes generator",
		Source:       "schedule",
		ScheduleID:   "sched_release_notes",
		ScheduleKind: sessionScheduleKindLoopTick,
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t2",
		Reason: sse.TurnEndCompleted,
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "task-state-scheduled-objective")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.TaskState == nil {
		t.Fatalf("task_state missing: found=%v summary=%+v", found, summary)
	}
	task := summary.TaskState
	if summary.TopicUserMessage != "Build a release notes generator and keep iterating until tests pass." {
		t.Fatalf("topic_user_message = %q, want durable first task request", summary.TopicUserMessage)
	}
	if task.Objective != "Build a release notes generator and keep iterating until tests pass." {
		t.Fatalf("objective = %q, want durable first task request", task.Objective)
	}
	if task.RequestSource != "schedule" || task.ScheduleKind != sessionScheduleKindLoopTick || task.ScheduleID != "sched_release_notes" {
		t.Fatalf("request provenance = source:%q kind:%q id:%q, want latest scheduled tick", task.RequestSource, task.ScheduleKind, task.ScheduleID)
	}
	if !stringSliceContains(task.KnownFacts, "latest request source: schedule "+sessionScheduleKindLoopTick+" sched_release_notes") {
		t.Fatalf("known_facts = %+v, want latest scheduled request fact", task.KnownFacts)
	}
}

func TestSummarizeDurableSessionRestoresRuntimeOwnedToolEvidence(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "task-state-schedule-evidence")
	dir := pool.sessionDirPath("task-state-schedule-evidence")
	body := sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Schedule recurring BTC price checks.",
	}) + sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t1",
		CallID: "schedule-1",
		Tool:   sessionScheduleToolName,
		Args:   map[string]any{"action": "create"},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:        "t1",
		CallID:        "schedule-1",
		ExitCode:      0,
		ResultSummary: "created schedule sched_btc",
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t1",
		Reason: sse.TurnEndCompleted,
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "task-state-schedule-evidence")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.TaskState == nil {
		t.Fatalf("task_state missing: found=%v summary=%+v", found, summary)
	}
	task := summary.TaskState
	if !taskActionsContain(task.AttemptedActions, sessionScheduleToolName, "create") {
		t.Fatalf("attempted_actions = %+v, want session_schedule create", task.AttemptedActions)
	}
	if !taskEvidenceContains(task.Evidence, sessionScheduleToolName, "create") {
		t.Fatalf("evidence = %+v, want session_schedule evidence", task.Evidence)
	}
	if !stringSliceContains(task.Sources, sessionScheduleToolName) {
		t.Fatalf("sources = %+v, want session_schedule", task.Sources)
	}
	if task.VerificationState != "unknown" {
		t.Fatalf("verification_state = %q, want unknown for non-shell state update", task.VerificationState)
	}
}

func TestSummarizeDurableSessionClassifiesGitHandoffEvidence(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "task-state-git-handoff")
	dir := pool.sessionDirPath("task-state-git-handoff")
	body := sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID: "t1",
		Text:   "Fix the code, commit, and push.",
	}) + sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t1",
		CallID: "commit-1",
		Tool:   "shell",
		Args:   map[string]any{"command": "git -C app commit -m fix"},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:        "t1",
		CallID:        "commit-1",
		ExitCode:      0,
		ResultSummary: "[main abc1234] fix",
	}) + sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t1",
		CallID: "push-1",
		Tool:   "shell",
		Args:   map[string]any{"command": "git push origin main"},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:        "t1",
		CallID:        "push-1",
		ExitCode:      0,
		ResultSummary: "main -> main",
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t1",
		Reason: sse.TurnEndCompleted,
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "task-state-git-handoff")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.TaskState == nil {
		t.Fatalf("task_state missing: found=%v summary=%+v", found, summary)
	}
	task := summary.TaskState
	if !taskEvidenceContains(task.Evidence, "git_commit", "git -C app commit") ||
		!taskEvidenceContains(task.Evidence, "git_push", "git push origin main") {
		t.Fatalf("evidence = %+v, want git commit and push handoff evidence", task.Evidence)
	}
	if !stringSliceContains(task.Sources, "git_commit") || !stringSliceContains(task.Sources, "git_push") {
		t.Fatalf("sources = %+v, want git commit and push handoff sources", task.Sources)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromLoopStateDecision(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "decision-loop-state")
	statePath := sessionLoopStatePath(pool, "decision-loop-state")

	if err := loopstate.WriteState(statePath, loopstate.State{
		Version:                1,
		LoopID:                 "decision-loop-state",
		OwnerSession:           "decision-loop-state",
		Status:                 "running",
		LastDecisionKind:       "evidence_quality",
		LastDecision:           "defer",
		LastDecisionAction:     "browser_network_read RECOVER-STATE-17",
		LastTurnEndReason:      sse.TurnEndMaxTurns,
		LastTurnLoopGuards:     1,
		LastTurnToolErrors:     1,
		LastTurnForcedNoTools:  1,
		LastTurnMemoryMisses:   2,
		LastTurnSessionSearch:  1,
		LastPlanStep:           "RECOVER-STATE-17 evidence",
		LastPlanStepStatus:     "in_progress",
		LastPlanStepIndex:      2,
		LastProtocolFeedMode:   "digest",
		NeedsFullProtocolFeed:  true,
		LastCalibrationAnswer:  "stop when network evidence is absent",
		CalibrationAnswers:     1,
		ContextCompactions:     1,
		LastCompactionReason:   "context_overflow",
		LastCompactionReactive: true,
		MemoryUpdateEvents:     0,
		LastMemoryUpdateAction: "",
		LastMemoryUpdateLoc:    "",
		LastMemoryUpdate:       "",
		LastMemoryUpdateNext:   "",
	}); err != nil {
		t.Fatalf("write loop state: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "decision-loop-state")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasLoopState {
		t.Fatalf("loop state summary missing: found=%v summary=%+v", found, summary)
	}
	for _, want := range []string{"loop decision evidence_quality=defer", "browser_network_read", "RECOVER-STATE-17", "end=max_turns", "guards=1", "tool_errors=1", "forced_no_tools=1", "mem_miss=2"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestSummarizeDurableSessionIgnoresRoutineLoopStateRecoveryHint(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "routine-loop-state")

	if err := loopstate.WriteState(sessionLoopStatePath(pool, "routine-loop-state"), loopstate.State{
		Version:       1,
		LoopID:        "routine-loop-state",
		OwnerSession:  "routine-loop-state",
		Status:        "running",
		LastDecision:  "continue",
		LastPlanStep:  "continue normal task",
		ProtocolFeeds: 3,
	}); err != nil {
		t.Fatalf("write loop state: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "routine-loop-state")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasLoopState {
		t.Fatalf("loop state summary missing: found=%v summary=%+v", found, summary)
	}
	if summary.LatestRecoveryHint != "" {
		t.Fatalf("latest_recovery_hint = %q, want no routine loop state hint", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRecoveryHintFromContextCompactionGap(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "compaction-summary-gap")
	dir := pool.sessionDirPath("compaction-summary-gap")

	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeContextCompact, map[string]any{
			"turn_id":          "t1",
			"before_messages":  80,
			"after_messages":   18,
			"removed_messages": 62,
			"reactive":         true,
			"reason":           "context_overflow",
			"summary_present":  false,
		}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "compaction-summary-gap")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"context compaction summary missing", "removed 62 message", "reason=context_overflow", "reactive=true", "recover from durable plan"} {
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

func TestSummarizeDurableSessionRestoresRecoveryHintFromMemorySearchNoAnchors(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "memory-miss-no-anchors")
	dir := pool.sessionDirPath("memory-miss-no-anchors")

	result := `{"ok":true,"message":"no entries matched. Next: retry with fewer/different keywords, search a specific topic from topics, or use action=list for full topic discovery.","target":"memory","results":[]}`
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{TurnID: "t1", CallID: "memory-1", ExitCode: 0, ResultSummary: result, Result: result}),
	), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "memory-miss-no-anchors")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"memory search found no direct hits", "target=memory", "no topic anchors", "action=list", "session_search"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestRecoveryHintFromConversationMemorySearchUserNoAnchors(t *testing.T) {
	result := `{"ok":true,"message":"no entries matched. Next: retry with fewer/different keywords.","target":"user","results":[]}`
	got := recoveryHintFromConversationMessage(agent.ChatMessage{
		Role:    "tool",
		Content: result,
	})
	for _, want := range []string{"memory search found no direct hits", "target=user", "no user-memory anchors", "session_search"} {
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

func TestSummarizeDurableSessionWarnsAboutSkippedEventLogRecords(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "trace-gap-hint")
	dir := pool.sessionDirPath("trace-gap-hint")

	body := sessionEventLine(t, sse.TypeTraceMeta, sse.TraceMetaPayload{SchemaVersion: sse.TraceSchemaVersion}) +
		strings.Repeat("x", maxSessionSummaryLineBytes+1) + "\n" +
		"{not valid json}\n" +
		sessionEventLine(t, sse.TypeTurnStart, sse.TurnStartPayload{TurnID: "after-gap"})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "trace-gap-hint")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	for _, want := range []string{"event log skipped 2", "skipped_lines", "oversized=1", "invalid=1"} {
		if !strings.Contains(summary.LatestRecoveryHint, want) {
			t.Fatalf("latest_recovery_hint missing %q: %q", want, summary.LatestRecoveryHint)
		}
	}
}

func TestSummarizeDurableSessionRestoresToolStatsFromEvents(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "durable-tool-stats")
	dir := pool.sessionDirPath("durable-tool-stats")

	body := sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t0",
		CallID: "skipped-fetch",
		Tool:   "web_fetch",
		Args:   map[string]any{"url": "https://api.taostats.io/api/subnet/120"},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:   "t0",
		CallID:   "skipped-fetch",
		ExitCode: 1,
		Result:   "(max_turns reached before this tool ran)",
	}) + sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t0",
		CallID: "bad-plan",
		Tool:   "plan",
		Args:   map[string]any{"action": "update"},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:   "t0",
		CallID:   "bad-plan",
		ExitCode: 1,
		Result:   "invalid args",
	}) + sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t0",
		CallID: "focused-review",
		Tool:   "run_task",
		Args:   map[string]any{"task_type": "review"},
		Delegation: &sse.DelegationMeta{
			Kind:     "focused_task",
			TaskType: "review",
		},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:   "t0",
		CallID:   "focused-review",
		ExitCode: 0,
		Result:   "ok",
		Delegation: &sse.DelegationMeta{
			Kind:     "focused_task",
			TaskType: "review",
		},
	}) + sessionEventLine(t, sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID: "t0",
		CallID: "subagent-research",
		Tool:   "subagent_run",
		Args:   map[string]any{"mode": "research"},
		Delegation: &sse.DelegationMeta{
			Kind: "subagent",
			Mode: "research",
		},
	}) + sessionEventLine(t, sse.TypeToolResult, sse.ToolResultPayload{
		TurnID:   "t0",
		CallID:   "subagent-research",
		ExitCode: 1,
		Result:   "max_turns reached",
		Delegation: &sse.DelegationMeta{
			Kind: "subagent",
			Mode: "research",
		},
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t1",
		Reason: sse.TurnEndCompleted,
		ToolStats: &sse.ToolRuntimeStats{
			ToolRequests:           2,
			ToolErrors:             1,
			ToolDurationMS:         15,
			LoopGuardInterventions: 1,
			ForcedNoTools:          1,
			SourceAccessNetwork:    1,
			MemorySearchCalls:      1,
			MemorySearchMisses:     1,
			ToolFailureByKind:      map[string]int{"dynamic_shell": 1},
		},
	}) + sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{
		TurnID: "t2",
		Reason: sse.TurnEndMaxTurns,
		ToolStats: &sse.ToolRuntimeStats{
			ToolRequests:            3,
			LoopGuardInterventions:  2,
			SessionSearchCalls:      1,
			SessionSearchResults:    4,
			ToolContextTruncated:    1,
			ToolContextOmittedBytes: 512,
			ToolFailureByKind:       map[string]int{"no_matches": 2},
		},
	})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "durable-tool-stats")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.Tools == nil {
		t.Fatalf("durable tool stats missing: found=%v summary=%+v", found, summary)
	}
	if summary.Tools.ToolRequests != 5 ||
		summary.Tools.ToolErrors != 1 ||
		summary.Tools.ToolDurationMS != 15 ||
		summary.Tools.LoopGuardInterventions != 3 ||
		summary.Tools.ForcedNoTools != 1 ||
		summary.Tools.SourceAccessNetwork != 1 ||
		summary.Tools.MemorySearchCalls != 1 ||
		summary.Tools.MemorySearchMisses != 1 ||
		summary.Tools.SessionSearchCalls != 1 ||
		summary.Tools.SessionSearchResults != 4 ||
		summary.Tools.ToolContextTruncated != 1 ||
		summary.Tools.ToolContextOmitted != 512 {
		t.Fatalf("durable tool stats = %+v, want aggregated turn.end stats", summary.Tools)
	}
	if summary.Tools.ToolFailureByKind["dynamic_shell"] != 1 ||
		summary.Tools.ToolFailureByKind["no_matches"] != 2 ||
		summary.Tools.ToolFailureByKind["loop_guard_no_budget"] != 1 {
		t.Fatalf("tool_failure_by_kind = %+v, want aggregated failure kinds", summary.Tools.ToolFailureByKind)
	}
	if summary.Tools.PlanCalls != 1 ||
		summary.Tools.PlanByAction["update"] != 1 ||
		summary.Tools.PlanErrors != 1 ||
		summary.Tools.FocusedTaskCalls != 1 ||
		summary.Tools.FocusedTaskByType["review"] != 1 ||
		summary.Tools.FocusedTaskErrors != 0 ||
		summary.Tools.SubagentCalls != 1 ||
		summary.Tools.SubagentByMode["research"] != 1 ||
		summary.Tools.SubagentErrors != 1 {
		t.Fatalf("tool governance stats = %+v, want plan/delegation request and error counts", summary.Tools)
	}
	if !strings.Contains(summary.LatestRecoveryHint, "top tool failure kind=no_matches (2)") {
		t.Fatalf("latest_recovery_hint = %q, want top tool failure kind", summary.LatestRecoveryHint)
	}
}

func TestSummarizeDurableSessionRestoresRuntimeStatsFromEvents(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	createDurableSessionDir(t, pool, "durable-runtime-stats")
	dir := pool.sessionDirPath("durable-runtime-stats")

	body := sessionEventLine(t, sse.TypeUsage, sse.UsagePayload{TurnID: "t1", InputTokens: 100, OutputTokens: 20}) +
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: "t1", Reason: sse.TurnEndMaxTurns}) +
		sessionEventLine(t, sse.TypeUsage, sse.UsagePayload{TurnID: "t2", InputTokens: 40, OutputTokens: 8}) +
		sessionEventLine(t, sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: "t2", Reason: sse.TurnEndError}) +
		sessionEventLine(t, sse.TypeError, sse.ErrorPayload{TurnID: "t2", Code: "llm_timeout", FailureKind: "llm_timeout", Recoverable: true}) +
		sessionEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{
			TurnID:          "t2",
			BeforeMessages:  120,
			AfterMessages:   80,
			RemovedMessages: 40,
			Reactive:        true,
			Reason:          "context_overflow",
			SummaryPresent:  false,
		})
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "durable-runtime-stats")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || summary.Runtime == nil {
		t.Fatalf("durable runtime stats missing: found=%v summary=%+v", found, summary)
	}
	if summary.Usage == nil || summary.Usage.InputTokens != 140 || summary.Usage.OutputTokens != 28 || summary.Usage.Turns != 2 {
		t.Fatalf("durable usage = %+v, want token totals and turn count from events", summary.Usage)
	}
	if summary.Runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 1 ||
		summary.Runtime.TurnEndByReason[sse.TurnEndError] != 1 ||
		summary.Runtime.RuntimeErrors != 1 ||
		summary.Runtime.RuntimeErrorByKind["llm_timeout"] != 1 ||
		summary.Runtime.ContextCompactions != 1 ||
		summary.Runtime.ContextCompactionsReactive != 1 ||
		summary.Runtime.ContextCompactionRemovedMessages != 40 ||
		summary.Runtime.ContextCompactionLatestReason != "context_overflow" ||
		summary.Runtime.ContextCompactionLatestState != "missing" {
		t.Fatalf("durable runtime stats = %+v, want aggregated runtime events", summary.Runtime)
	}
	if summary.ContextCompactions == nil || summary.ContextCompactions.Count != 1 || summary.ContextCompactions.LatestSummaryState != "missing" {
		t.Fatalf("context compaction summary = %+v, want existing durable summary preserved", summary.ContextCompactions)
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

func TestMergeSessionSummariesCarriesUsageAndPrefersActive(t *testing.T) {
	got := mergeSessionSummaries(
		sessionSummary{ID: "active"},
		sessionSummary{
			ID:      "active",
			Durable: true,
			Usage:   &UsageSnapshot{InputTokens: 100, OutputTokens: 20, Turns: 2},
		},
	)
	if got.Usage == nil || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 20 || got.Usage.Turns != 2 {
		t.Fatalf("merged durable usage = %+v, want carried over", got.Usage)
	}

	got = mergeSessionSummaries(
		sessionSummary{
			ID:      "active",
			Durable: true,
			Usage:   &UsageSnapshot{InputTokens: 100, OutputTokens: 20, Turns: 2},
		},
		sessionSummary{
			ID:     "active",
			Active: true,
			Usage:  &UsageSnapshot{InputTokens: 200, OutputTokens: 30, Turns: 3},
		},
	)
	if got.Usage == nil || got.Usage.InputTokens != 200 || got.Usage.OutputTokens != 30 || got.Usage.Turns != 3 {
		t.Fatalf("merged active usage = %+v, want active usage", got.Usage)
	}

	got = mergeSessionSummaries(
		sessionSummary{ID: "active", Active: true, Usage: &UsageSnapshot{}},
		sessionSummary{ID: "active", Durable: true, Usage: &UsageSnapshot{InputTokens: 100, OutputTokens: 20, Turns: 2}},
	)
	if got.Usage == nil || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 20 || got.Usage.Turns != 2 {
		t.Fatalf("merged empty-active usage = %+v, want durable usage", got.Usage)
	}

	got = mergeSessionSummaries(
		sessionSummary{ID: "active", Active: true, Usage: &UsageSnapshot{InputTokens: 7, OutputTokens: 3, Turns: 1}},
		sessionSummary{ID: "active", Durable: true, Usage: &UsageSnapshot{InputTokens: 100, OutputTokens: 20, Turns: 2}},
	)
	if got.Usage == nil || got.Usage.InputTokens != 100 || got.Usage.OutputTokens != 20 || got.Usage.Turns != 2 {
		t.Fatalf("merged stronger durable usage = %+v, want durable usage", got.Usage)
	}
}

func TestMergeSessionSummariesCarriesRuntimeStatsAndPrefersActive(t *testing.T) {
	got := mergeSessionSummaries(
		sessionSummary{ID: "active"},
		sessionSummary{
			ID:      "active",
			Durable: true,
			Runtime: &RuntimeStatsSnapshot{TurnEndByReason: map[string]int64{sse.TurnEndMaxTurns: 1}},
		},
	)
	if got.Runtime == nil || got.Runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 1 {
		t.Fatalf("merged durable runtime stats = %+v, want carried over", got.Runtime)
	}

	got = mergeSessionSummaries(
		sessionSummary{
			ID:      "active",
			Durable: true,
			Runtime: &RuntimeStatsSnapshot{TurnEndByReason: map[string]int64{sse.TurnEndMaxTurns: 1}},
		},
		sessionSummary{
			ID:      "active",
			Active:  true,
			Runtime: &RuntimeStatsSnapshot{TurnEndByReason: map[string]int64{sse.TurnEndCompleted: 2}},
		},
	)
	if got.Runtime == nil ||
		got.Runtime.TurnEndByReason[sse.TurnEndCompleted] != 2 ||
		got.Runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 0 {
		t.Fatalf("merged active runtime stats = %+v, want active stats", got.Runtime)
	}

	got = mergeSessionSummaries(
		sessionSummary{ID: "active", Active: true, Runtime: &RuntimeStatsSnapshot{}},
		sessionSummary{ID: "active", Durable: true, Runtime: &RuntimeStatsSnapshot{TurnEndByReason: map[string]int64{sse.TurnEndMaxTurns: 1}}},
	)
	if got.Runtime == nil || got.Runtime.TurnEndByReason[sse.TurnEndMaxTurns] != 1 {
		t.Fatalf("merged empty-active runtime stats = %+v, want durable stats", got.Runtime)
	}
}

func TestMergeSessionSummariesKeepsActiveToolStats(t *testing.T) {
	got := mergeSessionSummaries(
		sessionSummary{
			ID:     "active",
			Active: true,
			Tools:  &ToolStatsSnapshot{ToolRequests: 9, ToolErrors: 1},
		},
		sessionSummary{
			ID:      "active",
			Durable: true,
			Tools:   &ToolStatsSnapshot{ToolRequests: 2, LoopGuardInterventions: 1},
		},
	)
	if got.Tools == nil || got.Tools.ToolRequests != 9 || got.Tools.ToolErrors != 1 || got.Tools.LoopGuardInterventions != 0 {
		t.Fatalf("merged active-first tool stats = %+v, want active stats", got.Tools)
	}

	got = mergeSessionSummaries(
		sessionSummary{
			ID:      "active",
			Durable: true,
			Tools:   &ToolStatsSnapshot{ToolRequests: 2, LoopGuardInterventions: 1},
		},
		sessionSummary{
			ID:     "active",
			Active: true,
			Tools:  &ToolStatsSnapshot{ToolRequests: 9, ToolErrors: 1},
		},
	)
	if got.Tools == nil || got.Tools.ToolRequests != 9 || got.Tools.ToolErrors != 1 || got.Tools.LoopGuardInterventions != 0 {
		t.Fatalf("merged durable-first tool stats = %+v, want active stats", got.Tools)
	}

	got = mergeSessionSummaries(
		sessionSummary{ID: "active", Active: true, Tools: &ToolStatsSnapshot{}},
		sessionSummary{ID: "active", Durable: true, Tools: &ToolStatsSnapshot{ToolRequests: 2, LoopGuardInterventions: 1}},
	)
	if got.Tools == nil || got.Tools.ToolRequests != 2 || got.Tools.LoopGuardInterventions != 1 {
		t.Fatalf("merged empty-active tool stats = %+v, want durable stats", got.Tools)
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

func taskActionsContain(actions []sessionTaskStateAction, tool, summaryPart string) bool {
	for _, action := range actions {
		if action.Tool == tool && strings.Contains(action.Summary, summaryPart) {
			return true
		}
	}
	return false
}

func taskEvidenceContains(evidence []sessionTaskStateEvidence, source, summaryPart string) bool {
	for _, item := range evidence {
		if item.Source == source && strings.Contains(item.Summary, summaryPart) {
			return true
		}
	}
	return false
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

func TestSummarizeActiveSessionPrefersEventDisplayText(t *testing.T) {
	dir := t.TempDir()
	conv, err := agent.OpenConversationAt(filepath.Join(dir, "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	internalPrompt := sessionLoopSetupPrompt("market monitor")
	if err := conv.Append(agent.ChatMessage{Role: "user", Content: internalPrompt}); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID:      "t1",
		Text:        internalPrompt,
		DisplayText: "Set up loop: market monitor",
		Mode:        sessionMessageModeLoopSetup,
	})), 0o644); err != nil {
		t.Fatal(err)
	}

	summary := summarizeActiveSession(&Session{
		ID:         "active-loop-display",
		conv:       conv,
		registry:   agent.NewRegistry(),
		sessionDir: dir,
		workspace:  "/tmp/workspace",
		createdAt:  time.Now(),
		lastUsed:   time.Now(),
	}, Config{})
	if summary.LatestUserMessage != "Set up loop: market monitor" || summary.TopicUserMessage != "Set up loop: market monitor" {
		t.Fatalf("active latest/topic = %q/%q, want display text", summary.LatestUserMessage, summary.TopicUserMessage)
	}
	if strings.Contains(summary.LatestUserMessage, "Loop protocol activation is pending") {
		t.Fatalf("active summary leaked internal loop setup prompt: %q", summary.LatestUserMessage)
	}
}

func TestSummarizeActiveSessionReportsSchedulesWithoutLoopProtocol(t *testing.T) {
	dir := t.TempDir()
	conv, err := agent.OpenConversationAt(filepath.Join(dir, "conversation.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := conv.Append(agent.ChatMessage{Role: "user", Content: "check BTC every 30 minutes"}); err != nil {
		t.Fatal(err)
	}
	nextRunAt := time.Date(2026, 5, 29, 12, 0, 0, 0, time.UTC).Format(time.RFC3339)
	if err := writeSessionSchedulesFile(filepath.Join(dir, sessionSchedulesFileName), sessionSchedulesFile{
		Version: 1,
		Schedules: []sessionSchedule{
			{
				ID:                    "sched_active_timer",
				Kind:                  sessionScheduleKindCustom,
				Prompt:                "Check the BTC price.",
				DisplayText:           "BTC price check",
				Enabled:               true,
				NextRunAt:             nextRunAt,
				RepeatIntervalSeconds: 1800,
				CreatedAt:             "2026-05-29T11:29:04Z",
				UpdatedAt:             "2026-05-29T11:29:04Z",
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	summary := summarizeActiveSession(&Session{
		ID:         "active-scheduled",
		conv:       conv,
		registry:   agent.NewRegistry(),
		sessionDir: dir,
		workspace:  "/tmp/workspace",
		createdAt:  time.Now(),
		lastUsed:   time.Now(),
	}, Config{})
	if !summary.HasSchedules || summary.Schedules == nil {
		t.Fatalf("active session summary = %+v, want schedule summary without LOOP.md", summary)
	}
	if summary.Schedules.NextRunAt != nextRunAt || summary.Schedules.NextScheduleID != "sched_active_timer" || summary.Schedules.NextPromptPreview != "BTC price check" {
		t.Fatalf("schedule summary = %+v, want active timer from sessionDir", summary.Schedules)
	}
	if summary.HasLoopProtocol || summary.HasLoopState {
		t.Fatalf("summary = %+v, timer visibility must not imply loop state", summary)
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
	if summary.Count != 3 || summary.Enabled != 2 || summary.EnabledLoopTicks != 1 || summary.PendingLoopTicks != 0 || summary.NextScheduleID != "sched_next" || summary.NextScheduleKind != sessionScheduleKindLoopTick || summary.NextRunAt != now.Add(time.Hour).Format(time.RFC3339) || summary.NextPromptPreview != "Loop every 30m: scheduled-list" {
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
	if created.Summary == nil || created.Summary.Count != 1 || created.Summary.Enabled != 1 || created.Summary.EnabledLoopTicks != 1 || created.Summary.PendingLoopTicks != 0 || created.Summary.NextScheduleID != schedule.ID {
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
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("resume status = %d, want 200; body=%s", got, w.Body.String())
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
	protocol = blankDefaultLoopActivationFieldsForAPI(protocol)
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
	if !strings.Contains(w.Body.String(), "requires a recorded calibration question and user answer") {
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

func TestActivateSessionLoopProtocolRepairsRecordedCalibrationFromProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	sessionID := "loop-recorded-calibration"
	protocolPath := sessionLoopProtocolPath(pool, sessionID)
	if _, _, _, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       sessionID,
		OwnerSession: sessionID,
		Goal:         "Maintain a recurring global situation report.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "# Loop Protocol: "+sessionID, `# Loop Protocol: `+sessionID+`

## Calibration Q&A (recorded)

- **Q1**: Analysis scope? A: Cover major geopolitical, economic, and technology policy changes.
- **Q2**: Output cadence? A: Update the daily report each day and write one deeper weekly synthesis.`, 1)
	for _, replacement := range [][2]string{
		{"- status: draft", "- status: running"},
		{"- hard constraints:", "- hard constraints: cite current sources and stop when evidence is unavailable"},
		{"- known evidence:", "- known evidence: user requested recurring global situation reporting"},
		{"- current risk or blocker:", "- current risk or blocker: source freshness may vary by region"},
		{"- important artifacts:", "- important artifacts: reports/daily and reports/weekly"},
		{"- important trace spans:", "- important trace spans: loop setup calibration"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md, plan state, and recent trace before continuing"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}

	body, err := json.Marshal(sessionLoopProtocolUpdateRequest{
		Protocol: protocol,
		Activate: true,
		Reason:   "activate from recorded calibration section",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/loop-protocol", bytes.NewReader(body))
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
		resp.State.Status != "running" ||
		resp.State.CalibrationQuestions != 2 ||
		resp.State.CalibrationAnswers != 2 ||
		!strings.Contains(resp.State.LastCalibrationAnswer, "weekly synthesis") {
		t.Fatalf("state = %+v", resp.State)
	}
	if len(resp.Events) != 6 ||
		resp.Events[1].Type != "loop.protocol_calibration_request" ||
		resp.Events[2].Type != "loop.protocol_calibration" ||
		resp.Events[3].Type != "loop.protocol_calibration_request" ||
		resp.Events[4].Type != "loop.protocol_calibration" ||
		resp.Events[5].Type != "loop.protocol_activate" {
		t.Fatalf("events = %+v", resp.Events)
	}
}

func TestActivateSessionLoopProtocolAcceptsCompletedDraft(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	sessionID := "loop-completed-draft"
	protocolPath := sessionLoopProtocolPath(pool, sessionID)
	if _, _, _, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       sessionID,
		OwnerSession: sessionID,
		Goal:         "Maintain a recurring global situation report.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	for _, replacement := range [][2]string{
		{"- hard constraints:", "- hard constraints: cite current sources and stop when evidence is unavailable"},
		{"- known evidence:", "- known evidence: user requested recurring global situation reporting"},
		{"- current risk or blocker:", "- current risk or blocker: source freshness may vary by region"},
		{"- important artifacts:", "- important artifacts: reports/daily and reports/weekly"},
		{"- important trace spans:", "- important trace spans: loop setup calibration"},
		{"- last known recovery note:", "- last known recovery note: reload LOOP.md, plan state, and recent trace before continuing"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(protocolPath, "What cadence should this loop use?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(protocolPath, "Update daily and write one deeper weekly synthesis."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}

	body, err := json.Marshal(sessionLoopProtocolUpdateRequest{
		Protocol: protocol,
		Activate: true,
		Reason:   "activate completed draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/loop-protocol", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State == nil || resp.State.Status != "running" || loopstate.ProtocolStatus(resp.Protocol) != "running" {
		t.Fatalf("response state/status = %+v protocol status=%q", resp.State, loopstate.ProtocolStatus(resp.Protocol))
	}
}

func TestActivateSessionLoopProtocolActivatesSavedCompletedDraftWithoutProtocol(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	sessionID := "loop-saved-draft"
	protocolPath := sessionLoopProtocolPath(pool, sessionID)
	if _, _, _, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       sessionID,
		OwnerSession: sessionID,
		Goal:         "Keep a long-running implementation loop recoverable.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationQuestion(protocolPath, "When should this loop pause?"); err != nil {
		t.Fatalf("RecordProtocolCalibrationQuestion: %v", err)
	}
	if _, _, err := loopstate.RecordProtocolCalibrationAnswer(protocolPath, "Pause after tests pass and code is committed."); err != nil {
		t.Fatalf("RecordProtocolCalibrationAnswer: %v", err)
	}

	body, err := json.Marshal(sessionLoopProtocolUpdateRequest{
		Activate: true,
		Reason:   "activate saved completed draft",
	})
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/v1/sessions/"+sessionID+"/loop-protocol", bytes.NewReader(body))
	w := httptest.NewRecorder()
	handleSessionRoutes(pool).ServeHTTP(w, r)
	if got := w.Result().StatusCode; got != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", got, w.Body.String())
	}
	if activeSessionByID(pool, sessionID) != nil {
		t.Fatal("POST loop-protocol activation must not reopen an inactive durable session")
	}
	var resp sessionLoopProtocolResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.State == nil ||
		resp.State.Status != "running" ||
		resp.State.LastEventType != "loop.protocol_activate" ||
		loopstate.ProtocolStatus(resp.Protocol) != "running" {
		t.Fatalf("response state/status = %+v protocol status=%q", resp.State, loopstate.ProtocolStatus(resp.Protocol))
	}
}

func TestReadSessionLoopProtocolDoesNotRepairRecordedCalibration(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	sessionID := "loop-read-recorded-calibration"
	protocolPath := sessionLoopProtocolPath(pool, sessionID)
	if _, _, _, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       sessionID,
		OwnerSession: sessionID,
		Goal:         "Maintain a recurring global situation report.",
		Status:       "draft",
	}); err != nil {
		t.Fatalf("EnsureProtocolTemplate: %v", err)
	}
	protocol, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		t.Fatalf("ReadProtocol found=%v err=%v", found, err)
	}
	protocol = strings.Replace(protocol, "# Loop Protocol: "+sessionID, `# Loop Protocol: `+sessionID+`

## Calibration Q&A (recorded)

- **Q1**: Analysis scope? A: Cover major geopolitical, economic, and technology policy changes.`, 1)
	if err := loopstate.WriteProtocol(protocolPath, protocol); err != nil {
		t.Fatalf("WriteProtocol: %v", err)
	}

	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sessionID+"/loop-protocol", nil)
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
		resp.State.CalibrationQuestions != 0 ||
		resp.State.CalibrationAnswers != 0 ||
		resp.State.LastEventType != "loop.protocol_init" {
		t.Fatalf("read should not repair calibration state: %+v", resp.State)
	}
	if len(resp.Events) != 1 || resp.Events[0].Type != "loop.protocol_init" {
		t.Fatalf("read should not append calibration events: %+v", resp.Events)
	}
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, sessionID))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.CalibrationQuestions != 0 || state.CalibrationAnswers != 0 || state.LastEventType != "loop.protocol_init" {
		t.Fatalf("persisted state changed on read: %+v", state)
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

func TestSummarizeDurableSessionIgnoresEmptySessionDir(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	if err := os.MkdirAll(pool.sessionDirPath("empty-create-failure"), 0o755); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "empty-create-failure")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if found || summary.ID != "" {
		t.Fatalf("empty session dir should not be a durable chat: found=%v summary=%+v", found, summary)
	}
	summaries, _, err := listSessionSummaries(pool, sessionListOptions{Limit: 100})
	if err != nil {
		t.Fatalf("listSessionSummaries: %v", err)
	}
	if ids := sessionIDs(summaries); len(ids) != 0 {
		t.Fatalf("empty session dir leaked into list: %v", ids)
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
	if summary.Memory == nil || !summary.Memory.SharedUserMemory || summary.Memory.BucketCount != 1 || summary.Memory.EntryCount != 1 {
		t.Fatalf("shared user memory summary = %+v, want one shared user bucket", summary.Memory)
	}
	if summary.LastUsedAt != formatTime(oldTime) {
		t.Fatalf("LastUsedAt = %q, want session-local time %q", summary.LastUsedAt, formatTime(oldTime))
	}
}

func TestSummarizeDurableSessionMemorySummaryDoesNotMigrateLegacyMemory(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "legacy-memory-summary")
	dir := pool.sessionDirPath("legacy-memory-summary")
	if err := os.RemoveAll(filepath.Join(dir, "topics")); err != nil {
		t.Fatalf("remove topics: %v", err)
	}
	legacyPath := filepath.Join(dir, "MEMORY.md")
	if err := os.WriteFile(legacyPath, []byte("legacy fact for recovery\n"), 0o644); err != nil {
		t.Fatalf("write legacy memory: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "legacy-memory-summary")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found || !summary.HasMemory || summary.Memory == nil {
		t.Fatalf("legacy memory summary missing: found=%v summary=%+v", found, summary)
	}
	if summary.Memory.BucketCount != 1 ||
		summary.Memory.EntryCount != 1 ||
		summary.Memory.LatestTarget != "" ||
		summary.Memory.LatestTopic != "" {
		t.Fatalf("legacy unstamped memory summary = %+v, want one undated bucket without latest topic", summary.Memory)
	}
	if _, err := os.Stat(legacyPath); err != nil {
		t.Fatalf("legacy MEMORY.md should remain after read-only summary: %v", err)
	}
	if durableStatePathExists(filepath.Join(dir, "topics", "general.md")) {
		t.Fatal("read-only session summary must not migrate legacy MEMORY.md into topics/general.md")
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
	if !caps.Memory || !caps.SessionSearch || !caps.SessionSchedule || !caps.Subagent || !caps.FocusedTasks {
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

func blankDefaultLoopActivationFieldsForAPI(protocol string) string {
	for _, replacement := range [][2]string{
		{"- hard constraints: follow system, user, tool, workspace, and safety policy; preserve evidence requirements.", "- hard constraints:"},
		{"- known evidence: none recorded yet.", "- known evidence:"},
		{"- current risk or blocker: none recorded yet.", "- current risk or blocker:"},
		{"- important artifacts: none recorded yet.", "- important artifacts:"},
		{"- important trace spans: loop initialization.", "- important trace spans:"},
		{"- last known recovery note: reload LOOP.md, plan state, memory search/list, and recent trace before continuing.", "- last known recovery note:"},
	} {
		protocol = strings.Replace(protocol, replacement[0], replacement[1], 1)
	}
	return protocol
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
