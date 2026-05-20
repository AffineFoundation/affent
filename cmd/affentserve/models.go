package main

import (
	"encoding/json"
	"net/http"
	"time"
)

// /v1/models returns the static "served-by-this-process" model id.
// The server doesn't proxy arbitrary upstream models; one process is
// configured for one model so the LLMClient settings stay coherent.
// Embedders running multiple models stand up multiple affentserve
// instances behind a router.
//
// `created` is captured once at handler construction (process start)
// rather than recomputed per request: OpenAI's `created` field is a
// model-registration timestamp, not a current-time stamp, and a
// changing value makes the response uncacheable for downstream
// proxies without buying anything.
func handleModels(cfg Config) http.HandlerFunc {
	created := time.Now().Unix()
	return func(w http.ResponseWriter, _ *http.Request) {
		model := cfg.Model
		if model == "" {
			model = "default"
		}
		body := map[string]any{
			"object": "list",
			"data": []map[string]any{
				{
					"id":       model,
					"object":   "model",
					"owned_by": "affent",
					"created":  created,
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}
