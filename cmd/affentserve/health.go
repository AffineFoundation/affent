package main

import (
	"encoding/json"
	"net/http"
)

// handleHealth returns the readiness signal a load balancer polls.
// 200 + "ok" while accepting traffic; 503 + "shutting_down" the
// moment a graceful Shutdown starts so the LB can drain. Stays
// unauthenticated (per server.go: LBs can't carry the bearer
// token) — exposes only liveness state, no session data.
func handleHealth(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if pool.IsShuttingDown() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]any{"status": "shutting_down"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"status": "ok"})
	}
}
