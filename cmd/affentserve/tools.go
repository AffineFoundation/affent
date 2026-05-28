package main

import (
	"encoding/json"
	"net/http"

	agent "github.com/affinefoundation/affent/internal/agent"
)

type sessionToolsResponse struct {
	SessionID string      `json:"session_id"`
	Count     int         `json:"count"`
	Tools     []toolInfo  `json:"tools"`
	Surface   toolSurface `json:"surface,omitempty"`
}

type toolSurface struct {
	Headline        string   `json:"headline"`
	Detail          string   `json:"detail"`
	Tone            string   `json:"tone"`
	Status          string   `json:"status"`
	DisabledReasons []string `json:"disabled_reasons,omitempty"`
	Warnings        []string `json:"warnings,omitempty"`
}

type toolInfo struct {
	Name        string               `json:"name"`
	RawName     string               `json:"raw_name,omitempty"`
	Description string               `json:"description"`
	Parameters  json.RawMessage      `json:"parameters"`
	Group       string               `json:"group"`
	Source      string               `json:"source,omitempty"`
	ArgPolicy   *agent.ToolArgPolicy `json:"arg_policy,omitempty"`
}

func handleSessionTools(pool *SessionPool, sessionID string, w http.ResponseWriter, _ *http.Request) {
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
		writeJSONErrorTyped(w, http.StatusConflict, "session is not active; create or reopen it before listing tools", nil, "session_inactive")
		return
	}
	defs := sess.registry.Catalog()
	tools := make([]toolInfo, 0, len(defs))
	for _, def := range defs {
		tools = append(tools, toolInfo{
			Name:        def.Name,
			RawName:     def.RawName,
			Description: def.Description,
			Parameters:  def.Parameters,
			Group:       def.Group,
			Source:      def.Source,
			ArgPolicy:   def.ArgPolicy,
		})
	}
	surface := buildToolSurface(sess, pool.cfg, tools)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(sessionToolsResponse{
		SessionID: sessionID,
		Count:     len(tools),
		Tools:     tools,
		Surface:   surface,
	})
}

func buildToolSurface(sess *Session, cfg Config, tools []toolInfo) toolSurface {
	if sess == nil {
		return toolSurface{
			Headline: "Tool surface unavailable",
			Detail:   "This session is not active, so the tool surface could not be confirmed.",
			Tone:     "unknown",
			Status:   "unknown",
		}
	}
	caps := summarizeActiveCapabilities(sess, cfg)
	groupSeen := map[string]bool{}
	for _, tool := range tools {
		groupSeen[tool.Group] = true
	}
	disabledReasons := make([]string, 0, 4)
	if !caps.Builtins && !groupSeen["Workspace"] {
		disabledReasons = append(disabledReasons, "Workspace tools are off.")
	}
	if !caps.Memory && !caps.SessionSearch && !groupSeen["Memory"] && !groupSeen["History"] {
		disabledReasons = append(disabledReasons, "Memory and history tools are off.")
	}
	if !caps.WebSearch && !caps.Web && !caps.Browser && !caps.BrowserScreenshot && !groupSeen["Research"] {
		disabledReasons = append(disabledReasons, "Live sources are off.")
	}
	if !caps.Subagent && !caps.FocusedTasks && !groupSeen["Subtasks"] {
		disabledReasons = append(disabledReasons, "Nested work tools are off.")
	}
	if !caps.SkillInstall && !groupSeen["Skills"] {
		disabledReasons = append(disabledReasons, "Skill install tools are off.")
	}
	warnings := make([]string, 0, 2)
	if caps.EvalMode {
		warnings = append(warnings, "Eval mode may narrow the surface.")
	}
	if caps.Builtins && !groupSeen["Workspace"] && len(tools) > 0 {
		warnings = append(warnings, "Workspace tools are enabled but not visible in this catalog.")
	}
	if len(disabledReasons) == 0 {
		detail := "These tools are the subset currently exposed to this session."
		if len(warnings) > 0 {
			detail = "This surface is ready, with a few runtime warnings."
		}
		return toolSurface{
			Headline:        "Visible tool surface",
			Detail:          detail,
			Tone:            toneForToolSurface(disabledReasons, warnings),
			Status:          "allowed",
			Warnings:        warnings,
			DisabledReasons: disabledReasons,
		}
	}
	headline := "Filtered tool surface"
	detail := "Some tool groups are filtered out by runtime capabilities."
	status := "filtered"
	if len(disabledReasons) >= 3 {
		headline = "Restricted tool surface"
		detail = "Most tool groups are unavailable in this session."
		status = "restricted"
	}
	return toolSurface{
		Headline:        headline,
		Detail:          detail,
		Tone:            toneForToolSurface(disabledReasons, warnings),
		Status:          status,
		DisabledReasons: disabledReasons,
		Warnings:        warnings,
	}
}

func toneForToolSurface(disabledReasons, warnings []string) string {
	switch {
	case len(disabledReasons) > 0:
		return "warning"
	case len(warnings) > 0:
		return "warning"
	default:
		return "ready"
	}
}
