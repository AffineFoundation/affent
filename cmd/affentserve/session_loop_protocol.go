package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
)

type sessionLoopProtocolSummary = loopstate.Summary

const maxSessionLoopProtocolBodyBytes = loopstate.MaxProtocolBytes + 4096
const sessionLoopProtocolRecentEvents = 20

type sessionLoopProtocolResponse struct {
	SessionID string                      `json:"session_id"`
	Protocol  string                      `json:"protocol"`
	Summary   *sessionLoopProtocolSummary `json:"summary,omitempty"`
	State     *loopstate.State            `json:"state,omitempty"`
	Events    []loopstate.Event           `json:"events,omitempty"`
}

type sessionLoopProtocolUpdateRequest struct {
	Protocol        string   `json:"protocol"`
	Activate        bool     `json:"activate,omitempty"`
	Goal            string   `json:"goal,omitempty"`
	Reason          string   `json:"reason,omitempty"`
	SectionsChanged []string `json:"sections_changed,omitempty"`
}

type sessionLoopProtocolDeleteResponse struct {
	SessionID string            `json:"session_id"`
	Cleared   bool              `json:"cleared"`
	State     *loopstate.State  `json:"state,omitempty"`
	Events    []loopstate.Event `json:"events,omitempty"`
}

type sessionLoopProtocolValidationError struct {
	err error
}

func (e sessionLoopProtocolValidationError) Error() string {
	if e.err == nil {
		return "invalid loop protocol"
	}
	return e.err.Error()
}

func (e sessionLoopProtocolValidationError) Unwrap() error {
	return e.err
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
	protocol, summary, state, events, found, err := readSessionLoopProtocol(pool, sessionID)
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
		State:     state,
		Events:    events,
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
	if !req.Activate && strings.TrimSpace(req.Protocol) == "" {
		writeJSONErrorTyped(w, http.StatusBadRequest, "loop protocol is required", nil, "bad_request")
		return
	}
	if len([]byte(strings.TrimSpace(req.Protocol))) > loopstate.MaxProtocolBytes {
		writeJSONErrorTyped(w, http.StatusRequestEntityTooLarge, "loop protocol too large", nil, "bad_request")
		return
	}
	protocol, summary, state, events, err := writeSessionLoopProtocol(pool, sessionID, req)
	if err != nil {
		var validationErr sessionLoopProtocolValidationError
		if errors.As(err, &validationErr) {
			writeJSONErrorTyped(w, http.StatusBadRequest, "invalid loop protocol", validationErr.Unwrap(), "bad_request")
			return
		}
		writeJSONError(w, http.StatusInternalServerError, "write session loop protocol", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionLoopProtocolResponse{
		SessionID: sessionID,
		Protocol:  protocol,
		Summary:   &summary,
		State:     &state,
		Events:    events,
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
	cleared, state, events, err := clearSessionLoopProtocol(pool, sessionID)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "clear session loop protocol", err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionLoopProtocolDeleteResponse{
		SessionID: sessionID,
		Cleared:   cleared,
		State:     state,
		Events:    events,
	})
}

func decodeSessionLoopProtocolUpdateRequest(w http.ResponseWriter, r *http.Request) (sessionLoopProtocolUpdateRequest, error) {
	var req sessionLoopProtocolUpdateRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionLoopProtocolBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		return req, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return req, errors.New("request body must contain a single JSON object")
	}
	return req, nil
}

func readSessionLoopProtocol(pool *SessionPool, sessionID string) (string, sessionLoopProtocolSummary, *loopstate.State, []loopstate.Event, bool, error) {
	path := sessionLoopProtocolPath(pool, sessionID)
	protocol, found, err := loopstate.ReadProtocol(path)
	if err != nil || !found {
		return "", sessionLoopProtocolSummary{}, nil, nil, false, err
	}
	state, stateFound, err := loopstate.ReadState(sessionLoopStatePath(pool, sessionID))
	if err != nil {
		return "", sessionLoopProtocolSummary{}, nil, nil, false, err
	}
	summary, found, err := loopstate.SummarizeFile(path, loopstate.ProtocolRelPath(sessionID))
	if err != nil || !found {
		return "", sessionLoopProtocolSummary{}, nil, nil, false, err
	}
	events, _, err := readSessionLoopEvents(pool, sessionID)
	if err != nil {
		return "", sessionLoopProtocolSummary{}, nil, nil, false, err
	}
	if stateFound {
		return protocol, summary, &state, events, true, nil
	}
	return protocol, summary, nil, events, true, nil
}

func writeSessionLoopProtocol(pool *SessionPool, sessionID string, req sessionLoopProtocolUpdateRequest) (string, sessionLoopProtocolSummary, loopstate.State, []loopstate.Event, error) {
	if _, err := pool.allocSessionDir(sessionID); err != nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
	}
	path := sessionLoopProtocolPath(pool, sessionID)
	if req.Activate && strings.TrimSpace(req.Protocol) == "" {
		_, _, _, err := loopstate.EnsureProtocolTemplate(path, loopstate.ProtocolTemplateOptions{
			LoopID:       sessionID,
			OwnerSession: sessionID,
			Goal:         req.Goal,
			Status:       "draft",
			Plan:         serveLoopProtocolCurrentPlanCheckpoint(filepath.Join(pool.sessionDirPath(sessionID), "plan.json")),
		})
		if err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
		}
		protocol, summary, statePtr, events, found, err := readSessionLoopProtocol(pool, sessionID)
		if err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
		}
		if !found || statePtr == nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, os.ErrNotExist
		}
		return protocol, summary, *statePtr, events, nil
	}
	if req.Activate {
		if loopstate.ProtocolStatus(req.Protocol) != "running" {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, sessionLoopProtocolValidationError{err: errors.New("activate requires LOOP.md metadata status: running")}
		}
		if err := loopstate.ValidateProtocolActivation(req.Protocol); err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, sessionLoopProtocolValidationError{err: err}
		}
		if _, err := loopstate.RepairRecordedCalibrationFromProtocol(path, req.Protocol); err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
		}
		if err := loopstate.ValidateProtocolActivationReady(path); err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, sessionLoopProtocolValidationError{err: err}
		}
		if err := loopstate.WriteProtocol(path, req.Protocol); err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
		}
		if _, _, err := loopstate.RecordProtocolActivation(path, req.Reason); err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
		}
		protocol, summary, statePtr, events, found, err := readSessionLoopProtocol(pool, sessionID)
		if err != nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
		}
		if !found || statePtr == nil {
			return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, os.ErrNotExist
		}
		return protocol, summary, *statePtr, events, nil
	}
	if err := validateNonActivatingLoopProtocolUpdate(path, req.Protocol); err != nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, sessionLoopProtocolValidationError{err: err}
	}
	if err := loopstate.WriteProtocol(path, req.Protocol); err != nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
	}
	summary, found, err := loopstate.SummarizeFile(path, loopstate.ProtocolRelPath(sessionID))
	if err != nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
	}
	if !found {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, os.ErrNotExist
	}
	if _, err := recordSessionLoopProtocolEvent(pool, sessionID, loopstate.Event{
		Type:            "loop.protocol_update",
		Summary:         "Updated LOOP.md",
		SectionsChanged: sanitizeLoopProtocolSections(req.SectionsChanged),
		Reason:          strings.TrimSpace(req.Reason),
		Path:            loopstate.ProtocolRelPath(sessionID),
	}, summary); err != nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
	}
	protocol, summary, statePtr, events, found, err := readSessionLoopProtocol(pool, sessionID)
	if err != nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, err
	}
	if !found || statePtr == nil {
		return "", sessionLoopProtocolSummary{}, loopstate.State{}, nil, os.ErrNotExist
	}
	return protocol, summary, *statePtr, events, nil
}

func validateNonActivatingLoopProtocolUpdate(path, protocol string) error {
	if loopstate.ProtocolStatus(protocol) != "running" {
		return nil
	}
	current, found, err := loopstate.ReadProtocol(path)
	if err != nil {
		return err
	}
	if found && loopstate.ProtocolStatus(current) == "running" {
		return nil
	}
	return errors.New("status: running requires activate=true after loop calibration; ordinary protocol updates cannot activate LOOP.md")
}

func clearSessionLoopProtocol(pool *SessionPool, sessionID string) (bool, *loopstate.State, []loopstate.Event, error) {
	if _, found, err := durableSessionDirInfo(pool.sessionDirPath(sessionID)); err != nil || !found {
		return false, nil, nil, err
	}
	removed, err := loopstate.RemoveProtocol(sessionLoopProtocolPath(pool, sessionID))
	if err != nil {
		return false, nil, nil, err
	}
	state, stateFound, err := loopstate.ReadState(sessionLoopStatePath(pool, sessionID))
	if err != nil {
		return false, nil, nil, err
	}
	if !removed && !stateFound {
		return false, nil, nil, nil
	}
	state.Status = "disabled"
	updated, err := recordSessionLoopProtocolEvent(pool, sessionID, loopstate.Event{
		Type:    "loop.protocol_delete",
		Summary: "Disabled LOOP.md",
		Reason:  "loop protocol deleted",
		Path:    loopstate.ProtocolRelPath(sessionID),
	}, sessionLoopProtocolSummary{State: &state})
	if err != nil {
		return false, nil, nil, err
	}
	events, _, err := readSessionLoopEvents(pool, sessionID)
	if err != nil {
		return false, nil, nil, err
	}
	return removed, &updated, events, nil
}

func sessionLoopProtocolPath(pool *SessionPool, sessionID string) string {
	return loopstate.ProtocolPath(pool.sessionDirPath(sessionID), sessionID)
}

func sessionLoopStatePath(pool *SessionPool, sessionID string) string {
	return loopstate.StatePath(pool.sessionDirPath(sessionID), sessionID)
}

func sessionLoopEventsPath(pool *SessionPool, sessionID string) string {
	return loopstate.EventsPath(pool.sessionDirPath(sessionID), sessionID)
}

func summarizeSessionLoopProtocolFile(pool *SessionPool, sessionID string) *sessionLoopProtocolSummary {
	summary, found, err := loopstate.SummarizeFile(sessionLoopProtocolPath(pool, sessionID), loopstate.ProtocolRelPath(sessionID))
	if err != nil || !found {
		return nil
	}
	return &summary
}

func readSessionLoopState(pool *SessionPool, sessionID string) (*loopstate.State, bool, error) {
	state, found, err := loopstate.ReadState(sessionLoopStatePath(pool, sessionID))
	if err != nil || !found {
		return nil, found, err
	}
	return &state, true, nil
}

func readSessionLoopEvents(pool *SessionPool, sessionID string) ([]loopstate.Event, bool, error) {
	return loopstate.ReadRecentEvents(sessionLoopEventsPath(pool, sessionID), sessionLoopProtocolRecentEvents)
}

func recordSessionLoopProtocolEvent(pool *SessionPool, sessionID string, ev loopstate.Event, summary sessionLoopProtocolSummary) (loopstate.State, error) {
	now := time.Now().UTC()
	ev.Time = formatTime(now)
	written, err := loopstate.AppendEvent(sessionLoopEventsPath(pool, sessionID), ev)
	if err != nil {
		return loopstate.State{}, err
	}
	prev, found, err := loopstate.ReadState(sessionLoopStatePath(pool, sessionID))
	if err != nil {
		return loopstate.State{}, err
	}
	state := prev
	if !found {
		state = loopstate.State{
			Version:      1,
			LoopID:       sessionID,
			OwnerSession: sessionID,
			CreatedAt:    formatTime(now),
		}
	}
	if summary.State != nil {
		if summary.State.LoopID != "" {
			state.LoopID = summary.State.LoopID
		}
		if summary.State.OwnerSession != "" {
			state.OwnerSession = summary.State.OwnerSession
		}
		if summary.State.Status != "" {
			state.Status = summary.State.Status
		}
	}
	if summary.LoopID != "" {
		state.LoopID = summary.LoopID
	}
	if state.LoopID == "" {
		state.LoopID = sessionID
	}
	if summary.OwnerSession != "" {
		state.OwnerSession = summary.OwnerSession
	}
	if state.OwnerSession == "" {
		state.OwnerSession = sessionID
	}
	if summary.Status != "" {
		state.Status = summary.Status
	}
	if state.Status == "" {
		state.Status = "running"
	}
	state.ProtocolPath = loopstate.ProtocolRelPath(sessionID)
	state.UpdatedAt = formatTime(now)
	if written.Type == "loop.protocol_update" {
		state.LastProtocolUpdateAt = state.UpdatedAt
		state.ProtocolUpdates++
		if state.Status == "" || state.Status == "running" {
			state.NeedsFullProtocolFeed = true
		}
	}
	state.EventCount = written.Seq
	state.LastEventType = written.Type
	state.LastEventSummary = written.Summary
	state.LastEventAt = written.Time
	if err := loopstate.WriteState(sessionLoopStatePath(pool, sessionID), state); err != nil {
		return loopstate.State{}, err
	}
	return state, nil
}

func sanitizeLoopProtocolSections(in []string) []string {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, item := range in {
		item = strings.TrimSpace(item)
		if item == "" || seen[item] {
			continue
		}
		seen[item] = true
		out = append(out, item)
		if len(out) >= 16 {
			break
		}
	}
	return out
}
