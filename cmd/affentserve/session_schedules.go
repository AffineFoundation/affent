package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
)

const (
	sessionSchedulesFileName     = "schedules.json"
	sessionSchedulesFileMode     = 0o644
	maxSessionSchedulesFileBytes = 64 * 1024
	maxSessionSchedules          = 128
	maxSessionSchedulePrompt     = 8000
	maxSessionScheduleDisplay    = 512
	minSessionScheduleRepeat     = 60 * time.Second
	sessionScheduleSweepInterval = 30 * time.Second
	sessionScheduleRetryDelay    = time.Minute
	maxSessionScheduleErrorChars = 240
)

const (
	sessionScheduleKindCustom       = "custom"
	sessionScheduleKindCheckIn      = "checkin"
	sessionScheduleKindDailyCheckIn = "daily_checkin"
	sessionScheduleKindLoopTick     = "loop_tick"
)

type sessionSchedule struct {
	ID                    string `json:"id"`
	Kind                  string `json:"kind,omitempty"`
	Prompt                string `json:"prompt"`
	DisplayText           string `json:"display_text,omitempty"`
	Enabled               bool   `json:"enabled"`
	NextRunAt             string `json:"next_run_at"`
	RepeatIntervalSeconds int64  `json:"repeat_interval_seconds,omitempty"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
	LastRunAt             string `json:"last_run_at,omitempty"`
	LastTurnID            string `json:"last_turn_id,omitempty"`
	RunCount              int    `json:"run_count,omitempty"`
	LastError             string `json:"last_error,omitempty"`
}

type sessionSchedulesFile struct {
	Version   int               `json:"version"`
	Schedules []sessionSchedule `json:"schedules"`
}

type sessionSchedulesSummary struct {
	Count             int    `json:"count"`
	Enabled           int    `json:"enabled"`
	EnabledLoopTicks  int    `json:"enabled_loop_ticks,omitempty"`
	PendingLoopTicks  int    `json:"pending_loop_ticks,omitempty"`
	ErrorCount        int    `json:"error_count,omitempty"`
	LastError         string `json:"last_error,omitempty"`
	NextRunAt         string `json:"next_run_at,omitempty"`
	NextScheduleID    string `json:"next_schedule_id,omitempty"`
	NextScheduleKind  string `json:"next_schedule_kind,omitempty"`
	NextPromptPreview string `json:"next_prompt_preview,omitempty"`
}

type sessionSchedulesResponse struct {
	SessionID string                   `json:"session_id"`
	Schedules []sessionSchedule        `json:"schedules"`
	Summary   *sessionSchedulesSummary `json:"summary,omitempty"`
}

type sessionScheduleCreateRequest struct {
	Kind                  string `json:"kind,omitempty"`
	Prompt                string `json:"prompt"`
	DisplayText           string `json:"display_text,omitempty"`
	NextRunAt             string `json:"next_run_at"`
	RepeatIntervalSeconds int64  `json:"repeat_interval_seconds,omitempty"`
	Enabled               *bool  `json:"enabled,omitempty"`
}

type sessionScheduleUpdateRequest struct {
	Enabled *bool `json:"enabled,omitempty"`
}

type sessionScheduleDeleteResponse struct {
	SessionID  string                   `json:"session_id"`
	ScheduleID string                   `json:"schedule_id"`
	Cleared    bool                     `json:"cleared"`
	Summary    *sessionSchedulesSummary `json:"summary,omitempty"`
}

type sessionScheduleRun struct {
	SessionID    string
	ScheduleID   string
	ScheduleKind string
	Prompt       string
	DisplayText  string
	ClaimedAt    time.Time
}

func handleSessionSchedules(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	switch r.Method {
	case http.MethodGet:
		listSessionSchedules(pool, sessionID, w)
	case http.MethodPost:
		createSessionSchedule(pool, sessionID, w, r)
	default:
		writeJSONErrorTyped(w, http.StatusNotFound, "not found", nil, "not_found")
	}
}

func handleSessionScheduleUpdate(pool *SessionPool, sessionID, scheduleID string, w http.ResponseWriter, r *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	if err := validateSessionScheduleID(scheduleID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid schedule id", err, "bad_request")
		return
	}
	req, err := decodeSessionScheduleUpdateRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid schedule update request", err, "bad_request")
		return
	}
	if req.Enabled == nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "schedule update requires enabled", nil, "bad_request")
		return
	}
	pool.schedulesMu.Lock()
	defer pool.schedulesMu.Unlock()
	path := sessionSchedulesPath(pool, sessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session schedules", err)
		return
	}
	if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session schedules not found", nil, "not_found")
		return
	}
	updated := false
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range file.Schedules {
		if file.Schedules[i].ID != scheduleID {
			continue
		}
		file.Schedules[i].Enabled = *req.Enabled
		file.Schedules[i].UpdatedAt = now
		file.Schedules[i].LastError = ""
		updated = true
		break
	}
	if !updated {
		writeJSONErrorTyped(w, http.StatusNotFound, "session schedule not found", nil, "not_found")
		return
	}
	if err := writeSessionSchedulesFile(path, file); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write session schedules", err)
		return
	}
	schedules := sortedSessionSchedules(file.Schedules)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func handleSessionScheduleDelete(pool *SessionPool, sessionID, scheduleID string, w http.ResponseWriter, _ *http.Request) {
	if pool == nil {
		writeJSONError(w, http.StatusNotFound, "session not found", nil)
		return
	}
	if err := agent.ValidateSessionID(sessionID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid session id", err, "bad_request")
		return
	}
	if err := validateSessionScheduleID(scheduleID); err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid schedule id", err, "bad_request")
		return
	}
	pool.schedulesMu.Lock()
	defer pool.schedulesMu.Unlock()
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, sessionID))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session schedules", err)
		return
	}
	if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session schedules not found", nil, "not_found")
		return
	}
	next := file.Schedules[:0]
	cleared := false
	for _, schedule := range file.Schedules {
		if schedule.ID == scheduleID {
			cleared = true
			continue
		}
		next = append(next, schedule)
	}
	if !cleared {
		writeJSONErrorTyped(w, http.StatusNotFound, "session schedule not found", nil, "not_found")
		return
	}
	file.Schedules = next
	if err := writeSessionSchedulesFile(sessionSchedulesPath(pool, sessionID), file); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write session schedules", err)
		return
	}
	summary := summarizeSessionSchedulesForSession(pool, sessionID, file.Schedules)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionScheduleDeleteResponse{
		SessionID:  sessionID,
		ScheduleID: scheduleID,
		Cleared:    true,
		Summary:    summary,
	})
}

func listSessionSchedules(pool *SessionPool, sessionID string, w http.ResponseWriter) {
	if _, found, err := durableSessionDirInfo(pool.sessionDirPath(sessionID)); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session", err)
		return
	} else if !found {
		writeJSONErrorTyped(w, http.StatusNotFound, "session not found", nil, "not_found")
		return
	}
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, sessionID))
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session schedules", err)
		return
	}
	schedules := []sessionSchedule{}
	if found {
		schedules = sortedSessionSchedules(file.Schedules)
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func createSessionSchedule(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	req, err := decodeSessionScheduleCreateRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid schedule request", err, "bad_request")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
	displayText := strings.TrimSpace(req.DisplayText)
	nextRunAt, err := parseSessionScheduleTime(req.NextRunAt)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid next_run_at", err, "bad_request")
		return
	}
	if prompt == "" {
		writeJSONErrorTyped(w, http.StatusBadRequest, "schedule prompt is required", nil, "bad_request")
		return
	}
	if len([]byte(prompt)) > maxSessionSchedulePrompt {
		writeJSONErrorTyped(w, http.StatusRequestEntityTooLarge, "schedule prompt too large", nil, "bad_request")
		return
	}
	if len([]byte(displayText)) > maxSessionScheduleDisplay {
		writeJSONErrorTyped(w, http.StatusBadRequest, "schedule display_text too large", fmt.Errorf("display_text exceeds %d bytes", maxSessionScheduleDisplay), "bad_request")
		return
	}
	kind, err := normalizeSessionScheduleKind(req.Kind)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid schedule kind", err, "bad_request")
		return
	}
	if req.RepeatIntervalSeconds < 0 {
		writeJSONErrorTyped(w, http.StatusBadRequest, "repeat_interval_seconds must be non-negative", nil, "bad_request")
		return
	}
	if req.RepeatIntervalSeconds > 0 && time.Duration(req.RepeatIntervalSeconds)*time.Second < minSessionScheduleRepeat {
		writeJSONErrorTyped(w, http.StatusBadRequest, fmt.Sprintf("repeat_interval_seconds must be at least %d", int(minSessionScheduleRepeat.Seconds())), nil, "bad_request")
		return
	}
	if _, err := pool.allocSessionDir(sessionID); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "create session directory", err)
		return
	}
	path := sessionSchedulesPath(pool, sessionID)
	pool.schedulesMu.Lock()
	defer pool.schedulesMu.Unlock()
	file, found, err := readSessionSchedulesFile(path)
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, "read session schedules", err)
		return
	}
	if !found {
		file = sessionSchedulesFile{Version: 1}
	}
	if len(file.Schedules) >= maxSessionSchedules {
		writeJSONErrorTyped(w, http.StatusBadRequest, fmt.Sprintf("session has the maximum %d schedules", maxSessionSchedules), nil, "bad_request")
		return
	}
	now := time.Now().UTC().Format(time.RFC3339)
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	schedule := sessionSchedule{
		ID:                    newSessionScheduleID(),
		Kind:                  kind,
		Prompt:                prompt,
		DisplayText:           displayText,
		Enabled:               enabled,
		NextRunAt:             nextRunAt.Format(time.RFC3339),
		RepeatIntervalSeconds: req.RepeatIntervalSeconds,
		CreatedAt:             now,
		UpdatedAt:             now,
	}
	file.Schedules = append(file.Schedules, schedule)
	if err := writeSessionSchedulesFile(path, file); err != nil {
		writeJSONError(w, http.StatusInternalServerError, "write session schedules", err)
		return
	}
	schedules := sortedSessionSchedules(file.Schedules)
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func decodeSessionScheduleCreateRequest(w http.ResponseWriter, r *http.Request) (sessionScheduleCreateRequest, error) {
	var req sessionScheduleCreateRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSessionSchedulePrompt+4096))
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

func decodeSessionScheduleUpdateRequest(w http.ResponseWriter, r *http.Request) (sessionScheduleUpdateRequest, error) {
	var req sessionScheduleUpdateRequest
	if r.Body == nil || r.Body == http.NoBody {
		return req, errors.New("request body is required")
	}
	defer r.Body.Close()
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024))
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

func sessionSchedulesPath(pool *SessionPool, sessionID string) string {
	return filepath.Join(pool.sessionDirPath(sessionID), sessionSchedulesFileName)
}

func readSessionSchedulesFile(path string) (sessionSchedulesFile, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionSchedulesFile{}, false, nil
		}
		return sessionSchedulesFile{}, false, err
	}
	if info.IsDir() {
		return sessionSchedulesFile{}, false, errors.New("schedules path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return sessionSchedulesFile{}, false, errors.New("schedules path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sessionSchedulesFile{}, false, nil
		}
		return sessionSchedulesFile{}, false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxSessionSchedulesFileBytes+1))
	if err != nil {
		return sessionSchedulesFile{}, false, err
	}
	if len(raw) > maxSessionSchedulesFileBytes {
		return sessionSchedulesFile{}, false, fmt.Errorf("schedules file exceeds %d bytes", maxSessionSchedulesFileBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return sessionSchedulesFile{}, false, nil
	}
	var file sessionSchedulesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return sessionSchedulesFile{}, false, err
	}
	if file.Version == 0 {
		file.Version = 1
	}
	if len(file.Schedules) > maxSessionSchedules {
		return sessionSchedulesFile{}, false, fmt.Errorf("schedules file exceeds %d schedules", maxSessionSchedules)
	}
	for i := range file.Schedules {
		if err := normalizeSessionSchedule(&file.Schedules[i]); err != nil {
			return sessionSchedulesFile{}, false, err
		}
	}
	return file, true, nil
}

func writeSessionSchedulesFile(path string, file sessionSchedulesFile) error {
	if file.Version == 0 {
		file.Version = 1
	}
	if len(file.Schedules) > maxSessionSchedules {
		return fmt.Errorf("schedules file exceeds %d schedules", maxSessionSchedules)
	}
	for i := range file.Schedules {
		if err := normalizeSessionSchedule(&file.Schedules[i]); err != nil {
			return err
		}
	}
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	if len(raw) > maxSessionSchedulesFileBytes {
		return fmt.Errorf("schedules file exceeds %d bytes", maxSessionSchedulesFileBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("schedules path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("schedules path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".schedules-*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(sessionSchedulesFileMode); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func summarizeSessionSchedulesFile(pool *SessionPool, sessionID string) *sessionSchedulesSummary {
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, sessionID))
	if err != nil || !found {
		return nil
	}
	summary := summarizeSessionSchedulesForSession(pool, sessionID, file.Schedules)
	if summary.Count == 0 {
		return nil
	}
	return summary
}

func summarizeSessionSchedulesFileForDir(sessionDir, sessionID string) *sessionSchedulesSummary {
	if strings.TrimSpace(sessionDir) == "" || sessionDir == "." || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	file, found, err := readSessionSchedulesFile(filepath.Join(sessionDir, sessionSchedulesFileName))
	if err != nil || !found {
		return nil
	}
	summary := summarizeSessionSchedulesForDir(sessionDir, sessionID, file.Schedules)
	if summary.Count == 0 {
		return nil
	}
	return summary
}

func summarizeSessionSchedules(schedules []sessionSchedule) *sessionSchedulesSummary {
	return summarizeSessionSchedulesWithLoopState(schedules, true)
}

func summarizeSessionSchedulesForSession(pool *SessionPool, sessionID string, schedules []sessionSchedule) *sessionSchedulesSummary {
	return summarizeSessionSchedulesWithLoopState(schedules, true)
}

func summarizeSessionSchedulesForDir(sessionDir, sessionID string, schedules []sessionSchedule) *sessionSchedulesSummary {
	return summarizeSessionSchedulesWithLoopState(schedules, true)
}

func summarizeSessionSchedulesWithLoopState(schedules []sessionSchedule, loopProtocolRunning bool) *sessionSchedulesSummary {
	summary := &sessionSchedulesSummary{Count: len(schedules)}
	var next *sessionSchedule
	var latestError *sessionSchedule
	for i := range schedules {
		if strings.TrimSpace(schedules[i].LastError) != "" {
			summary.ErrorCount++
			if latestError == nil || scheduleTimeBefore(latestError.UpdatedAt, schedules[i].UpdatedAt) {
				latestError = &schedules[i]
			}
		}
		if schedules[i].Enabled {
			summary.Enabled++
			if schedules[i].Kind == sessionScheduleKindLoopTick {
				summary.EnabledLoopTicks++
			}
			if next == nil || scheduleTimeBefore(schedules[i].NextRunAt, next.NextRunAt) {
				next = &schedules[i]
			}
		}
	}
	if next != nil {
		summary.NextRunAt = next.NextRunAt
		summary.NextScheduleID = next.ID
		summary.NextScheduleKind = next.Kind
		summary.NextPromptPreview = previewSessionSchedulePrompt(sessionScheduleDisplayText(*next))
	}
	if latestError != nil {
		summary.LastError = latestError.LastError
	}
	return summary
}

func sortedSessionSchedules(schedules []sessionSchedule) []sessionSchedule {
	out := append([]sessionSchedule(nil), schedules...)
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Enabled != out[j].Enabled {
			return out[i].Enabled
		}
		if out[i].NextRunAt != out[j].NextRunAt {
			return scheduleTimeBefore(out[i].NextRunAt, out[j].NextRunAt)
		}
		return out[i].ID < out[j].ID
	})
	return out
}

func scheduleTimeBefore(a, b string) bool {
	ta, errA := time.Parse(time.RFC3339, a)
	tb, errB := time.Parse(time.RFC3339, b)
	if errA == nil && errB == nil {
		return ta.Before(tb)
	}
	return a < b
}

func normalizeSessionSchedule(schedule *sessionSchedule) error {
	if schedule == nil {
		return errors.New("schedule is nil")
	}
	kind, err := normalizeSessionScheduleKind(schedule.Kind)
	if err != nil {
		return err
	}
	schedule.Kind = kind
	if err := validateSessionScheduleID(schedule.ID); err != nil {
		return err
	}
	if strings.TrimSpace(schedule.Prompt) == "" {
		return errors.New("schedule prompt is required")
	}
	if len([]byte(schedule.Prompt)) > maxSessionSchedulePrompt {
		return fmt.Errorf("schedule prompt exceeds %d bytes", maxSessionSchedulePrompt)
	}
	schedule.DisplayText = strings.Join(strings.Fields(schedule.DisplayText), " ")
	if len([]byte(schedule.DisplayText)) > maxSessionScheduleDisplay {
		return fmt.Errorf("schedule display_text exceeds %d bytes", maxSessionScheduleDisplay)
	}
	if _, err := parseSessionScheduleTime(schedule.NextRunAt); err != nil {
		return fmt.Errorf("invalid next_run_at: %w", err)
	}
	if _, err := parseSessionScheduleTime(schedule.CreatedAt); err != nil {
		return fmt.Errorf("invalid created_at: %w", err)
	}
	if _, err := parseSessionScheduleTime(schedule.UpdatedAt); err != nil {
		return fmt.Errorf("invalid updated_at: %w", err)
	}
	if schedule.RepeatIntervalSeconds < 0 {
		return errors.New("repeat_interval_seconds must be non-negative")
	}
	if schedule.RepeatIntervalSeconds > 0 && time.Duration(schedule.RepeatIntervalSeconds)*time.Second < minSessionScheduleRepeat {
		return fmt.Errorf("repeat_interval_seconds must be at least %d", int(minSessionScheduleRepeat.Seconds()))
	}
	return nil
}

func normalizeSessionScheduleKind(kind string) (string, error) {
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return sessionScheduleKindCustom, nil
	}
	switch kind {
	case sessionScheduleKindCustom, sessionScheduleKindCheckIn, sessionScheduleKindDailyCheckIn, sessionScheduleKindLoopTick:
		return kind, nil
	default:
		return "", fmt.Errorf("schedule kind must be one of %s, %s, %s, or %s", sessionScheduleKindCustom, sessionScheduleKindCheckIn, sessionScheduleKindDailyCheckIn, sessionScheduleKindLoopTick)
	}
}

func validateSessionScheduleID(id string) error {
	if id == "" {
		return errors.New("schedule id is required")
	}
	if len(id) > 64 {
		return errors.New("schedule id is too long")
	}
	for _, r := range id {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			continue
		}
		return errors.New("schedule id must contain only letters, digits, underscore, or hyphen")
	}
	return nil
}

func parseSessionScheduleTime(value string) (time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return time.Time{}, errors.New("timestamp is required")
	}
	t, err := time.Parse(time.RFC3339, strings.TrimSpace(value))
	if err != nil {
		return time.Time{}, err
	}
	return t.UTC(), nil
}

func newSessionScheduleID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "sched_" + hex.EncodeToString([]byte(time.Now().UTC().Format("20060102150405.000000000")))
	}
	return "sched_" + hex.EncodeToString(b[:])
}

func previewSessionSchedulePrompt(prompt string) string {
	preview := strings.Join(strings.Fields(prompt), " ")
	const max = 120
	if len([]rune(preview)) <= max {
		return preview
	}
	runes := []rune(preview)
	return string(runes[:max]) + "..."
}

func sessionScheduleDisplayText(schedule sessionSchedule) string {
	if display := strings.TrimSpace(schedule.DisplayText); display != "" {
		return display
	}
	return schedule.Prompt
}

func (p *SessionPool) scheduleLoop() {
	defer close(p.scheduleDone)
	t := time.NewTicker(sessionScheduleSweepInterval)
	defer t.Stop()
	for {
		select {
		case <-p.scheduleStop:
			return
		case <-t.C:
			p.runDueSessionSchedulesOnce(time.Now().UTC())
		}
	}
}

func (p *SessionPool) runDueSessionSchedulesOnce(now time.Time) int {
	if p == nil || p.IsShuttingDown() {
		return 0
	}
	sessionIDs, err := p.sessionIDsWithSchedules()
	if err != nil {
		p.logger.Warn().Err(err).Msg("session schedule scan")
		return 0
	}
	runs := 0
	for _, sessionID := range sessionIDs {
		if p.IsShuttingDown() {
			return runs
		}
		if s := activeSessionByID(p, sessionID); s != nil && s.isActiveTurn() {
			continue
		}
		run, ok, err := p.claimNextDueSessionSchedule(sessionID, now)
		if err != nil {
			p.logger.Warn().Err(err).Str("session_id", sessionID).Msg("claim session schedule")
			continue
		}
		if !ok {
			continue
		}
		if err := p.executeClaimedSessionSchedule(now, run); err != nil {
			p.logger.Warn().Err(err).Str("session_id", run.SessionID).Str("schedule_id", run.ScheduleID).Msg("execute session schedule")
			continue
		}
		runs++
	}
	return runs
}

func (p *SessionPool) sessionIDsWithSchedules() ([]string, error) {
	entries, err := os.ReadDir(p.sessionRootPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	ids := make([]string, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		id := entry.Name()
		if err := agent.ValidateSessionID(id); err != nil {
			continue
		}
		if _, err := os.Lstat(filepath.Join(p.sessionDirPath(id), sessionSchedulesFileName)); err == nil {
			ids = append(ids, id)
		} else if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}
	sort.Strings(ids)
	return ids, nil
}

func (p *SessionPool) claimNextDueSessionSchedule(sessionID string, now time.Time) (sessionScheduleRun, bool, error) {
	p.schedulesMu.Lock()
	defer p.schedulesMu.Unlock()
	path := sessionSchedulesPath(p, sessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil || !found {
		return sessionScheduleRun{}, false, err
	}
	for i := range file.Schedules {
		schedule := &file.Schedules[i]
		if !schedule.Enabled || !sessionScheduleDue(*schedule, now) {
			continue
		}
		return sessionScheduleRun{
			SessionID:    sessionID,
			ScheduleID:   schedule.ID,
			ScheduleKind: schedule.Kind,
			Prompt:       schedule.Prompt,
			DisplayText:  strings.TrimSpace(schedule.DisplayText),
			ClaimedAt:    now.UTC(),
		}, true, nil
	}
	return sessionScheduleRun{}, false, nil
}

func sessionLoopProtocolRunning(pool *SessionPool, sessionID string) bool {
	if pool == nil {
		return false
	}
	return sessionLoopProtocolRunningAtPath(sessionLoopProtocolPath(pool, sessionID))
}

func sessionLoopProtocolRunningForDir(sessionDir, sessionID string) bool {
	if strings.TrimSpace(sessionDir) == "" || sessionDir == "." || strings.TrimSpace(sessionID) == "" {
		return false
	}
	return sessionLoopProtocolRunningAtPath(loopstate.ProtocolPath(sessionDir, sessionID))
}

func sessionLoopProtocolRunningAtPath(protocolPath string) bool {
	if loopstate.ProtocolStatusFromFile(protocolPath) != "running" {
		return false
	}
	state, found, err := loopstate.ReadState(filepath.Join(filepath.Dir(protocolPath), loopstate.StateFileName))
	if err != nil || !found {
		return true
	}
	status := strings.TrimSpace(strings.ToLower(state.Status))
	return status == "" || status == "running"
}

func (p *SessionPool) executeClaimedSessionSchedule(now time.Time, run sessionScheduleRun) error {
	sess, err := p.GetOrCreate(run.SessionID)
	if err != nil {
		_ = p.recordSessionScheduleFailure(run, now, err)
		return err
	}
	p.rememberSessionScheduleClaim(run)
	turnID, err := sess.SendUserWithOptions(context.Background(), run.Prompt, agent.TurnOptions{
		UserSource:      "schedule",
		UserDisplayText: run.DisplayText,
		ScheduleID:      run.ScheduleID,
		ScheduleKind:    run.ScheduleKind,
	})
	if err != nil {
		p.forgetSessionScheduleClaim(run.SessionID, run.ScheduleID)
		_ = p.recordSessionScheduleFailure(run, now, err)
		return err
	}
	p.logger.Info().Str("session_id", run.SessionID).Str("schedule_id", run.ScheduleID).Str("turn_id", turnID).Msg("session schedule fired")
	return nil
}

func (s *Session) observeScheduledTurn(ev sse.Event) {
	if s == nil || s.pool == nil {
		return
	}
	var payload sse.UserMessagePayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		s.pool.logger.Warn().Err(err).Str("session_id", s.ID).Msg("decode scheduled user message")
		return
	}
	if payload.Source != "schedule" || strings.TrimSpace(payload.ScheduleID) == "" || strings.TrimSpace(payload.TurnID) == "" {
		return
	}
	run := sessionScheduleRun{
		SessionID:    s.ID,
		ScheduleID:   payload.ScheduleID,
		ScheduleKind: payload.ScheduleKind,
		Prompt:       payload.Text,
		DisplayText:  payload.DisplayText,
		ClaimedAt:    s.pool.sessionScheduleClaimedAt(s.ID, payload.ScheduleID),
	}
	s.scheduleTurnsMu.Lock()
	s.scheduleTurns[payload.TurnID] = run
	s.scheduleTurnsMu.Unlock()
}

func (s *Session) completeScheduledTurn(ev sse.Event) {
	if s == nil || s.pool == nil {
		return
	}
	var payload sse.TurnEndPayload
	if err := json.Unmarshal(ev.Data, &payload); err != nil {
		s.pool.logger.Warn().Err(err).Str("session_id", s.ID).Msg("decode scheduled turn end")
		return
	}
	if strings.TrimSpace(payload.TurnID) == "" {
		return
	}
	s.scheduleTurnsMu.Lock()
	run, ok := s.scheduleTurns[payload.TurnID]
	if ok {
		delete(s.scheduleTurns, payload.TurnID)
	}
	s.scheduleTurnsMu.Unlock()
	if !ok {
		return
	}
	s.pool.forgetSessionScheduleClaim(run.SessionID, run.ScheduleID)
	if payload.Reason == sse.TurnEndCompleted {
		if err := s.pool.recordSessionScheduleSuccess(run, time.Now().UTC(), payload.TurnID); err != nil {
			s.pool.logger.Warn().Err(err).Str("session_id", run.SessionID).Str("schedule_id", run.ScheduleID).Msg("record session schedule success")
		}
		return
	}
	reason := strings.TrimSpace(payload.Reason)
	if reason == "" {
		reason = sse.TurnEndError
	}
	if err := s.pool.recordSessionScheduleFailure(run, time.Now().UTC(), fmt.Errorf("scheduled turn ended with %s", reason)); err != nil {
		s.pool.logger.Warn().Err(err).Str("session_id", run.SessionID).Str("schedule_id", run.ScheduleID).Msg("record session schedule failure")
	}
}

func (p *SessionPool) recordSessionScheduleSuccess(run sessionScheduleRun, now time.Time, turnID string) error {
	p.schedulesMu.Lock()
	defer p.schedulesMu.Unlock()
	path := sessionSchedulesPath(p, run.SessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil || !found {
		return err
	}
	nowStr := now.UTC().Format(time.RFC3339)
	for i := range file.Schedules {
		schedule := &file.Schedules[i]
		if schedule.ID != run.ScheduleID {
			continue
		}
		advanceAt := now
		if !run.ClaimedAt.IsZero() {
			advanceAt = run.ClaimedAt
		}
		if schedule.RepeatIntervalSeconds > 0 {
			schedule.NextRunAt = nextSessionScheduleRunAt(schedule.NextRunAt, schedule.RepeatIntervalSeconds, advanceAt).Format(time.RFC3339)
			schedule.Enabled = true
		} else {
			schedule.Enabled = false
		}
		schedule.LastRunAt = nowStr
		schedule.LastTurnID = turnID
		schedule.LastError = ""
		schedule.RunCount++
		schedule.UpdatedAt = nowStr
		return writeSessionSchedulesFile(path, file)
	}
	return nil
}

func (p *SessionPool) rememberSessionScheduleClaim(run sessionScheduleRun) {
	if p == nil || run.ClaimedAt.IsZero() {
		return
	}
	p.scheduleClaimsMu.Lock()
	p.scheduleClaims[sessionScheduleClaimKey(run.SessionID, run.ScheduleID)] = run.ClaimedAt.UTC()
	p.scheduleClaimsMu.Unlock()
}

func (p *SessionPool) sessionScheduleClaimedAt(sessionID, scheduleID string) time.Time {
	if p == nil {
		return time.Time{}
	}
	p.scheduleClaimsMu.Lock()
	claimedAt := p.scheduleClaims[sessionScheduleClaimKey(sessionID, scheduleID)]
	p.scheduleClaimsMu.Unlock()
	return claimedAt
}

func (p *SessionPool) forgetSessionScheduleClaim(sessionID, scheduleID string) {
	if p == nil {
		return
	}
	p.scheduleClaimsMu.Lock()
	delete(p.scheduleClaims, sessionScheduleClaimKey(sessionID, scheduleID))
	p.scheduleClaimsMu.Unlock()
}

func sessionScheduleClaimKey(sessionID, scheduleID string) string {
	return sessionID + "\x00" + scheduleID
}

func (p *SessionPool) recordSessionScheduleFailure(run sessionScheduleRun, now time.Time, cause error) error {
	p.schedulesMu.Lock()
	defer p.schedulesMu.Unlock()
	path := sessionSchedulesPath(p, run.SessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil || !found {
		return err
	}
	nowStr := now.UTC().Format(time.RFC3339)
	retryAt := now.UTC().Add(sessionScheduleRetryDelay).Format(time.RFC3339)
	for i := range file.Schedules {
		schedule := &file.Schedules[i]
		if schedule.ID != run.ScheduleID {
			continue
		}
		schedule.Enabled = true
		schedule.NextRunAt = retryAt
		schedule.LastError = previewSessionScheduleError(cause)
		schedule.UpdatedAt = nowStr
		return writeSessionSchedulesFile(path, file)
	}
	return nil
}

func sessionScheduleDue(schedule sessionSchedule, now time.Time) bool {
	next, err := parseSessionScheduleTime(schedule.NextRunAt)
	if err != nil {
		return false
	}
	return !next.After(now.UTC())
}

func nextSessionScheduleRunAt(current string, repeatSeconds int64, now time.Time) time.Time {
	interval := time.Duration(repeatSeconds) * time.Second
	if interval < minSessionScheduleRepeat {
		interval = minSessionScheduleRepeat
	}
	next, err := parseSessionScheduleTime(current)
	if err != nil {
		return now.UTC().Add(interval)
	}
	next = next.Add(interval)
	if next.After(now.UTC()) {
		return next.UTC()
	}
	missed := int64(now.UTC().Sub(next)/interval) + 1
	return next.Add(time.Duration(missed) * interval).UTC()
}

func previewSessionScheduleError(err error) string {
	if err == nil {
		return ""
	}
	preview := strings.Join(strings.Fields(err.Error()), " ")
	if len([]rune(preview)) <= maxSessionScheduleErrorChars {
		return preview
	}
	runes := []rune(preview)
	return string(runes[:maxSessionScheduleErrorChars]) + "..."
}
