package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
	"github.com/affinefoundation/affent/internal/toolfailure"
	"github.com/google/uuid"
)

const (
	maxSessionCommandBodyBytes = 16 * 1024
	maxSessionCommandBytes     = 8 * 1024
	maxSessionCommandCwdBytes  = 2 * 1024
)

type sessionCommandRequest struct {
	Command    string `json:"command"`
	Cwd        string `json:"cwd,omitempty"`
	TimeoutSec int    `json:"timeout_sec,omitempty"`
}

type sessionCommandResponse struct {
	SessionID   string `json:"session_id"`
	TurnID      string `json:"turn_id"`
	CallID      string `json:"call_id"`
	ExitCode    int    `json:"exit_code"`
	Result      string `json:"result"`
	DurationMS  int64  `json:"duration_ms,omitempty"`
	Workspace   string `json:"workspace,omitempty"`
	CompletedAt string `json:"completed_at"`
}

func handleSessionCommand(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	req, err := decodeSessionCommandRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid command request", err, "bad_request")
		return
	}
	sess, err := pool.GetOrCreate(sessionID)
	if err != nil {
		if errors.Is(err, ErrShuttingDown) {
			w.Header().Set("Retry-After", "5")
			writeJSONError(w, http.StatusServiceUnavailable, "server shutting down", err)
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "open session", err)
		return
	}
	resp, err := sess.RunWorkbenchCommand(r.Context(), req)
	if err != nil {
		switch {
		case errors.Is(err, agent.ErrTurnInFlight):
			w.Header().Set("Retry-After", "1")
			writeJSONErrorTyped(w, http.StatusConflict, "session busy", err, "session_busy")
		case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
			writeJSONError(w, 499, "client disconnected", err)
		default:
			writeJSONErrorTyped(w, http.StatusConflict, "run command", err, "command_unavailable")
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func decodeSessionCommandRequest(w http.ResponseWriter, r *http.Request) (sessionCommandRequest, error) {
	var req sessionCommandRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionCommandBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	req.Command = strings.TrimSpace(req.Command)
	req.Cwd = strings.TrimSpace(req.Cwd)
	if req.Command == "" {
		return req, errors.New("command is required")
	}
	if len(req.Command) > maxSessionCommandBytes {
		return req, fmt.Errorf("command exceeds %d bytes", maxSessionCommandBytes)
	}
	if len(req.Cwd) > maxSessionCommandCwdBytes {
		return req, fmt.Errorf("cwd exceeds %d bytes", maxSessionCommandCwdBytes)
	}
	if req.TimeoutSec < 0 {
		return req, errors.New("timeout_sec must be positive")
	}
	return req, nil
}

func (s *Session) RunWorkbenchCommand(ctx context.Context, req sessionCommandRequest) (sessionCommandResponse, error) {
	if s == nil || s.registry == nil {
		return sessionCommandResponse{}, errors.New("session is not available")
	}
	s.mu.Lock()
	if s.activeTurns.Load() > 0 {
		s.mu.Unlock()
		return sessionCommandResponse{}, agent.ErrTurnInFlight
	}
	s.activeTurns.Add(1)
	s.lastUsed = time.Now()
	s.mu.Unlock()
	tool, ok := s.registry.Get("shell")
	if !ok || tool == nil {
		s.endTurn()
		return sessionCommandResponse{}, errors.New("shell command runner is not configured for this session")
	}
	turnID := "turn_" + uuid.NewString()
	callID := "manual_shell_" + uuid.NewString()
	args := map[string]any{"command": req.Command}
	if req.Cwd != "" {
		args["cwd"] = req.Cwd
	}
	if req.TimeoutSec > 0 {
		args["timeout_sec"] = req.TimeoutSec
	}
	rawArgs, err := json.Marshal(args)
	if err != nil {
		s.endTurn()
		return sessionCommandResponse{}, err
	}

	s.publishSessionEvent(sse.TypeTurnStart, sse.TurnStartPayload{TurnID: turnID})
	s.publishSessionEvent(sse.TypeUserMessage, sse.UserMessagePayload{
		TurnID:      turnID,
		Text:        "Run command: " + req.Command,
		DisplayText: "Run command: " + req.Command,
		Mode:        "manual_command",
		Source:      "workbench",
	})
	s.publishSessionEvent(sse.TypeToolRequest, sse.ToolRequestPayload{
		TurnID:       turnID,
		CallID:       callID,
		Tool:         "shell",
		Args:         args,
		ArgsBytes:    len(rawArgs),
		ArgsCapBytes: len(rawArgs),
	})

	start := time.Now()
	result, execErr := tool.Execute(ctx, rawArgs)
	duration := time.Since(start)
	exitCode := shellExitCode(result, execErr)
	if execErr != nil {
		if strings.TrimSpace(result) != "" {
			result = fmt.Sprintf("Error: %v\n%s", execErr, result)
		} else {
			result = fmt.Sprintf("Error: %v", execErr)
		}
	}
	payload := sessionCommandToolResultPayload(turnID, callID, exitCode, result, duration)
	s.publishSessionEvent(sse.TypeToolResult, payload)
	reason := sse.TurnEndCompleted
	if execErr != nil {
		reason = sse.TurnEndError
	}
	s.publishSessionEvent(sse.TypeTurnEnd, sse.TurnEndPayload{TurnID: turnID, Reason: reason, ToolStats: &sse.ToolRuntimeStats{
		ToolRequests:   1,
		ToolErrors:     boolCount(exitCode != 0 || execErr != nil),
		ToolDurationMS: duration.Milliseconds(),
	}})
	s.endTurn()

	return sessionCommandResponse{
		SessionID:   s.ID,
		TurnID:      turnID,
		CallID:      callID,
		ExitCode:    exitCode,
		Result:      result,
		DurationMS:  duration.Milliseconds(),
		Workspace:   s.Workspace(),
		CompletedAt: time.Now().UTC().Format(time.RFC3339),
	}, nil
}

func sessionCommandToolResultPayload(turnID, callID string, exitCode int, result string, duration time.Duration) sse.ToolResultPayload {
	capped, truncated, omitted := capSessionCommandResult(result, agent.MaxToolResultBytesInEvent)
	payload := sse.ToolResultPayload{
		TurnID:             turnID,
		CallID:             callID,
		ExitCode:           exitCode,
		ResultSummary:      textutil.Preview(result, agent.MaxToolResultPreviewInEvent),
		Result:             capped,
		ResultTruncated:    truncated,
		ResultBytes:        len(result),
		ResultOmittedBytes: omitted,
		ResultCapBytes:     agent.MaxToolResultBytesInEvent,
	}
	if duration >= time.Millisecond {
		payload.DurationMS = duration.Milliseconds()
	}
	if exitCode != 0 {
		payload.FailureKind = toolfailure.KindForResult("shell", result, true)
		payload.FailureKinds = toolfailure.KindsForResult("shell", result, true)
	}
	return payload
}

func capSessionCommandResult(text string, maxBytes int) (string, bool, int) {
	if maxBytes <= 0 || len(text) <= maxBytes {
		return text, false, 0
	}
	omitted := len(text) - maxBytes
	return text[:maxBytes] + fmt.Sprintf("\n[... %d bytes omitted ...]", omitted), true, omitted
}

var shellExitRE = regexp.MustCompile(`(?m)^\[exit (-?\d+)\]\s*$`)

func shellExitCode(result string, err error) int {
	if matches := shellExitRE.FindStringSubmatch(result); len(matches) == 2 {
		var code int
		if _, scanErr := fmt.Sscanf(matches[1], "%d", &code); scanErr == nil {
			return code
		}
	}
	if err != nil {
		return -1
	}
	return 0
}

func boolCount(value bool) int {
	if value {
		return 1
	}
	return 0
}
