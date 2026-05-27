package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
)

type sessionLoopProtocolSummary = loopstate.Summary

const maxSessionLoopProtocolBodyBytes = loopstate.MaxProtocolBytes + 4096

type sessionLoopProtocolResponse struct {
	SessionID string                      `json:"session_id"`
	Protocol  string                      `json:"protocol"`
	Summary   *sessionLoopProtocolSummary `json:"summary,omitempty"`
}

type sessionLoopProtocolUpdateRequest struct {
	Protocol string `json:"protocol"`
}

type sessionLoopProtocolDeleteResponse struct {
	SessionID string `json:"session_id"`
	Cleared   bool   `json:"cleared"`
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

func handleSessionLoopProtocolUpdate(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	req, err := decodeSessionLoopProtocolUpdateRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid loop protocol request", err, "bad_request")
		return
	}
	if strings.TrimSpace(req.Protocol) == "" {
		writeJSONErrorTyped(w, http.StatusBadRequest, "loop protocol is required", nil, "bad_request")
		return
	}
	if len([]byte(strings.TrimSpace(req.Protocol))) > loopstate.MaxProtocolBytes {
		writeJSONErrorTyped(w, http.StatusRequestEntityTooLarge, "loop protocol too large", nil, "bad_request")
		return
	}
	protocol, summary, err := writeSessionLoopProtocol(pool, sessionID, req.Protocol)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write session loop protocol", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionLoopProtocolResponse{
		SessionID: sessionID,
		Protocol:  protocol,
		Summary:   &summary,
	})
}

func handleSessionLoopProtocolDelete(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	cleared, err := clearSessionLoopProtocol(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "clear session loop protocol", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionLoopProtocolDeleteResponse{
		SessionID: sessionID,
		Cleared:   cleared,
	})
}

func decodeSessionLoopProtocolUpdateRequest(w http.ResponseWriter, r *http.Request) (sessionLoopProtocolUpdateRequest, error) {
	var req sessionLoopProtocolUpdateRequest
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionLoopProtocolBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	return req, nil
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

func writeSessionLoopProtocol(pool *SessionPool, sessionID, protocol string) (string, sessionLoopProtocolSummary, error) {
	if _, err := pool.allocSessionDir(sessionID); err != nil {
		return "", sessionLoopProtocolSummary{}, err
	}
	path := sessionLoopProtocolPath(pool, sessionID)
	if err := loopstate.WriteProtocol(path, protocol); err != nil {
		return "", sessionLoopProtocolSummary{}, err
	}
	protocol, summary, found, err := readSessionLoopProtocol(pool, sessionID)
	if err != nil {
		return "", sessionLoopProtocolSummary{}, err
	}
	if !found {
		return "", sessionLoopProtocolSummary{}, os.ErrNotExist
	}
	return protocol, summary, nil
}

func clearSessionLoopProtocol(pool *SessionPool, sessionID string) (bool, error) {
	removed, err := loopstate.RemoveProtocol(sessionLoopProtocolPath(pool, sessionID))
	if err != nil || !removed {
		return removed, err
	}
	if d, err := os.Open(filepath.Dir(sessionLoopProtocolPath(pool, sessionID))); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return removed, nil
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
