package main

import (
	"sort"
	"strings"

	agent "github.com/affinefoundation/affent/internal/agent"
	"github.com/affinefoundation/affent/internal/sse"
)

type runtimeToolSurfaceIndex struct {
	Groups map[string]bool
	Tools  map[string]bool
}

type toolSurfaceCapabilityAxis struct {
	Label          string
	DisabledReason string
	ConfigExpected func(Config) bool
	Enabled        func(sessionCapabilities, map[string]bool) bool
	RuntimeEnabled func(sse.RuntimeCapabilities, runtimeToolSurfaceIndex) bool
}

type runtimeCapabilityContract struct {
	Status    string   `json:"status"`
	Expected  []string `json:"expected,omitempty"`
	Available []string `json:"available,omitempty"`
	Missing   []string `json:"missing,omitempty"`
	Warnings  []string `json:"warnings,omitempty"`
}

var toolSurfaceCapabilityAxes = []toolSurfaceCapabilityAxis{
	{
		Label:          "workspace",
		DisabledReason: "Workspace tools are off.",
		ConfigExpected: func(cfg Config) bool {
			return resolveServeRuntimeCapabilities(cfg).Builtins
		},
		Enabled: func(caps sessionCapabilities, groupSeen map[string]bool) bool {
			return caps.Builtins || groupSeen["Workspace"]
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.Builtins || len(caps.WorkspaceTools) > 0 || index.Groups["Workspace"]
		},
	},
	{
		Label:          "memory/history",
		DisabledReason: "Memory and history tools are off.",
		ConfigExpected: func(cfg Config) bool {
			caps := resolveServeRuntimeCapabilities(cfg)
			return caps.Memory || (caps.WorkflowTools && cfg.EnableBuiltins)
		},
		Enabled: func(caps sessionCapabilities, groupSeen map[string]bool) bool {
			return caps.Memory || caps.SessionSearch || groupSeen["Memory"] || groupSeen["History"]
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.Memory || caps.SessionSearch || index.Tools["memory"] || index.Tools["session_search"] || index.Groups["Memory"] || index.Groups["History"]
		},
	},
	{
		Label:          "live sources",
		DisabledReason: "Live sources are off.",
		ConfigExpected: func(cfg Config) bool {
			caps := resolveServeRuntimeCapabilities(cfg)
			return caps.Web || caps.WebSearch || caps.Browser
		},
		Enabled: func(caps sessionCapabilities, groupSeen map[string]bool) bool {
			return caps.WebSearch || caps.Web || caps.Browser || caps.BrowserScreenshot || groupSeen["Research"]
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.WebSearch || caps.WebFetch || caps.Browser || index.Groups["Research"] || index.Tools["web_fetch"] || index.Tools["web_search"]
		},
	},
	{
		Label:          "nested work",
		DisabledReason: "Nested work tools are off.",
		ConfigExpected: func(cfg Config) bool {
			caps := resolveServeRuntimeCapabilities(cfg)
			return caps.Subagent || caps.FocusedTasks
		},
		Enabled: func(caps sessionCapabilities, groupSeen map[string]bool) bool {
			return caps.Subagent || caps.FocusedTasks || groupSeen["Subtasks"]
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.Subagent || caps.FocusedTasks || index.Tools[agent.SubagentToolName] || index.Tools[agent.FocusedTaskToolName] || index.Groups["Subtasks"]
		},
	},
	{
		Label:          "skills",
		DisabledReason: "Skill install tools are off.",
		ConfigExpected: func(cfg Config) bool {
			return resolveServeRuntimeCapabilities(cfg).WorkflowTools && cfg.EnableBuiltins
		},
		Enabled: func(caps sessionCapabilities, groupSeen map[string]bool) bool {
			return caps.SkillInstall || groupSeen["Skills"]
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.Skill || index.Tools["skill"] || index.Groups["Skills"]
		},
	},
	{
		Label:          "loop protocol",
		DisabledReason: "Loop protocol is off.",
		ConfigExpected: func(cfg Config) bool {
			return resolveServeRuntimeCapabilities(cfg).LoopProtocol
		},
		Enabled: func(caps sessionCapabilities, _ map[string]bool) bool {
			return caps.LoopProtocol
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.LoopProtocol || index.Tools[agent.LoopProtocolToolName]
		},
	},
	{
		Label:          "schedules",
		DisabledReason: "Session schedules are off.",
		ConfigExpected: func(cfg Config) bool {
			return resolveServeRuntimeCapabilities(cfg).SessionSchedule
		},
		Enabled: func(caps sessionCapabilities, _ map[string]bool) bool {
			return caps.SessionSchedule
		},
		RuntimeEnabled: func(caps sse.RuntimeCapabilities, index runtimeToolSurfaceIndex) bool {
			return caps.SessionSchedule || index.Tools[agent.SessionScheduleToolName]
		},
	},
}

func toolSurfaceGroupIndex(tools []toolInfo) map[string]bool {
	groupSeen := map[string]bool{}
	for _, tool := range tools {
		groupSeen[tool.Group] = true
	}
	return groupSeen
}

func runtimeToolSurfaceIndexFromTools(tools []sse.RuntimeSurfaceTool) runtimeToolSurfaceIndex {
	index := runtimeToolSurfaceIndex{
		Groups: map[string]bool{},
		Tools:  map[string]bool{},
	}
	for _, tool := range tools {
		name := strings.TrimSpace(tool.Name)
		if name != "" {
			index.Tools[name] = true
		}
		group := strings.TrimSpace(tool.Group)
		if group != "" {
			index.Groups[group] = true
		}
	}
	return index
}

func runtimeSurfaceCapabilityLabels(surface *sse.RuntimeSurfacePayload) (available, unavailable []string) {
	if !runtimeSurfaceHasCapabilityData(surface) {
		return nil, nil
	}
	index := runtimeToolSurfaceIndexFromTools(surface.Tools)
	for _, axis := range toolSurfaceCapabilityAxes {
		if axis.RuntimeEnabled(surface.Capabilities, index) {
			available = append(available, axis.Label)
		} else {
			unavailable = append(unavailable, axis.Label)
		}
	}
	return available, unavailable
}

func runtimeSurfaceHasCapabilityData(surface *sse.RuntimeSurfacePayload) bool {
	if surface == nil {
		return false
	}
	caps := surface.Capabilities
	return surface.ToolCount > 0 ||
		len(surface.Tools) > 0 ||
		len(surface.ToolCallCaps) > 0 ||
		len(surface.CompletionGuards) > 0 ||
		len(caps.WorkspaceTools) > 0 ||
		caps.Builtins ||
		caps.Memory ||
		caps.Plan ||
		caps.LoopProtocol ||
		caps.SessionSchedule ||
		caps.SessionScheduleRunner ||
		caps.SessionSearch ||
		caps.WebFetch ||
		caps.WebSearch ||
		caps.Browser ||
		caps.Subagent ||
		caps.FocusedTasks ||
		caps.Skill ||
		caps.MCP
}

func buildServeRuntimeContract(cfg Config) runtimeCapabilityContract {
	expected := expectedCapabilityLabelsForConfig(cfg)
	contract := runtimeCapabilityContract{
		Status:   "configured",
		Expected: expected,
	}
	if cfg.EvalMode {
		contract.Warnings = append(contract.Warnings, "Eval mode may intentionally narrow the runtime surface.")
	}
	return contract
}

func buildSessionRuntimeContract(sess *Session, cfg Config) runtimeCapabilityContract {
	expected := expectedCapabilityLabelsForConfig(cfg)
	if sess == nil {
		status := "unknown"
		if len(expected) > 0 {
			status = "unavailable"
		}
		return runtimeCapabilityContract{
			Status:   status,
			Expected: expected,
			Missing:  append([]string(nil), expected...),
		}
	}
	caps := summarizeActiveCapabilities(sess, cfg)
	groupSeen := map[string]bool{}
	if sess.registry != nil {
		for _, def := range sess.registry.Catalog() {
			groupSeen[def.Group] = true
		}
	}
	available := make([]string, 0, len(toolSurfaceCapabilityAxes))
	for _, axis := range toolSurfaceCapabilityAxes {
		if axis.Enabled(caps, groupSeen) {
			available = append(available, axis.Label)
		}
	}
	missing := missingExpectedCapabilities(expected, available)
	status := "ok"
	if len(missing) > 0 {
		status = "degraded"
	}
	contract := runtimeCapabilityContract{
		Status:    status,
		Expected:  expected,
		Available: available,
		Missing:   missing,
	}
	if caps.EvalMode {
		contract.Warnings = append(contract.Warnings, "Eval mode may intentionally narrow the runtime surface.")
	}
	return contract
}

func expectedCapabilityLabelsForConfig(cfg Config) []string {
	var expected []string
	for _, axis := range toolSurfaceCapabilityAxes {
		if axis.ConfigExpected != nil && axis.ConfigExpected(cfg) {
			expected = append(expected, axis.Label)
		}
	}
	return expected
}

func missingExpectedCapabilities(expected, available []string) []string {
	if len(expected) == 0 {
		return nil
	}
	seen := make(map[string]bool, len(available))
	for _, label := range available {
		seen[label] = true
	}
	var missing []string
	for _, label := range expected {
		if !seen[label] {
			missing = append(missing, label)
		}
	}
	return missing
}

func validateSessionRuntimeContract(reg *agent.Registry, cfg Config) error {
	if reg == nil {
		return nil
	}
	var missing []string
	hasTool := func(name string) bool {
		_, ok := reg.Get(name)
		return ok
	}
	if !cfg.EvalMode && !hasTool(agent.SessionScheduleToolName) {
		missing = append(missing, agent.SessionScheduleToolName)
	}
	if cfg.EnableLoopProtocol && !cfg.EvalMode && !hasTool(agent.LoopProtocolToolName) {
		missing = append(missing, agent.LoopProtocolToolName)
	}
	if len(missing) == 0 {
		return nil
	}
	sort.Strings(missing)
	return runtimeContractError{MissingTools: missing}
}

type runtimeContractError struct {
	MissingTools []string
}

func (e runtimeContractError) Error() string {
	return "runtime contract missing required tool(s): " + strings.Join(e.MissingTools, ", ")
}
