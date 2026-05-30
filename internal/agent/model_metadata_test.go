package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseModelMetadataContextWindow(t *testing.T) {
	raw := []byte(`{
		"object": "list",
		"data": [
			{"id": "small", "context_window": 8192},
			{"id": "target", "context_window": 200000, "max_context_window": 128000}
		]
	}`)
	got, err := ParseModelMetadata(raw, "target")
	if err != nil {
		t.Fatalf("ParseModelMetadata: %v", err)
	}
	if got.ID != "target" || got.ContextWindowTokens != 128000 {
		t.Fatalf("metadata = %+v, want target/128000", got)
	}
}

func TestParseModelMetadataNestedAndStringFields(t *testing.T) {
	raw := []byte(`{
		"data": [
			{"id": "target", "metadata": {"max_model_len": "32768", "auto_compact_token_limit": "24000"}}
		]
	}`)
	got, err := ParseModelMetadata(raw, "target")
	if err != nil {
		t.Fatalf("ParseModelMetadata: %v", err)
	}
	if got.ContextWindowTokens != 32768 {
		t.Fatalf("ContextWindowTokens = %d, want 32768", got.ContextWindowTokens)
	}
	if got.AutoCompactTokenLimit != 24000 {
		t.Fatalf("AutoCompactTokenLimit = %d, want 24000", got.AutoCompactTokenLimit)
	}
}

func TestParseModelMetadataOpenRouterTopProviderContextLength(t *testing.T) {
	raw := []byte(`{
		"data": [
			{
				"id": "qwen/qwen3.6-35b-a3b",
				"context_length": 200000,
				"top_provider": {"context_length": "131072"}
			}
		]
	}`)
	got, err := ParseModelMetadata(raw, "Qwen3.6-35B-A3B")
	if err != nil {
		t.Fatalf("ParseModelMetadata: %v", err)
	}
	if got.ID != "qwen/qwen3.6-35b-a3b" || got.ContextWindowTokens != 131072 {
		t.Fatalf("metadata = %+v, want normalized provider id and top_provider context limit", got)
	}
}

func TestParseModelMetadataMatchesNormalizedProviderID(t *testing.T) {
	raw := []byte(`{
		"data": [
			{"id": "Qwen/Qwen3.6-35B-A3B", "context_window": 262144}
		]
	}`)
	got, err := ParseModelMetadata(raw, "qwen3.6-35b-a3b")
	if err != nil {
		t.Fatalf("ParseModelMetadata: %v", err)
	}
	if got.ID != "Qwen/Qwen3.6-35B-A3B" || got.ContextWindowTokens != 262144 {
		t.Fatalf("metadata = %+v, want normalized provider id match", got)
	}
}

func TestParseModelMetadataAppliesEffectiveContextWindowPercent(t *testing.T) {
	raw := []byte(`{
		"data": [
			{"id": "target", "context_window": 100000, "effective_context_window_percent": 95, "auto_compact_token_limit": 90000}
		]
	}`)
	got, err := ParseModelMetadata(raw, "target")
	if err != nil {
		t.Fatalf("ParseModelMetadata: %v", err)
	}
	if got.ContextWindowTokens != 95000 {
		t.Fatalf("ContextWindowTokens = %d, want effective 95000", got.ContextWindowTokens)
	}
	if got.EffectiveContextWindowPercent != 95 {
		t.Fatalf("EffectiveContextWindowPercent = %d, want 95", got.EffectiveContextWindowPercent)
	}
	if got.AutoCompactTokenLimit != 90000 {
		t.Fatalf("AutoCompactTokenLimit = %d, want 90000", got.AutoCompactTokenLimit)
	}
}

func TestParseModelMetadataClampsEffectiveContextWindowPercent(t *testing.T) {
	raw := []byte(`{
		"data": [
			{"id": "target", "context_window": 100000, "metadata": {"effective_context_window_percent": "120"}}
		]
	}`)
	got, err := ParseModelMetadata(raw, "target")
	if err != nil {
		t.Fatalf("ParseModelMetadata: %v", err)
	}
	if got.ContextWindowTokens != 100000 {
		t.Fatalf("ContextWindowTokens = %d, want clamped 100000", got.ContextWindowTokens)
	}
	if got.EffectiveContextWindowPercent != 100 {
		t.Fatalf("EffectiveContextWindowPercent = %d, want clamped 100", got.EffectiveContextWindowPercent)
	}
}

func TestFetchModelMetadataUsesModelsEndpoint(t *testing.T) {
	var sawAuth bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Fatalf("path = %q, want /v1/models", r.URL.Path)
		}
		sawAuth = r.Header.Get("Authorization") == "Bearer secret"
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"target","context_length":65536}]}`))
	}))
	defer srv.Close()

	client := NewLLMClient(srv.URL+"/v1", "secret", "target")
	got, err := client.FetchModelMetadata(context.Background())
	if err != nil {
		t.Fatalf("FetchModelMetadata: %v", err)
	}
	if !sawAuth {
		t.Fatal("FetchModelMetadata did not forward Authorization")
	}
	if got.ID != "target" || got.ContextWindowTokens != 65536 {
		t.Fatalf("metadata = %+v, want target/65536", got)
	}
}

func TestKnownModelMetadataNormalizesProviderIDs(t *testing.T) {
	got, ok := KnownModelMetadata("Qwen/Qwen3.6-35B-A3B")
	if !ok {
		t.Fatal("KnownModelMetadata did not match qwen provider id")
	}
	if got.ID != "qwen3.6-35b-a3b" || got.ContextWindowTokens != 262144 {
		t.Fatalf("metadata = %+v, want qwen3.6-35b-a3b/262144", got)
	}
}

func TestResolveModelMetadataFallsBackToRegistry(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[{"id":"qwen3.6-35b-a3b"}]}`))
	}))
	defer srv.Close()

	client := NewLLMClient(srv.URL+"/v1", "", "qwen3.6-35b-a3b")
	got, source, err := client.ResolveModelMetadata(context.Background())
	if err != nil {
		t.Fatalf("ResolveModelMetadata: %v", err)
	}
	if source != "registry" {
		t.Fatalf("source = %q, want registry", source)
	}
	if got.ContextWindowTokens != 262144 {
		t.Fatalf("ContextWindowTokens = %d, want 262144", got.ContextWindowTokens)
	}
}
