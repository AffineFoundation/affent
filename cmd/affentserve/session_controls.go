package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/planstate"
)

const (
	maxSessionMessageBodyBytes  = 4 * 1024 * 1024
	sessionPlanOnlyMaxToolCalls = 2
)

const (
	sessionMessageModeNormal      = "normal"
	sessionMessageModePlanOnly    = "plan_only"
	sessionMessageModeExecutePlan = "execute_plan"
)

type sessionMessageRequest struct {
	Content string `json:"content"`
	Mode    string `json:"mode,omitempty"`
}

type sessionMessageResponse struct {
	SessionID string `json:"session_id"`
	TurnID    string `json:"turn_id"`
}

type sessionCancelResponse struct {
	SessionID string `json:"session_id"`
	Accepted  bool   `json:"accepted"`
}

func handleSessionMessage(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	req, err := decodeSessionMessageRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session message request", err, "bad_request")
		return
	}
	content := strings.TrimSpace(req.Content)
	mode, err := normalizeSessionMessageMode(req.Mode)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session message request", err, "bad_request")
		return
	}
	if content == "" && mode != sessionMessageModeExecutePlan {
		writeJSONErrorTyped(w, http.StatusBadRequest, "content is required", nil, "bad_request")
		return
	}
	executePlanStepIndex := 0
	if sessionMessageModeRequiresPlanTool(mode) && !pool.cfg.EnableBuiltins {
		writeJSONErrorTyped(w, http.StatusConflict, "session mode unavailable", errors.New("plan tool is not available"), "mode_unavailable")
		return
	}
	if mode == sessionMessageModeExecutePlan {
		content, executePlanStepIndex, err = prepareSessionExecutePlan(pool, sessionID, content)
		if err != nil {
			writeJSONErrorTyped(w, http.StatusBadRequest, "execute plan", err, "bad_request")
			return
		}
	} else if mode == sessionMessageModePlanOnly {
		content = agent.PlanOnlyUserPrompt(content)
	}
	sess, err := pool.GetOrCreate(sessionID)
	if err != nil {
		if errors.Is(err, ErrShuttingDown) {
			w.Header().Set("Retry-After", "5")
			writeJSONError(w, http.StatusServiceUnavailable, "server shutting down", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "create session", err)
		return
	}
	opts, err := sessionMessageTurnOptions(sess, mode, executePlanStepIndex)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusConflict, "session mode unavailable", err, "mode_unavailable")
		return
	}
	turnID, err := sess.SendUserWithOptions(r.Context(), content, opts)
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrTurnInFlight):
			w.Header().Set("Retry-After", "1")
			writeJSONError(w, http.StatusConflict, "session busy", err)
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeJSONError(w, 499, "client disconnected", err)
		default:
			writeJSONError(w, http.StatusInternalServerError, "send user", err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	_ = json.NewEncoder(w).Encode(sessionMessageResponse{
		SessionID: sess.ID,
		TurnID:    turnID,
	})
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

func decodeSessionMessageRequest(w http.ResponseWriter, r *http.Request) (sessionMessageRequest, error) {
	var req sessionMessageRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionMessageBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			return req, fmt.Errorf("request body exceeds %d-byte limit", mbe.Limit)
		}
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	return req, nil
}

func normalizeSessionMessageMode(raw string) (string, error) {
	mode := strings.ToLower(strings.TrimSpace(raw))
	switch mode {
	case "", sessionMessageModeNormal:
		return sessionMessageModeNormal, nil
	case sessionMessageModePlanOnly, sessionMessageModeExecutePlan:
		return mode, nil
	default:
		return "", fmt.Errorf("mode must be one of %q, %q, or %q", sessionMessageModeNormal, sessionMessageModePlanOnly, sessionMessageModeExecutePlan)
	}
}

func sessionMessageTurnOptions(sess *Session, mode string, executePlanStepIndex int) (agent.TurnOptions, error) {
	if !sessionMessageModeRequiresPlanTool(mode) {
		return agent.TurnOptions{}, nil
	}
	if sess == nil || sess.registry == nil {
		return agent.TurnOptions{}, errors.New("plan tool is not available")
	}
	if mode != sessionMessageModePlanOnly {
		if _, ok := sess.registry.Get(agent.PlanToolName); !ok {
			return agent.TurnOptions{}, errors.New("plan tool is not available")
		}
		return agent.ExecutePlanTurnOptionsForStep(executePlanStepIndex), nil
	}
	return agent.PlanOnlyTurnOptions(sess.registry, sessionPlanOnlyMaxToolCalls)
}

func sessionMessageModeRequiresPlanTool(mode string) bool {
	return mode == sessionMessageModePlanOnly || mode == sessionMessageModeExecutePlan
}

func prepareSessionExecutePlan(pool *SessionPool, sessionID, request string) (string, int, error) {
	summary := summarizeSessionPlanFile(pool, sessionID)
	switch {
	case summary == nil:
		return "", 0, fmt.Errorf("session %q has no persisted plan; create one with mode=%q first", sessionID, sessionMessageModePlanOnly)
	case summary.Error:
		return "", 0, fmt.Errorf("session %q has an unreadable plan; inspect or clear it with the session plan API", sessionID)
	case summary.Label == planstate.LabelEmpty:
		return "", 0, fmt.Errorf("session %q has an empty plan; create a concrete plan with mode=%q first", sessionID, sessionMessageModePlanOnly)
	case summary.Done:
		return "", 0, fmt.Errorf("session %q plan is already done; clear it or create a new plan", sessionID)
	case summary.Blocked:
		return "", 0, fmt.Errorf("session %q plan is blocked at step %d; resolve the blocker before executing", sessionID, summary.CurrentStepIndex)
	case summary.TotalSteps == 0:
		return "", 0, fmt.Errorf("session %q has no executable plan steps", sessionID)
	case summary.CurrentStepIndex <= 0:
		return "", 0, fmt.Errorf("session %q has no current executable plan step", sessionID)
	}
	request = strings.TrimSpace(request)
	if request == "" {
		request = "Proceed with the active persisted plan."
	}
	return sessionExecutePlanPrompt(request, summary.Label), summary.CurrentStepIndex, nil
}

func sessionExecutePlanPrompt(request, label string) string {
	return `Execute-plan mode is enabled.

The user has confirmed execution of this session's persisted task plan (` + strings.TrimSpace(label) + `). Continue from AFFENT ACTIVE PLAN. Execute only the current unfinished step first, use the tools needed for that step, then call plan with action=update for that same step before the final answer. Mark the step completed only when its evidence or implementation is actually done; otherwise keep it in_progress or blocked with a short note. Do not restart planning or call action=set unless the persisted plan is stale or impossible to execute.

User confirmation/request:
` + strings.TrimSpace(request)
}
