package agenteval

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/affinefoundation/affent/internal/sourceaccess"
	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

const (
	timelinePromptPreviewBytes = 1200
	timelineArgsPreviewBytes   = 1600
	timelineResultPreviewBytes = 2000
	timelineErrorPreviewBytes  = 1200
	timelineMemoryPreviewBytes = 220
)

type timelineMemoryUpdate struct {
	Index    int
	Tool     ToolCall
	Action   string
	Location string
	Preview  string
}

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
	if workspace := traceWorkspaceForManifest(res); workspace != "" {
		fmt.Fprintf(&b, "- trace_workspace: `%s`\n", workspace)
	}
	if root := childTranscriptRootForManifest(res); root != "" {
		fmt.Fprintf(&b, "- child_transcript_root: `%s`\n", root)
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
	renderTimelineVerifier(&b, res.Verifier)
	renderTimelineRecoveryGuide(&b, res)
	renderTimelineDebugBrief(&b, res)
	renderTimelineChildTranscripts(&b, res.ChildTranscripts)
	renderTimelineScenarioExpectations(&b, scenario, res.OK)

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
	renderTimelineConversationRepairs(&b, trace)
	renderTimelineRuntimeSurface(&b, trace)
	renderTimelineContextInjections(&b, trace)
	renderTimelineLoopErrors(&b, trace)
	renderTimelineToolRepair(&b, trace)
	renderTimelineLoopGuard(&b, trace)
	renderTimelineCompactions(&b, trace)
	renderTimelineDecisions(&b, trace)
	renderTimelineMessageRejections(&b, trace)
	renderTimelineSourceEvidence(&b, trace)
	renderTimelineBrowserScrolls(&b, trace)
	renderTimelineBrowserNetworkSearches(&b, trace)
	renderTimelinePlan(&b, trace)
	renderTimelineMemoryUpdates(&b, trace)
	renderTimelineSessionSearch(&b, trace)
	renderTimelineToolTruncation(&b, trace)
	renderTimelineTools(&b, trace)
	renderTimelineFinal(&b, trace)
	return b.String()
}

func renderTimelineVerifier(b *strings.Builder, verifier VerifierResult) {
	if !verifier.Ran && strings.TrimSpace(verifier.Command) == "" {
		return
	}
	b.WriteString("\n## Verifier\n\n")
	status := "not_run"
	if verifier.Ran {
		if verifier.OK {
			status = "passed"
		} else {
			status = "failed"
		}
	}
	fmt.Fprintf(b, "- status: `%s`\n", status)
	if verifier.Command != "" {
		fmt.Fprintf(b, "- command: `%s`\n", timelineInline(verifier.Command, timelineArgsPreviewBytes))
	}
	if !verifier.Ran {
		return
	}
	fmt.Fprintf(b, "- exit_code: `%d`\n", verifier.ExitCode)
	fmt.Fprintf(b, "- duration_ms: `%d`\n", verifier.Duration.Milliseconds())
	fmt.Fprintf(b, "- output_bytes: `%d`\n", verifier.OutputBytes)
	fmt.Fprintf(b, "- output_truncated: `%t`\n", verifier.OutputTruncated)
	if verifier.OutputTruncated || verifier.OutputOmittedBytes > 0 {
		fmt.Fprintf(b, "- output_omitted_bytes: `%d`\n", verifier.OutputOmittedBytes)
	}
	if verifier.OutputCapBytes > 0 {
		fmt.Fprintf(b, "- output_cap_bytes: `%d`\n", verifier.OutputCapBytes)
	}
}

func renderTimelineRecoveryGuide(b *strings.Builder, res BatchResult) {
	guide := BuildDebugRecoveryGuide(res)
	if guide == nil {
		return
	}
	b.WriteString("\n## Recovery Guide\n\n")
	if guide.Summary != "" {
		fmt.Fprintf(b, "- summary: %s\n", timelineInline(guide.Summary, timelineErrorPreviewBytes))
	}
	if len(guide.Inspect) > 0 {
		b.WriteString("- inspect_order:\n")
		for _, item := range guide.Inspect {
			fmt.Fprintf(b, "  - `%s`\n", timelineInline(item, timelineArgsPreviewBytes))
		}
	}
	if len(guide.ExactRerunCommand) > 0 {
		b.WriteString("- exact_rerun_command:\n")
		b.WriteString("  ```text\n")
		b.WriteString(timelineBlock(shellQuoteCommand(guide.ExactRerunCommand), timelineArgsPreviewBytes))
		b.WriteString("\n  ```\n")
	}
	if len(guide.FullTraceRerunCommand) > 0 {
		b.WriteString("- full_trace_rerun_command:\n")
		b.WriteString("  ```text\n")
		b.WriteString(timelineBlock(shellQuoteCommand(guide.FullTraceRerunCommand), timelineArgsPreviewBytes))
		b.WriteString("\n  ```\n")
	}
	if guide.ContinuePrompt != "" {
		fmt.Fprintf(b, "- continue_prompt: %s\n", timelineInline(guide.ContinuePrompt, timelineResultPreviewBytes))
	}
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
	if res.ToolStats.ToolRequestsAdmitted > 0 || res.ToolStats.ToolRequestsSkipped > 0 {
		parts = append(parts, fmt.Sprintf("tool_admitted=%d,skipped=%d", res.ToolStats.ToolRequestsAdmitted, res.ToolStats.ToolRequestsSkipped))
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
	if res.ToolStats.MemorySearchCalls > 0 || res.ToolStats.MemorySearchMisses > 0 {
		parts = append(parts, fmt.Sprintf("memory_search=calls:%d,misses:%d",
			res.ToolStats.MemorySearchCalls,
			res.ToolStats.MemorySearchMisses,
		))
	}
	if res.ToolStats.SessionSearchCalls > 0 ||
		res.ToolStats.SessionSearchResults > 0 ||
		res.ToolStats.SessionSearchContextHits > 0 ||
		res.ToolStats.SessionSearchMatchedTerms > 0 ||
		res.ToolStats.SessionSearchRecent > 0 {
		recent := ""
		if res.ToolStats.SessionSearchRecent > 0 {
			recent = fmt.Sprintf(",recent:%d", res.ToolStats.SessionSearchRecent)
		}
		parts = append(parts, fmt.Sprintf("session_search=calls:%d,results:%d,context:%d,terms:%d%s,terms_per_call:%s",
			res.ToolStats.SessionSearchCalls,
			res.ToolStats.SessionSearchResults,
			res.ToolStats.SessionSearchContextHits,
			res.ToolStats.SessionSearchMatchedTerms,
			recent,
			timelineOptionalNumber(timelineOptionalRatio(res.ToolStats.SessionSearchMatchedTerms, res.ToolStats.SessionSearchCalls)),
		))
	}
	if res.ToolStats.ToolContextTruncated > 0 || res.ToolStats.ToolContextOmittedBytes > 0 {
		parts = append(parts, fmt.Sprintf("tool_context_trunc=%d,omitted=%d",
			res.ToolStats.ToolContextTruncated,
			res.ToolStats.ToolContextOmittedBytes,
		))
	}
	if res.ContextCompactions.Count > 0 {
		parts = append(parts, fmt.Sprintf("compactions=%d,reactive=%d,removed=%d,reduced_bytes=%d,summary_bytes=%d,summary_missing=%d,summary_empty=%d",
			res.ContextCompactions.Count,
			res.ContextCompactions.Reactive,
			res.ContextCompactions.RemovedMessages,
			res.ContextCompactions.ReducedBytes,
			res.ContextCompactions.SummaryBytes,
			res.ContextCompactions.SummaryMissing,
			res.ContextCompactions.SummaryEmpty,
		))
	}
	if res.ContextInjections.Count > 0 {
		parts = append(parts, fmt.Sprintf("context_injections=%d,bytes=%d,est_tokens=%d",
			res.ContextInjections.Count,
			res.ContextInjections.Bytes,
			res.ContextInjections.EstimatedTokens,
		))
	}
	if res.LoopTurnCheckpoints.Count > 0 {
		parts = append(parts, fmt.Sprintf("loop_turn_checkpoints=%d", res.LoopTurnCheckpoints.Count))
	}
	if res.LoopProtocolCalibrations.Count > 0 {
		parts = append(parts, fmt.Sprintf("loop_calibrations=%d", res.LoopProtocolCalibrations.Count))
	}
	if res.LoopProtocolCalibrationRequests.Count > 0 {
		parts = append(parts, fmt.Sprintf("loop_calibration_requests=%d", res.LoopProtocolCalibrationRequests.Count))
	}
	parts = append(parts, fmt.Sprintf("tokens=%d/%d", res.Usage.InputTokens, res.Usage.OutputTokens))
	return strings.Join(parts, " ")
}

func renderTimelineDebugBrief(b *strings.Builder, res BatchResult) {
	if !hasTimelineDebugBrief(res) {
		return
	}
	b.WriteString("\n## Debug Brief\n\n")
	if !res.OK {
		b.WriteString("- outcome: `failed`; inspect the failure list, then the sections named below.\n")
	}
	if res.TurnEndReason != "" && res.TurnEndReason != "completed" {
		fmt.Fprintf(b, "- turn_end: `%s`; inspect Final Message and Tool Timeline before rerunning.\n", res.TurnEndReason)
	}
	if len(res.ToolStats.ToolFailureByKind) > 0 {
		fmt.Fprintf(b, "- tool_failure_by_kind: `%s`; inspect Tool Timeline entries with matching `failure_kinds`.\n", timelineCounts(res.ToolStats.ToolFailureByKind))
	}
	for _, line := range timelineToolFailureExampleLines(res.ToolFailureExamples, 2) {
		fmt.Fprintf(b, "- %s\n", line)
	}
	if len(res.RuntimeErrorByKind) > 0 {
		fmt.Fprintf(b, "- runtime_error_by_kind: `%s`; inspect Runtime Errors and provider/server logs.\n", timelineCounts(res.RuntimeErrorByKind))
	}
	for _, line := range timelineRuntimeErrorExampleLines(res.RuntimeErrorExamples, 2) {
		fmt.Fprintf(b, "- %s\n", line)
	}
	if res.ToolStats.LoopGuardInterventions > 0 {
		fmt.Fprintf(b, "- loop_guard: `%d` intervention(s), `%d` forced no-tools; inspect Loop Guard, Loop Decisions, and latest tool guidance.\n", res.ToolStats.LoopGuardInterventions, res.ToolStats.ForcedNoTools)
	}
	if res.Delegation.HasAny() {
		fmt.Fprintf(b, "- delegation: focused_tasks=`%d`, focused_task_errors=`%d`, focused_task_incomplete=`%d`, subagents=`%d`, subagent_errors=`%d`, subagent_incomplete=`%d`; inspect child transcripts and parent merge quality.\n",
			res.Delegation.FocusedTaskCalls,
			res.Delegation.FocusedTaskErrors,
			res.Delegation.FocusedTaskIncomplete,
			res.Delegation.SubagentCalls,
			res.Delegation.SubagentErrors,
			res.Delegation.SubagentIncomplete,
		)
	}
	if len(res.ChildTranscripts) > 0 {
		fmt.Fprintf(b, "- child_transcripts: `%d` indexed; inspect Child Transcripts for isolated child work.\n", len(res.ChildTranscripts))
	}
	if res.Plan.HasAny() {
		fmt.Fprintf(b, "- plan: calls=`%d`, errors=`%d`, actions=`%s`; inspect plan state if resume quality drifted.\n",
			res.Plan.Calls,
			res.Plan.Errors,
			timelineCounts(res.Plan.ByAction),
		)
	}
	if res.ToolStats.SourceAccessResults > 0 {
		fmt.Fprintf(b, "- evidence: `%d/%d` verified, network=`%d`, partial=`%d`, discovery=`%d`; inspect Source Evidence before trusting the final answer.\n",
			res.ToolStats.SourceAccessVerified,
			res.ToolStats.SourceAccessResults,
			res.ToolStats.SourceAccessNetwork,
			res.ToolStats.SourceAccessDynamicPartial,
			res.ToolStats.SourceAccessDiscoveryOnly,
		)
	}
	if res.ToolStats.SessionSearchCalls > 0 || res.ToolStats.SessionSearchResults > 0 || res.ToolStats.SessionSearchRecent > 0 {
		tone := "recall"
		guidance := "inspect Session Search examples before trusting recovered state."
		if res.ToolStats.SessionSearchCalls > 0 && res.ToolStats.SessionSearchResults == 0 {
			tone = "empty_recall"
			if res.ToolStats.SessionSearchRecent > 0 {
				guidance = "no direct prior-session evidence matched; retry from recent Session Search anchors."
			} else {
				guidance = "recovery found no prior-session evidence."
			}
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchContextHits == 0 {
			tone = "recall_no_context"
			guidance = "hits lacked adjacent context or persisted task-state anchors; inspect Session Search examples for stale or shallow recovery."
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchMatchedTerms == 0 {
			tone = "recall_no_terms"
			guidance = "hits lacked matched terms; inspect Session Search examples before trusting recovery."
		} else if res.ToolStats.SessionSearchResults > 0 && res.ToolStats.SessionSearchContextHits < res.ToolStats.SessionSearchResults {
			tone = "recall_weak_context"
			guidance = "only some hits included adjacent context or persisted task-state anchors; inspect Session Search examples for incomplete recovery."
		}
		recent := ""
		if res.ToolStats.SessionSearchRecent > 0 {
			recent = fmt.Sprintf(", recent=`%d`", res.ToolStats.SessionSearchRecent)
		}
		fmt.Fprintf(b, "- %s: calls=`%d`, results=`%d`, context=`%d`, terms=`%d`%s; %s\n",
			tone,
			res.ToolStats.SessionSearchCalls,
			res.ToolStats.SessionSearchResults,
			res.ToolStats.SessionSearchContextHits,
			res.ToolStats.SessionSearchMatchedTerms,
			recent,
			guidance,
		)
	}
	if res.ContextCompactions.Count > 0 {
		extra := ""
		if res.ContextCompactions.SummaryMissing > 0 || res.ContextCompactions.SummaryEmpty > 0 {
			extra = fmt.Sprintf(", summary_missing=`%d`, summary_empty=`%d`", res.ContextCompactions.SummaryMissing, res.ContextCompactions.SummaryEmpty)
		}
		fmt.Fprintf(b, "- context: compactions=`%d`, reactive=`%d`, removed_messages=`%d`, reduced_bytes=`%d`, summary_bytes=`%d`%s; inspect Context Compactions for possible state loss.\n",
			res.ContextCompactions.Count,
			res.ContextCompactions.Reactive,
			res.ContextCompactions.RemovedMessages,
			res.ContextCompactions.ReducedBytes,
			res.ContextCompactions.SummaryBytes,
			extra,
		)
	}
	if hasDebugBriefTruncation(res) {
		contextTruncated := max(res.ToolStats.ToolContextTruncated, res.ToolTruncation.ContextTruncated)
		contextOmittedBytes := max(res.ToolStats.ToolContextOmittedBytes, res.ToolTruncation.ContextOmittedBytes)
		missingArtifacts := res.ToolTruncation.ResultMissingArtifacts + res.ToolTruncation.ContextMissingArtifacts
		if missingArtifacts == 0 && res.ToolTruncation.ResultsTruncated > res.ToolTruncation.ResultArtifacts {
			missingArtifacts = res.ToolTruncation.ResultsTruncated - res.ToolTruncation.ResultArtifacts
		}
		fmt.Fprintf(b, "- truncation: tool_context=%d omitted_context=%d args=%d args_omitted=%d results=%d results_omitted=%d artifacts=%d context_artifacts=%d missing_artifacts=%d; inspect Tool Truncation, artifacts, and capped tool outputs.\n",
			contextTruncated,
			contextOmittedBytes,
			res.ToolTruncation.ArgsTruncated,
			res.ToolTruncation.ArgsOmittedBytes,
			res.ToolTruncation.ResultsTruncated,
			res.ToolTruncation.ResultsOmittedBytes,
			res.ToolTruncation.ResultArtifacts,
			res.ToolTruncation.ContextArtifacts,
			missingArtifacts,
		)
	}
}

func renderTimelineChildTranscripts(b *strings.Builder, refs []DebugTranscriptRef) {
	if len(refs) == 0 {
		return
	}
	b.WriteString("\n## Child Transcripts\n\n")
	for _, ref := range refs {
		fmt.Fprintf(b, "- kind=`%s` path=`%s` bytes=`%d`\n", ref.Kind, ref.Path, ref.Bytes)
	}
}

func renderTimelineScenarioExpectations(b *strings.Builder, scenario BatchScenario, ok bool) {
	exp := debugScenarioExpectations(scenario)
	if !hasTimelineScenarioExpectations(exp) {
		return
	}
	b.WriteString("\n## Scenario Expectations\n\n")
	if caps := ExpectationCapabilityNames(exp); len(caps) > 0 {
		fmt.Fprintf(b, "- expectation_capabilities: `%s`", strings.Join(caps, "`, `"))
		if outcome := ExpectationCapabilityOutcome(ok, caps); outcome != "" {
			fmt.Fprintf(b, " outcome=`%s`", outcome)
		}
		b.WriteString("\n")
	}
	if len(exp.Suites) > 0 {
		fmt.Fprintf(b, "- suites: `%s`\n", strings.Join(exp.Suites, "`, `"))
	}
	if len(exp.Domains) > 0 {
		fmt.Fprintf(b, "- domains: `%s`\n", strings.Join(exp.Domains, "`, `"))
	}
	if exp.SessionID != "" || exp.ExecutePlan || exp.EnableMemory || exp.RequiredTurnEndReason != "" || exp.MaxTurns > 0 || exp.RuntimeMaxTurnInputTokens > 0 || exp.MaxLoopTurnInputTokens > 0 || exp.MaxLoopTurnTotalTokens > 0 || exp.CompactTrigger > 0 || exp.CompactTriggerInputTokens != 0 || exp.ModelContextWindowTokens > 0 || exp.CompactTriggerInputPercent > 0 || exp.CompactKeepLast > 0 {
		var parts []string
		if exp.SessionID != "" {
			parts = append(parts, fmt.Sprintf("session_id=%s", exp.SessionID))
		}
		if exp.ExecutePlan {
			parts = append(parts, "execute_plan=true")
		}
		if exp.EnableMemory {
			parts = append(parts, "enable_memory=true")
		}
		if exp.RequiredTurnEndReason != "" {
			parts = append(parts, fmt.Sprintf("required_turn_end_reason=%s", exp.RequiredTurnEndReason))
		}
		if exp.MaxTurns > 0 {
			parts = append(parts, fmt.Sprintf("max_turns=%d", exp.MaxTurns))
		}
		if exp.RuntimeMaxTurnInputTokens > 0 {
			parts = append(parts, fmt.Sprintf("runtime_max_turn_input_tokens=%d", exp.RuntimeMaxTurnInputTokens))
		}
		if exp.MaxLoopTurnInputTokens > 0 {
			parts = append(parts, fmt.Sprintf("max_loop_turn_input_tokens=%d", exp.MaxLoopTurnInputTokens))
		}
		if exp.MaxLoopTurnTotalTokens > 0 {
			parts = append(parts, fmt.Sprintf("max_loop_turn_total_tokens=%d", exp.MaxLoopTurnTotalTokens))
		}
		if exp.CompactTrigger > 0 {
			parts = append(parts, fmt.Sprintf("compact_trigger=%d", exp.CompactTrigger))
		}
		if exp.CompactTriggerInputTokens != 0 {
			parts = append(parts, fmt.Sprintf("compact_trigger_input_tokens=%d", exp.CompactTriggerInputTokens))
		}
		if exp.ModelContextWindowTokens > 0 {
			parts = append(parts, fmt.Sprintf("model_context_window_tokens=%d", exp.ModelContextWindowTokens))
		}
		if exp.CompactTriggerInputPercent > 0 {
			parts = append(parts, fmt.Sprintf("compact_trigger_input_percent=%d", exp.CompactTriggerInputPercent))
		}
		if exp.CompactKeepLast > 0 {
			parts = append(parts, fmt.Sprintf("compact_keep_last=%d", exp.CompactKeepLast))
		}
		fmt.Fprintf(b, "- runtime: `%s`\n", strings.Join(parts, " "))
	}
	if exp.VerifyCommand != "" {
		fmt.Fprintf(b, "- verify_command: `%s`\n", timelineInline(exp.VerifyCommand, timelineArgsPreviewBytes))
	}
	if exp.SourceRepoURL != "" {
		var parts []string
		parts = append(parts, fmt.Sprintf("url=%s", exp.SourceRepoURL))
		if exp.SourceRepoRef != "" {
			parts = append(parts, fmt.Sprintf("ref=%s", exp.SourceRepoRef))
		}
		if exp.SourceRepoDir != "" {
			parts = append(parts, fmt.Sprintf("dir=%s", exp.SourceRepoDir))
		}
		fmt.Fprintf(b, "- source_repo: `%s`\n", timelineInline(strings.Join(parts, " "), timelineArgsPreviewBytes))
	}
	if exp.ExpectedSkill != "" {
		fmt.Fprintf(b, "- expected_skill: `%s`\n", timelineInline(exp.ExpectedSkill, timelineArgsPreviewBytes))
	}
	writeTimelineStringList(b, "checks", exp.CheckNames)
	writeTimelineStringList(b, "required_tools", exp.RequiredTools)
	writeTimelineStringList(b, "forbidden_tools", exp.ForbiddenTools)
	writeTimelineStringList(b, "required_commands", exp.RequiredCommands)
	writeTimelineStringList(b, "forbidden_commands", exp.ForbiddenCommands)
	if exp.ForbidWorkspaceAbsolutePaths {
		fmt.Fprintf(b, "- forbid_workspace_absolute_paths: `true` (workspace tool args, including child transcripts)\n")
	}
	writeTimelineCountsLine(b, "required_tool_counts", exp.RequiredToolCounts)
	writeTimelineCountsLine(b, "required_command_counts", exp.RequiredCommandCounts)
	writeTimelineToolOrders(b, "required_tool_order", exp.RequiredToolOrder)
	writeTimelineCommandOrders(b, "required_command_order", exp.RequiredCommandOrder)
	writeTimelineCommandToolOrders(b, "required_command_before_tool", exp.RequiredCommandBeforeTool)
	writeTimelineCommandToolOrders(b, "required_command_after_tool", exp.RequiredCommandAfterTool)
	writeTimelineCountsLine(b, "required_tool_failure_kind_counts", exp.RequiredToolFailureKindCounts)
	writeTimelineCountsLine(b, "max_tool_failure_kind_counts", exp.MaxToolFailureKindCounts)
	writeTimelineCountsLine(b, "required_tool_stats_at_least", exp.RequiredToolStatsAtLeast)
	writeTimelineCountsLine(b, "required_trace_event_counts", exp.RequiredTraceEventCounts)
	writeTimelineCountsLine(b, "required_user_message_modes", exp.RequiredUserMessageModes)
	if strings.TrimSpace(exp.RequiredTaskStateRequestMode) != "" {
		fmt.Fprintf(b, "- required_task_state_request_mode: `%s`\n", timelineInline(exp.RequiredTaskStateRequestMode, timelineArgsPreviewBytes))
	}
	if strings.TrimSpace(exp.RequiredTaskStateRequestSource) != "" {
		fmt.Fprintf(b, "- required_task_state_request_source: `%s`\n", timelineInline(exp.RequiredTaskStateRequestSource, timelineArgsPreviewBytes))
	}
	if strings.TrimSpace(exp.RequiredTaskStateScheduleID) != "" {
		fmt.Fprintf(b, "- required_task_state_schedule_id: `%s`\n", timelineInline(exp.RequiredTaskStateScheduleID, timelineArgsPreviewBytes))
	}
	if strings.TrimSpace(exp.RequiredTaskStateScheduleKind) != "" {
		fmt.Fprintf(b, "- required_task_state_schedule_kind: `%s`\n", timelineInline(exp.RequiredTaskStateScheduleKind, timelineArgsPreviewBytes))
	}
	writeTimelineTaskStateActions(b, "required_task_state_attempted_actions", exp.RequiredTaskStateAttemptedActions)
	writeTimelineTaskStateChangedFiles(b, "required_task_state_changed_files", exp.RequiredTaskStateChangedFiles)
	writeTimelineTaskStateEvidence(b, "required_task_state_evidence", exp.RequiredTaskStateEvidence)
	writeTimelineCountsLine(b, "required_context_injection_sources", exp.RequiredContextInjectionSources)
	writeTimelineCountsLine(b, "required_conversation_repair_stats_at_least", exp.RequiredConversationRepairStatsAtLeast)
	writeTimelineCountsLine(b, "required_conversation_repair_kinds", exp.RequiredConversationRepairKinds)
	writeTimelineCountsLine(b, "required_loop_decision_kinds", exp.RequiredLoopDecisionKinds)
	writeTimelineCountsLine(b, "required_loop_decision_results", exp.RequiredLoopDecisionResults)
	writeTimelineStringList(b, "required_completion_guards", exp.RequiredCompletionGuards)
	if exp.RequiredLoopProtocolFeeds > 0 {
		fmt.Fprintf(b, "- required_loop_protocol_feeds: `%d`\n", exp.RequiredLoopProtocolFeeds)
	}
	if exp.RequiredLoopProtocolCalibrationRequests > 0 {
		fmt.Fprintf(b, "- required_loop_protocol_calibration_requests: `%d`\n", exp.RequiredLoopProtocolCalibrationRequests)
	}
	if exp.RequiredLoopProtocolCalibrations > 0 {
		fmt.Fprintf(b, "- required_loop_protocol_calibrations: `%d`\n", exp.RequiredLoopProtocolCalibrations)
	}
	writeTimelineStringList(b, "required_loop_protocol_calibration_request_text", exp.RequiredLoopProtocolCalibrationRequestText)
	writeTimelineCountsLine(b, "required_loop_protocol_calibration_request_statuses", exp.RequiredLoopProtocolCalibrationRequestStatuses)
	writeTimelineCountsLine(b, "required_loop_protocol_calibration_statuses", exp.RequiredLoopProtocolCalibrationStatuses)
	writeTimelineCountsLine(b, "required_loop_protocol_feed_modes", exp.RequiredLoopProtocolFeedModes)
	if exp.RequireLoopProtocolFullAfterCompact {
		fmt.Fprintf(b, "- required_loop_protocol_full_after_compaction: `true`\n")
	}
	writeTimelineCountsLine(b, "required_focused_task_counts", exp.RequiredFocusedTaskCounts)
	writeTimelineCountsLine(b, "required_focused_task_source_counts", exp.RequiredFocusedTaskSourceCounts)
	writeTimelineCountsLine(b, "required_subagent_mode_counts", exp.RequiredSubagentModeCounts)
	writeTimelineCountsLine(b, "required_subagent_source_counts", exp.RequiredSubagentSourceCounts)
	if exp.RequireNoDelegationErrors || exp.RequireNoPlanErrors {
		var parts []string
		if exp.RequireNoDelegationErrors {
			parts = append(parts, "delegation")
		}
		if exp.RequireNoPlanErrors {
			parts = append(parts, "plan")
		}
		fmt.Fprintf(b, "- required_no_errors: `%s`\n", strings.Join(parts, " "))
	}
	if exp.RequireFinalPlanCompleted {
		fmt.Fprintf(b, "- required_final_plan_completed: `true`\n")
	}
	if len(exp.RequiredLoopDecisionMatches) > 0 {
		for _, req := range exp.RequiredLoopDecisionMatches {
			min := req.Min
			if min <= 0 {
				min = 1
			}
			var parts []string
			if req.Kind != "" {
				parts = append(parts, fmt.Sprintf("kind=%s", req.Kind))
			}
			if req.Decision != "" {
				parts = append(parts, fmt.Sprintf("decision=%s", req.Decision))
			}
			if req.Trigger != "" {
				parts = append(parts, fmt.Sprintf("trigger=%s", req.Trigger))
			}
			parts = append(parts, fmt.Sprintf("min=%d", min))
			fmt.Fprintf(b, "- required_loop_decision: `%s`\n", strings.Join(parts, " "))
		}
	}
	if len(exp.RequiredLoopProtocolFeedMatches) > 0 {
		for _, req := range exp.RequiredLoopProtocolFeedMatches {
			min := req.Min
			if min <= 0 {
				min = 1
			}
			var parts []string
			if req.Mode != "" {
				parts = append(parts, fmt.Sprintf("mode=%s", req.Mode))
			}
			if req.PlanLabelContains != "" {
				parts = append(parts, fmt.Sprintf("plan_label_contains=%s", timelineInline(req.PlanLabelContains, 160)))
			}
			if req.PlanCurrentStepStatus != "" {
				parts = append(parts, fmt.Sprintf("plan_current_step_status=%s", req.PlanCurrentStepStatus))
			}
			if req.PlanCurrentStep != "" {
				parts = append(parts, fmt.Sprintf("plan_current_step=%s", timelineInline(req.PlanCurrentStep, 160)))
			}
			if req.CurrentSituation != "" {
				parts = append(parts, fmt.Sprintf("current_situation=%s", timelineInline(req.CurrentSituation, 160)))
			}
			if req.LastTurnEndReason != "" {
				parts = append(parts, fmt.Sprintf("last_turn_end_reason=%s", req.LastTurnEndReason))
			}
			appendMinRequirement := func(name string, value int) {
				if value > 0 {
					parts = append(parts, fmt.Sprintf("%s>=%d", name, value))
				}
			}
			appendMinRequirement("last_turn_tool_requests", req.MinLastTurnToolRequests)
			appendMinRequirement("last_turn_tool_errors", req.MinLastTurnToolErrors)
			appendMinRequirement("last_turn_forced_no_tools", req.MinLastTurnForcedNoTools)
			appendMinRequirement("last_turn_memory_updates", req.MinLastTurnMemoryUpdates)
			appendMinRequirement("last_turn_memory_search_calls", req.MinLastTurnMemorySearchCalls)
			appendMinRequirement("last_turn_memory_search_misses", req.MinLastTurnMemorySearchMisses)
			appendMinRequirement("last_turn_session_search_calls", req.MinLastTurnSessionSearchCalls)
			appendMinRequirement("last_turn_loop_guards", req.MinLastTurnLoopGuards)
			if req.LastDecisionKind != "" {
				parts = append(parts, fmt.Sprintf("last_decision_kind=%s", req.LastDecisionKind))
			}
			if req.LastDecisionTrigger != "" {
				parts = append(parts, fmt.Sprintf("last_decision_trigger=%s", req.LastDecisionTrigger))
			}
			if req.LastDecision != "" {
				parts = append(parts, fmt.Sprintf("last_decision=%s", req.LastDecision))
			}
			if req.LastDecisionConfidence != "" {
				parts = append(parts, fmt.Sprintf("last_decision_confidence=%s", req.LastDecisionConfidence))
			}
			if req.LastDecisionReason != "" {
				parts = append(parts, fmt.Sprintf("last_decision_reason=%s", timelineInline(req.LastDecisionReason, 160)))
			}
			if req.LastDecisionAction != "" {
				parts = append(parts, fmt.Sprintf("last_decision_action=%s", timelineInline(req.LastDecisionAction, 160)))
			}
			parts = append(parts, fmt.Sprintf("min=%d", min))
			fmt.Fprintf(b, "- required_loop_protocol_feed: `%s`\n", strings.Join(parts, " "))
		}
	}
	writeTimelineStringSliceMap(b, "required_tool_result_text", exp.RequiredToolResultText)
	writeTimelineStringSliceMap(b, "required_context_injection_text", exp.RequiredContextInjectionText)
	if len(exp.RequiredSourceAccess) > 0 {
		for _, req := range exp.RequiredSourceAccess {
			min := req.Min
			if min <= 0 {
				min = 1
			}
			var parts []string
			if req.Status != "" {
				parts = append(parts, fmt.Sprintf("status=%s", req.Status))
			}
			if req.Tool != "" {
				parts = append(parts, fmt.Sprintf("tool=%s", req.Tool))
			}
			if req.URLContains != "" {
				parts = append(parts, fmt.Sprintf("url_contains=%s", timelineInline(req.URLContains, 160)))
			}
			if req.RequestedURLContains != "" {
				parts = append(parts, fmt.Sprintf("requested_url_contains=%s", timelineInline(req.RequestedURLContains, 160)))
			}
			if req.SourceMethod != "" {
				parts = append(parts, fmt.Sprintf("source_method=%s", req.SourceMethod))
			}
			if req.JSONPath != "" {
				parts = append(parts, fmt.Sprintf("json_path=%s", timelineInline(req.JSONPath, 160)))
			}
			parts = append(parts, fmt.Sprintf("min=%d", min))
			fmt.Fprintf(b, "- required_source_access: `%s`\n", strings.Join(parts, " "))
		}
	}
	if len(exp.RequiredSessionSearch) > 0 {
		for _, req := range exp.RequiredSessionSearch {
			min := req.Min
			if min <= 0 {
				min = 1
			}
			var parts []string
			if req.QueryContains != "" {
				parts = append(parts, fmt.Sprintf("query_contains=%s", timelineInline(req.QueryContains, 160)))
			}
			if req.SessionID != "" {
				parts = append(parts, fmt.Sprintf("session=%s", req.SessionID))
			}
			if req.SnippetContains != "" {
				parts = append(parts, fmt.Sprintf("snippet_contains=%s", timelineInline(req.SnippetContains, 160)))
			}
			if len(req.MatchedTerms) > 0 {
				parts = append(parts, fmt.Sprintf("terms=%s", strings.Join(req.MatchedTerms, ",")))
			}
			if req.ContextIncluded {
				parts = append(parts, "context=true")
			}
			if req.TurnIdx > 0 {
				parts = append(parts, fmt.Sprintf("turn=%d", req.TurnIdx))
			}
			parts = append(parts, fmt.Sprintf("min=%d", min))
			fmt.Fprintf(b, "- required_session_search: `%s`\n", strings.Join(parts, " "))
		}
	}
	if len(exp.RequiredRecentSessionSearch) > 0 {
		for _, req := range exp.RequiredRecentSessionSearch {
			min := req.Min
			if min <= 0 {
				min = 1
			}
			var parts []string
			if req.QueryContains != "" {
				parts = append(parts, fmt.Sprintf("query_contains=%s", timelineInline(req.QueryContains, 160)))
			}
			if req.SessionID != "" {
				parts = append(parts, fmt.Sprintf("recent_session=%s", req.SessionID))
			}
			if req.UserContains != "" {
				parts = append(parts, fmt.Sprintf("user_contains=%s", timelineInline(req.UserContains, 160)))
			}
			if req.AssistantContains != "" {
				parts = append(parts, fmt.Sprintf("assistant_contains=%s", timelineInline(req.AssistantContains, 160)))
			}
			if req.PlanContains != "" {
				parts = append(parts, fmt.Sprintf("plan_contains=%s", timelineInline(req.PlanContains, 160)))
			}
			if req.LoopContains != "" {
				parts = append(parts, fmt.Sprintf("loop_contains=%s", timelineInline(req.LoopContains, 160)))
			}
			if req.RecoveryContains != "" {
				parts = append(parts, fmt.Sprintf("recovery_contains=%s", timelineInline(req.RecoveryContains, 160)))
			}
			if req.MessageContains != "" {
				parts = append(parts, fmt.Sprintf("message_contains=%s", timelineInline(req.MessageContains, 160)))
			}
			parts = append(parts, fmt.Sprintf("min=%d", min))
			fmt.Fprintf(b, "- required_recent_session_search: `%s`\n", strings.Join(parts, " "))
		}
	}
	writeTimelineStringList(b, "required_final_text", exp.RequiredFinalText)
	writeTimelineStringList(b, "forbidden_final_text", exp.ForbiddenFinalText)
	writeTimelineStringList(b, "required_truncated_results", exp.RequiredTruncatedResults)
	writeTimelineStringList(b, "required_result_artifacts", exp.RequiredResultArtifacts)
	if len(exp.RequiredToolArgContains) > 0 {
		for _, req := range exp.RequiredToolArgContains {
			min := req.Min
			if min <= 0 {
				min = 1
			}
			fmt.Fprintf(b, "- required_tool_arg: `%s.%s` contains `%s` min=`%d`\n", req.Tool, req.Arg, timelineInline(req.Substring, 160), min)
		}
	}
	if len(exp.ForbiddenToolArgContains) > 0 {
		for _, req := range exp.ForbiddenToolArgContains {
			fmt.Fprintf(b, "- forbidden_tool_arg: `%s.%s` contains `%s`\n", req.Tool, req.Arg, timelineInline(req.Substring, 160))
		}
	}
	if len(exp.MaxToolArgContains) > 0 {
		for _, req := range exp.MaxToolArgContains {
			max := req.Max
			if max <= 0 {
				max = 1
			}
			fmt.Fprintf(b, "- max_tool_arg: `%s.%s` contains `%s` max=`%d`\n", req.Tool, req.Arg, timelineInline(req.Substring, 160), max)
		}
	}
	if exp.RequiredContextCompactions > 0 || exp.RequiredReactiveCompactions > 0 || len(exp.RequiredContextCompactionReasons) > 0 || exp.RequiredCompactionRemovedMsgs > 0 || exp.RequiredCompactionReducedBytes > 0 || len(exp.RequiredContextSummaryText) > 0 || len(exp.RequiredContextLoopProtocolAnchorText) > 0 {
		var parts []string
		if exp.RequiredContextCompactions > 0 {
			parts = append(parts, fmt.Sprintf("compactions>=%d", exp.RequiredContextCompactions))
		}
		if exp.RequiredReactiveCompactions > 0 {
			parts = append(parts, fmt.Sprintf("reactive>=%d", exp.RequiredReactiveCompactions))
		}
		if exp.RequiredCompactionRemovedMsgs > 0 {
			parts = append(parts, fmt.Sprintf("removed_messages>=%d", exp.RequiredCompactionRemovedMsgs))
		}
		if exp.RequiredCompactionReducedBytes > 0 {
			parts = append(parts, fmt.Sprintf("reduced_bytes>=%d", exp.RequiredCompactionReducedBytes))
		}
		if len(parts) > 0 {
			fmt.Fprintf(b, "- context_requirements: `%s`\n", strings.Join(parts, " "))
		}
		writeTimelineCountsLine(b, "required_context_compaction_reasons", exp.RequiredContextCompactionReasons)
		writeTimelineStringList(b, "context_summary_contains", exp.RequiredContextSummaryText)
		writeTimelineStringList(b, "context_loop_protocol_anchor_contains", exp.RequiredContextLoopProtocolAnchorText)
	}
	writeTimelineStringList(b, "protected_files", exp.ProtectedFiles)
	writeTimelineStringSliceMap(b, "required_file_substrings", exp.RequiredFileSubstrings)
	writeTimelineStringSliceMap(b, "forbidden_file_substrings", exp.ForbiddenFileSubstrings)
	if exp.MaxParentToolCalls > 0 {
		fmt.Fprintf(b, "- max_parent_tool_calls: `%d`\n", exp.MaxParentToolCalls)
	}
	writeTimelineCountsLine(b, "max_successful_tool_calls_by_tool", exp.MaxSuccessfulToolCallsByTool)
}

func hasTimelineScenarioExpectations(exp DebugScenarioExpectations) bool {
	return len(exp.Suites) > 0 ||
		len(exp.Domains) > 0 ||
		len(exp.CheckNames) > 0 ||
		exp.SessionID != "" ||
		exp.ExecutePlan ||
		exp.EnableMemory ||
		exp.RequiredTurnEndReason != "" ||
		exp.VerifyCommand != "" ||
		exp.SourceRepoURL != "" ||
		exp.SourceRepoRef != "" ||
		exp.SourceRepoDir != "" ||
		exp.ExpectedSkill != "" ||
		len(exp.RequiredTools) > 0 ||
		len(exp.ForbiddenTools) > 0 ||
		len(exp.RequiredCommands) > 0 ||
		len(exp.ForbiddenCommands) > 0 ||
		len(exp.RequiredCommandCounts) > 0 ||
		len(exp.RequiredCommandOrder) > 0 ||
		len(exp.RequiredToolCounts) > 0 ||
		len(exp.RequiredCommandBeforeTool) > 0 ||
		len(exp.RequiredCommandAfterTool) > 0 ||
		len(exp.RequiredToolOrder) > 0 ||
		len(exp.RequiredToolFailureKindCounts) > 0 ||
		len(exp.MaxToolFailureKindCounts) > 0 ||
		len(exp.RequiredToolStatsAtLeast) > 0 ||
		len(exp.RequiredTraceEventCounts) > 0 ||
		len(exp.RequiredUserMessageModes) > 0 ||
		strings.TrimSpace(exp.RequiredTaskStateRequestMode) != "" ||
		strings.TrimSpace(exp.RequiredTaskStateRequestSource) != "" ||
		strings.TrimSpace(exp.RequiredTaskStateScheduleID) != "" ||
		strings.TrimSpace(exp.RequiredTaskStateScheduleKind) != "" ||
		len(exp.RequiredTaskStateAttemptedActions) > 0 ||
		len(exp.RequiredTaskStateEvidence) > 0 ||
		len(exp.RequiredContextInjectionSources) > 0 ||
		len(exp.RequiredContextInjectionText) > 0 ||
		len(exp.RequiredConversationRepairStatsAtLeast) > 0 ||
		len(exp.RequiredConversationRepairKinds) > 0 ||
		len(exp.RequiredLoopDecisionKinds) > 0 ||
		len(exp.RequiredLoopDecisionResults) > 0 ||
		len(exp.RequiredLoopDecisionMatches) > 0 ||
		len(exp.RequiredCompletionGuards) > 0 ||
		exp.RequiredLoopProtocolFeeds > 0 ||
		exp.RequiredLoopProtocolCalibrationRequests > 0 ||
		exp.RequiredLoopProtocolCalibrations > 0 ||
		len(exp.RequiredLoopProtocolCalibrationRequestText) > 0 ||
		len(exp.RequiredLoopProtocolCalibrationRequestStatuses) > 0 ||
		len(exp.RequiredLoopProtocolCalibrationStatuses) > 0 ||
		len(exp.RequiredLoopProtocolFeedModes) > 0 ||
		len(exp.RequiredLoopProtocolFeedMatches) > 0 ||
		exp.RequireLoopProtocolFullAfterCompact ||
		len(exp.RequiredFocusedTaskCounts) > 0 ||
		len(exp.RequiredFocusedTaskSourceCounts) > 0 ||
		len(exp.RequiredSubagentModeCounts) > 0 ||
		len(exp.RequiredSubagentSourceCounts) > 0 ||
		exp.RequireNoDelegationErrors ||
		exp.RequireNoPlanErrors ||
		exp.RequireFinalPlanCompleted ||
		len(exp.RequiredToolResultText) > 0 ||
		len(exp.RequiredToolArgContains) > 0 ||
		len(exp.ForbiddenToolArgContains) > 0 ||
		len(exp.MaxToolArgContains) > 0 ||
		len(exp.RequiredSourceAccess) > 0 ||
		len(exp.RequiredRecentSessionSearch) > 0 ||
		len(exp.RequiredFinalText) > 0 ||
		len(exp.ForbiddenFinalText) > 0 ||
		len(exp.RequiredTruncatedResults) > 0 ||
		len(exp.RequiredResultArtifacts) > 0 ||
		exp.RequiredContextCompactions > 0 ||
		exp.RequiredReactiveCompactions > 0 ||
		len(exp.RequiredContextCompactionReasons) > 0 ||
		exp.RequiredCompactionRemovedMsgs > 0 ||
		exp.RequiredCompactionReducedBytes > 0 ||
		len(exp.RequiredContextSummaryText) > 0 ||
		len(exp.RequiredContextLoopProtocolAnchorText) > 0 ||
		len(exp.ProtectedFiles) > 0 ||
		len(exp.RequiredFileSubstrings) > 0 ||
		len(exp.ForbiddenFileSubstrings) > 0 ||
		exp.MaxParentToolCalls > 0 ||
		len(exp.MaxSuccessfulToolCallsByTool) > 0 ||
		exp.RuntimeMaxTurnInputTokens > 0 ||
		exp.MaxLoopTurnInputTokens > 0 ||
		exp.MaxLoopTurnTotalTokens > 0 ||
		exp.MaxTurns > 0 ||
		exp.CompactTrigger > 0 ||
		exp.CompactTriggerInputTokens != 0 ||
		exp.ModelContextWindowTokens > 0 ||
		exp.CompactTriggerInputPercent > 0 ||
		exp.CompactKeepLast > 0
}

func writeTimelineStringList(b *strings.Builder, label string, values []string) {
	if len(values) == 0 {
		return
	}
	preview := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		preview = append(preview, timelineInline(value, 180))
	}
	if len(preview) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s: `%s`\n", label, strings.Join(preview, "`, `"))
}

func writeTimelineToolOrders(b *strings.Builder, label string, orders []DebugToolOrderRequirement) {
	for _, order := range orders {
		if strings.TrimSpace(order.Earlier) == "" && strings.TrimSpace(order.Later) == "" {
			continue
		}
		fmt.Fprintf(b, "- %s: `%s -> %s`\n", label, timelineInline(order.Earlier, 120), timelineInline(order.Later, 120))
	}
}

func writeTimelineCommandToolOrders(b *strings.Builder, label string, orders []DebugCommandToolOrderRequirement) {
	for _, order := range orders {
		if strings.TrimSpace(order.Command) == "" && strings.TrimSpace(order.Tool) == "" {
			continue
		}
		fmt.Fprintf(b, "- %s: `%s -> %s`\n", label, timelineInline(order.Command, 180), timelineInline(order.Tool, 120))
	}
}

func writeTimelineCommandOrders(b *strings.Builder, label string, orders []DebugCommandOrderRequirement) {
	for _, order := range orders {
		if strings.TrimSpace(order.Earlier) == "" && strings.TrimSpace(order.Later) == "" {
			continue
		}
		fmt.Fprintf(b, "- %s: `%s -> %s`\n", label, timelineInline(order.Earlier, 180), timelineInline(order.Later, 180))
	}
}

func writeTimelineTaskStateActions(b *strings.Builder, label string, reqs []DebugTaskStateActionRequirement) {
	for _, req := range reqs {
		if strings.TrimSpace(req.Tool) == "" && strings.TrimSpace(req.SummaryContains) == "" {
			continue
		}
		parts := []string{timelineInline(req.Tool, 120)}
		if strings.TrimSpace(req.SummaryContains) != "" {
			parts = append(parts, "summary~"+timelineInline(req.SummaryContains, 180))
		}
		if req.Min > 0 {
			parts = append(parts, fmt.Sprintf("min=%d", req.Min))
		}
		fmt.Fprintf(b, "- %s: `%s`\n", label, strings.Join(parts, " "))
	}
}

func writeTimelineTaskStateChangedFiles(b *strings.Builder, label string, reqs []DebugTaskStateChangedFileRequirement) {
	for _, req := range reqs {
		if strings.TrimSpace(req.PathContains) == "" && strings.TrimSpace(req.Action) == "" {
			continue
		}
		var parts []string
		if strings.TrimSpace(req.PathContains) != "" {
			parts = append(parts, "path~"+timelineInline(req.PathContains, 180))
		}
		if strings.TrimSpace(req.Action) != "" {
			parts = append(parts, "action="+timelineInline(req.Action, 80))
		}
		if req.Min > 0 {
			parts = append(parts, fmt.Sprintf("min=%d", req.Min))
		}
		fmt.Fprintf(b, "- %s: `%s`\n", label, strings.Join(parts, " "))
	}
}

func writeTimelineTaskStateEvidence(b *strings.Builder, label string, reqs []DebugTaskStateEvidenceRequirement) {
	for _, req := range reqs {
		if strings.TrimSpace(req.Source) == "" && strings.TrimSpace(req.SummaryContains) == "" {
			continue
		}
		parts := []string{timelineInline(req.Source, 120)}
		if strings.TrimSpace(req.SummaryContains) != "" {
			parts = append(parts, "summary~"+timelineInline(req.SummaryContains, 180))
		}
		if req.Min > 0 {
			parts = append(parts, fmt.Sprintf("min=%d", req.Min))
		}
		fmt.Fprintf(b, "- %s: `%s`\n", label, strings.Join(parts, " "))
	}
}

func writeTimelineStringSliceMap(b *strings.Builder, label string, values map[string][]string) {
	if len(values) == 0 {
		return
	}
	keys := make([]string, 0, len(values))
	for key, list := range values {
		if strings.TrimSpace(key) != "" && len(list) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	for _, key := range keys {
		preview := make([]string, 0, len(values[key]))
		for _, value := range values[key] {
			value = strings.TrimSpace(value)
			if value == "" {
				continue
			}
			preview = append(preview, timelineInline(value, 180))
		}
		if len(preview) == 0 {
			continue
		}
		fmt.Fprintf(b, "- %s[%s]: `%s`\n", label, key, strings.Join(preview, "`, `"))
	}
}

func writeTimelineCountsLine(b *strings.Builder, label string, counts map[string]int) {
	if len(counts) == 0 {
		return
	}
	fmt.Fprintf(b, "- %s: `%s`\n", label, timelineCounts(counts))
}

func hasTimelineDebugBrief(res BatchResult) bool {
	return BuildDebugBrief(res) != nil ||
		len(res.ToolFailureExamples) > 0 ||
		len(res.LoopGuardExamples) > 0 ||
		len(res.RuntimeErrorExamples) > 0
}

func timelineCounts(counts map[string]int) string {
	if len(counts) == 0 {
		return ""
	}
	keys := make([]string, 0, len(counts))
	for key, count := range counts {
		if key != "" && count > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, fmt.Sprintf("%s=%d", key, counts[key]))
	}
	return strings.Join(parts, ",")
}

func timelineToolFailureExampleLines(examples map[string][]ToolFailureExample, max int) []string {
	if max <= 0 || len(examples) == 0 {
		return nil
	}
	keys := make([]string, 0, len(examples))
	for key := range examples {
		if key != "" && len(examples[key]) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var lines []string
	for _, key := range keys {
		for _, ex := range examples[key] {
			if len(lines) >= max {
				return lines
			}
			line := fmt.Sprintf("tool_failure_example[%s]: tool=`%s` exit=`%d`", key, ex.Tool, ex.ExitCode)
			if ex.ArgsSummary != "" {
				line += " args=" + timelineInline(ex.ArgsSummary, 240)
			}
			if ex.ResultSummary != "" {
				line += " result=" + timelineInline(ex.ResultSummary, 360)
			}
			if ex.SuggestedNextStep != "" {
				line += " next=" + timelineInline(ex.SuggestedNextStep, 260)
			}
			lines = append(lines, line)
		}
	}
	return lines
}

func timelineRuntimeErrorExampleLines(examples map[string][]RuntimeErrorExample, max int) []string {
	if max <= 0 || len(examples) == 0 {
		return nil
	}
	keys := make([]string, 0, len(examples))
	for key := range examples {
		if key != "" && len(examples[key]) > 0 {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	var lines []string
	for _, key := range keys {
		for _, ex := range examples[key] {
			if len(lines) >= max {
				return lines
			}
			lines = append(lines, fmt.Sprintf("runtime_error_example[%s]: %s", key, timelineInline(ex.Message, 520)))
		}
	}
	return lines
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

func renderTimelineConversationRepairs(b *strings.Builder, trace *Trace) {
	if len(trace.ConversationRepairs) == 0 {
		return
	}
	b.WriteString("\n## Conversation Repairs\n\n")
	for i, repair := range trace.ConversationRepairs {
		if i >= 5 {
			fmt.Fprintf(b, "- ... %d more repair event(s)\n", len(trace.ConversationRepairs)-i)
			return
		}
		var parts []string
		if repair.SessionID != "" {
			parts = append(parts, fmt.Sprintf("session=`%s`", repair.SessionID))
		}
		if repair.MissingToolResults > 0 {
			parts = append(parts, fmt.Sprintf("missing_tool_results=`%d`", repair.MissingToolResults))
		}
		if repair.DuplicateToolResults > 0 {
			parts = append(parts, fmt.Sprintf("duplicate_tool_results=`%d`", repair.DuplicateToolResults))
		}
		if repair.UnexpectedToolResults > 0 {
			parts = append(parts, fmt.Sprintf("unexpected_tool_results=`%d`", repair.UnexpectedToolResults))
		}
		if repair.FailureKind != "" {
			parts = append(parts, fmt.Sprintf("failure_kind=`%s`", repair.FailureKind))
		}
		if repair.Next != "" {
			parts = append(parts, fmt.Sprintf("next=%s", timelineInline(repair.Next, 220)))
		}
		fmt.Fprintf(b, "- repair#%d %s\n", i+1, strings.Join(parts, " "))
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
	if surface.MaxTurnSteps > 0 {
		fmt.Fprintf(b, "- max_turn_steps: `%d`\n", surface.MaxTurnSteps)
	}
	if surface.MaxToolCalls > 0 {
		fmt.Fprintf(b, "- max_tool_calls: `%d`\n", surface.MaxToolCalls)
	}
	if surface.MaxTurnInputTokens > 0 {
		fmt.Fprintf(b, "- max_turn_input_tokens: `%d`\n", surface.MaxTurnInputTokens)
	}
	if surface.ModelContextWindowTokens > 0 ||
		surface.ReservedOutputTokens > 0 ||
		surface.CompactTriggerInputTokens > 0 ||
		surface.CompactTriggerInputPercent > 0 {
		var parts []string
		if surface.ModelContextWindowTokens > 0 {
			parts = append(parts, fmt.Sprintf("model_context_window_tokens=`%d`", surface.ModelContextWindowTokens))
		}
		if surface.ReservedOutputTokens > 0 {
			parts = append(parts, fmt.Sprintf("reserved_output_tokens=`%d`", surface.ReservedOutputTokens))
		}
		if surface.CompactTriggerInputTokens > 0 {
			parts = append(parts, fmt.Sprintf("compact_trigger_input_tokens=`%d`", surface.CompactTriggerInputTokens))
		}
		if surface.CompactTriggerInputPercent > 0 {
			parts = append(parts, fmt.Sprintf("compact_trigger_input_percent=`%d`", surface.CompactTriggerInputPercent))
		}
		fmt.Fprintf(b, "- context_policy: %s\n", strings.Join(parts, ", "))
	}
	if surface.ToolResultEventCapBytes > 0 || surface.ToolResultContextMaxBytes > 0 || surface.ToolResultContextBudgetBytes > 0 {
		fmt.Fprintf(b, "- tool_result_limits: event_cap_bytes=`%d`, context_max_bytes=`%d`, context_budget_bytes=`%d`\n",
			surface.ToolResultEventCapBytes,
			surface.ToolResultContextMaxBytes,
			surface.ToolResultContextBudgetBytes,
		)
	}
	if surface.ToolResultArtifactPrefix != "" {
		fmt.Fprintf(b, "- tool_result_artifacts: `%s`\n", surface.ToolResultArtifactPrefix)
	}
	if surface.TurnToolOverride {
		fmt.Fprintln(b, "- turn_tool_override: `true`")
	}
	if len(surface.CompletionGuards) > 0 {
		guards := append([]string(nil), surface.CompletionGuards...)
		sort.Strings(guards)
		fmt.Fprintf(b, "- completion_guards: `%s`\n", strings.Join(guards, "`, `"))
	}
	if len(surface.ToolCallCaps) > 0 {
		var caps []string
		for _, cap := range surface.ToolCallCaps {
			if cap.Tool != "" && cap.Max > 0 {
				caps = append(caps, fmt.Sprintf("%s=%d", cap.Tool, cap.Max))
			}
		}
		if len(caps) > 0 {
			fmt.Fprintf(b, "- tool_call_caps: `%s`\n", strings.Join(caps, "`, `"))
		}
	}
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
	if ws := surface.Workspace; ws != nil {
		if ws.DefaultCWD != "" || ws.PathMode != "" || len(ws.RootEntries) > 0 {
			var parts []string
			if ws.DefaultCWD != "" {
				parts = append(parts, "default_cwd="+ws.DefaultCWD)
			}
			if ws.PathMode != "" {
				parts = append(parts, "path_mode="+ws.PathMode)
			}
			if len(ws.RootEntries) > 0 {
				var entries []string
				for _, entry := range ws.RootEntries {
					label := entry.Name
					if entry.Kind != "" {
						label += " (" + entry.Kind + ")"
					}
					entries = append(entries, label)
				}
				if ws.RootEntriesTruncated {
					entries = append(entries, fmt.Sprintf("... (%d total)", ws.RootEntryCount))
				}
				parts = append(parts, "root_entries="+strings.Join(entries, ", "))
			}
			fmt.Fprintf(b, "- workspace: `%s`\n", strings.Join(parts, "; "))
		}
	}
	if pathArgs := runtimeSurfaceWorkspacePathArgs(surface); len(pathArgs) > 0 {
		fmt.Fprintf(b, "- workspace_path_args: `%s`\n", strings.Join(pathArgs, "`, `"))
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
	if surface.Capabilities.SessionSchedule {
		caps = append(caps, "session_schedule")
	}
	if surface.Capabilities.SessionScheduleRunner {
		caps = append(caps, "session_schedule_runner")
	}
	if surface.Capabilities.SessionSearch {
		caps = append(caps, "session_search")
	}
	if surface.Capabilities.Subagent {
		caps = append(caps, "subagent")
	}
	if surface.Capabilities.FocusedTasks {
		caps = append(caps, "focused_tasks")
	}
	if surface.Capabilities.Skill {
		caps = append(caps, "skill")
	}
	if surface.Capabilities.MCP {
		caps = append(caps, "mcp")
	}
	if len(caps) > 0 {
		fmt.Fprintf(b, "- capabilities: `%s`\n", strings.Join(caps, "`, `"))
	}
}

func runtimeSurfaceWorkspacePathArgs(surface sse.RuntimeSurfacePayload) []string {
	var out []string
	for _, tool := range surface.Tools {
		if tool.Name == "" || tool.ArgPolicy == nil || len(tool.ArgPolicy.WorkspacePathArgs) == 0 {
			continue
		}
		args := append([]string(nil), tool.ArgPolicy.WorkspacePathArgs...)
		sort.Strings(args)
		out = append(out, fmt.Sprintf("%s:%s", tool.Name, strings.Join(args, ",")))
	}
	sort.Strings(out)
	return out
}

func renderTimelineContextInjections(b *strings.Builder, trace *Trace) {
	if len(trace.ContextInjections) == 0 {
		return
	}
	stats := trace.ContextInjectionStats(0)
	b.WriteString("\n## Context Injections\n\n")
	fmt.Fprintf(b, "- count: `%d`\n", stats.Count)
	fmt.Fprintf(b, "- bytes: `%d`\n", stats.Bytes)
	fmt.Fprintf(b, "- estimated_tokens: `%d`\n", stats.EstimatedTokens)
	if len(stats.BySource) > 0 {
		keys := make([]string, 0, len(stats.BySource))
		for source := range stats.BySource {
			keys = append(keys, source)
		}
		sort.Strings(keys)
		parts := make([]string, 0, len(keys))
		for _, source := range keys {
			parts = append(parts, fmt.Sprintf("%s=%d", source, stats.BySource[source]))
		}
		fmt.Fprintf(b, "- by_source: `%s`\n", strings.Join(parts, "`, `"))
	}
	for i, injection := range trace.ContextInjections {
		if i >= 8 {
			fmt.Fprintf(b, "- ... `%d` more context injections omitted\n", len(trace.ContextInjections)-i)
			break
		}
		fmt.Fprintf(b, "%d. turn=`%s` source=`%s` title=%s bytes=%d estimated_tokens=%d\n",
			i+1,
			injection.TurnID,
			injection.Source,
			timelineInline(injection.Title, 180),
			injection.Bytes,
			injection.EstimatedTokens,
		)
		if injection.Name != "" {
			fmt.Fprintf(b, "   name: `%s`\n", injection.Name)
		}
		if injection.ContentSHA256 != "" {
			fmt.Fprintf(b, "   content_sha256: `%s`\n", injection.ContentSHA256)
		}
		if injection.Summary != "" {
			fmt.Fprintf(b, "   summary: %s\n", timelineInline(injection.Summary, timelineMemoryPreviewBytes))
		}
		if injection.Preview != "" {
			fmt.Fprintf(b, "   preview: %s\n", timelineInline(injection.Preview, timelineArgsPreviewBytes))
		}
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

func renderTimelineToolRepair(b *strings.Builder, trace *Trace) {
	examples := trace.ToolRepairExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Tool Repair\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d `%s`", i+1, ex.ToolIndex, ex.Tool)
		if ex.OriginalTool != "" {
			fmt.Fprintf(b, " original=`%s`", ex.OriginalTool)
		}
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		fmt.Fprintf(b, " canonicalized=`%t` args_repaired=`%t` exit=`%d`", ex.Canonicalized, ex.ArgsRepaired, ex.ExitCode)
		if len(ex.RepairKinds) > 0 {
			fmt.Fprintf(b, " kinds=`%s`", strings.Join(ex.RepairKinds, ","))
		}
		b.WriteByte('\n')
		if ex.OriginalArgsSummary != "" {
			fmt.Fprintf(b, "   original_args: %s\n", timelineInline(ex.OriginalArgsSummary, timelineMemoryPreviewBytes))
		}
		for _, note := range ex.RepairNotes {
			fmt.Fprintf(b, "   note: %s\n", timelineInline(note, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelineLoopGuard(b *strings.Builder, trace *Trace) {
	examples := trace.LoopGuardExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Loop Guard\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d `%s` kind=`%s` category=`%s` exit=`%d`",
			i+1,
			ex.ToolIndex,
			ex.Tool,
			ex.Kind,
			ex.Category,
			ex.ExitCode,
		)
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		b.WriteByte('\n')
		if ex.ArgsSummary != "" {
			fmt.Fprintf(b, "   args: %s\n", timelineInline(ex.ArgsSummary, timelineMemoryPreviewBytes))
		}
		if ex.GuardSummary != "" {
			fmt.Fprintf(b, "   guard: %s\n", timelineInline(ex.GuardSummary, timelineResultPreviewBytes))
		}
		if ex.SuggestedNextStep != "" {
			fmt.Fprintf(b, "   next: %s\n", timelineInline(ex.SuggestedNextStep, timelineResultPreviewBytes))
		}
		if ex.ResultSummary != "" {
			fmt.Fprintf(b, "   result: %s\n", timelineInline(ex.ResultSummary, timelineResultPreviewBytes))
		}
	}
}

func renderTimelineCompactions(b *strings.Builder, trace *Trace) {
	if len(trace.ContextCompactions) == 0 {
		return
	}
	b.WriteString("\n## Context Compactions\n\n")
	for i, c := range trace.ContextCompactions {
		summaryState := contextCompactionSummaryState(c)
		fmt.Fprintf(b, "%d. turn=`%s` reactive=`%t` messages=%d->%d removed=%d",
			i+1,
			c.TurnID,
			c.Reactive,
			c.BeforeMessages,
			c.AfterMessages,
			c.RemovedMessages,
		)
		if c.BeforeBytes > 0 || c.AfterBytes > 0 || c.ReducedBytes > 0 {
			fmt.Fprintf(b, " bytes=%d->%d reduced=%d", c.BeforeBytes, c.AfterBytes, c.ReducedBytes)
		}
		fmt.Fprintf(b, " summary_state=%s summary_bytes=%d reason=%s\n",
			summaryState,
			c.SummaryBytes,
			timelineInline(c.Reason, 300),
		)
		if c.LoopProtocolAnchor != "" {
			fmt.Fprintf(b, "   loop_protocol_anchor: `%s`\n", timelineInline(c.LoopProtocolAnchor, timelineArgsPreviewBytes))
		}
		if c.SummaryPreview != "" {
			b.WriteString("   summary_preview:\n")
			b.WriteString("   ```text\n")
			b.WriteString(indentTimelineText(timelineBlock(c.SummaryPreview, timelineResultPreviewBytes), "   "))
			b.WriteString("\n   ```\n")
		}
	}
}

func renderTimelineDecisions(b *strings.Builder, trace *Trace) {
	if len(trace.LoopDecisions) == 0 {
		return
	}
	b.WriteString("\n## Loop Decisions\n\n")
	for i, d := range trace.LoopDecisions {
		fmt.Fprintf(b, "%d. kind=`%s` decision=`%s` trigger=`%s` confidence=`%s`\n", i+1, d.Kind, d.Decision, d.Trigger, d.Confidence)
		if d.TokenBudget > 0 {
			fmt.Fprintf(b, "   token_budget: `%d`\n", d.TokenBudget)
		}
		if d.BudgetBytes > 0 {
			fmt.Fprintf(b, "   budget_bytes: `%d`\n", d.BudgetBytes)
		}
		if d.Reason != "" {
			fmt.Fprintf(b, "   reason: %s\n", timelineInline(d.Reason, 600))
		}
		if d.RequiredAction != "" {
			fmt.Fprintf(b, "   required_action: %s\n", timelineInline(d.RequiredAction, 600))
		}
	}
}

func renderTimelineMessageRejections(b *strings.Builder, trace *Trace) {
	if len(trace.MessageRejections) == 0 {
		return
	}
	b.WriteString("\n## Rejected Assistant Completions\n\n")
	for i, rejected := range trace.MessageRejections {
		fmt.Fprintf(b, "%d. turn=`%s` trigger=`%s`\n", i+1, rejected.TurnID, rejected.Trigger)
		if rejected.Reason != "" {
			fmt.Fprintf(b, "   reason: %s\n", timelineInline(rejected.Reason, 600))
		}
		if rejected.RequiredAction != "" {
			fmt.Fprintf(b, "   required_action: %s\n", timelineInline(rejected.RequiredAction, 600))
		}
		if rejected.Text != "" {
			b.WriteString("   rejected_text:\n")
			b.WriteString("   ```text\n")
			b.WriteString(indentTimelineText(timelineBlock(rejected.Text, timelineResultPreviewBytes), "   "))
			b.WriteString("\n   ```\n")
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
		if entry.Info.Ref != "" {
			fmt.Fprintf(b, " ref=`%s`", entry.Info.Ref)
		}
		if entry.Info.SourceMethod != "" {
			fmt.Fprintf(b, " source_method=`%s`", entry.Info.SourceMethod)
		}
		if entry.Info.HTTPStatus != "" {
			fmt.Fprintf(b, " http_status=`%s`", entry.Info.HTTPStatus)
		}
		if entry.Info.ContentType != "" {
			fmt.Fprintf(b, " content_type=`%s`", entry.Info.ContentType)
		}
		if entry.Info.JSONPath != "" {
			fmt.Fprintf(b, " json_path=`%s`", entry.Info.JSONPath)
		}
		if bodyPage := sourceAccessBodyPageInfo(entry.Tool.Result); bodyPage.BodyBytes > 0 {
			fmt.Fprintf(b, " body_bytes=`%d`", bodyPage.BodyBytes)
			if bodyPage.BodyOffset > 0 || bodyPage.ShowingBytes > 0 || bodyPage.OmittedAfter > 0 {
				fmt.Fprintf(b, " body_offset=`%d` showing=`%d`", bodyPage.BodyOffset, bodyPage.ShowingBytes)
			}
			if bodyPage.HasMore {
				fmt.Fprintf(b, " next_offset=`%d`", bodyPage.NextOffset)
			}
		}
		if entry.Tool.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", entry.Tool.CallID)
		}
		b.WriteByte('\n')
		if preview := sourceAccessResultPreview(entry.Tool.Result, entry.Tool.ResultSummary); preview != "" {
			fmt.Fprintf(b, "   preview: %s\n", timelineInline(preview, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelineBrowserScrolls(b *strings.Builder, trace *Trace) {
	examples := trace.BrowserScrollExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Browser Scrolls\n\n")
	b.WriteString("Scroll telemetry is page-position evidence, not citable factual evidence. If movement is none or a boundary is reached, use browser network reads before citing hidden values.\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d status=`%s`", i+1, ex.ToolIndex, ex.Status)
		if ex.Direction != "" {
			fmt.Fprintf(b, " direction=`%s`", timelineInline(ex.Direction, 80))
		}
		if ex.URL != "" {
			fmt.Fprintf(b, " url=`%s`", timelineInline(ex.URL, 300))
		}
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		if ex.Movement != "" {
			fmt.Fprintf(b, " movement=`%s`", timelineInline(ex.Movement, 80))
		}
		if ex.Boundary != "" {
			fmt.Fprintf(b, " boundary=`%s`", timelineInline(ex.Boundary, 80))
		}
		if ex.BeforeY != "" || ex.AfterY != "" || ex.MaxY != "" {
			fmt.Fprintf(b, " y=`%s->%s/%s`", timelineInline(ex.BeforeY, 80), timelineInline(ex.AfterY, 80), timelineInline(ex.MaxY, 80))
		}
		b.WriteByte('\n')
		if ex.ResultPreview != "" {
			fmt.Fprintf(b, "   preview: %s\n", timelineInline(ex.ResultPreview, timelineMemoryPreviewBytes))
		}
		if ex.SuggestedNextStep != "" {
			fmt.Fprintf(b, "   next: %s\n", timelineInline(ex.SuggestedNextStep, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelineBrowserNetworkSearches(b *strings.Builder, trace *Trace) {
	examples := trace.BrowserNetworkSearchExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Browser Network Searches\n\n")
	b.WriteString("These are refs/checks, not citable sources. Use Source Evidence from `browser_network_read` before citing values.\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d status=`%s`", i+1, ex.ToolIndex, ex.Status)
		if ex.Query != "" {
			fmt.Fprintf(b, " query=`%s`", timelineInline(ex.Query, 220))
		}
		if ex.CurrentPageURL != "" {
			fmt.Fprintf(b, " page=`%s`", timelineInline(ex.CurrentPageURL, 300))
		}
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		if ex.EvidenceStatus != "" {
			fmt.Fprintf(b, " evidence_status=`%s`", timelineInline(ex.EvidenceStatus, 220))
		}
		if ex.RequiresRead {
			fmt.Fprintf(b, " requires_read=`true`")
		}
		if ex.NotCitable {
			fmt.Fprintf(b, " citable=`false`")
		}
		b.WriteByte('\n')
		if len(ex.Refs) > 0 {
			fmt.Fprintf(b, "   refs: `%s`\n", strings.Join(ex.Refs, "`, `"))
		}
		for _, preview := range ex.Previews {
			fmt.Fprintf(b, "   preview: %s\n", timelineInline(preview, timelineMemoryPreviewBytes))
		}
		if ex.SuggestedNextStep != "" {
			fmt.Fprintf(b, "   next: %s\n", timelineInline(ex.SuggestedNextStep, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelinePlan(b *strings.Builder, trace *Trace) {
	examples := trace.PlanExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Plan Updates\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d action=`%s`", i+1, ex.ToolIndex, ex.Action)
		if ex.Index > 0 {
			fmt.Fprintf(b, " index=`%d`", ex.Index)
		}
		if ex.Status != "" {
			fmt.Fprintf(b, " status=`%s`", ex.Status)
		}
		if ex.TotalSteps > 0 {
			fmt.Fprintf(b, " progress=`%d/%d`", ex.CompletedSteps, ex.TotalSteps)
		}
		if ex.CurrentStepIndex > 0 {
			fmt.Fprintf(b, " current=`%d:%s`", ex.CurrentStepIndex, ex.CurrentStepStatus)
		}
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		if ex.Error {
			fmt.Fprintf(b, " error=`true`")
		}
		if ex.Skipped {
			fmt.Fprintf(b, " skipped=`true`")
		}
		if len(ex.FailureKinds) > 0 {
			fmt.Fprintf(b, " failure_kinds=`%s`", strings.Join(ex.FailureKinds, ","))
		}
		b.WriteByte('\n')
		if ex.StepText != "" {
			fmt.Fprintf(b, "   step: %s\n", timelineInline(ex.StepText, timelineMemoryPreviewBytes))
		}
		if ex.CurrentStep != "" && ex.CurrentStep != ex.StepText {
			fmt.Fprintf(b, "   current_step: %s\n", timelineInline(ex.CurrentStep, timelineMemoryPreviewBytes))
		}
		if len(ex.Evidence) > 0 {
			fmt.Fprintf(b, "   evidence: `%s`\n", strings.Join(ex.Evidence, "`, `"))
		}
		if ex.NotePreview != "" {
			fmt.Fprintf(b, "   note: %s\n", timelineInline(ex.NotePreview, timelineMemoryPreviewBytes))
		}
		if ex.ResultMessage != "" {
			fmt.Fprintf(b, "   message: %s\n", timelineInline(ex.ResultMessage, timelineMemoryPreviewBytes))
		}
		if ex.ResultSummary != "" {
			fmt.Fprintf(b, "   result: %s\n", timelineInline(ex.ResultSummary, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelineMemoryUpdates(b *strings.Builder, trace *Trace) {
	var entries []timelineMemoryUpdate
	for i, tool := range trace.Tools {
		update, ok := memoryUpdateFromTool(tool)
		if !ok {
			continue
		}
		update.Index = i + 1
		update.Tool = tool
		entries = append(entries, update)
	}
	if len(entries) == 0 {
		return
	}
	b.WriteString("\n## Memory Updates\n\n")
	for i, entry := range entries {
		fmt.Fprintf(b, "%d. tool#%d action=`%s` location=`%s`", i+1, entry.Index, entry.Action, entry.Location)
		if entry.Tool.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", entry.Tool.CallID)
		}
		b.WriteByte('\n')
		if entry.Preview != "" {
			fmt.Fprintf(b, "   %s\n", timelineInline(entry.Preview, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelineSessionSearch(b *strings.Builder, trace *Trace) {
	examples := trace.SessionSearchExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Session Search\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d query=`%s` total=`%d`", i+1, ex.ToolIndex, timelineInline(ex.Query, 220), ex.Total)
		if ex.SessionID != "" {
			fmt.Fprintf(b, " session=`%s`", ex.SessionID)
		}
		if ex.RecentSessionID != "" {
			fmt.Fprintf(b, " recent_session=`%s`", ex.RecentSessionID)
		}
		if ex.TurnIdx > 0 {
			fmt.Fprintf(b, " turn=`%d`", ex.TurnIdx)
		}
		if ex.MessageIdx > 0 {
			fmt.Fprintf(b, " message=`%d`", ex.MessageIdx)
		}
		if ex.Role != "" {
			fmt.Fprintf(b, " role=`%s`", ex.Role)
		}
		if ex.ModTime != "" {
			fmt.Fprintf(b, " mod_time=`%s`", timelineInline(ex.ModTime, 80))
		}
		if len(ex.MatchedTerms) > 0 {
			fmt.Fprintf(b, " terms=`%s`", strings.Join(ex.MatchedTerms, ","))
		}
		if ex.ContextIncluded {
			fmt.Fprintf(b, " context=`true`")
		}
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		b.WriteByte('\n')
		if ex.SnippetPreview != "" {
			fmt.Fprintf(b, "   snippet: %s\n", timelineInline(ex.SnippetPreview, timelineMemoryPreviewBytes))
		}
		if ex.RecentUserPreview != "" {
			fmt.Fprintf(b, "   recent_user: %s\n", timelineInline(ex.RecentUserPreview, timelineMemoryPreviewBytes))
		}
		if ex.RecentAssistantPreview != "" {
			fmt.Fprintf(b, "   recent_assistant: %s\n", timelineInline(ex.RecentAssistantPreview, timelineMemoryPreviewBytes))
		}
		if ex.RecentPlanPreview != "" {
			fmt.Fprintf(b, "   recent_plan: %s\n", timelineInline(ex.RecentPlanPreview, timelineMemoryPreviewBytes))
		}
		if ex.RecentLoopPreview != "" {
			fmt.Fprintf(b, "   recent_loop: %s\n", timelineInline(ex.RecentLoopPreview, timelineMemoryPreviewBytes))
		}
		if ex.RecentTaskStatePreview != "" {
			fmt.Fprintf(b, "   recent_task_state: %s\n", timelineInline(ex.RecentTaskStatePreview, timelineMemoryPreviewBytes))
		}
		if ex.RecentRecoveryPreview != "" {
			fmt.Fprintf(b, "   recent_recovery: %s\n", timelineInline(ex.RecentRecoveryPreview, timelineMemoryPreviewBytes))
		}
		if ex.Message != "" {
			fmt.Fprintf(b, "   message: %s\n", timelineInline(ex.Message, timelineMemoryPreviewBytes))
		}
	}
}

func renderTimelineToolTruncation(b *strings.Builder, trace *Trace) {
	examples := trace.ToolTruncationExamples(len(trace.Tools))
	if len(examples) == 0 {
		return
	}
	b.WriteString("\n## Tool Truncation\n\n")
	for i, ex := range examples {
		fmt.Fprintf(b, "%d. tool#%d `%s`", i+1, ex.ToolIndex, ex.Tool)
		if ex.CallID != "" {
			fmt.Fprintf(b, " call_id=`%s`", ex.CallID)
		}
		b.WriteByte('\n')
		if ex.ArgsTruncated || ex.ArgsOmittedBytes > 0 {
			fmt.Fprintf(b, "   args: truncated=`%t` bytes=`%d` omitted=`%d` cap=`%d`\n",
				ex.ArgsTruncated, ex.ArgsBytes, ex.ArgsOmittedBytes, ex.ArgsCapBytes)
		}
		if ex.ResultTruncated || ex.ResultOmittedBytes > 0 {
			fmt.Fprintf(b, "   result: truncated=`%t` bytes=`%d` omitted=`%d` cap=`%d`\n",
				ex.ResultTruncated, ex.ResultBytes, ex.ResultOmittedBytes, ex.ResultCapBytes)
		}
		if ex.ResultSummary != "" {
			fmt.Fprintf(b, "   summary: %s\n", timelineInline(ex.ResultSummary, timelineMemoryPreviewBytes))
		}
		if ex.ContextOmittedBytes > 0 || ex.ContextBytes > 0 || ex.ContextEstimatedTokens > 0 {
			fmt.Fprintf(b, "   context: bytes=`%d` omitted=`%d` estimated_tokens=`%d`\n",
				ex.ContextBytes, ex.ContextOmittedBytes, ex.ContextEstimatedTokens)
		}
		if ex.ResultArtifactPath != "" {
			fmt.Fprintf(b, "   artifact: `%s`\n", ex.ResultArtifactPath)
		}
	}
}

func memoryUpdateFromTool(tool ToolCall) (timelineMemoryUpdate, bool) {
	var zero timelineMemoryUpdate
	if tool.MemoryUpdate != nil {
		action := strings.ToLower(strings.TrimSpace(tool.MemoryUpdate.Action))
		switch action {
		case "add", "replace", "remove":
		default:
			return zero, false
		}
		location := strings.TrimSpace(tool.MemoryUpdate.Location)
		if location == "" {
			target := firstNonEmpty(tool.MemoryUpdate.Target, "memory")
			topic := normalizeTimelineMemoryTopic(target, firstNonEmpty(tool.MemoryUpdate.Topic, "general"))
			location = target + ":" + topic
		}
		preview := firstNonEmpty(
			tool.MemoryUpdate.Preview,
			timelineMemoryUpdatePreview(action, tool.MemoryUpdate.PreviousPreview, tool.MemoryUpdate.NextPreview),
		)
		return timelineMemoryUpdate{
			Action:   action,
			Location: location,
			Preview:  preview,
		}, true
	}
	if tool.Tool != "memory" || tool.ExitCode != 0 || tool.IsErr {
		return zero, false
	}
	var resp struct {
		OK     bool   `json:"ok"`
		Target string `json:"target"`
		Topic  string `json:"topic"`
	}
	if err := json.Unmarshal([]byte(tool.Result), &resp); err != nil || !resp.OK {
		return zero, false
	}
	action := strings.ToLower(strings.TrimSpace(stringMapArg(tool.Args, "action")))
	switch action {
	case "add", "replace", "remove":
	default:
		return zero, false
	}
	target := firstNonEmpty(resp.Target, stringMapArg(tool.Args, "target"), "memory")
	topic := normalizeTimelineMemoryTopic(target, firstNonEmpty(resp.Topic, stringMapArg(tool.Args, "topic"), "general"))
	oldText := stringMapArg(tool.Args, "old_text")
	newText := stringMapArg(tool.Args, "content")
	preview := timelineMemoryUpdatePreview(action, oldText, newText)
	return timelineMemoryUpdate{
		Action:   action,
		Location: target + ":" + topic,
		Preview:  preview,
	}, true
}

func timelineMemoryUpdatePreview(action, oldText, newText string) string {
	oldPreview := timelineInline(oldText, 100)
	newPreview := timelineInline(newText, 100)
	switch action {
	case "add":
		return firstNonEmpty(newPreview, "No content supplied")
	case "replace":
		if oldPreview != "" && newPreview != "" {
			return oldPreview + " -> " + newPreview
		}
		return firstNonEmpty(newPreview, oldPreview, "No content supplied")
	case "remove":
		return firstNonEmpty(oldPreview, "No content supplied")
	default:
		return ""
	}
}

func stringMapArg(args map[string]any, key string) string {
	value, ok := args[key]
	if !ok {
		return ""
	}
	if s, ok := value.(string); ok {
		return strings.TrimSpace(s)
	}
	return ""
}

func normalizeTimelineMemoryTopic(target, topic string) string {
	if target == "user" {
		return "user"
	}
	return firstNonEmpty(topic, "general")
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
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

func timelineOptionalRatio(numerator, denominator int) *float64 {
	if denominator <= 0 {
		return nil
	}
	v := float64(numerator) / float64(denominator)
	return &v
}

func timelineOptionalNumber(value *float64) string {
	if value == nil {
		return "n/a"
	}
	return fmt.Sprintf("%.2f", *value)
}

func indentTimelineText(s, prefix string) string {
	if s == "" {
		return prefix
	}
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
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
