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
func handleModels(cfg Config) http.HandlerFunc {
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
					"created":  time.Now().Unix(),
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	}
}
