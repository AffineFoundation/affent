package main

import (
	"encoding/json"
	"net/http"

	agent "github.com/affinefoundation/affent/internal/agent"
)

type sessionCancelResponse struct {
	SessionID string `json:"session_id"`
	Accepted  bool   `json:"accepted"`
}

func handleSessionCancel(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	sess := activeSessionByID(pool, sessionID)
	if sess == nil {
		writeJSONErrorTyped(w, http.StatusConflict, "session is not active; create or reopen it before cancelling", nil, "session_inactive")
		return
	}
	sess.CancelTurn()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(sessionCancelResponse{
		SessionID: sessionID,
		Accepted:  true,
	})
}
