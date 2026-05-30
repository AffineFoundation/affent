package agenteval

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/sse"
)

const evalSessionSchedulesRelPath = ".affent/schedules.json"
const evalSessionScheduleLoopTickUnavailableFailureKind = "session_schedule_loop_tick_unavailable"

type evalSessionScheduleToolArgs struct {
	Action                string `json:"action"`
	Kind                  string `json:"kind"`
	Prompt                string `json:"prompt"`
	DisplayText           string `json:"display_text"`
	NextRunAt             string `json:"next_run_at"`
	RepeatIntervalSeconds int64  `json:"repeat_interval_seconds"`
	Enabled               *bool  `json:"enabled"`
}

type evalSessionSchedulesFile struct {
	Version   int                   `json:"version"`
	Schedules []evalSessionSchedule `json:"schedules"`
}

type evalSessionSchedule struct {
	ID                    string `json:"id"`
	Kind                  string `json:"kind"`
	Prompt                string `json:"prompt"`
	DisplayText           string `json:"display_text,omitempty"`
	Enabled               bool   `json:"enabled"`
	NextRunAt             string `json:"next_run_at"`
	RepeatIntervalSeconds int64  `json:"repeat_interval_seconds,omitempty"`
	CreatedAt             string `json:"created_at"`
	UpdatedAt             string `json:"updated_at"`
}

type evalSessionScheduleResponse struct {
	SessionID string                   `json:"session_id"`
	Schedules []evalSessionSchedule    `json:"schedules"`
	Summary   evalSessionScheduleStats `json:"summary"`
}

type evalSessionScheduleStats struct {
	Count             int    `json:"count"`
	Enabled           int    `json:"enabled"`
	NextRunAt         string `json:"next_run_at,omitempty"`
	NextScheduleID    string `json:"next_schedule_id,omitempty"`
	NextScheduleKind  string `json:"next_schedule_kind,omitempty"`
	NextPromptPreview string `json:"next_prompt_preview,omitempty"`
}

func registerEvalSessionScheduleTool(reg *agent.Registry, workspaceDir string) {
	if reg == nil || strings.TrimSpace(workspaceDir) == "" {
		return
	}
	schema := json.RawMessage(`{
		"type": "object",
		"additionalProperties": false,
		"required": ["action"],
		"properties": {
			"action": {"type": "string", "enum": ["list", "create"], "description": "Use create for a future or recurring scheduled session turn. Use list to inspect saved schedules."},
			"kind": {"type": "string", "enum": ["custom", "checkin", "daily_checkin", "loop_tick"], "description": "Schedule category. Timers do not require LOOP.md. Use loop_tick only when the scheduled turn nudges an already-running loop protocol; otherwise use custom/checkin/daily_checkin."},
			"prompt": {"type": "string", "maxLength": 8000, "description": "Exact user message to inject when the schedule fires. Required for create."},
			"display_text": {"type": "string", "maxLength": 512, "description": "Optional compact label shown in history instead of the full prompt."},
			"next_run_at": {"type": "string", "description": "RFC3339 UTC timestamp for the next run. Required for create."},
			"repeat_interval_seconds": {"type": "integer", "minimum": 0, "description": "0 for one-shot, or a repeat interval of at least 60 seconds."},
			"enabled": {"type": "boolean", "description": "Create enabled by default."}
		}
	}`)
	reg.Add(&agent.Tool{
		Name:                  agent.SessionScheduleToolName,
		Description:           "Create or list eval session scheduled turns. This mirrors the serve runtime's timer semantics for eval scenarios: ordinary timers and recurring checks use session_schedule and do not require LOOP.md.",
		Schema:                schema,
		RuntimeSurfaceRefresh: evalSessionScheduleRuntimeSurfaceRefresh,
		CatalogGroup:          "Core",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			return executeEvalSessionScheduleTool(ctx, workspaceDir, args)
		},
	})
}

func evalSessionScheduleRuntimeSurfaceRefresh(args json.RawMessage, _ string, isErr bool) string {
	if isErr {
		return ""
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &req); err != nil {
		return ""
	}
	if strings.ToLower(strings.TrimSpace(req.Action)) == "create" {
		return sse.RuntimeSurfaceRefreshSchedulesChanged
	}
	return ""
}

func executeEvalSessionScheduleTool(ctx context.Context, workspaceDir string, args json.RawMessage) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	parsed, err := decodeEvalSessionScheduleToolArgs(args)
	if err != nil {
		return "", err
	}
	switch action := strings.ToLower(strings.TrimSpace(parsed.Action)); action {
	case "list":
		return evalSessionScheduleToolList(workspaceDir)
	case "create":
		return evalSessionScheduleToolCreate(workspaceDir, parsed)
	case "":
		return "", errors.New("action is required\nNext: retry session_schedule with action=list or action=create")
	default:
		return "", fmt.Errorf("unsupported action %q (valid: list, create)", action)
	}
}

func decodeEvalSessionScheduleToolArgs(args json.RawMessage) (evalSessionScheduleToolArgs, error) {
	var parsed evalSessionScheduleToolArgs
	dec := json.NewDecoder(bytes.NewReader(args))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&parsed); err != nil {
		return parsed, err
	}
	var extra struct{}
	if err := dec.Decode(&extra); !errors.Is(err, io.EOF) {
		return parsed, errors.New("arguments must contain a single JSON object")
	}
	return parsed, nil
}

func evalSessionScheduleToolList(workspaceDir string) (string, error) {
	file, err := readEvalSessionSchedulesFile(workspaceDir)
	if err != nil {
		return "", err
	}
	return marshalEvalSessionScheduleResponse(file)
}

func evalSessionScheduleToolCreate(workspaceDir string, parsed evalSessionScheduleToolArgs) (string, error) {
	prompt := strings.TrimSpace(parsed.Prompt)
	if prompt == "" {
		return "", errors.New("prompt is required for create\nNext: retry session_schedule action=create with the scheduled user message")
	}
	nextRun, err := time.Parse(time.RFC3339, strings.TrimSpace(parsed.NextRunAt))
	if err != nil {
		return "", fmt.Errorf("next_run_at must be RFC3339 UTC: %w", err)
	}
	if parsed.RepeatIntervalSeconds > 0 && parsed.RepeatIntervalSeconds < 60 {
		return "", errors.New("repeat_interval_seconds must be 0 or at least 60")
	}
	kind := strings.TrimSpace(parsed.Kind)
	if kind == "" {
		kind = "custom"
	}
	switch kind {
	case "custom", "checkin", "daily_checkin", "loop_tick":
	default:
		return "", fmt.Errorf("unsupported kind %q", kind)
	}
	if kind == "loop_tick" && !evalWorkspaceHasRunningLoopProtocol(workspaceDir) {
		return "", errors.New("loop_tick requires a running LOOP.md.\n" +
			"Next: activate the loop protocol before retrying loop_tick, or use custom/checkin/daily_checkin for ordinary eval timers.\n" +
			"Failure: kind=" + evalSessionScheduleLoopTickUnavailableFailureKind)
	}
	enabled := true
	if parsed.Enabled != nil {
		enabled = *parsed.Enabled
	}
	file, err := readEvalSessionSchedulesFile(workspaceDir)
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	file.Schedules = append(file.Schedules, evalSessionSchedule{
		ID:                    fmt.Sprintf("sched_eval_%03d", len(file.Schedules)+1),
		Kind:                  kind,
		Prompt:                prompt,
		DisplayText:           strings.TrimSpace(parsed.DisplayText),
		Enabled:               enabled,
		NextRunAt:             nextRun.UTC().Format(time.RFC3339),
		RepeatIntervalSeconds: parsed.RepeatIntervalSeconds,
		CreatedAt:             now,
		UpdatedAt:             now,
	})
	if err := writeEvalSessionSchedulesFile(workspaceDir, file); err != nil {
		return "", err
	}
	return marshalEvalSessionScheduleResponse(file)
}

func evalWorkspaceHasRunningLoopProtocol(workspaceDir string) bool {
	if strings.TrimSpace(workspaceDir) == "" {
		return false
	}
	paths, err := filepath.Glob(filepath.Join(workspaceDir, ".affent", "loops", "*", "LOOP.md"))
	if err != nil {
		return false
	}
	for _, path := range paths {
		if !evalLoopProtocolRunningAtPath(path) {
			continue
		}
		return true
	}
	return false
}

func evalLoopProtocolRunningAtPath(protocolPath string) bool {
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

func readEvalSessionSchedulesFile(workspaceDir string) (evalSessionSchedulesFile, error) {
	path := filepath.Join(workspaceDir, evalSessionSchedulesRelPath)
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return evalSessionSchedulesFile{Version: 1}, nil
	}
	if err != nil {
		return evalSessionSchedulesFile{}, err
	}
	var file evalSessionSchedulesFile
	if err := json.Unmarshal(raw, &file); err != nil {
		return evalSessionSchedulesFile{}, err
	}
	if file.Version == 0 {
		file.Version = 1
	}
	return file, nil
}

func writeEvalSessionSchedulesFile(workspaceDir string, file evalSessionSchedulesFile) error {
	path := filepath.Join(workspaceDir, evalSessionSchedulesRelPath)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	file.Version = 1
	sort.SliceStable(file.Schedules, func(i, j int) bool {
		return file.Schedules[i].NextRunAt < file.Schedules[j].NextRunAt
	})
	raw, err := json.MarshalIndent(file, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(raw, '\n'), 0o600)
}

func marshalEvalSessionScheduleResponse(file evalSessionSchedulesFile) (string, error) {
	sort.SliceStable(file.Schedules, func(i, j int) bool {
		return file.Schedules[i].NextRunAt < file.Schedules[j].NextRunAt
	})
	raw, err := json.MarshalIndent(evalSessionScheduleResponse{
		SessionID: "agenteval",
		Schedules: file.Schedules,
		Summary:   summarizeEvalSessionSchedules(file.Schedules),
	}, "", "  ")
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func summarizeEvalSessionSchedules(schedules []evalSessionSchedule) evalSessionScheduleStats {
	var stats evalSessionScheduleStats
	stats.Count = len(schedules)
	for _, schedule := range schedules {
		if !schedule.Enabled {
			continue
		}
		stats.Enabled++
		if stats.NextRunAt == "" || schedule.NextRunAt < stats.NextRunAt {
			stats.NextRunAt = schedule.NextRunAt
			stats.NextScheduleID = schedule.ID
			stats.NextScheduleKind = schedule.Kind
			stats.NextPromptPreview = previewEvalSchedulePrompt(schedule)
		}
	}
	return stats
}

func previewEvalSchedulePrompt(schedule evalSessionSchedule) string {
	if strings.TrimSpace(schedule.DisplayText) != "" {
		return strings.TrimSpace(schedule.DisplayText)
	}
	text := strings.TrimSpace(schedule.Prompt)
	if len([]rune(text)) <= 80 {
		return text
	}
	return string([]rune(text)[:80])
}
