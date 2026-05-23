package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const maxSessionPlanBytes = 32 * 1024

type sessionPlanResponse struct {
	SessionID string          `json:"session_id"`
	Plan      json.RawMessage `json:"plan"`
}

type sessionPlanSummary struct {
	Label          string `json:"label"`
	TotalSteps     int    `json:"total_steps"`
	CompletedSteps int    `json:"completed_steps"`
	Active         bool   `json:"active"`
	Blocked        bool   `json:"blocked"`
	Error          bool   `json:"error"`
}

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
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionPlanResponse{
		SessionID: sessionID,
		Plan:      plan,
	})
}

func readSessionPlan(pool *SessionPool, sessionID string) (json.RawMessage, bool, error) {
	path := filepath.Join(pool.sessionDirPath(sessionID), "plan.json")
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if info.IsDir() {
		return nil, false, errors.New("plan path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return nil, false, errors.New("plan path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, false, nil
		}
		return nil, false, err
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, maxSessionPlanBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > maxSessionPlanBytes {
		return nil, false, fmt.Errorf("plan file exceeds %d bytes", maxSessionPlanBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, false, errors.New("plan file is empty")
	}
	if !json.Valid(raw) {
		return nil, false, errors.New("plan file is not valid JSON")
	}
	return json.RawMessage(raw), true, nil
}

func summarizeSessionPlanFile(pool *SessionPool, sessionID string) *sessionPlanSummary {
	raw, found, err := readSessionPlan(pool, sessionID)
	if err != nil {
		return &sessionPlanSummary{Label: "plan:error", Error: true}
	}
	if !found {
		return nil
	}
	summary, err := summarizeSessionPlan(raw)
	if err != nil {
		return &sessionPlanSummary{Label: "plan:error", Error: true}
	}
	return summary
}

type sessionPlanSummaryState struct {
	Steps []struct {
		Status string `json:"status"`
	} `json:"steps"`
}

func summarizeSessionPlan(raw json.RawMessage) (*sessionPlanSummary, error) {
	var st sessionPlanSummaryState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, err
	}
	if len(st.Steps) == 0 {
		return &sessionPlanSummary{Label: "plan:empty"}, nil
	}
	out := &sessionPlanSummary{TotalSteps: len(st.Steps)}
	for _, step := range st.Steps {
		switch strings.TrimSpace(step.Status) {
		case "completed":
			out.CompletedSteps++
		case "in_progress":
			out.Active = true
		case "blocked":
			out.Blocked = true
		}
	}
	out.Label = fmt.Sprintf("plan:%d/%d", out.CompletedSteps, out.TotalSteps)
	if out.Active {
		out.Label += ":active"
	}
	if out.Blocked {
		out.Label += ":blocked"
	}
	return out, nil
}
