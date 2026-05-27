import type { ToolCallState, ToolCallStatus, TurnState } from "../store/sessionState";
import { detectConstraintDeviations } from "./constraintDeviation";
import type { DraftSource } from "./draftSource";
import { summarizeUserError } from "./errorSummary";
import { buildExecutionTree, formatTokenUsageCompact, type ExecutionTreeNode } from "./executionTree";
import { memoryUpdatesForTurn, type MemoryUpdateSummary } from "./memoryUpdate";
import { describeSourceAccess, sourceEvidenceLabel } from "./sourceAccess";
import { artifactCountLabel, buildTurnArtifacts } from "./turnArtifacts";
import { formatByteCount } from "./byteFormat";
import { contextCompactionSummaryLabel } from "./contextCompaction";

export type TurnActivityTone = "running" | "success" | "warning" | "error" | "muted";
export type TurnActivityKind = "reasoning" | "action" | "result" | "answer" | "attention";

export interface TurnActivityItem {
  id: string;
  kind: TurnActivityKind;
  label: string;
  title: string;
  detail?: string;
  meta?: string;
  tone: TurnActivityTone;
}

export interface TurnActivityNode {
  id: string;
  depth: number;
  label: string;
  title: string;
  detail?: string;
  meta?: string;
  copyText: string;
  nextHint?: string;
  suggestedNext: string[];
  evidence: TurnActivityEvidence[];
  tone: TurnActivityTone;
  status: ToolCallStatus;
  autoOpen: boolean;
  children: TurnActivityNode[];
}

export interface TurnActivityEvidence {
  label: string;
  value: string;
  displayValue?: string;
}

export interface TurnActivityView {
  title: string;
  statusLabel: string;
  live: boolean;
  tone: TurnActivityTone;
  digest: TurnActivityDigest;
  evidenceCount: number;
  evidencePreview: TurnActivityEvidence[];
  evidenceAction?: TurnActivityBriefAction;
  brief: TurnActivityBrief;
  items: TurnActivityItem[];
  nodes: TurnActivityNode[];
}

export interface TurnActivityDigest {
  label: string;
  summary: string;
  meta: string[];
  tone: TurnActivityTone;
}

export interface TurnActivityBrief {
  rows: TurnActivityBriefRow[];
}

export type TurnActivityBriefRow =
  | { id: string; label: string; value: string; tone?: TurnActivityTone; action?: TurnActivityBriefAction }
  | { id: string; label: string; evidence: readonly TurnActivityEvidence[]; tone?: TurnActivityTone; action?: TurnActivityBriefAction };

export interface TurnActivityBriefAction {
  label: string;
  draft: string;
  source: DraftSource;
}

export interface TurnActivityOptions {
  continuedAfterLimit?: boolean;
  continuedIntoTurnNumber?: number;
}

export function buildTurnActivity(turn: TurnState, opts: TurnActivityOptions = {}): TurnActivityView | undefined {
  const items: TurnActivityItem[] = [];
  const treeNodes = buildExecutionTree(turn);
  const shouldSurfaceReasoning = turn.status === "running";
  const constraintDeviations = detectConstraintDeviations(turn);

  if (shouldSurfaceReasoning && turn.thinkingText.trim()) {
    items.push({
      id: `${turn.id}:reasoning`,
      kind: "reasoning",
      label: "Thinking",
      title: "Thinking through the next step",
      detail: summarize(turn.thinkingText, 180),
      tone: turn.thinkingStreaming ? "running" : "muted",
    });
  } else if (turn.thinkingStreaming) {
    items.push({
      id: `${turn.id}:reasoning`,
      kind: "reasoning",
      label: "Thinking",
      title: "Thinking through the next step",
      tone: "running",
    });
  }

  if (turn.messageStreaming) {
    items.push({
      id: `${turn.id}:answer`,
      kind: "answer",
      label: "Answer",
      title: "Writing the reply",
      detail: turn.assistantText ? summarize(turn.assistantText, 150) : undefined,
      tone: "running",
    });
  }

  if (turn.status === "max_turns" && !opts.continuedAfterLimit) {
    items.push({
      id: `${turn.id}:continue`,
      kind: "attention",
      label: "Next",
      title: "Final answer not produced",
      detail: "Affent stopped at its action limit before synthesizing the final reply.",
      tone: "warning",
    });
  }

  if (turn.error) {
    const summary = summarizeUserError(turn.error.code, turn.error.message);
    items.push({
      id: `${turn.id}:error`,
      kind: "attention",
      label: "Issue",
      title: summary.title,
      detail: summary.detail,
      meta: turn.error.recoverable ? "recoverable" : "stopped",
      tone: "error",
    });
  }

  const decision = latestVisibleLoopDecision(turn);
  if (decision) {
    items.push({
      id: `${turn.id}:decision:${decision.eventId}`,
      kind: "attention",
      label: loopDecisionLabel(decision),
      title: loopDecisionTitle(decision),
      detail: loopDecisionDetail(decision),
      meta: decision.confidence,
      tone: loopDecisionTone(decision),
    });
  }

  const compaction = latestContextCompaction(turn);
  if (compaction) {
    items.push({
      id: `${turn.id}:compaction:${compaction.eventId}`,
      kind: "attention",
      label: "Context",
      title: compaction.reactive ? "Context compacted reactively" : "Context compacted",
      detail: contextCompactionDetail(compaction),
      meta: compaction.reason,
      tone: compaction.reactive ? "warning" : "muted",
    });
  }

  const loopFeed = latestLoopProtocolFeed(turn);
  if (loopFeed) {
    items.push({
      id: `${turn.id}:loop-feed:${loopFeed.eventId}`,
      kind: "reasoning",
      label: "Loop",
      title: "Loop protocol fed",
      detail: loopProtocolFeedDetail(loopFeed),
      meta: loopFeed.mode,
      tone: "muted",
    });
  }

  const memoryUpdates = memoryUpdatesForTurn(turn);
  memoryUpdates.forEach((update, index) => {
    items.push({
      id: `${turn.id}:memory:${index}:${update.action}:${update.location}`,
      kind: "attention",
      label: "Memory",
      title: update.label,
      detail: memoryUpdateDetail(update),
      meta: update.location,
      tone: memoryUpdateTone(update),
    });
  });

  if (items.length === 0 && treeNodes.length === 0) return undefined;

  const nodes = treeNodes.map((node) => activityNodeFromExecutionNode(turn, node));

  const digest = buildDigest(turn, nodes, items, opts);
  const evidence = collectBriefEvidence(nodes);
  const evidenceCount = countEvidence(nodes);
  const evidenceAction = evidence.length > 0 ? evidenceBriefAction(evidence) : undefined;

  return {
    title: activityTitle(turn, opts),
    statusLabel: opts.continuedAfterLimit ? "Continued" : activityStatusLabel(turn),
    live: turn.status === "running",
    tone: opts.continuedAfterLimit ? "muted" : activityTone(turn),
    digest,
    evidenceCount,
    evidencePreview: selectHeadlineEvidence(evidence, 3),
    evidenceAction,
    brief: buildBrief(turn, nodes, opts, constraintDeviations, evidenceAction, evidence),
    items,
    nodes,
  };
}

function activityTitle(turn: TurnState, opts: TurnActivityOptions): string {
  if (opts.continuedAfterLimit) return "Earlier work";
  if (turn.status === "running") return "What Affent is doing";
  if (turn.status === "error" || turn.error || turn.status === "max_turns") return "Issue";
  return "What Affent did";
}

function buildBrief(
  turn: TurnState,
  nodes: readonly TurnActivityNode[],
  opts: TurnActivityOptions,
  constraintDeviations = detectConstraintDeviations(turn),
  evidenceAction?: TurnActivityBriefAction,
  evidence: readonly TurnActivityEvidence[] = collectBriefEvidence(nodes),
): TurnActivityBrief {
  const rows: TurnActivityBriefRow[] = [];
  const goal = turn.userText ? summarize(turn.userText, 120) : undefined;
  for (const deviation of constraintDeviations) {
    rows.push({ id: `constraint:${deviation.id}`, label: "Constraint", value: deviation.detail, tone: "warning" });
  }

  const decision = latestVisibleLoopDecision(turn);
  if (decision) {
    rows.push({
      id: `decision:${decision.eventId}`,
      label: loopDecisionLabel(decision),
      value: loopDecisionBrief(decision),
      tone: loopDecisionTone(decision),
      action: decision.required_action
        ? { label: loopDecisionActionLabel(decision), draft: `Continue: ${decision.required_action}`, source: "tool_guidance" }
        : undefined,
    });
  }

  const compaction = latestContextCompaction(turn);
  if (compaction) {
    rows.push({
      id: `compaction:${compaction.eventId}`,
      label: "Context",
      value: contextCompactionBrief(compaction),
      tone: compaction.reactive ? "warning" : "muted",
    });
  }

  const loopFeed = latestLoopProtocolFeed(turn);
  if (loopFeed) {
    rows.push({
      id: `loop-feed:${loopFeed.eventId}`,
      label: "Loop",
      value: loopProtocolFeedBrief(loopFeed),
      tone: "muted",
    });
  }

  memoryUpdatesForTurn(turn).forEach((update, index) => {
    rows.push({
      id: `memory:${index}:${update.action}:${update.location}`,
      label: "Memory",
      value: memoryUpdateBrief(update),
      tone: memoryUpdateTone(update),
    });
  });

  if (evidence.length > 0) {
    rows.push({
      id: "evidence",
      label: "Sources",
      evidence,
      action: evidenceAction,
    });
  }

  const handled = handledIssueBrief(turn);
  if (handled) rows.push(handled);

  const next = opts.continuedAfterLimit ? undefined : nextBrief(turn, nodes);
  if (next) rows.push({ id: "next", label: "Next", value: next.value, tone: nextTone(turn), action: next.action });

  return { rows: goal && rows.length > 0 ? [{ id: "goal", label: "Goal", value: goal }, ...rows] : rows };
}

function collectBriefEvidence(nodes: readonly TurnActivityNode[]): TurnActivityEvidence[] {
  return selectHeadlineEvidence(collectAllEvidence(nodes), 4);
}

function collectAllEvidence(nodes: readonly TurnActivityNode[]): TurnActivityEvidence[] {
  const evidence: TurnActivityEvidence[] = [];
  collectBriefEvidenceInto(nodes, evidence);
  return uniqueEvidence(evidence);
}

function selectHeadlineEvidence(evidence: readonly TurnActivityEvidence[], visibleCount: number): TurnActivityEvidence[] {
  return evidence
    .map((item, index) => ({ item, index, score: evidenceHeadlineScore(item) }))
    .sort((left, right) => {
      if (right.score !== left.score) return right.score - left.score;
      return left.index - right.index;
    })
    .slice(0, visibleCount)
    .map(({ item }) => item);
}

function evidenceHeadlineScore(item: TurnActivityEvidence): number {
  if (item.label === "Network Source") return 120;
  if (item.label === "Verified Source") return 110;
  if (item.label === "Partial Source") return 95;
  if (item.label === "Discovery Source") return 35;
  if (item.label === "Fetched") return 100;
  if (item.label === "Searched") return 90;
  if (item.label === "History") return 85;
  if (item.label === "Read") return 80;
  if (item.label === "Network refs" || item.label === "Network check") return 75;
  if (item.label === "Changed") return 70;
  if (item.label === "MCP") return 60;
  if (item.label === "Listed") return 50;
  if (item.label === "Ran") return 40;
  if (item.label === "Failed") return 30;
  return 10;
}

function collectBriefEvidenceInto(nodes: readonly TurnActivityNode[], evidence: TurnActivityEvidence[]) {
  for (const node of nodes) {
    evidence.push(...node.evidence);
    collectBriefEvidenceInto(node.children, evidence);
  }
}

function nextBrief(
  turn: TurnState,
  nodes: readonly TurnActivityNode[],
): { value: string; action?: TurnActivityBriefAction } | undefined {
  const suggested = firstSuggestedNext(nodes, shouldLeadWithFailure(turn));
  if (suggested) {
    const value = summarize(suggested, 132);
    return {
      value,
      action: { label: "Use next step", draft: `Continue: ${value}`, source: "tool_guidance" },
    };
  }
  if (turn.status === "running") {
    return {
      value: "You can still guide this run while it is working.",
      action: { label: "Guide run", draft: "Guidance for current run: ", source: "tool_guidance" },
    };
  }
  if (turn.status === "max_turns") {
    return {
      value: "Ask for a final answer from the evidence already gathered.",
      action: {
        label: "Final answer",
        draft: "Do not call more tools. Based only on the evidence already gathered in this chat, produce the final answer.",
        source: "continuation",
      },
    };
  }
  const failed = shouldLeadWithFailure(turn) ? findFailedNode(nodes) : undefined;
  if (failed?.nextHint) {
    const value = summarize(failed.nextHint, 132);
    return {
      value,
      action: { label: "Use next step", draft: `Continue: ${value}`, source: "tool_guidance" },
    };
  }
  if (turn.error?.recoverable) {
    return {
      value: "Continue with the error context attached.",
      action: { label: "Continue", draft: `Continue after ${turn.error.code}: ${turn.error.message}`, source: "error" },
    };
  }
  return undefined;
}

function firstSuggestedNext(nodes: readonly TurnActivityNode[], includeFailures: boolean): string | undefined {
  for (const node of nodes) {
    if (!includeFailures && isIssueNode(node)) continue;
    const own = node.suggestedNext[0] ?? node.nextHint;
    if (own) return own;
    const child = firstSuggestedNext(node.children, includeFailures);
    if (child) return child;
  }
  return undefined;
}

function nextTone(turn: TurnState): TurnActivityTone {
  if (turn.status === "running") return "running";
  if (turn.status === "error" || turn.status === "max_turns") return "warning";
  return "muted";
}

function buildDigest(
  turn: TurnState,
  nodes: readonly TurnActivityNode[],
  items: readonly TurnActivityItem[],
  opts: TurnActivityOptions,
): TurnActivityDigest {
  if (opts.continuedAfterLimit) {
    return {
      label: "Handoff",
      summary: continuedSummary(turn, nodes, opts),
      meta: digestMeta(turn, nodes),
      tone: "muted",
    };
  }

  const running = findRunningNode(nodes);
  if (running) {
    return {
      label: "Now",
      summary: running.detail && running.detail !== "Running" ? `${running.title}: ${running.detail}` : running.title,
      meta: digestMeta(turn, nodes),
      tone: "running",
    };
  }

  const failed = shouldLeadWithFailure(turn) ? findFailedNode(nodes) : undefined;
  if (failed) {
    return {
      label: "Issue",
      summary: failed.detail ? `${failed.title}: ${failed.detail}` : failed.title,
      meta: digestMeta(turn, nodes),
      tone: "error",
    };
  }

  const attention = items.find((item) => item.kind === "attention");
  if (attention) {
    return {
      label: attention.label,
      summary: attention.detail ? `${attention.title}: ${attention.detail}` : attention.title,
      meta: digestMeta(turn, nodes),
      tone: attention.tone,
    };
  }

  if (turn.status === "completed" && turn.assistantText.trim() && hasIssueNode(nodes)) {
    return {
      label: "Process",
      summary: completedWithRecoveredWorkSummary(turn, nodes),
      meta: digestMeta(turn, nodes),
      tone: activityTone(turn),
    };
  }

  const conclusion = firstConclusion(nodes, shouldLeadWithFailure(turn));
  if (conclusion) {
    return {
      label: "Result",
      summary: conclusion,
      meta: digestMeta(turn, nodes),
      tone: activityTone(turn),
    };
  }

  if (nodes.length > 0) {
    return {
      label: "Result",
      summary: completedActionSummary(turn),
      meta: digestMeta(turn, nodes),
      tone: activityTone(turn),
    };
  }

  const reasoning = items.find((item) => item.kind === "reasoning");
  if (reasoning) {
    return {
      label: reasoning.label,
      summary: reasoning.detail ?? reasoning.title,
      meta: digestMeta(turn, nodes),
      tone: reasoning.tone,
    };
  }

  return {
    label: activityStatusLabel(turn),
    summary: turn.assistantText ? summarize(turn.assistantText, 150) : "No activity yet.",
    meta: digestMeta(turn, nodes),
    tone: activityTone(turn),
  };
}

function continuedSummary(
  turn: TurnState,
  nodes: readonly TurnActivityNode[],
  opts: TurnActivityOptions,
): string {
  const handoff = opts.continuedIntoTurnNumber
    ? `message ${opts.continuedIntoTurnNumber} continued the task`
    : "a later message continued the task";
  const progress = continuedProgressSummary(turn, nodes);
  return progress ? `${progress}; ${handoff}.` : `${capitalize(handoff)}.`;
}

function continuedProgressSummary(turn: TurnState, nodes: readonly TurnActivityNode[]): string {
  const actionCount = turn.toolCalls.length;
  const evidenceCount = countEvidence(nodes);
  const issueCount = turn.toolCalls.filter((call) => call.status === "error").length;
  const parts: string[] = [];
  if (actionCount > 0 && evidenceCount > 0) {
    parts.push(`checked ${evidenceCount} evidence ${pluralize("source", evidenceCount)} across ${actionCount} ${pluralize("action", actionCount)}`);
  } else if (evidenceCount > 0) {
    parts.push(`collected ${evidenceCount} evidence ${pluralize("source", evidenceCount)}`);
  } else if (actionCount > 0) {
    parts.push(`ran ${actionCount} ${pluralize("action", actionCount)}`);
  }
  if (issueCount > 0) parts.push(`${issueCount} ${pluralize("issue", issueCount)} carried forward`);
  if (parts.length === 0) return "";
  return capitalize(parts.join("; "));
}

function findRunningNode(nodes: readonly TurnActivityNode[]): TurnActivityNode | undefined {
  for (const node of nodes) {
    if (node.status === "running") return node;
    const child = findRunningNode(node.children);
    if (child) return child;
  }
  return undefined;
}

function shouldLeadWithFailure(turn: TurnState): boolean {
  if (turn.status === "error" || turn.error) return true;
  if (turn.status === "completed" && turn.assistantText.trim()) return false;
  return turn.toolCalls.some((call) => call.status === "error");
}

function findFailedNode(nodes: readonly TurnActivityNode[]): TurnActivityNode | undefined {
  for (let index = nodes.length - 1; index >= 0; index -= 1) {
    const node = nodes[index];
    const child = findFailedNode(node.children);
    if (child) return child;
    if (node.status === "error") return node;
  }
  return undefined;
}

function firstConclusion(nodes: readonly TurnActivityNode[], includeFailures: boolean): string | undefined {
  for (const node of nodes) {
    if (!includeFailures && isIssueNode(node)) continue;
    if (node.detail && node.detail !== "Finished" && node.detail !== "Running") return node.detail;
    const child = firstConclusion(node.children, includeFailures);
    if (child) return child;
  }
  return undefined;
}

function completedActionSummary(turn: TurnState): string {
  const count = turn.toolCalls.length;
  const failed = turn.toolCalls.filter((call) => call.status === "error").length;
  const actionLabel = `${count} ${pluralize("action", count)}`;
  if (count === 0) return "No action details.";
  if (failed > 0 && turn.status === "completed" && turn.assistantText.trim()) {
    return `${actionLabel} completed; worked around ${failed} ${pluralize("issue", failed)}.`;
  }
  if (failed > 0) return `${failed} of ${actionLabel} failed.`;
  return `${capitalize(actionLabel)} completed.`;
}

function completedWithRecoveredWorkSummary(turn: TurnState, nodes: readonly TurnActivityNode[]): string {
  const failed = turn.toolCalls.filter((call) => call.status === "error").length;
  const handledLabel = `${failed} ${pluralize("issue", failed)}`;
  if (hasDelegatedWork(nodes)) {
    return `Collected evidence through delegated work; worked around ${handledLabel}.`;
  }
  const count = turn.toolCalls.length;
  if (count > 1) return `Checked ${count} ${pluralize("action", count)}; worked around ${handledLabel}.`;
  return `Answered after working around ${handledLabel}.`;
}

function isIssueNode(node: TurnActivityNode): boolean {
  if (node.status === "error") return true;
  return node.meta?.includes("max_turns") ?? false;
}

function hasIssueNode(nodes: readonly TurnActivityNode[]): boolean {
  return nodes.some((node) => isIssueNode(node) || hasIssueNode(node.children));
}

function hasDelegatedWork(nodes: readonly TurnActivityNode[]): boolean {
  return nodes.some((node) => node.label === "Delegate" || node.label === "Focused task" || hasDelegatedWork(node.children));
}

function digestMeta(turn: TurnState, nodes: readonly TurnActivityNode[]): string[] {
  const meta: string[] = [];
  const actionCount = turn.toolCalls.length;
  const evidenceCount = countEvidence(nodes);
  const artifactLabel = artifactCountLabel(buildTurnArtifacts(turn));
  const delegatedCount = nodes.filter(isDelegatedNode).length;
  const tokenCount = turn.usage ? turn.usage.inputTokens + turn.usage.outputTokens : 0;
  if (delegatedCount > 0) {
    meta.push(`${delegatedCount} delegated ${pluralize("task", delegatedCount)}`);
  } else if (nodes.length > 0 && actionCount <= 3) {
    meta.push(`${nodes.length} ${pluralize("step", nodes.length)}`);
  }
  if (actionCount > 0) meta.push(`${actionCount} ${pluralize("action", actionCount)}`);
  if (tokenCount > 0) meta.push(`${formatTokenCount(tokenCount)} ${pluralize("token", tokenCount)}`);
  if (artifactLabel) meta.push(artifactLabel);
  if (evidenceCount > 0) meta.push(`${evidenceCount} evidence`);
  const decisionCount = visibleLoopDecisions(turn).length;
  if (decisionCount > 0) meta.push(`${decisionCount} ${pluralize("decision", decisionCount)}`);
  const compactionCount = turn.contextCompactions?.length ?? 0;
  if (compactionCount > 0) meta.push(`${compactionCount} ${pluralize("compaction", compactionCount)}`);
  const loopFeedCount = turn.loopProtocolFeeds?.length ?? 0;
  if (loopFeedCount > 0) meta.push(`${loopFeedCount} loop ${pluralize("feed", loopFeedCount)}`);
  const memoryUpdateCount = memoryUpdatesForTurn(turn).length;
  if (memoryUpdateCount > 0) meta.push(`${memoryUpdateCount} memory ${pluralize("update", memoryUpdateCount)}`);
  return meta;
}

function visibleLoopDecisions(turn: TurnState) {
  return (turn.loopDecisions ?? []).filter((decision) => decision.visible_in_ui !== false);
}

function latestVisibleLoopDecision(turn: TurnState) {
  return visibleLoopDecisions(turn).at(-1);
}

function loopDecisionLabel(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): string {
  if (decision.kind === "research_checkpoint") return "Research";
  return "Decision";
}

function loopDecisionTitle(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): string {
  const kind = loopDecisionDisplayName(decision);
  return `${capitalize(kind)}: ${decision.decision || "decision"}`;
}

function loopDecisionDetail(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): string | undefined {
  const parts = [decision.reason, decision.required_action ? `Next: ${decision.required_action}` : undefined].filter(Boolean);
  return parts.length > 0 ? summarize(parts.join(" "), 180) : undefined;
}

function loopDecisionBrief(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): string {
  const parts = [
    decision.kind === "research_checkpoint" ? "checkpoint triggered" : decision.decision,
    decision.reason,
    decision.required_action ? `Next: ${decision.required_action}` : undefined,
  ].filter(Boolean);
  return summarize(parts.join(" · "), 150);
}

function loopDecisionDisplayName(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): string {
  if (decision.kind === "research_checkpoint") return "research checkpoint";
  return decision.kind ? decision.kind.replace(/_/g, " ") : "runtime";
}

function loopDecisionActionLabel(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): string {
  if (decision.kind === "research_checkpoint") return "Research next";
  return "Use action";
}

function loopDecisionTone(decision: NonNullable<ReturnType<typeof latestVisibleLoopDecision>>): TurnActivityTone {
  if (decision.kind === "research_checkpoint" || decision.decision === "defer" || decision.required_action) return "warning";
  return "muted";
}

function latestContextCompaction(turn: TurnState) {
  return turn.contextCompactions?.at(-1);
}

function latestLoopProtocolFeed(turn: TurnState) {
  return turn.loopProtocolFeeds?.at(-1);
}

function loopProtocolFeedDetail(feed: NonNullable<ReturnType<typeof latestLoopProtocolFeed>>): string {
  const parts = [
    feed.mode ? `${feed.mode} feed` : "feed",
    feed.feed_number > 0 ? `#${feed.feed_number}` : undefined,
    feed.protocol_path,
    loopProtocolFeedCalibration(feed),
    loopProtocolFeedPlan(feed),
  ].filter(Boolean);
  return summarize(parts.join(" · "), 180);
}

function loopProtocolFeedBrief(feed: NonNullable<ReturnType<typeof latestLoopProtocolFeed>>): string {
  const parts = [
    feed.mode ? `${feed.mode} feed` : "feed",
    feed.feed_number > 0 ? `#${feed.feed_number}` : undefined,
    loopProtocolFeedCalibration(feed),
    loopProtocolFeedPlan(feed),
    feed.protocol_path,
  ].filter(Boolean);
  return summarize(parts.join(" · "), 180);
}

function loopProtocolFeedCalibration(feed: NonNullable<ReturnType<typeof latestLoopProtocolFeed>>): string | undefined {
  if (!feed.calibration_answers && !feed.last_calibration_answer_preview) return undefined;
  const label = feed.calibration_answers ? `calibration ${feed.calibration_answers}` : "calibration";
  return feed.last_calibration_answer_preview
    ? `${label} · ${feed.last_calibration_answer_preview}`
    : label;
}

function loopProtocolFeedPlan(feed: NonNullable<ReturnType<typeof latestLoopProtocolFeed>>): string | undefined {
  const checkpoint = [
    feed.plan_label,
    feed.plan_current_step_index ? `step ${feed.plan_current_step_index}` : undefined,
    feed.plan_current_step_status,
    feed.plan_current_step,
  ].filter(Boolean);
  return checkpoint.length > 0 ? checkpoint.join(" · ") : undefined;
}

function contextCompactionDetail(compaction: NonNullable<ReturnType<typeof latestContextCompaction>>): string {
  const parts = [
    `${compaction.before_messages}->${compaction.after_messages} messages`,
    compaction.removed_messages > 0 ? `removed ${compaction.removed_messages}` : undefined,
    compaction.summary_bytes && compaction.summary_bytes > 0 ? `${formatByteCount(compaction.summary_bytes)} summary` : undefined,
    contextCompactionSummaryLabel(compaction),
    compaction.summary_preview ? `summary: ${summarize(compaction.summary_preview, 180)}` : undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function contextCompactionBrief(compaction: NonNullable<ReturnType<typeof latestContextCompaction>>): string {
  const prefix = compaction.reactive ? "reactive" : "scheduled";
  const reason = compaction.reason ? ` · ${compaction.reason}` : "";
  return `${prefix} · ${contextCompactionDetail(compaction)}${reason}`;
}

function memoryUpdateDetail(update: MemoryUpdateSummary): string {
  return summarize([update.location, update.preview].filter(Boolean).join(" · "), 180);
}

function memoryUpdateBrief(update: MemoryUpdateSummary): string {
  return summarize([update.label, update.location, update.preview].filter(Boolean).join(" · "), 180);
}

function memoryUpdateTone(update: MemoryUpdateSummary): TurnActivityTone {
  return update.action === "remove" ? "warning" : "success";
}

function formatTokenCount(count: number): string {
  if (count < 1000) return String(count);
  const value = count / 1000;
  const precision = count < 10000 ? 1 : 0;
  return `${value.toFixed(precision).replace(/\.0$/, "")}k`;
}

function isDelegatedNode(node: TurnActivityNode): boolean {
  return node.label === "Delegate" || node.label === "Focused task";
}

function countEvidence(nodes: readonly TurnActivityNode[]): number {
  return collectAllEvidence(nodes).length;
}

function pluralize(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function capitalize(text: string): string {
  return text ? `${text[0].toUpperCase()}${text.slice(1)}` : text;
}

function activityNodeFromExecutionNode(turn: TurnState, node: ExecutionTreeNode): TurnActivityNode {
  const call = turn.toolCalls.find((candidate) => candidate.callId === node.callId);
  return {
    id: node.id,
    depth: node.depth,
    label: actionLabel(node),
    title: summarize(node.title, 120),
    detail: actionDetail(node),
    meta: actionMeta(node, call),
    copyText: activityNodeRecordText(node),
    nextHint: node.nextHint,
    suggestedNext: node.suggestedNext,
    evidence: collectEvidence(node),
    tone: toneFromStatus(node.status),
    status: node.status,
    autoOpen: node.status === "running" || node.children.some(hasRunningDescendant),
    children: node.children.map((child) => activityNodeFromExecutionNode(turn, child)),
  };
}

function activityNodeRecordText(node: ExecutionTreeNode): string {
  const lines = [
    `Action: ${node.title}`,
    `Kind: ${node.label}`,
    `Tool: ${node.tool}`,
    `Status: ${copyStatusLabel(node.status)}`,
  ];
  if (node.durationMs != null) lines.push(`Duration: ${formatDuration(node.durationMs)}`);
  if (node.exitCode != null) lines.push(`Exit: ${node.exitCode}`);
  if (node.callId) lines.push(`Request ID: ${node.callId}`);
  if (node.objective) lines.push(`Task: ${node.objective}`);
  if (node.mcpServer) lines.push(`MCP server: ${node.mcpServer}`);
  if (node.mcpTool) lines.push(`MCP action: ${node.mcpTool}`);
  if (node.nextHint) lines.push(`Next: ${node.nextHint}`);
  if (node.args) lines.push(`Input:\n${JSON.stringify(node.args, null, 2)}`);
  const result = primaryCopyResult(node);
  if (result) lines.push(`Output:\n${summarize(result, 2000)}`);
  return lines.join("\n");
}

function primaryCopyResult(node: ExecutionTreeNode): string | undefined {
  if (node.report) return node.report;
  if (node.summary) return node.summary;
  if (node.resultText && node.resultText !== node.resultSummary) return node.resultText;
  return node.resultSummary ?? node.resultText;
}

function copyStatusLabel(status: ToolCallStatus): string {
  if (status === "running") return "running";
  if (status === "error") return "failed";
  return "done";
}

function hasRunningDescendant(node: ExecutionTreeNode): boolean {
  if (node.status === "running") return true;
  return node.children.some(hasRunningDescendant);
}

function actionLabel(node: ExecutionTreeNode): string {
  if (node.kind === "subagent") return "Delegate";
  if (node.kind === "focused_task") return "Focused task";
  if (node.kind === "mcp") return "MCP";
  return "Action";
}

function actionDetail(node: ExecutionTreeNode): string | undefined {
  if (node.status === "running") return "Running";
  if (node.status === "error") {
    const next = node.nextHint ? `Next: ${node.nextHint}` : undefined;
    return next ?? (summarize(node.preview ?? node.resultSummary ?? node.resultText ?? "", 150) || "Failed");
  }
  const detail = node.summary ?? conclusionFromReport(node.report) ?? node.preview ?? node.resultSummary ?? node.resultText;
  return detail ? summarize(detail, 150) : "Finished";
}

function conclusionFromReport(report?: string): string | undefined {
  if (!report) return undefined;
  const conclusion = report.match(/(?:^|\n)\s*Conclusion:\s*([\s\S]*?)(?:\n\s*(?:Evidence|Findings|Warnings|Next):|\n\s*-\s|\n{2,}|$)/i)?.[1];
  const value = conclusion?.trim() || report.split(/\r?\n/).find((line) => line.trim() && !/^\s*(conclusion|evidence):\s*$/i.test(line));
  return value?.replace(/^\s*[-*]\s*/, "").trim() || undefined;
}

function collectEvidence(node: ExecutionTreeNode): TurnActivityEvidence[] {
  if (!shouldShowEvidence(node)) return [];
  const evidence: TurnActivityEvidence[] = [];
  collectEvidenceInto(node, evidence);
  return uniqueEvidence(evidence).slice(0, 5);
}

function shouldShowEvidence(node: ExecutionTreeNode): boolean {
  return node.children.length > 0 || node.kind === "subagent" || node.kind === "focused_task" || isEvidenceTool(node.tool) || !!sourceAccessFromNode(node);
}

function isEvidenceTool(tool: string): boolean {
  return tool === "web_fetch" || tool === "web_search" || tool === "session_search" || tool === "browser_navigate" || tool === "browser_snapshot" || tool === "browser_find" || tool === "browser_network" || tool === "browser_network_read";
}

function collectEvidenceInto(node: ExecutionTreeNode, evidence: TurnActivityEvidence[]) {
  const own = evidenceFromNode(node);
  if (own) evidence.push(own);
  for (const child of node.children) collectEvidenceInto(child, evidence);
}

function evidenceFromNode(node: ExecutionTreeNode): TurnActivityEvidence | undefined {
  if (node.status !== "success") return undefined;
  const sourceAccess = sourceAccessFromNode(node);
  if (sourceAccess) {
    const displayValue = sourceEvidenceDisplayValue(sourceAccess);
    return {
      label: titleCase(sourceEvidenceLabel(sourceAccess)),
      value: sourceAccess.accessedUrl,
      displayValue,
    };
  }
  if (node.tool === "session_search") return sessionSearchEvidence(node);
  if (node.tool === "browser_network") return browserNetworkEvidence(node);
  const url = stringArg(node, "url");
  if (node.tool === "web_fetch" && url) return { label: "Fetched", value: url, displayValue: readableUrl(url) };
  const path = stringArg(node, "path") ?? stringArg(node, "file") ?? stringArg(node, "filename");
  if (node.tool === "read_file" && path) return { label: "Read", value: path };
  if (node.tool === "list_files" && path) return { label: "Listed", value: path };
  if ((node.tool === "write_file" || node.tool === "edit_file") && path) return { label: "Changed", value: path };
  const query = stringArg(node, "query") ?? stringArg(node, "q");
  if (node.tool === "web_search" && query) return { label: "Searched", value: query };
  if (node.kind === "mcp" && query) return { label: "MCP", value: query };
  const command = stringArg(node, "command");
  if (node.tool === "shell" && command) return { label: "Ran", value: command };
  return undefined;
}

function sourceAccessFromNode(node: ExecutionTreeNode) {
  return describeSourceAccess(node.resultText ?? node.resultSummary);
}

function sessionSearchEvidence(node: ExecutionTreeNode): TurnActivityEvidence | undefined {
  const payload = parseJsonObject(node.resultText) ?? parseJsonObject(node.resultSummary);
  if (!payload) return undefined;
  const results = Array.isArray(payload.results) ? payload.results : [];
  for (const candidate of results) {
    if (!isRecord(candidate)) continue;
    const sessionId = stringField(candidate, "session_id");
    if (!sessionId) continue;
    const turnIndex = numberField(candidate, "turn_idx");
    const messageIndex = numberField(candidate, "message_idx");
    const matchedTerms = stringArrayField(candidate, "matched_terms").slice(0, 3);
    const contextIncluded = booleanField(candidate, "context_included");
    const snippet = stringField(candidate, "snippet");
    const total = numberField(payload, "total") ?? results.length;
    const extra = Math.max(0, total - 1);
    const value = [sessionId, turnIndex == null ? undefined : `turn-${turnIndex}`].filter(Boolean).join(":");
    const displayValue = [
      total > 1 ? `${total} hits` : undefined,
      sessionId,
      turnIndex == null ? undefined : `turn ${turnIndex}`,
      messageIndex == null ? undefined : `message ${messageIndex}`,
      matchedTerms.length > 0 ? matchedTerms.join(", ") : undefined,
      contextIncluded ? "context" : undefined,
      snippet ? `snippet ${summarize(snippet, 96)}` : undefined,
      extra > 0 ? `+${extra} more` : undefined,
    ].filter(Boolean).join(" · ");
    return { label: "History", value, displayValue };
  }
  return undefined;
}

function browserNetworkEvidence(node: ExecutionTreeNode): TurnActivityEvidence | undefined {
  const result = node.resultText ?? node.resultSummary ?? "";
  if (!result.includes("BROWSER NETWORK EVIDENCE")) return undefined;
  const page = firstPrefixedLineValue(result, "CURRENT_PAGE:");
  const query = firstPrefixedLineValue(result, "query:");
  const value = page || query || "browser_network";
  const matchLabel = browserNetworkMatchLabel(result);
  const refs = browserNetworkRefs(result);
  const previews = browserNetworkPreviews(result);
  const displayParts = [
    page ? readableUrl(page) : undefined,
    query ? query.replace(/^"|"$/g, "") : undefined,
    matchLabel,
    refs.length > 0 ? `refs ${refs.slice(0, 3).join(", ")}` : undefined,
    previews.length > 0 ? `preview ${previews.slice(0, 2).map((preview) => summarize(preview, 56)).join(" | ")}` : undefined,
    browserNetworkEvidenceCaution(matchLabel),
  ].filter((part): part is string => !!part);
  return {
    label: matchLabel === "matches" ? "Network refs" : "Network check",
    value,
    displayValue: displayParts.join(" · ") || value,
  };
}

function browserNetworkMatchLabel(result: string): string | undefined {
  for (const line of result.split(/\r?\n/)) {
    const trimmed = line.trim();
    if (trimmed === "MATCHES: none") return "no matches";
    if (trimmed === "MATCHES:") return "matches";
  }
  return undefined;
}

function browserNetworkRefs(result: string): string[] {
  const refs: string[] = [];
  for (const line of result.split(/\r?\n/)) {
    const match = line.trim().match(/^-\s+([a-z]\d+)\b/i);
    if (match?.[1] && !refs.includes(match[1])) refs.push(match[1]);
  }
  return refs;
}

function browserNetworkPreviews(result: string): string[] {
  const previews: string[] = [];
  for (const line of result.split(/\r?\n/)) {
    const match = line.trim().match(/^preview:\s*(.+)$/i);
    const value = match?.[1]?.trim();
    if (value && !previews.includes(value)) previews.push(value);
  }
  return previews;
}

function browserNetworkEvidenceCaution(matchLabel: string | undefined): string | undefined {
  if (matchLabel === "matches") return "read before citing";
  if (matchLabel === "no matches") return "no citable source";
  return "refs only";
}

function firstPrefixedLineValue(result: string, prefix: string): string | undefined {
  const line = result.split(/\r?\n/).find((candidate) => candidate.trimStart().startsWith(prefix));
  const value = line?.trim().slice(prefix.length).trim();
  return value || undefined;
}

function parseJsonObject(text?: string): Record<string, unknown> | undefined {
  if (!text) return undefined;
  try {
    const value = JSON.parse(text);
    return isRecord(value) ? value : undefined;
  } catch {
    return undefined;
  }
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringField(record: Record<string, unknown>, key: string): string | undefined {
  const value = record[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function numberField(record: Record<string, unknown>, key: string): number | undefined {
  const value = record[key];
  if (typeof value === "number" && Number.isFinite(value)) return value;
  if (typeof value !== "string" || !value.trim()) return undefined;
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : undefined;
}

function booleanField(record: Record<string, unknown>, key: string): boolean {
  return record[key] === true;
}

function stringArrayField(record: Record<string, unknown>, key: string): string[] {
  const value = record[key];
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string" && !!item.trim()).map((item) => item.trim());
}

function sourceEvidenceDisplayValue(sourceAccess: NonNullable<ReturnType<typeof sourceAccessFromNode>>): string {
  return [
    readableUrl(sourceAccess.accessedUrl),
    sourceAccess.requestedUrl && sourceAccess.requestedUrl !== sourceAccess.accessedUrl ? `from ${readableUrl(sourceAccess.requestedUrl)}` : undefined,
    sourceAccess.ref ? `ref ${sourceAccess.ref}` : undefined,
    sourceAccess.httpStatus ? `http ${sourceAccess.httpStatus}` : undefined,
    sourceAccess.contentType,
    sourceAccess.jsonPath,
    sourceAccess.resultPreview ? `preview ${summarize(sourceAccess.resultPreview, 96)}` : undefined,
  ].filter(Boolean).join(" · ");
}

function titleCase(value: string): string {
  return value.replace(/\b\w/g, (char) => char.toUpperCase());
}

function handledIssueBrief(turn: TurnState): TurnActivityBriefRow | undefined {
  if (turn.status !== "completed" || !turn.assistantText.trim()) return undefined;
  const failed = turn.toolCalls.filter((call) => call.status === "error");
  if (failed.length === 0) return undefined;
  const evidence = issueEvidenceFromFailedCalls(failed).slice(0, 3);
  return {
    id: "handled",
    label: "Tool issues",
    evidence,
    tone: "warning",
    action: evidence.length > 0
      ? {
        label: "Use issue context",
        draft: issueContextDraft(evidence),
        source: "error",
      }
      : undefined,
  };
}

function issueEvidenceFromFailedCalls(failed: readonly ToolCallState[]): TurnActivityEvidence[] {
  const evidence: TurnActivityEvidence[] = [];
  for (const call of failed) {
    const item = issueTargetEvidence(call);
    if (item) evidence.push(item);
  }
  return uniqueEvidence(evidence);
}

function issueContextDraft(evidence: readonly TurnActivityEvidence[]): string {
  return [
    "Use these issue targets in the next step:",
    ...evidence.map((item) => `- ${evidenceDraftValue(item, { useRawValue: true })}`),
  ].join("\n");
}

function issueTargetEvidence(call: ToolCallState): TurnActivityEvidence | undefined {
  const failureKind = call.failureKind ?? call.failureKinds?.[0];
  const label = failureKind ? `Failed ${failureKind}` : "Failed";
  const url = typeof call.args.url === "string" ? call.args.url.trim() : undefined;
  if (call.tool === "web_fetch" && url) {
    return { label, value: url, displayValue: readableUrl(url) };
  }
  const query = typeof call.args.query === "string" ? call.args.query.trim() : undefined;
  if (call.tool === "web_search" && query) {
    return { label, value: query, displayValue: summarize(query, 42) };
  }
  const command = typeof call.args.command === "string" ? call.args.command.trim() : undefined;
  if (call.tool === "shell" && command) {
    return { label, value: command, displayValue: summarize(command, 42) };
  }
  const task = typeof call.args.task === "string" ? call.args.task.trim() : undefined;
  if (task) return { label, value: task, displayValue: summarize(task, 42) };
  const objective = typeof call.args.objective === "string" ? call.args.objective.trim() : undefined;
  if (objective) return { label, value: objective, displayValue: summarize(objective, 42) };
  const path =
    typeof call.args.path === "string"
      ? call.args.path.trim()
      : typeof call.args.file === "string"
        ? call.args.file.trim()
        : undefined;
  if (path) return { label, value: path, displayValue: summarize(path, 42) };
  return { label, value: call.tool };
}

function readableUrl(value: string): string {
  try {
    const url = new URL(value);
    const host = url.hostname.replace(/^www\./, "");
    const path = url.pathname.replace(/\/+$/, "");
    if (!path || path === "/") return host;
    const segments = path.split("/").filter(Boolean).slice(0, 2);
    return `${host}/${segments.join("/")}`;
  } catch {
    return summarize(value, 64);
  }
}

function stringArg(node: ExecutionTreeNode, key: string): string | undefined {
  const value = node.args?.[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function uniqueEvidence(items: readonly TurnActivityEvidence[]): TurnActivityEvidence[] {
  const seen = new Set<string>();
  const unique: TurnActivityEvidence[] = [];
  for (const item of items) {
    const key = evidenceKey(item);
    if (seen.has(key)) continue;
    seen.add(key);
    unique.push(item);
  }
  return unique;
}

function evidenceKey(item: TurnActivityEvidence): string {
  if (item.label === "Fetched") return `${item.label}:${canonicalUrlKey(item.value)}`;
  return `${item.label}:${item.value.trim()}`;
}

function evidenceDraft(evidence: readonly TurnActivityEvidence[]): string {
  return [
    "Use this evidence in the next step:",
    ...evidence.map((item) => `- ${evidenceDraftValue(item)}`),
  ].join("\n");
}

function evidenceBriefAction(evidence: readonly TurnActivityEvidence[]): TurnActivityBriefAction {
  return {
    label: "Use sources",
    draft: evidenceDraft(evidence),
    source: "evidence",
  };
}

function evidenceDraftValue(item: TurnActivityEvidence, opts: { useRawValue?: boolean } = {}): string {
  const value = opts.useRawValue ? item.value : item.displayValue || item.value;
  if (item.label === "Network refs") return `${item.label} ${value} (call browser_network_read before citing values)`;
  if (item.label === "Network check") return `${item.label} ${value} (not a citable source)`;
  return `${item.label} ${value}`;
}

function canonicalUrlKey(value: string): string {
  try {
    const url = new URL(value);
    const host = url.hostname.replace(/^www\./, "").toLowerCase();
    const path = url.pathname.replace(/\/+$/, "");
    const search = url.searchParams.toString();
    return `${host}${path || "/"}${search ? `?${search}` : ""}`;
  } catch {
    return value.trim();
  }
}

function actionMeta(node: ExecutionTreeNode, call?: ToolCallState): string | undefined {
  const parts = [
    statusLabel(node.status, node.exitCode),
    node.turnEndReason && node.turnEndReason !== "completed" ? node.turnEndReason : undefined,
    node.durationMs != null ? formatDuration(node.durationMs) : undefined,
    node.kind === "subagent" || node.kind === "focused_task"
      ? node.tokenUsage ? formatTokenUsageCompact(node.tokenUsage) : undefined
      : undefined,
    call?.resultArtifactPath ? "file saved" : undefined,
    call?.argsRepaired || call?.canonicalized ? "repaired" : undefined,
    call?.resultTruncated || call?.argsTruncated ? "truncated" : undefined,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : undefined;
}

function statusLabel(status: ToolCallStatus, exitCode?: number): string {
  if (status === "running") return "running";
  if (status === "error") return exitCode == null ? "failed" : `exit ${exitCode}`;
  return "done";
}

function toneFromStatus(status: ToolCallStatus): TurnActivityTone {
  if (status === "running") return "running";
  if (status === "error") return "error";
  return "success";
}

function activityStatusLabel(turn: TurnState): string {
  if (turn.status === "running") return "Live";
  if (turn.status === "error") return "Issue";
  if (turn.status === "max_turns") return "Continue";
  if (turn.status === "cancelled") return "Stopped";
  return "Done";
}

function activityTone(turn: TurnState): TurnActivityTone {
  if (turn.error || turn.status === "error") return "error";
  if (turn.toolCalls.some((call) => call.status === "error")) return turn.status === "completed" ? "warning" : "error";
  if (turn.status === "running") return "running";
  if (turn.status === "max_turns" || turn.status === "cancelled") return "warning";
  return "success";
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  return `${s.toFixed(s < 10 ? 2 : 1)}s`;
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}
