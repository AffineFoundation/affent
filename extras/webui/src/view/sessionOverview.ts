import { EventType, type ToolRuntimeStats } from "../api/events";
import type { SessionContextSummary, SessionPlanSummary } from "../api/sessions";
import { latestAssistantMessageText, type SessionState, type TurnState } from "../store/sessionState";
import type { WorkflowStatus } from "../store/workflowStatus";
import { conversationTopicFromTurns } from "./continuationPrompt";
import { formatByteCount } from "./byteFormat";
import { memoryUpdatesForTurn } from "./memoryUpdate";
import { summarizeSessionTitle } from "./sessionList";
import { buildTurnActivity, type TurnActivityView } from "./turnActivity";
import { sessionArtifactLabel } from "./sessionArtifacts";
import { summarizeAnswerPreview, summarizePreview } from "./textPreview";
import { contextCompactionSummaryLabel } from "./contextCompaction";

export type SessionOverviewTone = "ready" | "running" | "success" | "warning" | "error";

export interface SessionOverviewMetric {
  label: string;
  value: string;
  tone?: SessionOverviewTone;
}

export interface SessionOverview {
  headline: string;
  detail: string;
  stateLabel: string;
  tone: SessionOverviewTone;
  active: boolean;
  metrics: SessionOverviewMetric[];
}

export function displaySessionOverviewMetrics(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric[] {
  return metrics.filter((metric) => {
    if (metric.label === "Work" && isPlainActionCount(metric.value)) return false;
    if (metric.label === "Earlier work" && isPlainActionCount(metric.value)) return false;
    if (metric.tone === "error" || metric.tone === "warning" || metric.tone === "running") return true;
    return !lowSignalMetricLabels.has(metric.label);
  });
}

export function displayChatContextMetrics(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric[] {
  const visible: SessionOverviewMetric[] = [];
  const tokenMetric = metrics.find((metric) => metric.label === "Session tokens")
    ?? metrics.find((metric) => metric.label === "Chat tokens")
    ?? metrics.find((metric) => metric.label === "Tokens");
  if (tokenMetric) visible.push({ ...tokenMetric, label: "Tokens" });
  for (const metric of metrics) {
    if (metric === tokenMetric) continue;
    if (chatContextHiddenMetricLabels.has(metric.label)) continue;
    if (metric.label === "Context" && !metric.tone) continue;
    if (metric.tone === "error" || metric.tone === "warning" || metric.tone === "running") {
      visible.push(metric);
      continue;
    }
    if (chatContextStatusMetricLabels.has(metric.label)) visible.push(metric);
  }
  return visible.slice(0, 3);
}

const lowSignalMetricLabels = new Set(["Tokens", "Turn tokens", "Chat tokens", "Session tokens", "End"]);
const chatContextHiddenMetricLabels = new Set(["Work", "Earlier work", "Tool context", "Source", "Sources", "Recall", "Memory"]);
const chatContextStatusMetricLabels = new Set(["Plan", "Automation", "Artifact", "Artifacts", "Compaction", "Compactions"]);

function isPlainActionCount(value: string): boolean {
  return /^\d+ actions?$/.test(value.trim());
}

export function buildSessionOverview({
  session,
  workflow,
  hasSelectedSession,
  pendingTask,
  pendingGuidance,
  sessionTitle,
  planSummary,
  contextSummary,
  recoveryHint,
}: {
  session: SessionState;
  workflow: WorkflowStatus;
  hasSelectedSession: boolean;
  pendingTask?: string;
  pendingGuidance?: string;
  sessionTitle?: string;
  planSummary?: SessionPlanSummary;
  contextSummary?: SessionContextSummary;
  recoveryHint?: string;
}): SessionOverview {
  const latestTurn = session.turns.at(-1);
  const latestActivity = latestTurn ? buildTurnActivity(latestTurn) : undefined;
  const latestTask = latestTurn?.userText && !isInternalRuntimePrompt(latestTurn.userText) ? summarizeSessionTitle(latestTurn.userText) : undefined;
  const rawTopic = conversationTopicFromTurns(session.turns);
  const topic = rawTopic && !isInternalRuntimePrompt(rawTopic) ? rawTopic : undefined;
  const task = topic ? summarizeSessionTitle(topic) : latestTask;
  const pending = pendingTask?.trim();
  const guidance = pendingGuidance?.trim();

  if (pending) {
    return {
      headline: summarizeSessionTitle(pending),
      detail: pendingTaskDetail({ hasSelectedSession, hasLatestTurn: Boolean(latestTurn) }),
      stateLabel: "Sending",
      tone: "running",
      active: true,
      metrics: buildMetrics(session, undefined, undefined, planSummary, contextSummary, recoveryHint),
    };
  }

  if (guidance && latestTurn?.status === "running") {
    return {
      headline: sessionTitle ?? task ?? "Live turn",
      detail: "Applying your guidance to the current run.",
      stateLabel: "Sending guidance",
      tone: "running",
      active: true,
      metrics: buildMetrics(session, latestTurn, latestActivity, planSummary, contextSummary, recoveryHint),
    };
  }

  if (!hasSelectedSession && !latestTurn) {
    return {
      headline: "Start a chat",
      detail: "Describe the outcome you want; Affent will create the chat.",
      stateLabel: "Ready",
      tone: "ready",
      active: false,
      metrics: buildMetrics(session, undefined, undefined, planSummary, contextSummary, recoveryHint),
    };
  }

  if (!latestTurn) {
    return {
      headline: "Message Affent",
      detail: "The answer and the action details will stay together in this chat.",
      stateLabel: "Ready",
      tone: "ready",
      active: false,
      metrics: buildMetrics(session, undefined, undefined, planSummary, contextSummary, recoveryHint),
    };
  }

  const tone = toneForTurn(latestTurn);
  return {
    headline: sessionTitle ?? task ?? workflow.title,
    detail: overviewDetail(latestTurn, latestActivity, workflow),
    stateLabel: workflow.title,
    tone,
    active: workflow.active,
    metrics: buildMetrics(session, latestTurn, latestActivity, planSummary, contextSummary, recoveryHint),
  };
}

function pendingTaskDetail({ hasSelectedSession, hasLatestTurn }: { hasSelectedSession: boolean; hasLatestTurn: boolean }): string {
  if (!hasSelectedSession) return "Creating chat; first update will appear here.";
  if (hasLatestTurn) return "Follow-up sent; next update will appear here.";
  return "Message sent; first update will appear here.";
}

function overviewDetail(turn: TurnState, activity: TurnActivityView | undefined, workflow: WorkflowStatus): string {
  const latestAnswer = latestAssistantMessageText(turn);
  if (activity?.digest.summary && activity.digest.summary !== "No activity yet.") {
    const summary = summarize(activity.digest.summary, 140);
    if (
      turn.status === "completed" &&
      latestAnswer &&
      (activity.digest.label === "Process" || isMechanicalActivitySummary(summary))
    ) {
      return summarizeAnswer(latestAnswer, 140);
    }
    return summary;
  }
  if (turn.status === "completed" && latestAnswer) {
    return summarizeAnswer(latestAnswer, 140);
  }
  if (latestAnswer) return summarizeAnswer(latestAnswer, 140);
  return workflow.detail;
}

function isMechanicalActivitySummary(summary: string): boolean {
  return /^(\d+ )?actions? completed(?:;|\.|$)/i.test(summary) || summary === "No action details.";
}

function buildMetrics(
  session: SessionState,
  latestTurn?: TurnState,
  latestActivity?: TurnActivityView,
  planSummary?: SessionPlanSummary,
  contextSummary?: SessionContextSummary,
  recoveryHint?: string,
): SessionOverviewMetric[] {
  const metrics: SessionOverviewMetric[] = [];

  const currentIssueCount = latestTurn ? currentTurnIssueCount(latestTurn) : 0;
  if (currentIssueCount > 0) metrics.push({ label: currentIssueCount === 1 ? "Issue" : "Issues", value: String(currentIssueCount), tone: "error" });
  const contextMetric = buildContextUsageMetric(session, contextSummary);
  if (contextMetric) metrics.push(contextMetric);
  const settledIssues = latestTurn ? settledToolIssueCount(latestTurn) : 0;
  if (settledIssues > 0) metrics.push({ label: settledIssues === 1 ? "Tool issue" : "Tool issues", value: String(settledIssues), tone: "warning" });
  const recoveryMetric = latestTurn ? buildToolRecoveryMetric(latestTurn) : undefined;
  if (recoveryMetric) metrics.push(recoveryMetric);
  const summaryRecoveryMetric = !recoveryMetric && (!latestTurn || currentIssueCount > 0) ? buildSummaryRecoveryMetric(recoveryHint) : undefined;
  if (summaryRecoveryMetric) metrics.push(summaryRecoveryMetric);
  const artifactMetric = buildArtifactMetric(session);
  if (artifactMetric) metrics.push(artifactMetric);
  const automationMetric = buildAutomationMetric(session);
  if (automationMetric) metrics.push(automationMetric);
  const compactMetric = buildContextCompactionMetric(session);
  if (compactMetric) metrics.push(compactMetric);
  const toolContextMetric = buildToolContextMetric(session);
  if (toolContextMetric) metrics.push(toolContextMetric);
  const memoryMetric = buildMemoryUpdateMetric(session);
  if (memoryMetric) metrics.push(memoryMetric);
  const recallMetric = buildSessionRecallMetric(session);
  if (recallMetric) metrics.push(recallMetric);
  const sourceMetric = buildSourceAccessMetric(session);
  if (sourceMetric) metrics.push(sourceMetric);
  const planMetric = buildPlanMetric(planSummary);
  if (planMetric) metrics.push(planMetric);
  const workMetric = buildWorkMetric(latestTurn, latestActivity, currentIssueCount > 0);
  if (workMetric) metrics.push(workMetric);
  const threadMetrics = latestTurn ? buildThreadMetrics(session, latestTurn) : undefined;
  if (threadMetrics) {
    if (threadMetrics.handledIssues > 0 && settledIssues === 0) {
      metrics.push({ label: threadMetrics.handledIssues === 1 ? "Tool issue" : "Tool issues", value: String(threadMetrics.handledIssues), tone: "warning" });
    }
    metrics.push({ label: "Earlier work", value: summarizeThreadMetrics(threadMetrics) });
  }
  const latestTokens = turnTokenCount(latestTurn);
  const totalTokens = sessionTokenCount(session);
  if (latestTokens > 0 && totalTokens > latestTokens) {
    metrics.push({ label: "Turn tokens", value: formatTokenMillions(latestTokens) });
    metrics.push({ label: "Session tokens", value: formatTokenMillions(totalTokens) });
  } else if (latestTokens > 0) {
    metrics.push({ label: "Session tokens", value: formatTokenMillions(latestTokens) });
  } else if (totalTokens > 0) {
    metrics.push({ label: "Session tokens", value: formatTokenMillions(totalTokens) });
  }
  if (latestTurn?.endReason && latestTurn.endReason !== latestTurn.status) {
    metrics.push({ label: "End", value: latestTurn.endReason, tone: latestTurn.status === "max_turns" ? "warning" : undefined });
  }
  if (session.unknownEventCount > 0) metrics.push({ label: "Unclassified", value: String(session.unknownEventCount), tone: "warning" });

  return metrics;
}

function buildSummaryRecoveryMetric(hint?: string): SessionOverviewMetric | undefined {
  const value = hint?.replace(/\s+/g, " ").trim();
  if (value && isInternalRuntimePrompt(value)) return undefined;
  return value ? { label: "Next step", value: summarize(value, 72), tone: "warning" } : undefined;
}

function buildToolRecoveryMetric(turn: TurnState): SessionOverviewMetric | undefined {
  for (const call of [...turn.toolCalls].reverse()) {
    if (call.status !== "error" && (!call.exitCode || call.exitCode === 0)) continue;
    const next = toolNextHint(call.resultSummary, call.result);
    if (!next) continue;
    return { label: "Next step", value: summarize(next, 72), tone: "warning" };
  }
  return undefined;
}

function toolNextHint(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const next = match?.[1]?.trim();
  if (next && isInternalRuntimePrompt(next)) return undefined;
  if (next && isGenericContinuationHint(next)) return undefined;
  return next || undefined;
}

function isInternalRuntimePrompt(text: string): boolean {
  const normalized = text.replace(/\s+/g, " ").trim().toLowerCase();
  return normalized.startsWith("the tool-step budget for this turn is exhausted") ||
    normalized.startsWith("tool-step budget for this turn is exhausted") ||
    normalized.startsWith("tools are disabled for the rest of this turn") ||
    normalized.startsWith("do not call tools.") ||
    normalized.startsWith("do not call tools again.") ||
    normalized.startsWith("do not call more tools.") ||
    normalized.startsWith("do not execute the task yet.") ||
    normalized.includes("previous assistant step still requested another tool") ||
    normalized.includes("use only existing tool results");
}

function isGenericContinuationHint(text: string): boolean {
  const normalized = text.replace(/\s+/g, " ").trim().toLowerCase();
  return normalized.startsWith("continue from the current plan state") ||
    normalized.startsWith("execute the next concrete step");
}

function buildContextCompactionMetric(session: SessionState): SessionOverviewMetric | undefined {
  if (session.contextCompactions.length === 0) return undefined;
  const latest = session.contextCompactions.at(-1);
  const parts = [String(session.contextCompactions.length)];
  if (latest?.reactive) parts.push("reactive");
  if (latest?.removed_messages && latest.removed_messages > 0) {
    parts.push(`-${formatCount(latest.removed_messages)} msgs`);
  }
  if (latest?.summary_bytes && latest.summary_bytes > 0) {
    parts.push(`${formatBytes(latest.summary_bytes)} summary`);
  }
  const summaryLabel = latest ? contextCompactionSummaryLabel(latest) : undefined;
  if (summaryLabel) parts.push(summaryLabel);
  return {
    label: session.contextCompactions.length === 1 ? "Compaction" : "Compactions",
    value: parts.join(" · "),
    tone: summaryLabel ? "error" : latest?.reactive ? "warning" : undefined,
  };
}

function buildToolContextMetric(session: SessionState): SessionOverviewMetric | undefined {
  const stats = session.turns.reduce((acc, turn) => {
    acc.truncated += turn.toolStats?.tool_context_truncated ?? 0;
    acc.omittedBytes += turn.toolStats?.tool_context_omitted_bytes ?? 0;
    return acc;
  }, { truncated: 0, omittedBytes: 0 });
  if (stats.truncated <= 0) return undefined;
  const parts = [`${stats.truncated} ${stats.truncated === 1 ? "trim" : "trims"}`];
  if (stats.omittedBytes > 0) parts.push(`${formatBytes(stats.omittedBytes)} omitted`);
  return { label: "Tool context", value: parts.join(" · "), tone: "warning" };
}

function buildMemoryUpdateMetric(session: SessionState): SessionOverviewMetric | undefined {
  const updates = session.turns.flatMap(memoryUpdatesForTurn);
  if (updates.length === 0) return undefined;
  const latest = updates.at(-1);
  const parts = [`${updates.length} ${updates.length === 1 ? "update" : "updates"}`];
  if (latest) {
    parts.push(`${latest.location}: ${summarizePreview(latest.preview, 48)}`);
  }
  return { label: "Memory", value: parts.join(" · "), tone: "success" };
}

function buildAutomationMetric(session: SessionState): SessionOverviewMetric | undefined {
  const stats = session.turns.reduce((acc, turn) => {
    acc.interventions += turn.toolStats?.loop_guard_interventions ?? 0;
    acc.forcedNoTools += turn.toolStats?.forced_no_tools ?? 0;
    if (turn.status === "max_turns" || turn.endReason === "max_turns") acc.maxTurns += 1;
    return acc;
  }, { interventions: 0, forcedNoTools: 0, maxTurns: 0 });
  const visibleDecisions = session.loopDecisions.filter((decision) => decision.visible_in_ui !== false);
  const loopFeeds = session.loopProtocolFeeds;
  if (stats.interventions <= 0 && stats.forcedNoTools <= 0 && stats.maxTurns <= 0 && visibleDecisions.length === 0 && loopFeeds.length === 0) return undefined;
  const parts: string[] = [];
  if (stats.maxTurns > 0) parts.push(`${stats.maxTurns} action limit${stats.maxTurns === 1 ? "" : "s"}`);
  if (stats.interventions > 0) parts.push(`${stats.interventions} recovery limit${stats.interventions === 1 ? "" : "s"}`);
  if (stats.forcedNoTools > 0) parts.push(`${stats.forcedNoTools} no-tool retry${stats.forcedNoTools === 1 ? "" : "s"}`);
  if (loopFeeds.length > 0) parts.push(loopProtocolFeedMetric(loopFeeds));
  if (visibleDecisions.length > 0) {
    const latest = visibleDecisions.at(-1);
    parts.push(loopDecisionMetric(visibleDecisions.length, latest));
  }
  return { label: "Automation", value: parts.join(" · "), tone: stats.maxTurns > 0 || stats.interventions > 0 ? "warning" : undefined };
}

function loopDecisionMetric(count: number, decision: SessionState["loopDecisions"][number] | undefined): string {
  return [
    `${count} ${loopDecisionMetricName(decision)}${count === 1 ? "" : "s"}`,
    decision?.decision,
    loopDecisionBudgetPressure(decision),
  ].filter(Boolean).join(" ");
}

function loopDecisionMetricName(decision: SessionState["loopDecisions"][number] | undefined): string {
  if (decision?.kind === "research_checkpoint") return "research checkpoint";
  if (decision?.kind === "input_budget") return "input budget decision";
  if (decision?.kind === "tool_context_budget") return "context budget decision";
  return "decision";
}

function loopDecisionBudgetPressure(decision: SessionState["loopDecisions"][number] | undefined): string | undefined {
  if (!decision) return undefined;
  if (decision.kind === "input_budget") {
    const observed = decision.observed_input_tokens;
    const projected = decision.projected_input_tokens;
    const budget = decision.token_budget;
    if (projected && projected > 0 && budget && budget > 0) return `projected ${projected.toLocaleString()}/${budget.toLocaleString()} tokens`;
    if (observed && observed > 0 && budget && budget > 0) return `observed ${observed.toLocaleString()}/${budget.toLocaleString()} tokens`;
    if (projected && projected > 0) return `projected ${projected.toLocaleString()} tokens`;
    if (observed && observed > 0) return `observed ${observed.toLocaleString()} tokens`;
    if (budget && budget > 0) return `budget ${budget.toLocaleString()} tokens`;
  }
  if (decision.kind === "tool_context_budget" && decision.budget_bytes && decision.budget_bytes > 0) return `budget ${formatByteCount(decision.budget_bytes)}`;
  return undefined;
}

function loopProtocolFeedMetric(feeds: SessionState["loopProtocolFeeds"]): string {
  const latest = feeds.at(-1);
  const count = `${feeds.length} ${feeds.length === 1 ? "feed" : "feeds"}`;
  if (!latest) return count;
  const checkpoint = [
    latest.mode,
    latest.plan_label,
    latest.plan_current_step_index ? `step ${latest.plan_current_step_index}` : undefined,
    latest.plan_current_step_status,
    latest.current_situation_preview ? `situation ${summarizePreview(latest.current_situation_preview, 48)}` : undefined,
  ].filter(Boolean);
  return checkpoint.length > 0 ? `${count} ${checkpoint.join(" ")}` : count;
}

function buildSourceAccessMetric(session: SessionState): SessionOverviewMetric | undefined {
  const stats = session.turns.reduce<Required<Pick<ToolRuntimeStats,
    | "source_access_results"
    | "source_access_verified"
    | "source_access_discovery_only"
    | "source_access_network"
    | "source_access_dynamic_partial"
  >>>((acc, turn) => {
    const toolStats = turn.toolStats;
    acc.source_access_results += toolStats?.source_access_results ?? 0;
    acc.source_access_verified += toolStats?.source_access_verified ?? 0;
    acc.source_access_discovery_only += toolStats?.source_access_discovery_only ?? 0;
    acc.source_access_network += toolStats?.source_access_network ?? 0;
    acc.source_access_dynamic_partial += toolStats?.source_access_dynamic_partial ?? 0;
    return acc;
  }, {
    source_access_results: 0,
    source_access_verified: 0,
    source_access_discovery_only: 0,
    source_access_network: 0,
    source_access_dynamic_partial: 0,
  });

  if (stats.source_access_results <= 0) return undefined;
  const parts = [`${stats.source_access_verified}/${stats.source_access_results} verified`];
  if (stats.source_access_network > 0) parts.push(`${stats.source_access_network} network`);
  if (stats.source_access_dynamic_partial > 0) parts.push(`${stats.source_access_dynamic_partial} partial`);
  if (stats.source_access_discovery_only > 0) parts.push(`${stats.source_access_discovery_only} discovery`);
  return {
    label: "Evidence",
    value: parts.join(" · "),
    tone: stats.source_access_verified < stats.source_access_results || stats.source_access_dynamic_partial > 0 ? "warning" : undefined,
  };
}

function buildSessionRecallMetric(session: SessionState): SessionOverviewMetric | undefined {
  const stats = session.turns.reduce<Required<Pick<ToolRuntimeStats,
    | "session_search_calls"
    | "session_search_results"
    | "session_search_context_hits"
    | "session_search_matched_terms"
  >>>((acc, turn) => {
    const toolStats = turn.toolStats;
    acc.session_search_calls += toolStats?.session_search_calls ?? 0;
    acc.session_search_results += toolStats?.session_search_results ?? 0;
    acc.session_search_context_hits += toolStats?.session_search_context_hits ?? 0;
    acc.session_search_matched_terms += toolStats?.session_search_matched_terms ?? 0;
    return acc;
  }, {
    session_search_calls: 0,
    session_search_results: 0,
    session_search_context_hits: 0,
    session_search_matched_terms: 0,
  });

  if (stats.session_search_calls <= 0 && stats.session_search_results <= 0) return undefined;
  const parts = [`${stats.session_search_results} ${stats.session_search_results === 1 ? "hit" : "hits"}`];
  if (stats.session_search_calls > 1 || stats.session_search_results === 0) {
    parts.push(`${stats.session_search_calls} ${stats.session_search_calls === 1 ? "search" : "searches"}`);
  }
  if (stats.session_search_context_hits > 0) parts.push(`${stats.session_search_context_hits} context`);
  if (stats.session_search_matched_terms > 0) parts.push(`${stats.session_search_matched_terms} terms`);
  return {
    label: "Recall",
    value: parts.join(" · "),
    tone: stats.session_search_results > 0 ? "success" : "warning",
  };
}

function buildContextUsageMetric(session: SessionState, context?: SessionContextSummary): SessionOverviewMetric | undefined {
  const limit = context?.compact_trigger;
  if (!limit || limit <= 0) return undefined;
  const eventCount = estimateModelContextMessages(session);
  const summaryCount = context?.message_count ?? 0;
  const count = Math.max(eventCount, summaryCount);
  if (count <= 0) return undefined;
  const localPercent = Math.round((count / limit) * 100);
  const summaryPercent = context?.compact_percent != null && context.compact_percent > 0
    ? Math.round(context.compact_percent)
    : 0;
  const percent = Math.max(localPercent, summaryPercent);
  return {
    label: "Context",
    value: `${formatCount(count)}/${formatCount(limit)} · ${percent}%`,
    tone: percent >= 95 ? "error" : percent >= 80 ? "warning" : undefined,
  };
}

function estimateModelContextMessages(session: SessionState): number {
  let count = 0;
  let assistantToolTurn: string | undefined;
  let toolRequestsInBatch = 0;
  for (const event of session.events) {
    switch (event.type) {
      case EventType.UserMessage:
        count += 1;
        assistantToolTurn = undefined;
        toolRequestsInBatch = 0;
        break;
      case EventType.MessageDone:
        count += 1;
        assistantToolTurn = undefined;
        toolRequestsInBatch = 0;
        break;
      case EventType.ToolRequest:
        if (event.turnId !== assistantToolTurn || toolRequestsInBatch === 0) {
          count += 1;
          assistantToolTurn = event.turnId;
          toolRequestsInBatch = 1;
        } else {
          toolRequestsInBatch += 1;
        }
        break;
      case EventType.ToolResult:
        count += 1;
        assistantToolTurn = undefined;
        toolRequestsInBatch = 0;
        break;
      case EventType.ContextCompacted: {
        const after = readNumber(event.data, "after_messages");
        if (after != null) count = after;
        assistantToolTurn = undefined;
        toolRequestsInBatch = 0;
        break;
      }
    }
  }
  return count;
}

function buildPlanMetric(plan: SessionPlanSummary | undefined): SessionOverviewMetric | undefined {
  if (!plan) return undefined;
  if (plan.error) return { label: "Plan", value: "unreadable", tone: "warning" };
  if (plan.total_steps <= 0) return undefined;
  const parts = [`${plan.completed_steps}/${plan.total_steps}`];
  if (plan.current_step_index && !plan.done) {
    parts.push(`step ${plan.current_step_index} ${planStatusLabel(plan)}`);
  } else if (plan.done) {
    parts.push("done");
  } else if (plan.last_completed_step_index) {
    parts.push(`last step ${plan.last_completed_step_index}`);
  }
  return {
    label: "Plan",
    value: parts.join(" · "),
    tone: plan.blocked || plan.error ? "warning" : plan.done ? "success" : undefined,
  };
}

function planStatusLabel(plan: SessionPlanSummary): string {
  const status = plan.current_step_status?.trim();
  if (status === "in_progress") return "active";
  if (status === "blocked") return "blocked";
  if (status === "completed") return "done";
  if (status === "pending") return "pending";
  if (plan.active) return "active";
  if (plan.blocked) return "blocked";
  return "pending";
}

function buildArtifactMetric(session: SessionState): SessionOverviewMetric | undefined {
  const label = sessionArtifactLabel(session);
  if (!label) return undefined;
  return { label: label.startsWith("1 file") ? "Artifact" : "Artifacts", value: label };
}

function buildWorkMetric(
  latestTurn: TurnState | undefined,
  latestActivity: TurnActivityView | undefined,
  hasCurrentIssue: boolean,
): SessionOverviewMetric | undefined {
  const actionCount = latestTurn?.toolCalls.length ?? 0;
  const sourceCount = countActivityEvidence(latestActivity);
  if (actionCount === 0 && sourceCount === 0) return undefined;
  const parts: string[] = [];
  if (actionCount > 0) parts.push(`${actionCount} ${actionCount === 1 ? "action" : "actions"}`);
  if (sourceCount > 0) parts.push(`${sourceCount} source${sourceCount === 1 ? "" : "s"}`);
  const failed = latestTurn?.toolCalls.some((call) => call.status === "error") ?? false;
  return {
    label: "Work",
    value: parts.join(" · "),
    tone: failed ? (hasCurrentIssue ? "error" : "warning") : undefined,
  };
}

interface ThreadMetrics {
  actions: number;
  sources: number;
  handledIssues: number;
}

function summarizeThreadMetrics(metrics: ThreadMetrics): string {
  const parts = [`${metrics.actions} action${metrics.actions === 1 ? "" : "s"}`];
  if (metrics.sources > 0) parts.push(`${metrics.sources} source${metrics.sources === 1 ? "" : "s"}`);
  return parts.join(" · ");
}

function buildThreadMetrics(session: SessionState, latestTurn: TurnState): ThreadMetrics | undefined {
  if (!shouldShowThreadMetrics(session, latestTurn)) return undefined;
  let actions = 0;
  let sources = 0;
  let handledIssues = 0;
  for (const turn of session.turns) {
    if (turn === latestTurn) continue;
    actions += turn.toolCalls.length;
    sources += countActivityEvidence(buildTurnActivity(turn));
    handledIssues += turn.toolCalls.filter((call) => call.status === "error").length;
  }
  if (actions === 0) return undefined;
  return { actions, sources, handledIssues };
}

function shouldShowThreadMetrics(session: SessionState, latestTurn: TurnState): boolean {
  const latestAnswer = latestAssistantMessageText(latestTurn);
  if (session.turns.length < 2) return false;
  if (latestTurn.toolCalls.length > 0) return false;
  if (latestTurn.status !== "completed" || !latestAnswer) return false;
  if (!previousTurnsHaveToolWork(session, latestTurn)) return false;
  return looksLikeThreadFinalization(latestTurn.userText) || latestAnswerUsesPriorWork(latestAnswer);
}

function previousTurnsHaveToolWork(session: SessionState, latestTurn: TurnState): boolean {
  return session.turns.some((turn) => turn !== latestTurn && turn.toolCalls.length > 0);
}

function looksLikeThreadFinalization(text?: string): boolean {
  const value = text?.toLowerCase() ?? "";
  if (!value.trim()) return false;
  return /previous|已有|已经|前面|前两轮|本 session|session|不要再调用|直接基于|最终报告|final report|summari[sz]e/.test(value);
}

function latestAnswerUsesPriorWork(text: string): boolean {
  const value = text.toLowerCase();
  return /based on (?:the )?(?:previous|existing|collected)|基于(?:本|前|已|以上)|已经收集|实际查阅|工具调用|来源/.test(value);
}

function currentTurnIssueCount(turn: TurnState): number {
  if (turn.status === "error" || turn.error || turn.status === "max_turns") return 1;
  if (turn.status === "completed" && turn.assistantText.trim()) return 0;
  return turn.toolCalls.some((call) => call.status === "error") ? 1 : 0;
}

function settledToolIssueCount(turn: TurnState): number {
  if (currentTurnIssueCount(turn) > 0) return 0;
  if (turn.status !== "completed" || !turn.assistantText.trim()) return 0;
  return turn.toolCalls.filter((call) => call.status === "error").length;
}

function countActivityEvidence(activity?: TurnActivityView): number {
  return activity?.evidenceCount ?? 0;
}

function turnTokenCount(turn?: TurnState): number {
  if (!turn?.usage) return 0;
  return turn.usage.inputTokens + turn.usage.outputTokens;
}

function sessionTokenCount(session: SessionState): number {
  return session.totalUsage.inputTokens + session.totalUsage.outputTokens;
}

function formatCount(value: number): string {
  if (value < 1000) return String(value);
  if (value < 10_000) return `${(value / 1000).toFixed(1)}k`;
  return `${Math.round(value / 1000)}k`;
}

function formatTokenMillions(value: number): string {
  const millions = value / 1_000_000;
  if (value < 10_000) return `${millions.toFixed(4)}M`;
  if (value < 100_000) return `${millions.toFixed(3)}M`;
  return `${millions.toFixed(2)}M`;
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  if (value < 1024 * 1024) return `${Math.round(value / 1024)} KiB`;
  return `${Math.round(value / (1024 * 1024))} MiB`;
}

function readNumber(value: unknown, key: string): number | undefined {
  if (!value || typeof value !== "object") return undefined;
  const raw = (value as Record<string, unknown>)[key];
  return typeof raw === "number" && Number.isFinite(raw) ? raw : undefined;
}

function toneForTurn(turn: TurnState): SessionOverviewTone {
  if (turn.status === "running") return "running";
  if (turn.status === "completed") {
    const failedTools = turn.toolCalls.filter((call) => call.status === "error").length;
    if (failedTools > 0) return turn.assistantText.trim() ? "warning" : "error";
    return "success";
  }
  if (turn.status === "max_turns") return "warning";
  if (turn.status === "cancelled" || turn.status === "error" || turn.error) return "error";
  return "ready";
}

function summarize(text: string, limit: number): string {
  return summarizePreview(text, limit);
}

function summarizeAnswer(text: string, limit: number): string {
  return summarizeAnswerPreview(text, limit);
}
