package main

import (
	"encoding/json"
	"net/http"

	agent "github.com/affinefoundation/affent/internal/agent"
)

type sessionToolsResponse struct {
	SessionID string     `json:"session_id"`
	Count     int        `json:"count"`
	Tools     []toolInfo `json:"tools"`
}

type toolInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"`
}

func handleSessionTools(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
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
		writeJSONErrorTyped(w, http.StatusConflict, "session is not active; create or reopen it before listing tools", nil, "session_inactive")
		return
	}
	defs := sess.registry.Defs()
	tools := make([]toolInfo, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, toolInfo{
			Name:        def.Function.Name,
			Description: def.Function.Description,
			Parameters:  def.Function.Parameters,
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionToolsResponse{
		SessionID: sessionID,
		Count:     len(tools),
		Tools:     tools,
	})
}
