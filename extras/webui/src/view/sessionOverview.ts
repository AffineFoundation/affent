import type { SessionState, TurnState } from "../store/sessionState";
import type { WorkflowStatus } from "../store/workflowStatus";
import { conversationTopicFromTurns } from "./continuationPrompt";
import { summarizeSessionTitle } from "./sessionList";
import { buildTurnActivity, type TurnActivityView } from "./turnActivity";
import { summarizeAnswerPreview, summarizePreview } from "./textPreview";

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
}: {
  session: SessionState;
  workflow: WorkflowStatus;
  hasSelectedSession: boolean;
  pendingTask?: string;
  pendingGuidance?: string;
  sessionTitle?: string;
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
      metrics: buildMetrics(session),
    };
  }

  if (guidance && latestTurn?.status === "running") {
    return {
      headline: sessionTitle ?? task ?? "Live turn",
      detail: "Applying your guidance to the current run.",
      stateLabel: "Sending guidance",
      tone: "running",
      active: true,
      metrics: buildMetrics(session, latestTurn, latestActivity),
    };
  }

  if (!hasSelectedSession && !latestTurn) {
    return {
      headline: "Start a chat",
      detail: "Describe the outcome you want; Affent will create the chat.",
      stateLabel: "Ready",
      tone: "ready",
      active: false,
      metrics: buildMetrics(session),
    };
  }

  if (!latestTurn) {
    return {
      headline: "Message Affent",
      detail: "The answer and the action details will stay together in this chat.",
      stateLabel: "Ready",
      tone: "ready",
      active: false,
      metrics: buildMetrics(session),
    };
  }

  const tone = toneForTurn(latestTurn);
  return {
    headline: sessionTitle ?? task ?? workflow.title,
    detail: overviewDetail(latestTurn, latestActivity, workflow),
    stateLabel: workflow.title,
    tone,
    active: workflow.active,
    metrics: buildMetrics(session, latestTurn, latestActivity),
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

function buildMetrics(session: SessionState, latestTurn?: TurnState, latestActivity?: TurnActivityView): SessionOverviewMetric[] {
  const metrics: SessionOverviewMetric[] = [];

  const currentIssueCount = latestTurn ? currentTurnIssueCount(latestTurn) : 0;
  if (currentIssueCount > 0) metrics.push({ label: currentIssueCount === 1 ? "Issue" : "Issues", value: String(currentIssueCount), tone: "error" });
  const settledIssues = latestTurn ? settledToolIssueCount(latestTurn) : 0;
  if (settledIssues > 0) metrics.push({ label: settledIssues === 1 ? "Tool issue" : "Tool issues", value: String(settledIssues), tone: "warning" });
  if (latestTurn?.toolCalls.length) {
    const failed = latestTurn.toolCalls.some((call) => call.status === "error");
    metrics.push({ label: "Actions", value: String(latestTurn.toolCalls.length), tone: currentIssueCount > 0 && failed ? "error" : undefined });
  }
  const evidenceCount = countActivityEvidence(latestActivity);
  if (evidenceCount > 0) metrics.push({ label: "Evidence", value: String(evidenceCount) });
  const threadMetrics = latestTurn ? buildThreadMetrics(session, latestTurn) : undefined;
  if (threadMetrics) {
    if (threadMetrics.handledIssues > 0 && settledIssues === 0) {
      metrics.push({ label: threadMetrics.handledIssues === 1 ? "Tool issue" : "Tool issues", value: String(threadMetrics.handledIssues), tone: "warning" });
    }
    metrics.push({ label: "Task actions", value: String(threadMetrics.actions) });
    if (threadMetrics.evidence > 0) metrics.push({ label: "Task evidence", value: String(threadMetrics.evidence) });
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
  if (session.unknownEventCount > 0) metrics.push({ label: "Notes", value: String(session.unknownEventCount), tone: "warning" });

  return metrics;
}

interface ThreadMetrics {
  actions: number;
  evidence: number;
  handledIssues: number;
}

function buildThreadMetrics(session: SessionState, latestTurn: TurnState): ThreadMetrics | undefined {
  if (!shouldShowThreadMetrics(session, latestTurn)) return undefined;
  let actions = 0;
  let evidence = 0;
  let handledIssues = 0;
  for (const turn of session.turns) {
    if (turn === latestTurn) continue;
    actions += turn.toolCalls.length;
    evidence += countActivityEvidence(buildTurnActivity(turn));
    handledIssues += turn.toolCalls.filter((call) => call.status === "error").length;
  }
  if (actions === 0) return undefined;
  return { actions, evidence, handledIssues };
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

function toneForTurn(turn: TurnState): SessionOverviewTone {
  if (turn.status === "running") return "running";
  if (turn.status === "completed") return "success";
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
