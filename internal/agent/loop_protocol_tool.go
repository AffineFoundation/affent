package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/affinefoundation/affent/internal/loopstate"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	// LoopProtocolToolName is the registry name for the session-scoped
	// LOOP.md maintenance tool.
	LoopProtocolToolName                     = "loop_protocol"
	loopProtocolActivationStatusFailureKind  = "loop_protocol_activation_status"
	loopProtocolActivationInvalidFailureKind = "loop_protocol_activation_invalid"
	loopProtocolActivationUnreadyFailureKind = "loop_protocol_activation_unready"
	loopProtocolActivationStatusNext         = "Next: if the user has answered a calibration question and the protocol is complete, call loop_protocol action=complete_activation. The tool performs the draft-to-running transition. If intent is still unclear, ask one concise follow-up in a later turn and keep the saved draft status=draft."
	maxLoopProtocolActionBytes               = 32
	maxLoopProtocolGoalBytes                 = 1024
	maxLoopProtocolReasonBytes               = 1024
	maxLoopProtocolSectionBytes              = 120
	maxLoopProtocolPatchBodyBytes            = 2048
)

type loopProtocolToolArgs struct {
	Action          string   `json:"action"`
	Status          string   `json:"status"`
	Goal            string   `json:"goal"`
	Protocol        string   `json:"protocol"`
	Reason          string   `json:"reason"`
	SectionsChanged []string `json:"sections_changed"`
	Patches         []struct {
		Heading string `json:"heading"`
		Body    string `json:"body"`
	} `json:"patches"`
}

func RegisterLoopProtocolOnly(r *Registry, protocolPath string) {
	if r == nil || strings.TrimSpace(protocolPath) == "" {
		return
	}
	r.Add(loopProtocolTool(protocolPath))
}

func loopProtocolTool(protocolPath string) *Tool {
	schema := json.RawMessage(fmt.Sprintf(`{
        "type": "object",
        "additionalProperties": false,
        "required": ["action"],
        "properties": {
            "action": {"type": "string", "minLength": 1, "maxLength": %d, "enum": ["start_setup", "read", "patch_draft", "update_draft", "complete_activation", "close"], "description": "start_setup initializes a non-active draft LOOP.md for chat-driven activation; read returns the current LOOP.md; patch_draft replaces compact known sections without resending the full file; update_draft writes a full non-active draft; complete_activation usually omits protocol and activates the saved draft after calibration is recorded; close marks an active loop completed, blocked, or paused."},
            "status": {"type": "string", "enum": ["completed", "blocked", "paused"], "description": "Status for action=close. Use completed only when the loop objective is done with evidence, blocked only when continuation needs external input/state, and paused for deliberate operator pause."},
            "goal": {"type": "string", "maxLength": %d, "description": "Compact long-run goal for start_setup. Use the user's own intent, not a broad generic goal."},
            "protocol": {"type": "string", "maxLength": %d, "description": "Full LOOP.md markdown for update_draft or unusual complete_activation structural rewrites. Prefer patch_draft plus complete_activation without protocol for normal setup."},
            "reason": {"type": "string", "maxLength": %d, "description": "Short reason for the protocol update or activation."},
            "sections_changed": {"type": "array", "maxItems": 16, "items": {"type": "string", "minLength": 1, "maxLength": %d}, "description": "Optional LOOP.md section names changed by this update."},
            "patches": {"type": "array", "maxItems": 4, "items": {"type": "object", "additionalProperties": false, "required": ["heading", "body"], "properties": {"heading": {"type": "string", "enum": ["## 2. Current Situation", "## 5. Rules", "## 7. Evidence And Recovery Index"], "description": "Existing LOOP.md section heading to replace."}, "body": {"type": "string", "minLength": 1, "maxLength": %d, "description": "Compact replacement body for the section."}}}, "description": "Section patches for action=patch_draft. Use this instead of sending the full protocol for ordinary setup supplementation."}
        }
    }`, maxLoopProtocolActionBytes, maxLoopProtocolGoalBytes, loopstate.MaxProtocolBytes, maxLoopProtocolReasonBytes, maxLoopProtocolSectionBytes, maxLoopProtocolPatchBodyBytes))
	return &Tool{
		Name:         LoopProtocolToolName,
		Description:  "Start setup, read, patch, update, complete activation, or close this session's LOOP.md. Use during loop activation or long-run protocol maintenance. If the user asks in chat to enable loop and LOOP.md is missing, call start_setup, then ask a concise calibration question. Do not call complete_activation until the user has answered at least one calibration question and the intent is understood. When ready, prefer patch_draft for compact section updates, then call complete_activation without protocol to activate the saved draft; do not use update_draft to write running status. When the loop objective is complete or cannot continue, use action=close with status completed, blocked, or paused.",
		Schema:       schema,
		CatalogGroup: "Core",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			_ = ctx
			p, err := decodeBuiltinToolArgs[loopProtocolToolArgs](LoopProtocolToolName, args, "action, goal, protocol, reason, sections_changed, patches", "Use action=start_setup when the user asks to enable loop and LOOP.md is missing; use action=read when unsure; use patch_draft for compact draft supplementation; use update_draft only for full draft rewrites; use complete_activation without protocol after calibration is recorded and the saved draft is complete.")
			if err != nil {
				return "", err
			}
			action := strings.ToLower(strings.TrimSpace(p.Action))
			if action == "" {
				return "", errors.New("action is required\nNext: call loop_protocol with action=start_setup, action=read, action=update_draft, or action=complete_activation")
			}
			if len(action) > maxLoopProtocolActionBytes {
				return "", fmt.Errorf("action is %d bytes; loop_protocol action supports up to %d bytes\nNext: retry with action=start_setup, action=read, action=update_draft, or action=complete_activation", len(action), maxLoopProtocolActionBytes)
			}
			if len([]byte(p.Goal)) > maxLoopProtocolGoalBytes {
				return "", fmt.Errorf("goal is %d bytes; loop_protocol goal supports up to %d bytes\nNext: retry start_setup with a shorter concrete long-run goal", len([]byte(p.Goal)), maxLoopProtocolGoalBytes)
			}
			if len([]byte(p.Reason)) > maxLoopProtocolReasonBytes {
				return "", fmt.Errorf("reason is %d bytes; loop_protocol reason supports up to %d bytes\nNext: retry with a shorter reason", len([]byte(p.Reason)), maxLoopProtocolReasonBytes)
			}
			switch action {
			case "start_setup":
				return startLoopProtocolSetup(protocolPath, p)
			case "read":
				return readLoopProtocolToolResult(protocolPath)
			case "patch_draft":
				return patchLoopProtocolDraft(protocolPath, p)
			case "update_draft":
				return updateLoopProtocolDraft(protocolPath, p)
			case "complete_activation":
				return completeLoopProtocolActivation(protocolPath, p)
			case "close":
				return closeLoopProtocol(protocolPath, p)
			default:
				return "", fmt.Errorf("unsupported action %q (valid: start_setup, read, patch_draft, update_draft, complete_activation, close)\nNext: retry loop_protocol with one of the supported actions", action)
			}
		},
	}
}

func startLoopProtocolSetup(protocolPath string, p loopProtocolToolArgs) (string, error) {
	goal := strings.TrimSpace(p.Goal)
	if goal == "" {
		goal = strings.TrimSpace(p.Reason)
	}
	if goal == "" {
		return "", errors.New("goal is required for start_setup\nNext: retry loop_protocol with action=start_setup and a compact goal from the user's loop request, then ask one concise calibration question")
	}
	created, state, event, err := loopstate.EnsureProtocolTemplate(protocolPath, loopstate.ProtocolTemplateOptions{
		LoopID:       loopIDFromProtocolPath(protocolPath),
		OwnerSession: loopIDFromProtocolPath(protocolPath),
		Goal:         goal,
		Status:       "draft",
	})
	if err != nil {
		return "", err
	}
	if !created {
		status := state.Status
		if status == "" {
			status = "unknown"
		}
		return fmt.Sprintf("LOOP.md setup already exists status=%s path=%s next=read LOOP.md and ask one concise calibration question if activation is still draft", status, loopstate.ProtocolRelPath(loopIDFromProtocolPath(protocolPath))), nil
	}
	return fmt.Sprintf("initialized LOOP.md draft status=%s event_seq=%d path=%s next=ask one concise calibration question before activation", state.Status, event.Seq, loopstate.ProtocolRelPath(loopIDFromProtocolPath(protocolPath))), nil
}

func readLoopProtocolToolResult(protocolPath string) (string, error) {
	content, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("LOOP.md is not initialized for this session\nNext: ask the runtime or user to start loop activation before reading the loop protocol")
	}
	status := loopstate.ProtocolStatus(content)
	if status == "" {
		status = "unknown"
	}
	return fmt.Sprintf("loop_protocol status=%s bytes=%d path=%s\n\n%s", status, len([]byte(content)), loopstate.ProtocolRelPath(loopIDFromProtocolPath(protocolPath)), content), nil
}

func patchLoopProtocolDraft(protocolPath string, p loopProtocolToolArgs) (string, error) {
	if len(p.Patches) == 0 {
		return "", errors.New("patches are required for patch_draft\nNext: retry with compact patches for Current Situation, Rules, or Evidence And Recovery Index, or use update_draft only when a full rewrite is necessary")
	}
	protocol, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil {
		return "", err
	}
	if !found {
		return "", errors.New("LOOP.md is not initialized for this session\nNext: call loop_protocol action=start_setup before patching the draft")
	}
	status := loopstate.ProtocolStatus(protocol)
	if status != "draft" {
		return "", fmt.Errorf("patch_draft requires LOOP.md status=draft, got %q\nNext: use patch_draft only during setup; for an active loop, use update_draft with a deliberate full maintenance edit if needed", status)
	}
	patches := make([]loopstate.ProtocolSectionPatch, 0, len(p.Patches))
	for _, patch := range p.Patches {
		heading := strings.TrimSpace(patch.Heading)
		body := strings.TrimSpace(patch.Body)
		if !allowedLoopProtocolPatchHeading(heading) {
			return "", fmt.Errorf("unsupported patch heading %q\nNext: patch only ## 2. Current Situation, ## 5. Rules, or ## 7. Evidence And Recovery Index", heading)
		}
		if len([]byte(body)) > maxLoopProtocolPatchBodyBytes {
			return "", fmt.Errorf("patch body for %s is %d bytes; supports up to %d bytes\nNext: keep LOOP.md sections compact and move detailed history to plan, trace, artifacts, or memory", heading, len([]byte(body)), maxLoopProtocolPatchBodyBytes)
		}
		patches = append(patches, loopstate.ProtocolSectionPatch{Heading: heading, Body: body})
	}
	next, changed, err := loopstate.ApplyProtocolSectionPatches(protocol, patches)
	if err != nil {
		return "", err
	}
	if err := loopstate.WriteProtocol(protocolPath, next); err != nil {
		return "", err
	}
	if len(p.SectionsChanged) > 0 {
		changed = p.SectionsChanged
	}
	state, event, err := loopstate.RecordProtocolUpdate(protocolPath, p.Reason, changed)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("patched LOOP.md draft status=%s event_seq=%d updates=%d sections=%s next=call complete_activation without protocol after calibration is recorded and placeholders are resolved", state.Status, event.Seq, state.ProtocolUpdates, strings.Join(changed, ", ")), nil
}

func allowedLoopProtocolPatchHeading(heading string) bool {
	switch heading {
	case "## 2. Current Situation", "## 5. Rules", "## 7. Evidence And Recovery Index":
		return true
	default:
		return false
	}
}

func updateLoopProtocolDraft(protocolPath string, p loopProtocolToolArgs) (string, error) {
	protocol := strings.TrimSpace(p.Protocol)
	if protocol == "" {
		return "", errors.New("protocol is required for update_draft\nNext: call loop_protocol action=read, revise the markdown, then retry update_draft with the full LOOP.md")
	}
	status := loopstate.ProtocolStatus(protocol)
	if status == "running" {
		return "", loopProtocolFailure(
			"update_draft cannot activate a loop protocol with status=running\n"+loopProtocolActivationStatusNext,
			loopProtocolActivationStatusFailureKind,
		)
	}
	if status == "" {
		return "", loopProtocolFailure(
			"LOOP.md metadata must include a valid status before update_draft\nNext: use patch_draft for compact section changes, or keep metadata status=draft for a deliberate full draft rewrite; activate later with complete_activation.",
			loopProtocolActivationStatusFailureKind,
		)
	}
	if err := loopstate.WriteProtocol(protocolPath, protocol); err != nil {
		return "", err
	}
	state, event, err := loopstate.RecordProtocolUpdate(protocolPath, p.Reason, p.SectionsChanged)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("updated LOOP.md draft status=%s event_seq=%d updates=%d preview=%s", state.Status, event.Seq, state.ProtocolUpdates, textutil.Preview(protocol, MaxToolResultPreviewInEvent)), nil
}

func completeLoopProtocolActivation(protocolPath string, p loopProtocolToolArgs) (string, error) {
	protocol, ignoredMalformedProtocol, err := loopstate.PrepareProtocolActivation(protocolPath, p.Protocol)
	if err != nil {
		return "", loopProtocolFailure(
			err.Error()+"\n"+loopProtocolActivationStatusNext,
			loopProtocolActivationStatusFailureKind,
		)
	}
	if err := loopstate.ValidateProtocolActivation(protocol); err != nil {
		return "", loopProtocolFailure(
			fmt.Sprintf("%v\nNext: keep status=draft, ask or wait for the needed calibration details, fill the unresolved LOOP.md fields, keep Current Situation compact, and retry activation only after the protocol is complete", err),
			loopProtocolActivationInvalidFailureKind,
		)
	}
	if _, err := loopstate.RepairRecordedCalibrationFromProtocol(protocolPath, protocol); err != nil {
		return "", err
	}
	if err := loopstate.ValidateProtocolActivationReady(protocolPath); err != nil {
		return "", loopProtocolFailure(
			fmt.Sprintf("%v\nNext: ask one concise calibration question, wait for the user's answer, then retry loop_protocol action=complete_activation after the runtime records that answer", err),
			loopProtocolActivationUnreadyFailureKind,
		)
	}
	if err := loopstate.WriteProtocol(protocolPath, protocol); err != nil {
		return "", err
	}
	state, event, err := loopstate.RecordProtocolActivation(protocolPath, p.Reason)
	if err != nil {
		return "", err
	}
	ignored := ""
	if ignoredMalformedProtocol {
		ignored = " ignored_protocol_payload=missing_metadata"
	}
	return fmt.Sprintf("activated LOOP.md status=%s event_seq=%d updates=%d%s next=active loop protocol will be fed on future turns", state.Status, event.Seq, state.ProtocolUpdates, ignored), nil
}

func closeLoopProtocol(protocolPath string, p loopProtocolToolArgs) (string, error) {
	status := strings.ToLower(strings.TrimSpace(p.Status))
	switch status {
	case "completed", "blocked", "paused":
	default:
		return "", errors.New("status is required for close and must be completed, blocked, or paused\nNext: retry loop_protocol action=close with the smallest accurate status and a compact reason")
	}
	state, event, err := loopstate.RecordProtocolStatus(protocolPath, status, p.Reason)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("closed LOOP.md status=%s event_seq=%d updates=%d next=active loop protocol feed is disabled unless reopened deliberately", state.Status, event.Seq, state.ProtocolUpdates), nil
}

func savedLoopProtocolForActivation(protocolPath string) (string, bool, error) {
	saved, found, err := loopstate.ReadProtocol(protocolPath)
	if err != nil || !found {
		return "", false, err
	}
	status := loopstate.ProtocolStatus(saved)
	switch status {
	case "draft":
		var ok bool
		saved, ok = loopstate.ProtocolWithStatus(saved, "running")
		if !ok {
			return "", false, nil
		}
	case "running":
	default:
		return "", false, nil
	}
	return saved, true, nil
}

func loopProtocolFailure(message, kind string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "loop protocol operation failed"
	}
	if !strings.Contains(message, "Next:") {
		message += "\nNext: inspect the saved loop protocol state, correct the failing input, then retry loop_protocol with the smallest necessary action."
	}
	kind = strings.TrimSpace(kind)
	if kind == "" {
		return errors.New(message)
	}
	return fmt.Errorf("%s\nFailure: kind=%s", message, kind)
}

func loopIDFromProtocolPath(protocolPath string) string {
	protocolPath = strings.TrimRight(strings.TrimSpace(protocolPath), `/\`)
	if protocolPath == "" {
		return "loop"
	}
	parts := strings.FieldsFunc(protocolPath, func(r rune) bool { return r == '/' || r == '\\' })
	if len(parts) < 2 {
		return "loop"
	}
	return parts[len(parts)-2]
}

const loopProtocolSystemGuidanceMarker = "Loop protocol maintenance:"

func WithLoopProtocolSystemGuidance(prompt string) string {
	if strings.TrimSpace(prompt) == "" {
		prompt = DefaultSystemPrompt
	}
	if strings.Contains(prompt, loopProtocolSystemGuidanceMarker) {
		return prompt
	}
	return prompt + `

` + loopProtocolSystemGuidanceMarker + `
- If the user asks in ordinary chat to start, enable, resume, or modify a loop/LOOP.md, treat it as loop protocol maintenance and follow the same calibration-first protocol as UI-driven setup.
- If the user asks to enable loop and LOOP.md is missing, call loop_protocol action=start_setup with a compact goal from the user's request, then ask one concise calibration question. Do not tell the user to press the UI button.
- During loop activation, first understand the user's concrete long-run intent and ask exactly one concise calibration question before activation, even when the initial goal seems clear. If the goal, stop conditions, memory policy, or recovery expectations remain unclear after the answer, ask one focused follow-up in a later turn and leave LOOP.md as draft until complete.
- Do not complete activation in the same turn that created or first discovered a draft unless this turn is responding to an earlier explicit calibration answer. The useful behavior is to ask, wait, then supplement and activate.
- Use loop_protocol action=start_setup/read/patch_draft/update_draft/complete_activation to maintain the session LOOP.md; do not use ordinary workspace file tools for server-managed loop state.
- Never claim that a loop is running after only draft creation. Only call complete_activation after the user answers and you supplement the protocol with the user's intent, current situation snapshot, operational stop conditions, memory lookup/update rules in durable rules when needed, self-attack checks, and recovery anchors. Prefer patch_draft for compact section updates, then complete_activation without protocol; use full update_draft only for broad structural edits. Use complete_activation for the draft-to-running transition; do not use update_draft for running status.
- When the active loop objective is complete, blocked on external input/state, or deliberately paused, call loop_protocol action=close with status completed, blocked, or paused and a compact evidence-based reason.
- Keep LOOP.md compact. Put detailed task progress in the plan, artifacts, memory, or trace instead of duplicating it in the protocol.`
}
