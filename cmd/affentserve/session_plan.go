package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/planstate"
)

type sessionPlanResponse struct {
	SessionID string              `json:"session_id"`
	Plan      json.RawMessage     `json:"plan"`
	Summary   *sessionPlanSummary `json:"summary,omitempty"`
}

type sessionPlanDeleteResponse struct {
	SessionID string `json:"session_id"`
	Cleared   bool   `json:"cleared"`
}

type sessionPlanSummary = planstate.Summary

func handleSessionPlan(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	plan, found, err := readSessionPlan(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session plan", err)
		return
	}
	if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session plan not found", nil, "not_found")
		return
	}
	summary, err := planstate.SummarizeJSON(plan)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "summarize session plan", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionPlanResponse{
		SessionID: sessionID,
		Plan:      plan,
		Summary:   &summary,
	})
}

func handleSessionPlanDelete(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	cleared, err := clearSessionPlan(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "clear session plan", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionPlanDeleteResponse{
		SessionID: sessionID,
		Cleared:   cleared,
	})
}

func readSessionPlan(pool *SessionPool, sessionID string) (json.RawMessage, bool, error) {
	path := filepath.Join(pool.sessionDirPath(sessionID), "plan.json")
	return planstate.ReadFile(path)
}

func clearSessionPlan(pool *SessionPool, sessionID string) (bool, error) {
	path := filepath.Join(pool.sessionDirPath(sessionID), "plan.json")
	removed, err := planstate.RemoveFile(path)
	if err != nil || !removed {
		return removed, err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return removed, nil
}

func summarizeSessionPlanFile(pool *SessionPool, sessionID string) *sessionPlanSummary {
	raw, found, err := readSessionPlan(pool, sessionID)
	if err != nil {
		summary := planstate.ErrorSummary()
		return &summary
	}
	if !found {
		return nil
	}
	summary, err := planstate.SummarizeJSON(raw)
	if err != nil {
		summary := planstate.ErrorSummary()
		return &summary
	}
	return &summary
}
