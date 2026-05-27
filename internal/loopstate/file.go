package loopstate

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	ProtocolFileName = "LOOP.md"
	StateFileName    = "state.json"
	EventsFileName   = "events.jsonl"
	MaxProtocolBytes = 64 * 1024
)

type ProtocolTemplateOptions struct {
	LoopID       string
	OwnerSession string
	Goal         string
	Workspace    string
	CreatedAt    time.Time
}

type Summary struct {
	Path         string `json:"path,omitempty"`
	LoopID       string `json:"loop_id,omitempty"`
	OwnerSession string `json:"owner_session,omitempty"`
	Status       string `json:"status,omitempty"`
	UpdatedAt    string `json:"updated_at,omitempty"`
	Bytes        int    `json:"bytes"`
	Preview      string `json:"preview,omitempty"`
	State        *State `json:"state,omitempty"`
}

func ProtocolDir(sessionDir, loopID string) string {
	return filepath.Join(sessionDir, ".affent", "loops", loopID)
}

func ProtocolPath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), ProtocolFileName)
}

func StatePath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), StateFileName)
}

func EventsPath(sessionDir, loopID string) string {
	return filepath.Join(ProtocolDir(sessionDir, loopID), EventsFileName)
}

func ProtocolRelPath(loopID string) string {
	return filepath.ToSlash(filepath.Join(".affent", "loops", loopID, ProtocolFileName))
}

func DefaultProtocolTemplate(opts ProtocolTemplateOptions) string {
	loopID := strings.TrimSpace(opts.LoopID)
	if loopID == "" {
		loopID = "loop"
	}
	owner := strings.TrimSpace(opts.OwnerSession)
	if owner == "" {
		owner = loopID
	}
	createdAt := opts.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	goal := templateLine(opts.Goal)
	if goal == "" {
		goal = "Make steady, evidence-backed progress on the user's long-running objective without losing recovery context."
	}
	workspace := templateLine(opts.Workspace)
	if workspace == "" {
		workspace = "not recorded"
	}
	return strings.TrimSpace(`# Loop Protocol: ` + templateLine(loopID) + `

## 0. Metadata

- loop_id: ` + templateLine(loopID) + `
- owner_session: ` + templateLine(owner) + `
- status: running
- protocol_version: 1
- created_at: ` + formatTime(createdAt) + `
- updated_at: ` + formatTime(createdAt) + `
- workspace: ` + workspace + `

## 1. North Star

Long-term objective:

1. ` + goal + `
2. Prefer practical completion, low wasted tokens, reliable recovery, and cited evidence over broad but shallow exploration.
3. Keep the loop useful for smaller models: externalize durable state, avoid relying on hidden attention, and use tools only when they materially reduce uncertainty.

Do not:

1. Change the north star silently.
2. Duplicate authoritative task state here; plan/step state remains authoritative.
3. Continue a loop that is completed, unsafe, irrecoverably blocked, or no longer serving the user's objective.

## 2. Evolution Protocol

The model may maintain this file, but every update must be compact and justified.

1. Preserve the north star unless the user explicitly changes it.
2. Merge similar rules and remove stale rules that no longer trigger.
3. Move detailed history to trace, artifacts, memory, or plan state instead of growing this file.
4. If context is thin after compaction, reload this protocol, memory indexes, plan state, and recent trace pointers before guessing.
5. Record only durable lessons, recovery anchors, and decision rules that should survive many turns.

Latest protocol update:

- time: ` + formatTime(createdAt) + `
- change: initialized default loop protocol
- reason: loop protocol activation

## 3. Memory Index

Memory locations:

- user memory: shared user preferences and stable cross-session facts
- project memory: workspace-level durable facts and decisions
- loop memory: this file plus .affent/loops/<loop_id>/state.json and events.jsonl
- session trace: conversation and event JSONL for replay
- artifacts: durable outputs, tool-result payloads, reports, and evidence files

Check memory when:

1. Resuming after compaction, process restart, or a long delay.
2. The task depends on facts, preferences, prior decisions, or earlier evidence that are not visible in the current context.
3. A repeated failure suggests an old rule, source, or artifact may already explain the fix.

## 4. Self-Attack

Before continuing a long-running step, challenge the current direction:

1. What claim or action lacks evidence?
2. What has changed in the world or repository since the last checkpoint?
3. What repeated failure pattern should become a rule or a stop condition?
4. Is this loop still advancing the north star, or only consuming turns?
5. Should this continue in the current session, pause for user input, or hand off to a focused subtask?

## 5. Rules

Durable rules:

1. Prefer verified primary evidence for live web facts; rendered pages and network responses are often better than raw HTML on JS-heavy sites.
2. After editing code, run the narrowest meaningful tests first, then broaden when the blast radius justifies it.
3. If a tool result is blocked or low quality, change strategy before retrying the same failed input.

Candidate rules:

1. Promote only if the same failure recurs or the lesson applies across tasks.

## 6. Plan/Step Pointers

Authoritative task progress lives outside this file.

- current plan: session plan state if present
- active step: injected by the active-plan checkpoint when available
- completed steps: plan state and trace
- blocked steps: plan state and loop decisions
- related trace: session event log and .affent/loops/<loop_id>/events.jsonl

## 7. Evidence And Recovery Index

Keep this section short. Store detailed history in artifacts or trace.

- latest checkpoint: state.json
- recent loop events: events.jsonl
- important artifacts:
- important trace spans:
- last known recovery note:
`)
}

func EnsureProtocolTemplate(path string, opts ProtocolTemplateOptions) (bool, State, Event, error) {
	content, found, err := ReadProtocol(path)
	if err != nil {
		return false, State{}, Event{}, err
	}
	if found && strings.TrimSpace(content) != "" {
		state, stateFound, err := ReadState(filepath.Join(filepath.Dir(path), StateFileName))
		if err != nil {
			return false, State{}, Event{}, err
		}
		if stateFound {
			return false, state, Event{}, nil
		}
		return false, State{}, Event{}, nil
	}
	loopDir := filepath.Dir(path)
	loopID := strings.TrimSpace(opts.LoopID)
	if loopID == "" {
		loopID = filepath.Base(loopDir)
	}
	if opts.OwnerSession == "" {
		opts.OwnerSession = loopID
	}
	opts.LoopID = loopID
	now := opts.CreatedAt
	if now.IsZero() {
		now = time.Now().UTC()
	}
	opts.CreatedAt = now
	if err := WriteProtocol(path, DefaultProtocolTemplate(opts)); err != nil {
		return false, State{}, Event{}, err
	}
	event, err := AppendEvent(filepath.Join(loopDir, EventsFileName), Event{
		Type:    "loop.protocol_init",
		Summary: "Initialized LOOP.md",
		Reason:  "loop protocol activation",
		Path:    ProtocolRelPath(loopID),
		Time:    formatTime(now),
	})
	if err != nil {
		return false, State{}, Event{}, err
	}
	statePath := filepath.Join(loopDir, StateFileName)
	state, found, err := ReadState(statePath)
	if err != nil {
		return false, State{}, Event{}, err
	}
	state = normalizeStateForProtocol(state, found, loopID, now)
	state.OwnerSession = strings.TrimSpace(opts.OwnerSession)
	if state.OwnerSession == "" {
		state.OwnerSession = loopID
	}
	state.Status = "running"
	state.LastProtocolUpdateAt = event.Time
	state.ProtocolUpdates++
	state.UpdatedAt = event.Time
	state.EventCount = event.Seq
	state.LastEventType = event.Type
	state.LastEventSummary = event.Summary
	state.LastEventAt = event.Time
	if err := WriteState(statePath, state); err != nil {
		return false, State{}, Event{}, err
	}
	return true, state, event, nil
}

func ReadProtocol(path string) (string, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	if info.IsDir() {
		return "", false, errors.New("loop protocol path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return "", false, errors.New("loop protocol path must not be a symlink")
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "", false, nil
		}
		return "", false, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxProtocolBytes+1))
	if err != nil {
		return "", false, err
	}
	if len(raw) > MaxProtocolBytes {
		return "", false, fmt.Errorf("loop protocol file exceeds %d bytes", MaxProtocolBytes)
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return "", false, nil
	}
	return string(raw), true, nil
}

func WriteProtocol(path, content string) error {
	content = strings.TrimSpace(content)
	if content == "" {
		return errors.New("loop protocol content is required")
	}
	if len([]byte(content)) > MaxProtocolBytes {
		return fmt.Errorf("loop protocol file exceeds %d bytes", MaxProtocolBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("loop protocol path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("loop protocol path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	tmp := path + ".tmp"
	if err := os.Remove(tmp); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte(content + "\n")); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func RemoveProtocol(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if info.IsDir() {
		return false, errors.New("loop protocol path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return false, errors.New("loop protocol path must not be a symlink")
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return false, nil
		}
		return false, err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return true, nil
}

func SummarizeFile(path, relPath string) (Summary, bool, error) {
	info, err := os.Lstat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Summary{}, false, nil
		}
		return Summary{}, false, err
	}
	content, found, err := ReadProtocol(path)
	if err != nil {
		return Summary{}, false, err
	}
	if !found {
		return Summary{}, false, nil
	}
	summary := Summary{
		Path:      relPath,
		UpdatedAt: formatTime(info.ModTime()),
		Bytes:     len([]byte(content)),
		Preview:   textutil.Preview(content, 240),
	}
	for _, line := range strings.Split(content, "\n") {
		key, value, ok := parseMetadataLine(line)
		if !ok {
			continue
		}
		switch key {
		case "loop_id":
			summary.LoopID = value
		case "owner_session":
			summary.OwnerSession = value
		case "status":
			summary.Status = value
		}
	}
	if state, found, err := ReadState(filepath.Join(filepath.Dir(path), StateFileName)); err != nil {
		return Summary{}, false, err
	} else if found {
		summary.State = &state
		if state.LoopID != "" {
			summary.LoopID = state.LoopID
		}
		if state.OwnerSession != "" {
			summary.OwnerSession = state.OwnerSession
		}
		if state.Status != "" {
			summary.Status = state.Status
		}
		if state.UpdatedAt != "" {
			summary.UpdatedAt = state.UpdatedAt
		}
	}
	return summary, true, nil
}

func parseMetadataLine(line string) (string, string, bool) {
	line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
	key, value, ok := strings.Cut(line, ":")
	if !ok {
		return "", "", false
	}
	key = strings.ToLower(strings.TrimSpace(key))
	value = strings.TrimSpace(value)
	if key == "" || value == "" {
		return "", "", false
	}
	return key, value, true
}

func templateLine(s string) string {
	s = strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
	s = strings.ReplaceAll(s, "\x00", "")
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}
