package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
)

const (
	sessionScheduleToolName      = agent.SessionScheduleToolName
	maxSessionScheduleActionSize = 16
)

type sessionScheduleToolArgs struct {
	Action                string `json:"action"`
	ScheduleID            string `json:"schedule_id"`
	Kind                  string `json:"kind"`
	Prompt                string `json:"prompt"`
	DisplayText           string `json:"display_text"`
	NextRunAt             string `json:"next_run_at"`
	RepeatIntervalSeconds int64  `json:"repeat_interval_seconds"`
	Enabled               *bool  `json:"enabled"`
}

func registerSessionScheduleTool(r *agent.Registry, pool *SessionPool, sessionID string) {
	if r == nil || pool == nil || strings.TrimSpace(sessionID) == "" {
		return
	}
	schema := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["action"],
		"properties": {
			"action": {"type": "string", "enum": ["list", "create", "update", "delete"], "description": "Use create for a future or recurring scheduled session turn. Use list/update/delete to inspect, pause/resume, or remove saved schedules."},
			"schedule_id": {"type": "string", "maxLength": 64, "description": "Required for update and delete."},
			"kind": {"type": "string", "enum": ["custom", "checkin", "daily_checkin", "loop_tick"], "description": "Schedule category. Timers do not require LOOP.md. Use loop_tick only when the scheduled turn is meant to nudge an already-running loop protocol; otherwise use custom/checkin/daily_checkin."},
			"prompt": {"type": "string", "maxLength": 8000, "description": "Exact user message to inject when the schedule fires. Required for create."},
			"display_text": {"type": "string", "maxLength": 512, "description": "Optional compact label shown in history instead of the full prompt."},
			"next_run_at": {"type": "string", "description": "RFC3339 UTC timestamp for the next run. Required for create."},
			"repeat_interval_seconds": {"type": "integer", "minimum": 0, "description": "0 for one-shot, or a repeat interval of at least 60 seconds."},
			"enabled": {"type": "boolean", "description": "Create enabled by default. Required for update to pause or resume."}
		}
	}`)
	r.Add(&agent.Tool{
		Name:         sessionScheduleToolName,
		Description:  "Create, list, pause/resume, or delete session scheduled turns. This is the correct runtime tool for timers, reminders, recurring checks, and future follow-ups. It writes schedules.json and does not create or require LOOP.md; use loop_protocol only for durable long-running task state.",
		Schema:       schema,
		CatalogGroup: "Core",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return executeSessionScheduleTool(ctx, pool, sessionID, args)
		},
	})
}

func executeSessionScheduleTool(ctx context.Context, pool *SessionPool, sessionID string, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	p, err := decodeSessionScheduleToolArgs(args)
	if err != nil {
		return "", err
	}
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if action == "" {
		return "", errors.New("action is required\nNext: retry session_schedule with action=list or action=create")
	}
	if len(action) > maxSessionScheduleActionSize {
		return "", fmt.Errorf("action is %d bytes; session_schedule action supports up to %d bytes", len(action), maxSessionScheduleActionSize)
	}
	switch action {
	case "list":
		return sessionScheduleToolList(pool, sessionID)
	case "create":
		return sessionScheduleToolCreate(pool, sessionID, p)
	case "update":
		return sessionScheduleToolUpdate(pool, sessionID, p)
	case "delete":
		return sessionScheduleToolDelete(pool, sessionID, p)
	default:
		return "", fmt.Errorf("unsupported action %q (valid: list, create, update, delete)", action)
	}
}

func decodeSessionScheduleToolArgs(args json.RawMessage) (sessionScheduleToolArgs, error) {
	var p sessionScheduleToolArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&p); err != nil {
		return p, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return p, errors.New("arguments must contain a single JSON object")
	}
	return p, nil
}

func sessionScheduleToolList(pool *SessionPool, sessionID string) (string, error) {
	file, found, err := readSessionSchedulesFile(sessionSchedulesPath(pool, sessionID))
	if err != nil {
		return "", err
	}
	schedules := []sessionSchedule{}
	if found {
		schedules = sortedSessionSchedules(file.Schedules)
	}
	return marshalSessionScheduleToolResponse(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func sessionScheduleToolCreate(pool *SessionPool, sessionID string, p sessionScheduleToolArgs) (string, error) {
	prompt := strings.TrimSpace(p.Prompt)
	displayText := strings.TrimSpace(p.DisplayText)
	if prompt == "" {
		return "", errors.New("prompt is required for create")
	}
	if len([]byte(prompt)) > maxSessionSchedulePrompt {
		return "", fmt.Errorf("prompt exceeds %d bytes", maxSessionSchedulePrompt)
	}
	if len([]byte(displayText)) > maxSessionScheduleDisplay {
		return "", fmt.Errorf("display_text exceeds %d bytes", maxSessionScheduleDisplay)
	}
	nextRunAt, err := parseSessionScheduleTime(p.NextRunAt)
	if err != nil {
		return "", fmt.Errorf("invalid next_run_at: %w", err)
	}
	kind, err := normalizeSessionScheduleKind(p.Kind)
	if err != nil {
		return "", err
	}
	if err := validateSessionScheduleKindForSession(pool, sessionID, kind); err != nil {
		return "", err
	}
	if p.RepeatIntervalSeconds < 0 {
		return "", errors.New("repeat_interval_seconds must be non-negative")
	}
	if p.RepeatIntervalSeconds > 0 && time.Duration(p.RepeatIntervalSeconds)*time.Second < minSessionScheduleRepeat {
		return "", fmt.Errorf("repeat_interval_seconds must be at least %d", int(minSessionScheduleRepeat.Seconds()))
	}
	if _, err := pool.allocSessionDir(sessionID); err != nil {
		return "", err
	}
	pool.schedulesMu.Lock()
	defer pool.schedulesMu.Unlock()
	path := sessionSchedulesPath(pool, sessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil {
		return "", err
	}
	if !found {
		file = sessionSchedulesFile{Version: 1}
	}
	if len(file.Schedules) >= maxSessionSchedules {
		return "", fmt.Errorf("session has the maximum %d schedules", maxSessionSchedules)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	enabled := true
	if p.Enabled != nil {
		enabled = *p.Enabled
	}
	file.Schedules = append(file.Schedules, sessionSchedule{
		ID:                    newSessionScheduleID(),
		Kind:                  kind,
		Prompt:                prompt,
		DisplayText:           displayText,
		Enabled:               enabled,
		NextRunAt:             nextRunAt.Format(time.RFC3339),
		RepeatIntervalSeconds: p.RepeatIntervalSeconds,
		CreatedAt:             now,
		UpdatedAt:             now,
	})
	if err := writeSessionSchedulesFile(path, file); err != nil {
		return "", err
	}
	schedules := sortedSessionSchedules(file.Schedules)
	return marshalSessionScheduleToolResponse(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func sessionScheduleToolUpdate(pool *SessionPool, sessionID string, p sessionScheduleToolArgs) (string, error) {
	if p.Enabled == nil {
		return "", errors.New("enabled is required for update")
	}
	if err := validateSessionScheduleID(p.ScheduleID); err != nil {
		return "", err
	}
	pool.schedulesMu.Lock()
	defer pool.schedulesMu.Unlock()
	path := sessionSchedulesPath(pool, sessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("session schedules not found")
	}
	updated := false
	now := time.Now().UTC().Format(time.RFC3339)
	for i := range file.Schedules {
		if file.Schedules[i].ID != p.ScheduleID {
			continue
		}
		if *p.Enabled {
			if err := validateSessionScheduleKindForSession(pool, sessionID, file.Schedules[i].Kind); err != nil {
				return "", err
			}
		}
		file.Schedules[i].Enabled = *p.Enabled
		file.Schedules[i].UpdatedAt = now
		file.Schedules[i].LastError = ""
		file.Schedules[i].LastErrorKind = ""
		updated = true
		break
	}
	if !updated {
		return "", errors.New("session schedule not found")
	}
	if err := writeSessionSchedulesFile(path, file); err != nil {
		return "", err
	}
	schedules := sortedSessionSchedules(file.Schedules)
	return marshalSessionScheduleToolResponse(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func sessionScheduleToolDelete(pool *SessionPool, sessionID string, p sessionScheduleToolArgs) (string, error) {
	if err := validateSessionScheduleID(p.ScheduleID); err != nil {
		return "", err
	}
	pool.schedulesMu.Lock()
	defer pool.schedulesMu.Unlock()
	path := sessionSchedulesPath(pool, sessionID)
	file, found, err := readSessionSchedulesFile(path)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("session schedules not found")
	}
	next := file.Schedules[:0]
	deleted := false
	for _, schedule := range file.Schedules {
		if schedule.ID == p.ScheduleID {
			deleted = true
			continue
		}
		next = append(next, schedule)
	}
	if !deleted {
		return "", errors.New("session schedule not found")
	}
	file.Schedules = next
	if err := writeSessionSchedulesFile(path, file); err != nil {
		return "", err
	}
	schedules := sortedSessionSchedules(file.Schedules)
	return marshalSessionScheduleToolResponse(sessionSchedulesResponse{
		SessionID: sessionID,
		Schedules: schedules,
		Summary:   summarizeSessionSchedulesForSession(pool, sessionID, schedules),
	})
}

func marshalSessionScheduleToolResponse(resp sessionSchedulesResponse) (string, error) {
	raw, err := json.MarshalIndent(resp, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}
