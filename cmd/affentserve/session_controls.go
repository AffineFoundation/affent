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
)

const maxSessionMessageBodyBytes = 4 * 1024 * 1024

type sessionMessageRequest struct {
	Content string `json:"content"`
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
	if content == "" {
		writeJSONErrorTyped(w, http.StatusBadRequest, "content is required", nil, "bad_request")
		return
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
	turnID, err := sess.SendUser(r.Context(), content)
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
