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
	"strings"
	"testing"
	"unicode/utf8"

	agent "github.com/affinefoundation/affent/internal/agent"
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
	assertSessionCapabilities(t, created.Session.Capabilities, sessionCapabilities{
		Builtins:         true,
		WorkspaceTools:   []string{"shell", "read_file", "file_context", "write_file", "edit_file", "list_files", agent.SymbolContextToolName, "repo_search"},
		SkillInstall:     true,
		Plan:             true,
		Memory:           true,
		SessionSearch:    false,
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
	compactions := &sessionContextCompactionSummary{Count: 1, Reactive: 1, RemovedMessages: 32, SummaryBytes: 2048, LatestReason: "context_overflow", LatestReactive: true}
	got = mergeSessionSummaries(sessionSummary{ID: "active", Active: true}, sessionSummary{ID: "active", Durable: true, ContextCompactions: compactions})
	if got.ContextCompactions != compactions {
		t.Fatalf("context compactions = %+v, want durable compactions carried over", got.ContextCompactions)
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
	if err := os.WriteFile(filepath.Join(dir, "events.jsonl"), []byte(
		sessionEventLine(t, sse.TypeUserMessage, sse.UserMessagePayload{TurnID: "t1", Text: "affine 是 Bittensor 的一个子网，请收集信息并向我介绍"})+
			sessionEventLine(t, sse.TypeContextCompact, sse.ContextCompactPayload{TurnID: "t1", BeforeMessages: 48, AfterMessages: 12, RemovedMessages: 36, Reactive: true, Reason: "context_overflow", SummaryPresent: true, SummaryBytes: 1024})+
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
	if summary.ContextCompactions == nil {
		t.Fatal("context_compactions should be summarized from durable events")
	}
	if summary.ContextCompactions.Count != 1 ||
		summary.ContextCompactions.Reactive != 1 ||
		summary.ContextCompactions.RemovedMessages != 36 ||
		summary.ContextCompactions.SummaryBytes != 1024 ||
		summary.ContextCompactions.LatestReason != "context_overflow" ||
		!summary.ContextCompactions.LatestReactive {
		t.Fatalf("context_compactions = %+v, want durable compaction summary", summary.ContextCompactions)
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
	if caps.Builtins || caps.SkillInstall || caps.Plan || caps.SessionSearch || caps.RepoSearch {
		t.Fatalf("tool-light session should not report builtin-only tools: %+v", caps)
	}
	if !caps.Memory || !caps.Subagent || !caps.FocusedTasks {
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
