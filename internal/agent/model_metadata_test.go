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
