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
	LoopProtocolToolName        = "loop_protocol"
	maxLoopProtocolActionBytes  = 32
	maxLoopProtocolGoalBytes    = 1024
	maxLoopProtocolReasonBytes  = 1024
	maxLoopProtocolSectionBytes = 120
)

type loopProtocolToolArgs struct {
	Action          string   `json:"action"`
	Goal            string   `json:"goal"`
	Protocol        string   `json:"protocol"`
	Reason          string   `json:"reason"`
	SectionsChanged []string `json:"sections_changed"`
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
            "action": {"type": "string", "minLength": 1, "maxLength": %d, "enum": ["start_setup", "read", "update_draft", "complete_activation"], "description": "start_setup initializes a non-active draft LOOP.md for chat-driven activation; read returns the current LOOP.md; update_draft writes a non-active draft; complete_activation writes the supplemented protocol and marks it active only when metadata status is running."},
            "goal": {"type": "string", "maxLength": %d, "description": "Compact long-run goal for start_setup. Use the user's own intent, not a broad generic goal."},
            "protocol": {"type": "string", "maxLength": %d, "description": "Full LOOP.md markdown for update_draft or complete_activation."},
            "reason": {"type": "string", "maxLength": %d, "description": "Short reason for the protocol update or activation."},
            "sections_changed": {"type": "array", "maxItems": 16, "items": {"type": "string", "minLength": 1, "maxLength": %d}, "description": "Optional LOOP.md section names changed by this update."}
        }
    }`, maxLoopProtocolActionBytes, maxLoopProtocolGoalBytes, loopstate.MaxProtocolBytes, maxLoopProtocolReasonBytes, maxLoopProtocolSectionBytes))
	return &Tool{
		Name:         LoopProtocolToolName,
		Description:  "Start setup, read, update, or complete activation for this session's LOOP.md. Use during loop activation or long-run protocol maintenance. If the user asks in chat to enable loop and LOOP.md is missing, call start_setup, then ask a concise calibration question. Do not call complete_activation until the user has answered at least one calibration question, the intent is understood, the protocol is supplemented, and metadata status is running; ask concise questions and keep the protocol as draft when setup is incomplete.",
		Schema:       schema,
		CatalogGroup: "Core",
		Execute: func(ctx context.Context, args json.RawMessage) (string, error) {
			_ = ctx
			p, err := decodeBuiltinToolArgs[loopProtocolToolArgs](LoopProtocolToolName, args, "action, goal, protocol, reason, sections_changed", "Use action=start_setup when the user asks to enable loop and LOOP.md is missing; use action=read when unsure; use update_draft for incomplete protocols and complete_activation only after setting metadata status: running.")
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
			case "update_draft":
				return updateLoopProtocolDraft(protocolPath, p)
			case "complete_activation":
				return completeLoopProtocolActivation(protocolPath, p)
			default:
				return "", fmt.Errorf("unsupported action %q (valid: start_setup, read, update_draft, complete_activation)\nNext: retry loop_protocol with one of the supported actions", action)
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

func updateLoopProtocolDraft(protocolPath string, p loopProtocolToolArgs) (string, error) {
	protocol := strings.TrimSpace(p.Protocol)
	if protocol == "" {
		return "", errors.New("protocol is required for update_draft\nNext: call loop_protocol action=read, revise the markdown, then retry update_draft with the full LOOP.md")
	}
	status := loopstate.ProtocolStatus(protocol)
	if status == "running" {
		return "", errors.New("update_draft cannot activate a loop protocol with status=running\nNext: if the user has answered a calibration question and the protocol is complete, call loop_protocol with action=complete_activation; otherwise keep status=draft")
	}
	if status == "" {
		return "", errors.New("LOOP.md metadata must include a valid status before update_draft\nNext: keep metadata status: draft until the protocol is complete enough to activate")
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
	protocol := strings.TrimSpace(p.Protocol)
	if protocol != "" {
	} else {
		var found bool
		var err error
		protocol, found, err = loopstate.ReadProtocol(protocolPath)
		if err != nil {
			return "", err
		}
		if !found {
			return "", errors.New("LOOP.md is not initialized for this session\nNext: start loop activation before completing activation")
		}
	}
	if loopstate.ProtocolStatus(protocol) != "running" {
		return "", errors.New("complete_activation requires LOOP.md metadata status: running\nNext: ask at least one concise calibration question before activation; if user intent is still unclear, ask up to two concise questions and leave the protocol in draft; otherwise update the full protocol with status: running after the user answers and retry")
	}
	if err := loopstate.ValidateProtocolActivation(protocol); err != nil {
		return "", fmt.Errorf("%w\nNext: keep status=draft, ask or wait for the needed calibration details, fill the unresolved LOOP.md fields, and retry activation only after the protocol is complete", err)
	}
	if err := loopstate.ValidateProtocolActivationReady(protocolPath); err != nil {
		return "", fmt.Errorf("%w\nNext: ask one concise calibration question, wait for the user's answer, then retry activation after the runtime records that answer", err)
	}
	if strings.TrimSpace(p.Protocol) != "" {
		if err := loopstate.WriteProtocol(protocolPath, protocol); err != nil {
			return "", err
		}
	}
	state, event, err := loopstate.RecordProtocolActivation(protocolPath, p.Reason)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("activated LOOP.md status=%s event_seq=%d updates=%d next=active loop protocol will be fed on future turns", state.Status, event.Seq, state.ProtocolUpdates), nil
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
- During loop activation, first understand the user's concrete long-run intent and ask at least one concise calibration question before activation, even when the initial goal seems clear. If the goal, stop conditions, memory policy, or recovery expectations remain unclear, ask at most two concise questions and leave LOOP.md as draft.
- Do not complete activation in the same turn that created or first discovered a draft unless this turn is responding to an earlier explicit calibration answer. The useful behavior is to ask, wait, then supplement and activate.
- Use loop_protocol action=start_setup/read/update_draft/complete_activation to maintain the session LOOP.md; do not use ordinary workspace file tools for server-managed loop state.
- Never claim that a loop is running after only draft creation. Only complete_activation after the user answers and you supplement the protocol with the user's intent, current situation snapshot, operational stop conditions, memory lookup/update rules in durable rules when needed, self-attack checks, and recovery anchors, with metadata status: running.
- Keep LOOP.md compact. Put detailed task progress in the plan, artifacts, memory, or trace instead of duplicating it in the protocol.`
}
