package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	PlanToolName = "plan"

	maxPlanSteps         = 12
	maxPlanStepTextBytes = 240
	maxPlanNoteBytes     = 500
	maxPlanEvidence      = 6
	maxPlanEvidenceBytes = 240
	maxPlanStateBytes    = 32 * 1024
	planStateVersion     = 1

	maxActivePlanStepTextBytes    = 160
	maxActivePlanNoteBytes        = 160
	maxActivePlanEvidenceRefs     = 3
	maxActivePlanEvidenceRefBytes = 120
)

type planToolArgs struct {
	Action   string       `json:"action"`
	Steps    []planStep   `json:"steps"`
	Updates  []planUpdate `json:"updates"`
	Index    int          `json:"index"`
	Status   string       `json:"status"`
	Text     string       `json:"text"`
	Evidence []string     `json:"evidence"`
	Note     string       `json:"note"`
}

type planUpdate struct {
	Index    int      `json:"index"`
	Status   string   `json:"status"`
	Text     string   `json:"text"`
	Evidence []string `json:"evidence"`
	Note     string   `json:"note"`
}

type planState struct {
	Version   int        `json:"version"`
	UpdatedAt string     `json:"updated_at"`
	Steps     []planStep `json:"steps"`
	Message   string     `json:"message,omitempty"`
}

type planStep struct {
	Index    int      `json:"index,omitempty"`
	Text     string   `json:"text"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence,omitempty"`
	Note     string   `json:"note,omitempty"`
}

type planMutationReceipt struct {
	Version        int               `json:"version"`
	Message        string            `json:"message,omitempty"`
	Label          string            `json:"label,omitempty"`
	TotalSteps     int               `json:"total_steps,omitempty"`
	CompletedSteps int               `json:"completed_steps,omitempty"`
	Current        *planReceiptStep  `json:"current,omitempty"`
	Changed        []planReceiptStep `json:"changed,omitempty"`
	Next           string            `json:"next,omitempty"`
}

type planReceiptStep struct {
	Index    int      `json:"index"`
	Text     string   `json:"text"`
	Status   string   `json:"status"`
	Evidence []string `json:"evidence,omitempty"`
	Note     string   `json:"note,omitempty"`
}

func planTool(path string) *Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["action"],
        "properties": {
            "action": {"type": "string", "enum": ["view", "set", "update", "clear"], "description": "view returns the current plan; set creates a plan when no active unfinished plan exists; update changes one or more steps; clear removes the active plan."},
			"steps": {"type": "array", "minItems": 1, "maxItems": %d, "items": {"type": "object", "additionalProperties": false, "required": ["text"], "properties": {"index": {"type": "integer", "minimum": 1, "maximum": %d, "description": "Optional ordinal label accepted for model ergonomics; storage derives order from the array."}, "text": {"type": "string", "minLength": 1, "maxLength": %d}, "status": {"type": "string", "enum": ["pending", "in_progress", "completed", "blocked"]}, "evidence": {"type": "array", "maxItems": %d, "items": {"type": "string", "minLength": 1, "maxLength": %d}}, "note": {"type": "string", "maxLength": %d}}}},
            "updates": {"type": "array", "minItems": 1, "maxItems": %d, "description": "Batch step updates for action=update; prefer this when completing one step and starting or closing another in the same turn.", "items": {"type": "object", "additionalProperties": false, "required": ["index"], "properties": {"index": {"type": "integer", "minimum": 1, "maximum": %d}, "status": {"type": "string", "enum": ["pending", "in_progress", "completed", "blocked"]}, "text": {"type": "string", "minLength": 1, "maxLength": %d}, "evidence": {"type": "array", "maxItems": %d, "items": {"type": "string", "minLength": 1, "maxLength": %d}}, "note": {"type": "string", "maxLength": %d}}}},
            "index": {"type": "integer", "minimum": 1, "maximum": %d, "description": "1-based step index for single-step update."},
            "status": {"type": "string", "enum": ["pending", "in_progress", "completed", "blocked"], "description": "Replacement status for update."},
            "text": {"type": "string", "minLength": 1, "maxLength": %d, "description": "Replacement step text for update."},
            "evidence": {"type": "array", "maxItems": %d, "items": {"type": "string", "minLength": 1, "maxLength": %d}, "description": "Replacement evidence refs for update."},
            "note": {"type": "string", "maxLength": %d, "description": "Replacement note for update."}
        }
	}`, maxPlanSteps, maxPlanSteps, maxPlanStepTextBytes, maxPlanEvidence, maxPlanEvidenceBytes, maxPlanNoteBytes, maxPlanSteps, maxPlanSteps, maxPlanStepTextBytes, maxPlanEvidence, maxPlanEvidenceBytes, maxPlanNoteBytes, maxPlanSteps, maxPlanStepTextBytes, maxPlanEvidence, maxPlanEvidenceBytes, maxPlanNoteBytes))

	var mu sync.Mutex
	return &Tool{
		Name:        PlanToolName,
		Description: "Maintain a concise, persistent task plan for multi-step work. Use only for non-trivial tasks; do not call for simple one-shot questions.",
		Schema:      schema,
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			_ = ctx
			if strings.TrimSpace(path) == "" {
				return "", errors.New("plan storage path is not configured\nNext: continue without the plan tool and summarize progress in your final answer")
			}
			p, present, err := decodePlanToolArgs(args)
			if err != nil {
				return "", fmt.Errorf("decode args for plan: %w\nFailure: kind=invalid_args\nNext: retry plan with a single JSON object using only documented fields: action, steps, index, status, text, evidence, note. action must be view, set, update, or clear.", err)
			}
			action := normalizePlanAction(p.Action)
			if action == "" {
				return "", errors.New("action is required\nNext: call plan with action=view, action=set, action=update, or action=clear")
			}
			if err := rejectUnusedPlanArgs(action, present); err != nil {
				return "", err
			}

			mu.Lock()
			defer mu.Unlock()

			switch action {
			case "view":
				st, err := readPlanState(path)
				if err != nil {
					return "", err
				}
				if len(st.Steps) == 0 {
					st.Message = "no active plan"
				}
				return marshalPlanState(st)
			case "set":
				existing, err := readPlanState(path)
				if err != nil {
					return "", err
				}
				if planHasOpenWork(existing) {
					return "", planActiveReplacementError(existing)
				}
				steps, err := normalizePlanSteps(p.Steps)
				if err != nil {
					return "", err
				}
				st := newPlanState(steps, "plan set")
				if err := writePlanState(path, st); err != nil {
					return "", err
				}
				return marshalPlanMutationReceipt(st, nil)
			case "update":
				st, err := readPlanState(path)
				if err != nil {
					return "", err
				}
				if len(st.Steps) == 0 {
					return "", errors.New("no active plan to update\nNext: call plan with action=set and concise steps before updating a step")
				}
				changed, updated, err := applyPlanToolUpdate(&st, p, present)
				if err != nil {
					return "", err
				}
				if !changed {
					return "", errors.New("update requires at least one of status, text, evidence, note, or updates\nNext: retry with the step field you intend to change")
				}
				autoAdvancePlan(&st)
				steps, err := normalizePlanSteps(st.Steps)
				if err != nil {
					return "", err
				}
				st.Steps = steps
				st = newPlanState(st.Steps, planUpdateMessage(updated))
				if err := writePlanState(path, st); err != nil {
					return "", err
				}
				return marshalPlanMutationReceipt(st, updated)
			case "clear":
				if err := clearPlanState(path); err != nil {
					return "", err
				}
				return marshalPlanMutationReceipt(newPlanState(nil, "plan cleared"), nil)
			default:
				return "", fmt.Errorf("unknown action %q\nNext: retry with action=view, action=set, action=update, or action=clear", action)
			}
		},
	}
}

func decodePlanToolArgs(args json.RawMessage) (planToolArgs, map[string]bool, error) {
	p, err := decodeStrictToolArgs[planToolArgs](args)
	if err != nil {
		return p, nil, err
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(args, &raw); err != nil {
		return p, nil, err
	}
	present := make(map[string]bool, len(raw))
	for k := range raw {
		present[k] = true
	}
	return p, present, nil
}

func normalizePlanAction(action string) string {
	return strings.ToLower(strings.TrimSpace(action))
}

func rejectUnusedPlanArgs(action string, present map[string]bool) error {
	allowed := map[string]bool{"action": true}
	switch action {
	case "view", "clear":
	case "set":
		allowed["steps"] = true
	case "update":
		allowed["updates"] = true
		allowed["index"] = true
		allowed["status"] = true
		allowed["text"] = true
		allowed["evidence"] = true
		allowed["note"] = true
	default:
		return nil
	}
	var unused []string
	for k := range present {
		if !allowed[k] {
			unused = append(unused, k)
		}
	}
	if len(unused) > 0 {
		sort.Strings(unused)
		return fmt.Errorf("unused field(s) for action=%s: %s\nNext: remove fields that action does not use", action, strings.Join(unused, ", "))
	}
	return nil
}

func singlePlanUpdateFromArgs(p planToolArgs) planUpdate {
	return planUpdate{
		Index:    p.Index,
		Status:   p.Status,
		Text:     p.Text,
		Evidence: p.Evidence,
		Note:     p.Note,
	}
}

func applyPlanToolUpdate(st *planState, p planToolArgs, present map[string]bool) (bool, []int, error) {
	if present["updates"] {
		if present["index"] || present["status"] || present["text"] || present["evidence"] || present["note"] {
			return false, nil, errors.New("updates cannot be combined with top-level index, status, text, evidence, or note\nNext: either use updates for a batch change or top-level fields for one step")
		}
		if len(p.Updates) == 0 {
			return false, nil, errors.New("updates is empty\nNext: provide at least one step update")
		}
		changed := false
		updated := make([]int, 0, len(p.Updates))
		seen := map[int]bool{}
		for _, update := range p.Updates {
			if update.Index < 1 || update.Index > len(st.Steps) {
				return false, nil, fmt.Errorf("index %d is outside the current plan length %d\nNext: call plan with action=view, then update a valid 1-based index", update.Index, len(st.Steps))
			}
			if seen[update.Index] {
				return false, nil, fmt.Errorf("updates repeats index %d\nNext: merge duplicate updates for the same step", update.Index)
			}
			seen[update.Index] = true
			stepChanged, err := applyPlanUpdate(&st.Steps[update.Index-1], update, update.presentFields())
			if err != nil {
				return false, nil, fmt.Errorf("update index %d: %w", update.Index, err)
			}
			if !stepChanged {
				return false, nil, fmt.Errorf("update index %d requires at least one of status, text, evidence, or note\nNext: include the step field you intend to change", update.Index)
			}
			changed = true
			updated = append(updated, update.Index)
		}
		return changed, updated, nil
	}
	if !present["index"] {
		return false, nil, errors.New("index is required when action=update\nNext: call plan with action=view, then retry with a valid 1-based index")
	}
	if p.Index < 1 || p.Index > len(st.Steps) {
		return false, nil, fmt.Errorf("index %d is outside the current plan length %d\nNext: call plan with action=view, then update a valid 1-based index", p.Index, len(st.Steps))
	}
	changed, err := applyPlanUpdate(&st.Steps[p.Index-1], singlePlanUpdateFromArgs(p), present)
	if err != nil {
		return false, nil, err
	}
	return changed, []int{p.Index}, nil
}

func (p planUpdate) presentFields() map[string]bool {
	present := map[string]bool{"index": true}
	if strings.TrimSpace(p.Status) != "" {
		present["status"] = true
	}
	if strings.TrimSpace(p.Text) != "" {
		present["text"] = true
	}
	if p.Evidence != nil {
		present["evidence"] = true
	}
	if strings.TrimSpace(p.Note) != "" {
		present["note"] = true
	}
	return present
}

func autoAdvancePlan(st *planState) {
	if st == nil || len(st.Steps) == 0 {
		return
	}
	for _, step := range st.Steps {
		if strings.TrimSpace(step.Status) == "in_progress" {
			return
		}
	}
	for i := range st.Steps {
		if strings.TrimSpace(st.Steps[i].Status) == "pending" {
			st.Steps[i].Status = "in_progress"
			return
		}
	}
}

func planUpdateMessage(updated []int) string {
	if len(updated) == 1 {
		return fmt.Sprintf("updated step %d", updated[0])
	}
	parts := make([]string, 0, len(updated))
	for _, index := range updated {
		parts = append(parts, fmt.Sprint(index))
	}
	return "updated steps " + strings.Join(parts, ",")
}

func planHasOpenWork(st planState) bool {
	for _, step := range st.Steps {
		if strings.TrimSpace(step.Text) == "" {
			continue
		}
		if step.Status != "completed" {
			return true
		}
	}
	return false
}

func planActiveReplacementError(existing planState) error {
	existing.Message = "active plan unchanged"
	current := activePlanCurrentStepIndex(existing.Steps)
	next := "use action=update for the current step"
	if current > 0 {
		next = fmt.Sprintf("use action=update for step %d", current)
	}
	state := ""
	if raw, err := marshalPlanState(existing); err == nil {
		state = "\nCurrent plan state:\n" + raw
	}
	return fmt.Errorf("active plan already has unfinished work; action=set would replace persisted task state%s\nNext: %s, action=view to inspect the plan, or action=clear only when the user explicitly wants to discard the current plan.\nFailure: kind=plan_active_replacement", state, next)
}

func normalizePlanSteps(steps []planStep) ([]planStep, error) {
	if len(steps) == 0 {
		return nil, errors.New("steps is required when action=set\nNext: provide 2-6 concise steps for this task")
	}
	if len(steps) > maxPlanSteps {
		return nil, fmt.Errorf("plan supports at most %d steps\nNext: merge or drop low-value steps", maxPlanSteps)
	}
	out := make([]planStep, 0, len(steps))
	seenText := map[string]int{}
	inProgress := 0
	for i, step := range steps {
		n, err := normalizePlanStep(step)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		textKey := canonicalPlanStepText(n.Text)
		if first, ok := seenText[textKey]; ok {
			return nil, fmt.Errorf("step %d duplicates step %d\nNext: merge duplicate plan steps or replace one with a distinct action", i+1, first)
		}
		seenText[textKey] = i + 1
		if n.Status == "" {
			n.Status = "pending"
		}
		if n.Status == "in_progress" {
			inProgress++
		}
		out = append(out, n)
	}
	if inProgress > 1 {
		return nil, errors.New("only one plan step may be in_progress\nNext: mark one active step in_progress and keep the rest pending, completed, or blocked")
	}
	return out, nil
}

func normalizePersistedPlanSteps(steps []planStep) ([]planStep, error) {
	out := make([]planStep, 0, len(steps))
	seenText := map[string]bool{}
	inProgress := 0
	for i, step := range steps {
		n, err := normalizePlanStep(step)
		if err != nil {
			return nil, fmt.Errorf("step %d: %w", i+1, err)
		}
		textKey := canonicalPlanStepText(n.Text)
		if seenText[textKey] {
			continue
		}
		seenText[textKey] = true
		if n.Status == "" {
			n.Status = "pending"
		}
		if n.Status == "in_progress" {
			inProgress++
			if inProgress > 1 {
				n.Status = "pending"
			}
		}
		out = append(out, n)
	}
	if len(out) > maxPlanSteps {
		return nil, fmt.Errorf("plan supports at most %d steps\nNext: clear the plan and set a concise replacement", maxPlanSteps)
	}
	return out, nil
}

func canonicalPlanStepText(text string) string {
	return strings.ToLower(textutil.CompactWhitespace(text))
}

func normalizePlanStep(step planStep) (planStep, error) {
	step.Index = 0
	step.Text = strings.TrimSpace(step.Text)
	if step.Text == "" {
		return planStep{}, errors.New("text is required")
	}
	if len(step.Text) > maxPlanStepTextBytes {
		return planStep{}, fmt.Errorf("text is %d bytes; max is %d", len(step.Text), maxPlanStepTextBytes)
	}
	status, err := normalizePlanStatus(step.Status)
	if err != nil {
		return planStep{}, err
	}
	step.Status = status
	ev, err := normalizePlanEvidence(step.Evidence)
	if err != nil {
		return planStep{}, err
	}
	step.Evidence = ev
	step.Note = strings.TrimSpace(step.Note)
	if len(step.Note) > maxPlanNoteBytes {
		return planStep{}, fmt.Errorf("note is %d bytes; max is %d", len(step.Note), maxPlanNoteBytes)
	}
	return step, nil
}

func normalizePlanStatus(status string) (string, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	if status == "" {
		return "", nil
	}
	switch status {
	case "pending", "in_progress", "completed", "blocked":
		return status, nil
	default:
		return "", fmt.Errorf("status %q is invalid\nNext: use pending, in_progress, completed, or blocked", status)
	}
}

func normalizePlanEvidence(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, ref := range in {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		if len(ref) > maxPlanEvidenceBytes {
			return nil, fmt.Errorf("evidence ref is %d bytes; max is %d", len(ref), maxPlanEvidenceBytes)
		}
		if seen[ref] {
			continue
		}
		seen[ref] = true
		out = append(out, ref)
	}
	if len(out) > maxPlanEvidence {
		return nil, fmt.Errorf("evidence supports at most %d refs\nNext: keep only the strongest file, command, or URL refs", maxPlanEvidence)
	}
	return out, nil
}

func applyPlanUpdate(step *planStep, p planUpdate, present map[string]bool) (bool, error) {
	changed := false
	if present["status"] {
		status, err := normalizePlanStatus(p.Status)
		if err != nil {
			return false, err
		}
		if status == "" {
			return false, errors.New("status cannot be blank when provided\nNext: use pending, in_progress, completed, or blocked")
		}
		step.Status = status
		changed = true
	}
	if present["text"] {
		text := strings.TrimSpace(p.Text)
		if text == "" {
			return false, errors.New("text cannot be blank when provided\nNext: use a concise step description")
		}
		if len(text) > maxPlanStepTextBytes {
			return false, fmt.Errorf("text is %d bytes; max is %d", len(text), maxPlanStepTextBytes)
		}
		step.Text = text
		changed = true
	}
	if present["evidence"] {
		ev, err := normalizePlanEvidence(p.Evidence)
		if err != nil {
			return false, err
		}
		step.Evidence = ev
		changed = true
	}
	if present["note"] {
		note := strings.TrimSpace(p.Note)
		if len(note) > maxPlanNoteBytes {
			return false, fmt.Errorf("note is %d bytes; max is %d", len(note), maxPlanNoteBytes)
		}
		step.Note = note
		changed = true
	}
	return changed, nil
}

func newPlanState(steps []planStep, message string) planState {
	if steps == nil {
		steps = []planStep{}
	}
	return planState{
		Version:   planStateVersion,
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Steps:     steps,
		Message:   message,
	}
}

func readPlanState(path string) (planState, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return newPlanState(nil, ""), nil
	}
	if err != nil {
		return planState{}, err
	}
	if info.IsDir() {
		return planState{}, errors.New("plan path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return planState{}, errors.New("plan path must not be a symlink")
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return newPlanState(nil, ""), nil
	}
	if err != nil {
		return planState{}, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxPlanStateBytes+1))
	if err != nil {
		return planState{}, err
	}
	if len(raw) > maxPlanStateBytes {
		return planState{}, fmt.Errorf("plan file exceeds %d bytes\nNext: clear the plan and set a concise replacement", maxPlanStateBytes)
	}
	raw = []byte(strings.TrimSpace(string(raw)))
	if len(raw) == 0 {
		return newPlanState(nil, ""), nil
	}
	var st planState
	if err := json.Unmarshal(raw, &st); err != nil {
		return planState{}, fmt.Errorf("read plan state: %w", err)
	}
	if st.Version == 0 {
		st.Version = planStateVersion
	}
	if st.Steps == nil {
		st.Steps = []planStep{}
	}
	if len(st.Steps) > 0 {
		steps, err := normalizePersistedPlanSteps(st.Steps)
		if err != nil {
			return planState{}, fmt.Errorf("read plan state: %w", err)
		}
		st.Steps = steps
	}
	return st, nil
}

func writePlanState(path string, st planState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(path); err == nil {
		if info.IsDir() {
			return errors.New("plan path is a directory")
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return errors.New("plan path must not be a symlink")
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	raw, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
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
	if _, err := f.Write(append(raw, '\n')); err != nil {
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

func clearPlanState(path string) error {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if info.IsDir() {
		return errors.New("plan path is a directory")
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return errors.New("plan path must not be a symlink")
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if d, err := os.Open(filepath.Dir(path)); err == nil {
		_ = d.Sync()
		_ = d.Close()
	}
	return nil
}

func marshalPlanState(st planState) (string, error) {
	if st.Steps == nil {
		st.Steps = []planStep{}
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func marshalPlanMutationReceipt(st planState, changedIndexes []int) (string, error) {
	receipt := compactPlanMutationReceipt(st, changedIndexes)
	raw, err := json.Marshal(receipt)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

func compactPlanMutationReceipt(st planState, changedIndexes []int) planMutationReceipt {
	receipt := planMutationReceipt{
		Version:        planStateVersion,
		Message:        st.Message,
		Label:          activePlanStatusLabel(st.Steps),
		TotalSteps:     len(st.Steps),
		CompletedSteps: completedPlanStepCount(st.Steps),
	}
	if current := activePlanCurrentStepIndex(st.Steps); current > 0 {
		step := planStepReceipt(current, st.Steps[current-1])
		receipt.Current = &step
		receipt.Next = fmt.Sprintf("continue from step %d; call plan action=view only if the full plan is needed", current)
	} else if len(st.Steps) == 0 {
		receipt.Next = "no active plan"
	} else {
		receipt.Next = "all plan steps are completed; final answer may proceed after required verification"
	}
	if len(changedIndexes) > 0 {
		receipt.Changed = make([]planReceiptStep, 0, len(changedIndexes))
		for _, index := range changedIndexes {
			if index < 1 || index > len(st.Steps) {
				continue
			}
			receipt.Changed = append(receipt.Changed, planStepReceipt(index, st.Steps[index-1]))
		}
	}
	return receipt
}

func completedPlanStepCount(steps []planStep) int {
	completed := 0
	for _, step := range steps {
		if planStepCompleted(step) {
			completed++
		}
	}
	return completed
}

func planStepReceipt(index int, step planStep) planReceiptStep {
	return planReceiptStep{
		Index:    index,
		Text:     textutil.Preview(textutil.CompactWhitespace(step.Text), maxActivePlanStepTextBytes),
		Status:   strings.TrimSpace(step.Status),
		Evidence: compactPlanReceiptEvidence(step.Evidence),
		Note:     textutil.Preview(textutil.CompactWhitespace(step.Note), maxActivePlanNoteBytes),
	}
}

func compactPlanReceiptEvidence(evidence []string) []string {
	if len(evidence) == 0 {
		return nil
	}
	limit := len(evidence)
	if limit > maxActivePlanEvidenceRefs {
		limit = maxActivePlanEvidenceRefs
	}
	out := make([]string, 0, limit)
	for _, ev := range evidence[:limit] {
		ev = textutil.Preview(textutil.CompactWhitespace(ev), maxActivePlanEvidenceRefBytes)
		if ev != "" {
			out = append(out, ev)
		}
	}
	return out
}

const planSystemGuidanceMarker = "Affent plan tool guidance:"

func WithPlanSystemGuidance(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	if strings.Contains(prompt, planSystemGuidanceMarker) {
		return prompt
	}
	return prompt + `

` + planSystemGuidanceMarker + `
- If the plan tool is available, use it for non-trivial multi-step work such as code changes, investigations, migrations, or long reviews.
- Do not use plan for simple one-shot questions or a single file read.
- Keep plans short, update them when evidence changes, and clear them when the task is done.`
}

func PlanFirstToolPolicy() *FirstToolPolicy {
	return &FirstToolPolicy{
		ToolName: PlanToolName,
		Trigger:  func(string) bool { return true },
		Rejection: "plan_only: create or update the persisted task plan before using any other tool.\n" +
			"Next: call plan with action=set for a new plan, or action=update when revising an existing plan.",
	}
}

// PlanOnlyTurnOptions narrows one turn to the plan tool and a small explicit
// tool-call budget. It is shared by CLI and server modes so plan-only behavior
// does not drift between entrypoints.
func PlanOnlyTurnOptions(reg *Registry, maxToolCalls int) (TurnOptions, error) {
	if maxToolCalls <= 0 {
		return TurnOptions{}, errors.New("plan-only max tool calls must be positive")
	}
	if reg == nil {
		return TurnOptions{}, errors.New("plan tool is not available")
	}
	planTool, ok := reg.Get(PlanToolName)
	if !ok {
		return TurnOptions{}, errors.New("plan tool is not available")
	}
	planOnlyTools := NewRegistry()
	planOnlyTools.Add(planTool)
	return TurnOptions{
		Tools:                  planOnlyTools,
		FirstToolPolicy:        PlanFirstToolPolicy(),
		MaxToolCalls:           maxToolCalls,
		FinalNoToolsOnMaxTurns: true,
		UserMode:               UserModePlanOnly,
	}, nil
}

func ExecutePlanTurnOptions() TurnOptions {
	return ExecutePlanTurnOptionsForStep(0)
}

func ExecutePlanTurnOptionsForStep(currentStepIndex int) TurnOptions {
	return TurnOptions{
		ToolCallPolicies: []*ToolCallPolicy{PlanExecuteToolCallPolicyForStep(currentStepIndex)},
		UserMode:         UserModeExecutePlan,
	}
}

func PlanExecuteToolCallPolicy() *ToolCallPolicy {
	return PlanExecuteToolCallPolicyForStep(0)
}

func PlanExecuteToolCallPolicyForStep(currentStepIndex int) *ToolCallPolicy {
	return &ToolCallPolicy{
		ToolName: PlanToolName,
		Reject: func(ctx ToolCallPolicyContext) (string, bool) {
			action := planActionFromRawArgs(ctx.Args)
			switch action {
			case "set", "clear":
				return "execute_plan: the persisted plan is already confirmed; do not replace or clear it during execution.\nNext: execute the current active step, then call plan with action=update for that same step using status, evidence, or note.", true
			case "update":
				index := planIndexFromRawArgs(ctx.Args)
				if currentStepIndex > 0 && index > 0 && index != currentStepIndex {
					return fmt.Sprintf("execute_plan: update only the current active step %d during this turn; do not skip ahead or rewrite another step.\nNext: execute step %d, then call plan with action=update index=%d using status, evidence, or note.", currentStepIndex, currentStepIndex, currentStepIndex), true
				}
				return "", false
			default:
				return "", false
			}
		},
	}
}

func planActionFromRawArgs(args json.RawMessage) string {
	var raw struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return ""
	}
	return normalizePlanAction(raw.Action)
}

func planIndexFromRawArgs(args json.RawMessage) int {
	var raw struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(args, &raw); err != nil {
		return 0
	}
	return raw.Index
}

func PlanOnlyUserPrompt(request string) string {
	request = strings.TrimSpace(request)
	if request == "" {
		request = "(empty request)"
	}
	return `Plan-only mode is enabled.

Do not execute the task yet. Create or update a concise persisted plan with the plan tool, then answer with the proposed plan, what confirmation is needed before execution, and that execution should continue in the same session with affentctl run --execute-plan after confirmation. Do not call shell, file, web, browser, memory, skill, subagent, or focused-task tools in this turn.

Original user request:
` + request
}

func WithActivePlanSkillProvider(planPath string, next SkillProvider) SkillProvider {
	return func(userText string) string {
		blocks := make([]string, 0, 2)
		if block := activePlanSkillBlock(planPath); block != "" {
			blocks = append(blocks, block)
		}
		if next != nil {
			if block := strings.TrimSpace(next(userText)); block != "" {
				blocks = append(blocks, block)
			}
		}
		return strings.Join(blocks, "\n\n")
	}
}

func ActivePlanCompletionGuard(planPath string) CompletionGuard {
	return func() CompletionGuardResult {
		st, err := readPlanState(planPath)
		if err != nil || len(st.Steps) == 0 || planStateDone(st) {
			return CompletionGuardResult{}
		}
		label := activePlanStatusLabel(st.Steps)
		if label == "" {
			return CompletionGuardResult{}
		}
		reason := fmt.Sprintf("Persisted plan state is unfinished: %s.", label)
		if current := activePlanCurrentStepIndex(st.Steps); current > 0 {
			status := activePlanStepStatus(st.Steps[current-1])
			reason = fmt.Sprintf("Persisted plan state is unfinished: %s; current step %d is %s.", label, current, status)
		}
		required := "Use the plan tool to update the authoritative plan state before finalizing; if work is blocked, mark the relevant step blocked with evidence."
		prompt := "AFFENT COMPLETION GUARD:\n" +
			reason + "\n" +
			required + "\n" +
			"Do not answer as complete while the persisted plan has unfinished steps. If the task is complete, call the plan tool and mark every finished step completed with concise evidence, then provide the final answer. If it is not complete, continue from the current step or mark the step blocked with evidence."
		return CompletionGuardResult{
			Blocked:        true,
			ID:             "active-plan-unfinished",
			Trigger:        "active_plan_unfinished",
			Reason:         reason,
			RequiredAction: required,
			Prompt:         prompt,
		}
	}
}

func activePlanSkillBlock(planPath string) string {
	if strings.TrimSpace(planPath) == "" {
		return ""
	}
	st, err := readPlanState(planPath)
	if err != nil || len(st.Steps) == 0 {
		return ""
	}
	if planStateDone(st) {
		return ""
	}
	var b strings.Builder
	b.WriteString("AFFENT ACTIVE PLAN:\n")
	b.WriteString("This is the persisted task plan for the current session. Continue from it, update it when progress changes, and avoid restarting already completed steps.\n")
	if label := activePlanStatusLabel(st.Steps); label != "" {
		fmt.Fprintf(&b, "Plan status: %s.\n", label)
	}
	completed := 0
	for _, step := range st.Steps {
		if planStepCompleted(step) {
			completed++
		}
	}
	if completed > 0 {
		fmt.Fprintf(&b, "Completed steps: %d (details omitted from active context).\n", completed)
	}
	if current := activePlanCurrentStepIndex(st.Steps); current > 0 {
		fmt.Fprintf(&b, "Current step: %d. Execute this step before broadening; when progress changes, call plan action=update for this step with status, evidence, or note.\n", current)
	}
	for i, step := range st.Steps {
		if planStepCompleted(step) {
			continue
		}
		b.WriteString(formatActivePlanStep(i+1, step))
	}
	return strings.TrimSpace(b.String())
}

func activePlanStatusLabel(steps []planStep) string {
	total := len(steps)
	if total == 0 {
		return ""
	}
	completed := 0
	active := false
	blocked := false
	for _, step := range steps {
		status := strings.ToLower(strings.TrimSpace(step.Status))
		switch status {
		case "completed":
			completed++
		case "in_progress":
			active = true
		case "blocked":
			blocked = true
		}
	}
	label := fmt.Sprintf("plan:%d/%d", completed, total)
	if completed == total {
		return label + ":done"
	}
	if active {
		label += ":active"
	}
	if blocked {
		label += ":blocked"
	}
	return label
}

func activePlanCurrentStepIndex(steps []planStep) int {
	for _, status := range []string{"in_progress", "pending", "blocked"} {
		for i, step := range steps {
			if strings.TrimSpace(step.Status) == status {
				return i + 1
			}
		}
	}
	for i, step := range steps {
		if !planStepCompleted(step) {
			return i + 1
		}
	}
	return 0
}

func formatActivePlanStep(index int, step planStep) string {
	status := strings.TrimSpace(step.Status)
	if status == "" {
		status = "pending"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d. [%s] %s", index, status, textutil.Preview(strings.TrimSpace(step.Text), maxActivePlanStepTextBytes))
	if evidence := activePlanEvidenceSummary(step.Evidence); evidence != "" {
		fmt.Fprintf(&b, " evidence: %s", evidence)
	}
	if note := strings.TrimSpace(step.Note); note != "" {
		fmt.Fprintf(&b, " note: %s", textutil.Preview(note, maxActivePlanNoteBytes))
	}
	b.WriteByte('\n')
	return b.String()
}

func activePlanEvidenceSummary(evidence []string) string {
	refs := make([]string, 0, min(len(evidence), maxActivePlanEvidenceRefs))
	for _, ref := range evidence {
		ref = strings.TrimSpace(ref)
		if ref == "" {
			continue
		}
		refs = append(refs, textutil.Preview(ref, maxActivePlanEvidenceRefBytes))
		if len(refs) == maxActivePlanEvidenceRefs {
			break
		}
	}
	if len(refs) == 0 {
		return ""
	}
	summary := strings.Join(refs, ", ")
	if omitted := len(evidence) - len(refs); omitted > 0 {
		summary += fmt.Sprintf(" (+%d more)", omitted)
	}
	return summary
}

func planStateDone(st planState) bool {
	if len(st.Steps) == 0 {
		return false
	}
	for _, step := range st.Steps {
		if !planStepCompleted(step) {
			return false
		}
	}
	return true
}

func planStepCompleted(step planStep) bool {
	return strings.TrimSpace(step.Status) == "completed"
}

func activePlanStepStatus(step planStep) string {
	status := strings.ToLower(strings.TrimSpace(step.Status))
	if status == "" {
		return "pending"
	}
	return status
}
