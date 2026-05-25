import type { ToolCallState, ToolCallStatus, TurnState } from "../store/sessionState";
import { detectConstraintDeviations } from "./constraintDeviation";
import type { DraftSource } from "./draftSource";
import { summarizeUserError } from "./errorSummary";
import { buildExecutionTree, formatTokenUsageCompact, type ExecutionTreeNode } from "./executionTree";

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
  | { id: string; label: string; evidence: TurnActivityEvidence[]; tone?: TurnActivityTone };

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
      label: "Reasoning",
      title: turn.thinkingStreaming ? "Thinking through the next step" : "Working plan",
      detail: summarize(turn.thinkingText, 180),
      tone: turn.thinkingStreaming ? "running" : "muted",
    });
  } else if (turn.thinkingStreaming) {
    items.push({
      id: `${turn.id}:reasoning`,
      kind: "reasoning",
      label: "Reasoning",
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
      detail: "The runtime stopped at its action limit before synthesizing the final reply.",
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

  if (items.length === 0 && treeNodes.length === 0) return undefined;

  const nodes = treeNodes.map((node) => activityNodeFromExecutionNode(turn, node));

  const digest = buildDigest(turn, nodes, items, opts);
  const evidenceCount = countEvidence(nodes);

  return {
    title: "Agent activity",
    statusLabel: opts.continuedAfterLimit ? "Continued" : activityStatusLabel(turn),
    live: turn.status === "running",
    tone: opts.continuedAfterLimit ? "muted" : activityTone(turn),
    digest,
    evidenceCount,
    evidencePreview: collectBriefEvidence(nodes).slice(0, 3),
    brief: buildBrief(turn, nodes, digest, opts, constraintDeviations),
    items,
    nodes,
  };
}

function buildBrief(
  turn: TurnState,
  nodes: readonly TurnActivityNode[],
  digest: TurnActivityDigest,
  opts: TurnActivityOptions,
  constraintDeviations = detectConstraintDeviations(turn),
): TurnActivityBrief {
  const rows: TurnActivityBriefRow[] = [];
  const goal = turn.userText ? summarize(turn.userText, 120) : undefined;
  if (goal) rows.push({ id: "goal", label: "Goal", value: goal });
  for (const deviation of constraintDeviations) {
    rows.push({ id: `constraint:${deviation.id}`, label: "Constraint", value: deviation.detail, tone: "warning" });
  }
  rows.push({ id: "focus", label: focusLabel(digest), value: digest.summary, tone: digest.tone });

  const evidence = collectBriefEvidence(nodes);
  if (evidence.length > 0) rows.push({ id: "evidence", label: "Evidence", evidence });

  const handled = handledIssueBrief(turn);
  if (handled) rows.push(handled);

  const next = opts.continuedAfterLimit ? undefined : nextBrief(turn, nodes);
  if (next) rows.push({ id: "next", label: "Next", value: next.value, tone: nextTone(turn), action: next.action });

  return { rows };
}

function focusLabel(digest: TurnActivityDigest): string {
  if (digest.label === "Now") return "Current focus";
  if (digest.tone === "error") return "Issue";
  return "Result";
}

function collectBriefEvidence(nodes: readonly TurnActivityNode[]): TurnActivityEvidence[] {
  return collectAllEvidence(nodes).slice(0, 4);
}

function collectAllEvidence(nodes: readonly TurnActivityNode[]): TurnActivityEvidence[] {
  const evidence: TurnActivityEvidence[] = [];
  collectBriefEvidenceInto(nodes, evidence);
  return uniqueEvidence(evidence);
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
      value: "Guidance can still be sent while this work is running.",
      action: { label: "Guide turn", draft: "Guidance for the current work: ", source: "tool_guidance" },
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
      label: "Needs attention",
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
      label: hasDelegatedWork(nodes) ? "Result" : "Action summary",
      summary: conclusion,
      meta: digestMeta(turn, nodes),
      tone: activityTone(turn),
    };
  }

  if (nodes.length > 0) {
    return {
      label: hasDelegatedWork(nodes) ? "Result" : "Action summary",
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
    return `${actionLabel} completed; handled ${failed} ${pluralize("issue", failed)}.`;
  }
  if (failed > 0) return `${failed} of ${actionLabel} need attention.`;
  return `${capitalize(actionLabel)} completed.`;
}

function completedWithRecoveredWorkSummary(turn: TurnState, nodes: readonly TurnActivityNode[]): string {
  const failed = turn.toolCalls.filter((call) => call.status === "error").length;
  const handledLabel = `${failed} ${pluralize("issue", failed)}`;
  if (hasDelegatedWork(nodes)) {
    return `Collected evidence through delegated work; handled ${handledLabel}.`;
  }
  const count = turn.toolCalls.length;
  if (count > 1) return `Checked ${count} ${pluralize("action", count)}; handled ${handledLabel}.`;
  return `Answered after handling ${handledLabel}.`;
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
  const delegatedCount = nodes.filter(isDelegatedNode).length;
  if (delegatedCount > 0) {
    meta.push(`${delegatedCount} delegated ${pluralize("task", delegatedCount)}`);
  } else if (nodes.length > 0 && actionCount <= 3) {
    meta.push(`${nodes.length} ${pluralize("step", nodes.length)}`);
  }
  if (actionCount > 0) meta.push(`${actionCount} ${pluralize("action", actionCount)}`);
  if (evidenceCount > 0) meta.push(`${evidenceCount} evidence`);
  return meta;
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
    nextHint: node.nextHint,
    suggestedNext: node.suggestedNext,
    evidence: collectEvidence(node),
    tone: toneFromStatus(node.status),
    status: node.status,
    autoOpen: node.status === "running" || node.children.some(hasRunningDescendant),
    children: node.children.map((child) => activityNodeFromExecutionNode(turn, child)),
  };
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
  return node.children.length > 0 || node.kind === "subagent" || node.kind === "focused_task" || isEvidenceTool(node.tool);
}

function isEvidenceTool(tool: string): boolean {
  return tool === "web_fetch" || tool === "web_search";
}

function collectEvidenceInto(node: ExecutionTreeNode, evidence: TurnActivityEvidence[]) {
  const own = evidenceFromNode(node);
  if (own) evidence.push(own);
  for (const child of node.children) collectEvidenceInto(child, evidence);
}

function evidenceFromNode(node: ExecutionTreeNode): TurnActivityEvidence | undefined {
  if (node.status !== "success") return undefined;
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

function handledIssueBrief(turn: TurnState): TurnActivityBriefRow | undefined {
  if (turn.status !== "completed" || !turn.assistantText.trim()) return undefined;
  const failed = turn.toolCalls.filter((call) => call.status === "error");
  if (failed.length === 0) return undefined;
  const names = uniqueStrings(failed.map((call) => toolTargetLabel(call)).filter(Boolean)).slice(0, 3);
  const suffix = names.length > 0 ? `: ${names.join(", ")}` : "";
  return {
    id: "handled",
    label: "Handled",
    value: `${failed.length} ${pluralize("tool issue", failed.length)} worked around${suffix}.`,
    tone: "warning",
  };
}

function toolTargetLabel(call: ToolCallState): string | undefined {
  const url = typeof call.args.url === "string" ? readableUrl(call.args.url) : undefined;
  if (call.tool === "web_fetch" && url) return url;
  const query = typeof call.args.query === "string" ? call.args.query.trim() : undefined;
  if (call.tool === "web_search" && query) return summarize(query, 42);
  const task = typeof call.args.task === "string" ? call.args.task.trim() : undefined;
  const objective = typeof call.args.objective === "string" ? call.args.objective.trim() : undefined;
  const path =
    typeof call.args.path === "string"
      ? call.args.path.trim()
      : typeof call.args.file === "string"
        ? call.args.file.trim()
        : undefined;
  return summarize(task || objective || path || call.tool, 42);
}

function uniqueStrings(items: readonly (string | undefined)[]): string[] {
  const seen = new Set<string>();
  const unique: string[] = [];
  for (const item of items) {
    const value = item?.trim();
    if (!value || seen.has(value)) continue;
    seen.add(value);
    unique.push(value);
  }
  return unique;
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
  if (turn.status === "error") return "Needs attention";
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
