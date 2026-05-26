package agenteval

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	timelinePromptPreviewBytes = 1200
	timelineArgsPreviewBytes   = 1600
	timelineResultPreviewBytes = 2000
	timelineErrorPreviewBytes  = 1200
)

func renderDebugTimeline(res BatchResult, scenario BatchScenario, trace *Trace) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Affent Eval Timeline\n\n")
	fmt.Fprintf(&b, "- scenario: `%s`\n", res.BatchScenario)
	fmt.Fprintf(&b, "- ok: `%t`\n", res.OK)
	fmt.Fprintf(&b, "- run_exit_code: `%d`\n", res.RunExitCode)
	if res.TurnEndReason != "" {
		fmt.Fprintf(&b, "- turn_end_reason: `%s`\n", res.TurnEndReason)
	}
	if res.TracePath != "" {
		fmt.Fprintf(&b, "- trace_jsonl: `%s`\n", res.TracePath)
	}
	if res.TraceDeltas {
		fmt.Fprintf(&b, "- trace_deltas: `true`\n")
	} else {
		fmt.Fprintf(&b, "- trace_deltas: `false` (streaming deltas skipped; rerun with `--trace-deltas` for full trace)\n")
	}
	if res.FinalTextPath != "" {
		fmt.Fprintf(&b, "- final_text: `%s`\n", res.FinalTextPath)
	}
	if res.StdoutPath != "" {
		fmt.Fprintf(&b, "- stdout: `%s`\n", res.StdoutPath)
	}
	if res.StderrPath != "" {
		fmt.Fprintf(&b, "- stderr: `%s`\n", res.StderrPath)
	}
	if len(res.AffentctlCommand) > 0 {
		b.WriteString("- affentctl_command:\n")
		b.WriteString("  ```text\n")
		b.WriteString(timelineBlock(shellQuoteCommand(res.AffentctlCommand), timelineArgsPreviewBytes))
		b.WriteString("\n  ```\n")
	}
	fmt.Fprintf(&b, "- metrics: %s\n", timelineMetricsSummary(res))
	if len(res.Failures) > 0 {
		b.WriteString("\n## Failures\n\n")
		for _, failure := range res.Failures {
			fmt.Fprintf(&b, "- %s\n", timelineInline(failure, timelineErrorPreviewBytes))
		}
	}

	b.WriteString("\n## Prompt\n\n")
	b.WriteString("```text\n")
	b.WriteString(timelineBlock(scenario.Prompt, timelinePromptPreviewBytes))
	b.WriteString("\n```\n")

	if trace == nil {
		b.WriteString("\n## Trace\n\n")
		b.WriteString("Trace parsing failed or did not produce a structured trace. Open `trace.jsonl` for raw events.\n")
		return b.String()
	}

	renderTimelineTraceEvents(&b, trace)
	renderTimelineRuntimeSurface(&b, trace)
	renderTimelineLoopErrors(&b, trace)
	renderTimelineCompactions(&b, trace)
	renderTimelineDecisions(&b, trace)
	renderTimelineSourceEvidence(&b, trace)
	renderTimelineTools(&b, trace)
	renderTimelineFinal(&b, trace)
	return b.String()
}

func timelineMetricsSummary(res BatchResult) string {
	parts := []string{
		fmt.Sprintf("tools=%d", res.ToolCalls),
		fmt.Sprintf("tool_errors=%d", res.ToolStats.ToolErrors),
		fmt.Sprintf("repaired=%d", res.ToolStats.ToolArgsRepaired),
		fmt.Sprintf("canonicalized=%d", res.ToolStats.ToolNameCanonicalized),
		fmt.Sprintf("loop_guard=%d", res.ToolStats.LoopGuardInterventions),
		fmt.Sprintf("forced_no_tools=%d", res.ToolStats.ForcedNoTools),
	}
	if res.ToolStats.SourceAccessResults > 0 ||
		res.ToolStats.SourceAccessVerified > 0 ||
		res.ToolStats.SourceAccessNetwork > 0 ||
		res.ToolStats.SourceAccessDynamicPartial > 0 ||
		res.ToolStats.SourceAccessDiscoveryOnly > 0 {
		parts = append(parts, fmt.Sprintf("evidence=%d/%d_verified,network=%d,partial=%d,discovery=%d",
			res.ToolStats.SourceAccessVerified,
			res.ToolStats.SourceAccessResults,
			res.ToolStats.SourceAccessNetwork,
			res.ToolStats.SourceAccessDynamicPartial,
			res.ToolStats.SourceAccessDiscoveryOnly,
		))
	}
	if res.ToolStats.MemoryUpdates > 0 ||
		res.ToolStats.MemoryUpdateAdd > 0 ||
		res.ToolStats.MemoryUpdateReplace > 0 ||
		res.ToolStats.MemoryUpdateRemove > 0 {
		parts = append(parts, fmt.Sprintf("memory_updates=%d(add:%d,replace:%d,remove:%d)",
			res.ToolStats.MemoryUpdates,
			res.ToolStats.MemoryUpdateAdd,
			res.ToolStats.MemoryUpdateReplace,
			res.ToolStats.MemoryUpdateRemove,
		))
	}
	if res.ToolStats.ToolContextTruncated > 0 || res.ToolStats.ToolContextOmittedBytes > 0 {
		parts = append(parts, fmt.Sprintf("tool_context_trunc=%d,omitted=%d",
			res.ToolStats.ToolContextTruncated,
			res.ToolStats.ToolContextOmittedBytes,
		))
	}
	if res.ContextCompactions.Count > 0 {
		parts = append(parts, fmt.Sprintf("compactions=%d,reactive=%d,removed=%d,summary_bytes=%d",
			res.ContextCompactions.Count,
			res.ContextCompactions.Reactive,
			res.ContextCompactions.RemovedMessages,
			res.ContextCompactions.SummaryBytes,
		))
	}
	parts = append(parts, fmt.Sprintf("tokens=%d/%d", res.Usage.InputTokens, res.Usage.OutputTokens))
	return strings.Join(parts, " ")
}

func renderTimelineTraceEvents(b *strings.Builder, trace *Trace) {
	if len(trace.RawTypes) == 0 {
		return
	}
	b.WriteString("\n## Trace Events\n\n")
	keys := make([]string, 0, len(trace.RawTypes))
	total := 0
	for typ, count := range trace.RawTypes {
		if typ == "" || count <= 0 {
			continue
		}
		keys = append(keys, typ)
		total += count
	}
	sort.Strings(keys)
	fmt.Fprintf(b, "- total: `%d`\n", total)
	for _, typ := range keys {
		fmt.Fprintf(b, "- `%s`: `%d`\n", typ, trace.RawTypes[typ])
	}
}

func renderTimelineRuntimeSurface(b *strings.Builder, trace *Trace) {
	if len(trace.RuntimeSurfaces) == 0 {
		return
	}
	surface := trace.RuntimeSurfaces[len(trace.RuntimeSurfaces)-1]
	b.WriteString("\n## Runtime Surface\n\n")
	fmt.Fprintf(b, "- turn_id: `%s`\n", surface.TurnID)
	fmt.Fprintf(b, "- tool_count: `%d`\n", surface.ToolCount)
	var names []string
	for _, tool := range surface.Tools {
		if tool.Name != "" {
			names = append(names, tool.Name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		fmt.Fprintf(b, "- tools: `%s`\n", strings.Join(names, "`, `"))
	}
	var caps []string
	if surface.Capabilities.Builtins {
		caps = append(caps, "workspace_tools")
	} else if len(surface.Capabilities.WorkspaceTools) > 0 {
		caps = append(caps, "workspace:"+strings.Join(surface.Capabilities.WorkspaceTools, ","))
	}
	if surface.Capabilities.WebFetch {
		caps = append(caps, "web_fetch")
	}
	if surface.Capabilities.WebSearch {
		caps = append(caps, "web_search")
	}
	if surface.Capabilities.Browser {
		caps = append(caps, "browser")
	}
	if surface.Capabilities.Memory {
		caps = append(caps, "memory")
	}
	if surface.Capabilities.Plan {
		caps = append(caps, "plan")
	}
	if surface.Capabilities.Subagent {
		caps = append(caps, "subagent")
	}
	if surface.Capabilities.FocusedTasks {
		caps = append(caps, "focused_tasks")
	}
	if len(caps) > 0 {
		fmt.Fprintf(b, "- capabilities: `%s`\n", strings.Join(caps, "`, `"))
	}
}

func renderTimelineLoopErrors(b *strings.Builder, trace *Trace) {
	if len(trace.LoopErrors) == 0 && len(trace.RuntimeErrors) == 0 {
		return
	}
	b.WriteString("\n## Runtime Errors\n\n")
	if len(trace.RuntimeErrors) > 0 {
		for _, err := range trace.RuntimeErrors {
			if err.Kind != "" {
				fmt.Fprintf(b, "- kind=`%s` %s\n", err.Kind, timelineInline(err.Message, timelineErrorPreviewBytes))
			} else {
				fmt.Fprintf(b, "- %s\n", timelineInline(err.Message, timelineErrorPreviewBytes))
			}
		}
	}
	for _, err := range trace.LoopErrors {
		fmt.Fprintf(b, "- %s\n", timelineInline(err, timelineErrorPreviewBytes))
	}
}

func renderTimelineCompactions(b *strings.Builder, trace *Trace) {
	if len(trace.ContextCompactions) == 0 {
		return
	}
	b.WriteString("\n## Context Compactions\n\n")
	for i, c := range trace.ContextCompactions {
		fmt.Fprintf(b, "%d. turn=`%s` reactive=`%t` messages=%d->%d removed=%d summary_bytes=%d reason=%s\n",
			i+1,
			c.TurnID,
			c.Reactive,
			c.BeforeMessages,
			c.AfterMessages,
			c.RemovedMessages,
			c.SummaryBytes,
			timelineInline(c.Reason, 300),
		)
	}
}

func renderTimelineDecisions(b *strings.Builder, trace *Trace) {
	if len(trace.LoopDecisions) == 0 {
		return
	}
	b.WriteString("\n## Loop Decisions\n\n")
	for i, d := range trace.LoopDecisions {
		fmt.Fprintf(b, "%d. kind=`%s` decision=`%s` trigger=`%s` confidence=`%s`\n", i+1, d.Kind, d.Decision, d.Trigger, d.Confidence)
		if d.Reason != "" {
			fmt.Fprintf(b, "   reason: %s\n", timelineInline(d.Reason, 600))
		}
		if d.RequiredAction != "" {
			fmt.Fprintf(b, "   required_action: %s\n", timelineInline(d.RequiredAction, 600))
		}
	}
}

func renderTimelineSourceEvidence(b *strings.Builder, trace *Trace) {
	type sourceEvidence struct {
		Index int
		Tool  ToolCall
		Info  sourceaccess.Info
	}
	var entries []sourceEvidence
	for i, tool := range trace.Tools {
		info, ok := sourceaccess.FirstInfoFromResult(tool.Result)
		if !ok {
			continue
		}
		entries = append(entries, sourceEvidence{Index: i + 1, Tool: tool, Info: info})
	}
	if len(entries) == 0 {
		return
	}
	b.WriteString("\n## Source Evidence\n\n")
	for i, entry := range entries {
		status := "verified"
		switch {
		case entry.Info.IsNetworkSource():
			status = "network"
		case entry.Info.IsDynamicPartial() || sourceaccess.HasDynamicPartialEvidence(entry.Tool.Result):
			status = "dynamic_partial"
		case entry.Info.IsDiscoveryOnly():
			status = "discovery_only"
		}
		url := entry.Info.AccessedURL
		if url == "" {
			url = "(unknown)"
		}
		fmt.Fprintf(b, "%d. tool#%d `%s` status=`%s` url=`%s`", i+1, entry.Index, entry.Tool.Tool, status, url)
		if entry.Info.RequestedURL != "" {
			fmt.Fprintf(b, " requested=`%s`", entry.Info.RequestedURL)
		}
		if entry.Info.JSONPath != "" {
			fmt.Fprintf(b, " json_path=`%s`", entry.Info.JSONPath)
		}
		if entry.Tool.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", entry.Tool.CallID)
		}
		b.WriteByte('\n')
	}
}

func renderTimelineTools(b *strings.Builder, trace *Trace) {
	b.WriteString("\n## Tool Timeline\n\n")
	if len(trace.Tools) == 0 {
		b.WriteString("No tool calls were recorded.\n")
		return
	}
	for i, tool := range trace.Tools {
		status := "ok"
		if tool.ExitCode != 0 || len(tool.FailureKinds) > 0 || tool.IsErr {
			status = "error"
		}
		fmt.Fprintf(b, "### %d. `%s` (%s)\n\n", i+1, tool.Tool, status)
		if tool.CallID != "" {
			fmt.Fprintf(b, "- call_id: `%s`\n", tool.CallID)
		}
		if tool.TurnID != "" {
			fmt.Fprintf(b, "- turn_id: `%s`\n", tool.TurnID)
		}
		fmt.Fprintf(b, "- exit_code: `%d`\n", tool.ExitCode)
		if tool.DurationMS > 0 {
			fmt.Fprintf(b, "- duration_ms: `%d`\n", tool.DurationMS)
		}
		if tool.Canonicalized && tool.OriginalTool != "" {
			fmt.Fprintf(b, "- canonicalized_from: `%s`\n", tool.OriginalTool)
		}
		if tool.ArgsRepaired || len(tool.RepairNotes) > 0 {
			fmt.Fprintf(b, "- args_repaired: `%t`\n", tool.ArgsRepaired)
			for _, note := range tool.RepairNotes {
				fmt.Fprintf(b, "  - repair_note: %s\n", timelineInline(note, 400))
			}
		}
		if len(tool.FailureKinds) > 0 {
			fmt.Fprintf(b, "- failure_kinds: `%s`\n", strings.Join(tool.FailureKinds, "`, `"))
		} else if tool.FailureKind != "" {
			fmt.Fprintf(b, "- failure_kind: `%s`\n", tool.FailureKind)
		}
		if tool.ArgsTruncated {
			fmt.Fprintf(b, "- args_truncated: true bytes=%d omitted=%d cap=%d\n", tool.ArgsBytes, tool.ArgsOmittedBytes, tool.ArgsCapBytes)
		}
		if tool.ResultTruncated {
			fmt.Fprintf(b, "- result_truncated: true bytes=%d omitted=%d cap=%d\n", tool.ResultBytes, tool.ResultOmittedBytes, tool.ResultCapBytes)
		}
		if tool.ResultArtifactPath != "" {
			fmt.Fprintf(b, "- result_artifact: `%s`\n", tool.ResultArtifactPath)
		}
		b.WriteString("\nargs:\n\n```json\n")
		b.WriteString(timelineJSON(tool.Args, timelineArgsPreviewBytes))
		b.WriteString("\n```\n\nresult preview:\n\n```text\n")
		b.WriteString(timelineBlock(tool.Result, timelineResultPreviewBytes))
		b.WriteString("\n```\n\n")
	}
}

func renderTimelineFinal(b *strings.Builder, trace *Trace) {
	b.WriteString("\n## Final Message\n\n")
	if trace.FinishReason != "" {
		fmt.Fprintf(b, "- finish_reason: `%s`\n", trace.FinishReason)
	}
	b.WriteString("\n```text\n")
	b.WriteString(timelineBlock(trace.FinalText, timelineResultPreviewBytes))
	b.WriteString("\n```\n")
}

func timelineJSON(v any, maxBytes int) string {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return timelineBlock(fmt.Sprint(v), maxBytes)
	}
	return timelineBlock(string(raw), maxBytes)
}

func timelineBlock(s string, maxBytes int) string {
	if strings.TrimSpace(s) == "" {
		return "(empty)"
	}
	return textutil.Preview(s, maxBytes, "\n[... omitted ...]")
}

func timelineInline(s string, maxBytes int) string {
	return textutil.CompactWhitespace(textutil.Preview(s, maxBytes, "..."))
}

func shellQuoteCommand(args []string) string {
	if len(args) == 0 {
		return ""
	}
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, shellQuoteToken(arg))
	}
	return strings.Join(quoted, " ")
}

func shellQuoteToken(s string) string {
	if s == "" {
		return "''"
	}
	if strings.ContainsAny(s, " \t\n'\"\\$`!&|;()<>*?[#~=%") {
		return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
	}
	return s
}
