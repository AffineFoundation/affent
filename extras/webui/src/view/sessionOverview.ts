import { EventType, type ToolRuntimeStats } from "../api/events";
import type { SessionContextSummary, SessionPlanSummary } from "../api/sessions";
import type { SessionState, TurnState } from "../store/sessionState";
import type { WorkflowStatus } from "../store/workflowStatus";
import { conversationTopicFromTurns } from "./continuationPrompt";
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

export function buildSessionOverview({
  session,
  workflow,
  hasSelectedSession,
  pendingTask,
  pendingGuidance,
  sessionTitle,
  planSummary,
  contextSummary,
}: {
  session: SessionState;
  workflow: WorkflowStatus;
  hasSelectedSession: boolean;
  pendingTask?: string;
  pendingGuidance?: string;
  sessionTitle?: string;
  planSummary?: SessionPlanSummary;
  contextSummary?: SessionContextSummary;
}): SessionOverview {
  const latestTurn = session.turns.at(-1);
  const latestActivity = latestTurn ? buildTurnActivity(latestTurn) : undefined;
  const latestTask = latestTurn?.userText ? summarizeSessionTitle(latestTurn.userText) : undefined;
  const topic = conversationTopicFromTurns(session.turns);
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
      metrics: buildMetrics(session, undefined, undefined, planSummary, contextSummary),
    };
  }

  if (guidance && latestTurn?.status === "running") {
    return {
      headline: sessionTitle ?? task ?? "Live turn",
      detail: "Applying your guidance to the current run.",
      stateLabel: "Sending guidance",
      tone: "running",
      active: true,
      metrics: buildMetrics(session, latestTurn, latestActivity, planSummary, contextSummary),
    };
  }

  if (!hasSelectedSession && !latestTurn) {
    return {
      headline: "Start a chat",
      detail: "Describe the outcome you want; Affent will create the chat.",
      stateLabel: "Ready",
      tone: "ready",
      active: false,
      metrics: buildMetrics(session, undefined, undefined, planSummary, contextSummary),
    };
  }

  if (!latestTurn) {
    return {
      headline: "Message Affent",
      detail: "The answer and the action details will stay together in this chat.",
      stateLabel: "Ready",
      tone: "ready",
      active: false,
      metrics: buildMetrics(session, undefined, undefined, planSummary, contextSummary),
    };
  }

  const tone = toneForTurn(latestTurn);
  return {
    headline: sessionTitle ?? task ?? workflow.title,
    detail: overviewDetail(latestTurn, latestActivity, workflow),
    stateLabel: workflow.title,
    tone,
    active: workflow.active,
    metrics: buildMetrics(session, latestTurn, latestActivity, planSummary, contextSummary),
  };
}

function pendingTaskDetail({ hasSelectedSession, hasLatestTurn }: { hasSelectedSession: boolean; hasLatestTurn: boolean }): string {
  if (!hasSelectedSession) return "Creating chat; first update will appear here.";
  if (hasLatestTurn) return "Follow-up sent; next update will appear here.";
  return "Message sent; first update will appear here.";
}

function overviewDetail(turn: TurnState, activity: TurnActivityView | undefined, workflow: WorkflowStatus): string {
  if (activity?.digest.summary && activity.digest.summary !== "No activity yet.") {
    const summary = summarize(activity.digest.summary, 140);
    if (
      turn.status === "completed" &&
      turn.assistantText.trim() &&
      (activity.digest.label === "Process" || isMechanicalActivitySummary(summary))
    ) {
      return summarizeAnswer(turn.assistantText, 140);
    }
    return summary;
  }
  if (turn.status === "completed" && turn.assistantText.trim()) {
    return summarizeAnswer(turn.assistantText, 140);
  }
  if (turn.assistantText.trim()) return summarizeAnswer(turn.assistantText, 140);
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
): SessionOverviewMetric[] {
  const metrics: SessionOverviewMetric[] = [];

  const currentIssueCount = latestTurn ? currentTurnIssueCount(latestTurn) : 0;
  if (currentIssueCount > 0) metrics.push({ label: currentIssueCount === 1 ? "Issue" : "Issues", value: String(currentIssueCount), tone: "error" });
  const contextMetric = buildContextUsageMetric(session, contextSummary);
  if (contextMetric) metrics.push(contextMetric);
  const settledIssues = latestTurn ? settledToolIssueCount(latestTurn) : 0;
  if (settledIssues > 0) metrics.push({ label: settledIssues === 1 ? "Tool issue" : "Tool issues", value: String(settledIssues), tone: "warning" });
  const artifactMetric = buildArtifactMetric(session);
  if (artifactMetric) metrics.push(artifactMetric);
  const loopMetric = buildLoopMetric(session);
  if (loopMetric) metrics.push(loopMetric);
  const compactMetric = buildContextCompactionMetric(session);
  if (compactMetric) metrics.push(compactMetric);
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
    metrics.push({ label: "Turn tokens", value: formatCount(latestTokens) });
    metrics.push({ label: "Chat tokens", value: formatCount(totalTokens) });
  } else if (latestTokens > 0) {
    metrics.push({ label: "Tokens", value: formatCount(latestTokens) });
  } else if (totalTokens > 0) {
    metrics.push({ label: "Chat tokens", value: formatCount(totalTokens) });
  }
  if (latestTurn?.endReason && latestTurn.endReason !== latestTurn.status) {
    metrics.push({ label: "End", value: latestTurn.endReason, tone: latestTurn.status === "max_turns" ? "warning" : undefined });
  }
  if (session.unknownEventCount > 0) metrics.push({ label: "Unclassified", value: String(session.unknownEventCount), tone: "warning" });

  return metrics;
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

function buildLoopMetric(session: SessionState): SessionOverviewMetric | undefined {
  const stats = session.turns.reduce((acc, turn) => {
    acc.interventions += turn.toolStats?.loop_guard_interventions ?? 0;
    acc.forcedNoTools += turn.toolStats?.forced_no_tools ?? 0;
    if (turn.status === "max_turns" || turn.endReason === "max_turns") acc.maxTurns += 1;
    return acc;
  }, { interventions: 0, forcedNoTools: 0, maxTurns: 0 });
  const visibleDecisions = session.loopDecisions.filter((decision) => decision.visible_in_ui !== false);
  if (stats.interventions <= 0 && stats.forcedNoTools <= 0 && stats.maxTurns <= 0 && visibleDecisions.length === 0) return undefined;
  const parts: string[] = [];
  if (stats.maxTurns > 0) parts.push(`${stats.maxTurns} max-turn${stats.maxTurns === 1 ? "" : "s"}`);
  if (stats.interventions > 0) parts.push(`${stats.interventions} guard${stats.interventions === 1 ? "" : "s"}`);
  if (stats.forcedNoTools > 0) parts.push(`${stats.forcedNoTools} no-tools`);
  if (visibleDecisions.length > 0) {
    const latest = visibleDecisions.at(-1);
    parts.push(`${visibleDecisions.length} decision${visibleDecisions.length === 1 ? "" : "s"}${latest?.decision ? ` ${latest.decision}` : ""}`);
  }
  return { label: "Loop", value: parts.join(" · "), tone: stats.maxTurns > 0 || stats.interventions > 0 ? "warning" : undefined };
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
  const percent = Math.round((count / limit) * 100);
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
  if (session.turns.length < 2) return false;
  if (latestTurn.toolCalls.length > 0) return false;
  if (latestTurn.status !== "completed" || !latestTurn.assistantText.trim()) return false;
  if (!previousTurnsHaveToolWork(session, latestTurn)) return false;
  return looksLikeThreadFinalization(latestTurn.userText) || latestAnswerUsesPriorWork(latestTurn.assistantText);
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
