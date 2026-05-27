package main

import (
	"encoding/json"
	"net/http"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
)

type sessionLoopProtocolSummary = loopstate.Summary

type sessionLoopProtocolResponse struct {
	SessionID string                      `json:"session_id"`
	Protocol  string                      `json:"protocol"`
	Summary   *sessionLoopProtocolSummary `json:"summary,omitempty"`
}

func handleSessionLoopProtocol(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	protocol, summary, found, err := readSessionLoopProtocol(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session loop protocol", err)
		return
	}
	if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session loop protocol not found", nil, "not_found")
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionLoopProtocolResponse{
		SessionID: sessionID,
		Protocol:  protocol,
		Summary:   &summary,
	})
}

func readSessionLoopProtocol(pool *SessionPool, sessionID string) (string, sessionLoopProtocolSummary, bool, error) {
	path := sessionLoopProtocolPath(pool, sessionID)
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		return "", sessionLoopProtocolSummary{}, false, err
	}
	summary, found, err := loopstate.SummarizeFile(path, loopstate.ProtocolRelPath(sessionID))
	if err != nil || !found {
		return "", sessionLoopProtocolSummary{}, false, err
	}
	return protocol, summary, true, nil
}

func sessionLoopProtocolPath(pool *SessionPool, sessionID string) string {
	return loopstate.ProtocolPath(pool.sessionDirPath(sessionID), sessionID)
}

func summarizeSessionLoopProtocolFile(pool *SessionPool, sessionID string) *sessionLoopProtocolSummary {
	summary, found, err := loopstate.SummarizeFile(sessionLoopProtocolPath(pool, sessionID), loopstate.ProtocolRelPath(sessionID))
	if err != nil || !found {
		return nil
	}
	return &summary
}
