package main

import (
	"encoding/json"
	"net/http"
)

type healthResponse struct {
	Status string `json:"status"`
	buildInfo
}

// handleHealth returns the readiness signal a load balancer polls.
// 200 + "ok" while accepting traffic; 503 + "shutting_down" the
// moment a graceful Shutdown starts so the LB can drain. Stays
// unauthenticated (per server.go: LBs can't carry the bearer
// token) — exposes only liveness/build state, no session data.
func handleHealth(pool *SessionPool) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		resp := healthResponse{buildInfo: currentBuildInfo()}
		if pool.IsShuttingDown() {
			w.WriteHeader(http.StatusServiceUnavailable)
			resp.Status = "shutting_down"
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
		resp.Status = "ok"
		_ = json.NewEncoder(w).Encode(resp)
	}
}
