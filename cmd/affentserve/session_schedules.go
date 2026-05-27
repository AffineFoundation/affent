package main

import (
	"bytes"
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
)

const (
	sessionSchedulesFileName     = "schedules.json"
	maxSessionSchedulesFileBytes = 64 * 1024
	maxSessionSchedules          = 128
	maxSessionSchedulePrompt     = 8000
	minSessionScheduleRepeat     = 60 * time.Second
)

type sessionSchedule struct {
	ID                    string `json:"id"`
	Prompt                string `json:"prompt"`
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
	NextRunAt         string `json:"next_run_at,omitempty"`
	NextScheduleID    string `json:"next_schedule_id,omitempty"`
	NextPromptPreview string `json:"next_prompt_preview,omitempty"`
}

type sessionSchedulesResponse struct {
	SessionID string                   `json:"session_id"`
	Schedules []sessionSchedule        `json:"schedules"`
	Summary   *sessionSchedulesSummary `json:"summary,omitempty"`
}

type sessionScheduleCreateRequest struct {
	Prompt                string `json:"prompt"`
	NextRunAt             string `json:"next_run_at"`
	RepeatIntervalSeconds int64  `json:"repeat_interval_seconds,omitempty"`
	Enabled               *bool  `json:"enabled,omitempty"`
}

type sessionScheduleDeleteResponse struct {
	SessionID  string                   `json:"session_id"`
	ScheduleID string                   `json:"schedule_id"`
	Cleared    bool                     `json:"cleared"`
	Summary    *sessionSchedulesSummary `json:"summary,omitempty"`
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
	summary := summarizeSessionSchedules(file.Schedules)
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
		Summary:   summarizeSessionSchedules(schedules),
	})
}

func createSessionSchedule(pool *SessionPool, sessionID string, w http.ResponseWriter, r *http.Request) {
	req, err := decodeSessionScheduleCreateRequest(w, r)
	if err != nil {
		writeJSONErrorTyped(w, http.StatusBadRequest, "invalid schedule request", err, "bad_request")
		return
	}
	prompt := strings.TrimSpace(req.Prompt)
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
		Prompt:                prompt,
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
		Summary:   summarizeSessionSchedules(schedules),
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
	for _, schedule := range file.Schedules {
		if err := validateSessionSchedule(schedule); err != nil {
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
	for _, schedule := range file.Schedules {
		if err := validateSessionSchedule(schedule); err != nil {
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
	summary := summarizeSessionSchedules(file.Schedules)
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
	summary := summarizeSessionSchedules(file.Schedules)
	if summary.Count == 0 {
		return nil
	}
	return summary
}

func summarizeSessionSchedules(schedules []sessionSchedule) *sessionSchedulesSummary {
	summary := &sessionSchedulesSummary{Count: len(schedules)}
	var next *sessionSchedule
	for i := range schedules {
		if schedules[i].Enabled {
			summary.Enabled++
			if next == nil || scheduleTimeBefore(schedules[i].NextRunAt, next.NextRunAt) {
				next = &schedules[i]
			}
		}
	}
	if next != nil {
		summary.NextRunAt = next.NextRunAt
		summary.NextScheduleID = next.ID
		summary.NextPromptPreview = previewSessionSchedulePrompt(next.Prompt)
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

func validateSessionSchedule(schedule sessionSchedule) error {
	if err := validateSessionScheduleID(schedule.ID); err != nil {
		return err
	}
	if strings.TrimSpace(schedule.Prompt) == "" {
		return errors.New("schedule prompt is required")
	}
	if len([]byte(schedule.Prompt)) > maxSessionSchedulePrompt {
		return fmt.Errorf("schedule prompt exceeds %d bytes", maxSessionSchedulePrompt)
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
