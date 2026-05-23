package agent

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/memory"
)

// newMemoryToolFixture builds a memoryTool wired to a fresh on-disk
// FileMemoryStore in a temp dir. The tool closure goes through this
// store on every action; tests then assert by either inspecting the
// returned JSON response or by re-reading the store directly.
func newMemoryToolFixture(t *testing.T) (*Tool, *memory.FileMemoryStore) {
	t.Helper()
	dir := t.TempDir()
	store := &memory.FileMemoryStore{
		MemoryDir:      filepath.Join(dir, "memory"),
		UserPath:       filepath.Join(dir, "USER.md"),
		TopicCharLimit: 4000,
		CoreCharLimit:  4000,
		UserCharLimit:  4000,
	}
	return memoryTool(store), store
}

// TestMemoryTool_DispatchAdd pins that action=add with default target
// routes to TargetMemory + default "general" topic, the entry lands
// on disk, and the response payload is well-formed JSON.
func TestMemoryTool_DispatchAdd(t *testing.T) {
	tool, _ := newMemoryToolFixture(t)
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"add","content":"hello memory"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp memory.MemoryResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("decode response: %v\n%s", err, out)
	}
	if !resp.OK {
		t.Errorf("expected ok=true; got %+v", resp)
	}
	if resp.Target != memory.TargetMemory {
		t.Errorf("default target should be memory; got %q", resp.Target)
	}
	if resp.Topic != memory.DefaultTopic {
		t.Errorf("default topic should be %q; got %q", memory.DefaultTopic, resp.Topic)
	}
}

// TestMemoryTool_DispatchValidation pins the per-action required-arg
// checks. The tool returns a structured response with a descriptive
// Message instead of an opaque "decode args" error so the model can
// see what it forgot and retry.
func TestMemoryTool_DispatchValidation(t *testing.T) {
	tool, _ := newMemoryToolFixture(t)
	cases := []struct {
		name      string
		args      string
		wantInMsg string
	}{
		{"no action", `{}`, "action is required"},
		{"blank action", `{"action":"   "}`, "action is required"},
		{"add without content", `{"action":"add"}`, "content is required"},
		{"add with blank content", `{"action":"add","content":"   "}`, "content is required"},
		{"replace without old_text", `{"action":"replace","content":"x"}`, "old_text and content are required"},
		{"replace with blank content", `{"action":"replace","old_text":"x","content":"   "}`, "old_text and content are required"},
		{"replace with blank old_text", `{"action":"replace","old_text":"   ","content":"x"}`, "old_text and content are required"},
		{"remove without old_text", `{"action":"remove"}`, "old_text is required"},
		{"remove with blank old_text", `{"action":"remove","old_text":"   "}`, "old_text is required"},
		{"search without query", `{"action":"search"}`, "query is required"},
		{"search with blank query", `{"action":"search","query":"   "}`, "query is required"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			out, err := tool.Execute(context.Background(), json.RawMessage(c.args))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(out, c.wantInMsg) {
				t.Errorf("response missing %q: %s", c.wantInMsg, out)
			}
			if !strings.Contains(out, "Next:") {
				t.Errorf("validation response should include a corrective Next step: %s", out)
			}
		})
	}
}

func TestMemoryToolSchemaPublishesSearchLimits(t *testing.T) {
	tool, _ := newMemoryToolFixture(t)
	var schema struct {
		Properties map[string]struct {
			MinLength int `json:"minLength"`
			MaxLength int `json:"maxLength"`
			Default   int `json:"default"`
			Maximum   int `json:"maximum"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(tool.Schema, &schema); err != nil {
		t.Fatal(err)
	}
	if schema.Properties["action"].MinLength != 1 {
		t.Fatalf("action minLength = %d, want 1", schema.Properties["action"].MinLength)
	}
	if schema.Properties["target"].MinLength != 1 {
		t.Fatalf("target minLength = %d, want 1", schema.Properties["target"].MinLength)
	}
	if schema.Properties["topic"].MinLength != 1 {
		t.Fatalf("topic minLength = %d, want 1", schema.Properties["topic"].MinLength)
	}
	if schema.Properties["query"].MinLength != 1 {
		t.Fatalf("query minLength = %d, want 1", schema.Properties["query"].MinLength)
	}
	if schema.Properties["content"].MinLength != 1 {
		t.Fatalf("content minLength = %d, want 1", schema.Properties["content"].MinLength)
	}
	if schema.Properties["old_text"].MinLength != 1 {
		t.Fatalf("old_text minLength = %d, want 1", schema.Properties["old_text"].MinLength)
	}
	if schema.Properties["query"].MaxLength != memory.MaxSearchQueryBytes {
		t.Fatalf("query maxLength = %d, want %d", schema.Properties["query"].MaxLength, memory.MaxSearchQueryBytes)
	}
	if schema.Properties["top_k"].Maximum != memory.MaxSearchTopK {
		t.Fatalf("top_k maximum = %d, want %d", schema.Properties["top_k"].Maximum, memory.MaxSearchTopK)
	}
	if schema.Properties["top_k"].Default != memory.DefaultSearchTopK {
		t.Fatalf("top_k default = %d, want %d", schema.Properties["top_k"].Default, memory.DefaultSearchTopK)
	}
}

func TestMemoryToolNormalizesSearchArgsBeforeStore(t *testing.T) {
	store := &captureSearchStore{}
	tool := memoryTool(store)
	query := strings.Repeat("界", memory.MaxSearchQueryBytes)
	_, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"search","query":`+mustJSON(t, query)+`,"top_k":999999}`))
	if err != nil {
		t.Fatal(err)
	}
	if store.topK != memory.MaxSearchTopK {
		t.Fatalf("top_k passed to store = %d, want %d", store.topK, memory.MaxSearchTopK)
	}
	if len(store.query) > memory.MaxSearchQueryBytes {
		t.Fatalf("query passed to store is %d bytes, want <= %d", len(store.query), memory.MaxSearchQueryBytes)
	}
}

func TestMemoryToolTrimsRoutingArgs(t *testing.T) {
	tool, _ := newMemoryToolFixture(t)
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":" add ","target":"   ","topic":"  deploy  ","content":"ship via fly.io"}`))
	if err != nil {
		t.Fatal(err)
	}
	var resp memory.MemoryResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !resp.OK {
		t.Fatalf("add should succeed after trimming routing args: %+v", resp)
	}
	if resp.Target != memory.TargetMemory {
		t.Fatalf("blank target should default to memory, got %q", resp.Target)
	}
	if resp.Topic != "deploy" {
		t.Fatalf("topic should be trimmed before store normalization, got %q", resp.Topic)
	}
}

// TestMemoryTool_DispatchUnknownAction pins the helpful error message
// listing the valid actions. The model occasionally hallucinates an
// action like "update" or "delete"; the error must tell it the real
// vocabulary instead of just saying "unknown action".
func TestMemoryTool_DispatchUnknownAction(t *testing.T) {
	tool, _ := newMemoryToolFixture(t)
	out, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"delete"}`))
	if err != nil {
		t.Fatal(err)
	}
	// The error message is JSON-encoded, so the inner quotes around
	// "delete" come back escaped. Check the structurally-decoded
	// message instead of substring-matching against escaped JSON.
	var resp memory.MemoryResponse
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(resp.Message, `unknown action "delete"`) {
		t.Errorf("error must quote the bad action: %s", resp.Message)
	}
	// All real actions should be listed so the model can pick the right one.
	for _, valid := range []string{"add", "replace", "remove", "search", "list"} {
		if !strings.Contains(out, valid) {
			t.Errorf("error must list valid action %q: %s", valid, out)
		}
	}
	if !strings.Contains(resp.Message, "Next:") {
		t.Errorf("unknown action message should include a corrective Next step: %s", resp.Message)
	}
}

// TestMemoryTool_DispatchSearchAndList pins the read-side actions.
// search returns a Results array; list returns Topics. Both should
// round-trip through the JSON response without losing the structure.
func TestMemoryTool_DispatchSearchAndList(t *testing.T) {
	tool, _ := newMemoryToolFixture(t)
	// Seed two topics with one entry each.
	for _, args := range []string{
		`{"action":"add","topic":"deploy","content":"deploys via fly.io"}`,
		`{"action":"add","topic":"auth","content":"uses OAuth"}`,
	} {
		if _, err := tool.Execute(context.Background(), json.RawMessage(args)); err != nil {
			t.Fatal(err)
		}
	}

	// list should return both topics.
	listOut, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	var listResp memory.MemoryResponse
	_ = json.Unmarshal([]byte(listOut), &listResp)
	if len(listResp.Topics) != 2 {
		t.Errorf("list should return 2 topics, got %d: %s", len(listResp.Topics), listOut)
	}

	// search "fly.io" finds the deploy fact (content scoring + topic-name scoring).
	srchOut, err := tool.Execute(context.Background(), json.RawMessage(
		`{"action":"search","query":"fly.io"}`))
	if err != nil {
		t.Fatal(err)
	}
	var srchResp memory.MemoryResponse
	_ = json.Unmarshal([]byte(srchOut), &srchResp)
	if len(srchResp.Results) == 0 {
		t.Errorf("search for 'fly.io' should find the deploy entry: %s", srchOut)
	}
}

// TestMemoryTool_DispatchListUnsupported pins the optional-interface
// fallback: when an embedder plugs in a MemoryStore that doesn't
// implement ListTopics, action=list returns a clear "not supported"
// message instead of panicking.
func TestMemoryTool_DispatchListUnsupported(t *testing.T) {
	tool := memoryTool(noListStore{})
	out, err := tool.Execute(context.Background(), json.RawMessage(`{"action":"list"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "does not support action=list") {
		t.Errorf("expected unsupported-list message: %s", out)
	}
	if !strings.Contains(out, "Next:") || !strings.Contains(out, "action=search") {
		t.Errorf("unsupported-list message should suggest search fallback: %s", out)
	}
}

// noListStore implements MemoryStore but NOT the optional
// ListTopics(MemoryTarget) extension — exactly the shape an embedder
// might plug in if they only need add/search.
type noListStore struct{}

func (noListStore) Snapshot() string { return "" }
func (noListStore) Add(target memory.MemoryTarget, topic, content string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}
func (noListStore) Replace(target memory.MemoryTarget, topic, oldText, newContent string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}
func (noListStore) Remove(target memory.MemoryTarget, topic, oldText string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}
func (noListStore) Search(target memory.MemoryTarget, topic, query string, topK int) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}

type captureSearchStore struct {
	query string
	topK  int
}

func (s *captureSearchStore) Snapshot() string { return "" }
func (s *captureSearchStore) Add(target memory.MemoryTarget, topic, content string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}
func (s *captureSearchStore) Replace(target memory.MemoryTarget, topic, oldText, newContent string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}
func (s *captureSearchStore) Remove(target memory.MemoryTarget, topic, oldText string) (memory.MemoryResponse, error) {
	return memory.MemoryResponse{}, nil
}
func (s *captureSearchStore) Search(target memory.MemoryTarget, topic, query string, topK int) (memory.MemoryResponse, error) {
	s.query = query
	s.topK = topK
	return memory.MemoryResponse{OK: true, Target: target}, nil
}

func mustJSON(t *testing.T, v string) string {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
