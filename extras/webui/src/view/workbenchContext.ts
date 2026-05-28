import type { SessionSummary } from "../api/sessions";
import type { SessionState, TurnState } from "../store/sessionState";
import { buildExecutionTree, formatTokenUsageDetail, type ExecutionTreeNode } from "./executionTree";
import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import { displaySessionOverviewMetrics, type SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import type { SessionWorkspaceView } from "./sessionWorkspace";
import type { TurnArtifact } from "./turnArtifacts";
import type { WorkbenchAttention } from "./workbenchAttention";
import type { WorkbenchTab } from "./workbenchNav";

export interface WorkbenchContextEvidenceItem {
  target: WorkbenchTab;
  label: string;
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

export interface WorkbenchContextUsageItem {
  label: string;
  value: string;
  detail?: string;
}

export interface WorkbenchContextUsageView {
  items: WorkbenchContextUsageItem[];
}

export interface WorkbenchContextEvidenceInput {
  overview: SessionOverview;
  hasSelectedSession: boolean;
  attention?: WorkbenchAttention;
  workspace?: SessionWorkspaceView;
  changes?: SessionChangesView;
  artifacts?: readonly TurnArtifact[];
  files?: SessionFilesView;
  run?: SessionRunView;
  usage?: WorkbenchContextUsageView;
  automationTitle?: string;
  automationDetail?: string;
}

export function workbenchContextSummary(overview: SessionOverview, hasSelectedSession: boolean): string {
  if (overview.active) return overview.stateLabel;
  if (!hasSelectedSession) return "Fresh task";
  if (overview.tone === "error") return "Needs attention";
  if (overview.tone === "warning") return "Review needed";
  return overview.stateLabel || "Chat ready";
}

export function workbenchContextStatusDetail(input: Pick<WorkbenchContextEvidenceInput, "overview" | "attention">): string {
  return input.attention?.target === "context" ? input.attention.detail : input.overview.detail;
}

export function buildWorkbenchContextEvidence({
  workspace,
  changes,
  artifacts = [],
  files,
  run,
}: Pick<WorkbenchContextEvidenceInput, "workspace" | "changes" | "artifacts" | "files" | "run">): WorkbenchContextEvidenceItem[] {
  const items: WorkbenchContextEvidenceItem[] = [];
  if (workspace?.hasData) {
    items.push({
      target: "workspace",
      label: "Workspace",
      summary: workspace.summary,
      detail: workspace.detail,
      tone: workspace.tone,
    });
  }
  if (changes && changes.files.length > 0) {
    items.push({
      target: "changes",
      label: "Changes",
      summary: changes.summary,
      detail: changes.detail,
      tone: changes.tone,
    });
  }
  if (files && files.items.length > 0) {
    items.push({
      target: "files",
      label: "Files",
      summary: files.summary,
      detail: files.detail,
      tone: files.tone,
    });
  }
  if (run && run.commands.length > 0) {
    items.push({
      target: "run",
      label: "Run",
      summary: run.summary,
      detail: run.detail,
      tone: run.tone,
    });
  }
  if (artifacts.length > 0) {
    const latest = artifacts[artifacts.length - 1];
    items.push({
      target: "artifacts",
      label: "Artifacts",
      summary: `${artifacts.length} ${artifacts.length === 1 ? "artifact" : "artifacts"}`,
      detail: latest?.summary || latest?.path || "Generated files available",
    });
  }
  return items;
}

export function buildWorkbenchContextUsage(session: SessionState, summary?: SessionSummary): WorkbenchContextUsageView {
  const items: WorkbenchContextUsageItem[] = [];
  const traceTotal = tokenTotal(session.totalUsage.inputTokens, session.totalUsage.outputTokens);
  const summaryInput = summary?.usage?.input_tokens ?? 0;
  const summaryOutput = summary?.usage?.output_tokens ?? 0;
  const summaryTotal = tokenTotal(summaryInput, summaryOutput);
  if (traceTotal > 0) {
    items.push({
      label: "Session tokens",
      value: formatTokenSplit(session.totalUsage.inputTokens, session.totalUsage.outputTokens),
      detail: `${session.turns.length} ${session.turns.length === 1 ? "turn" : "turns"} from loaded trace`,
    });
  } else if (summaryTotal > 0) {
    const turns = summary?.usage?.turns ?? 0;
    items.push({
      label: "Session tokens",
      value: formatTokenSplit(summaryInput, summaryOutput),
      detail: turns > 0 ? `${turns} ${turns === 1 ? "turn" : "turns"} from session index` : "from session index",
    });
  }

  const latestTurn = latestTurnWithUsage(session);
  if (latestTurn?.usage) {
    items.push({
      label: "Latest turn tokens",
      value: formatTokenSplit(latestTurn.usage.inputTokens, latestTurn.usage.outputTokens),
      detail: latestTurn.id,
    });
  } else {
    const checkpoint = latestCheckpointUsage(summary);
    if (checkpoint) {
      items.push({
        label: "Latest turn tokens",
        value: formatTokenSplit(checkpoint.inputTokens, checkpoint.outputTokens),
        detail: "from loop checkpoint",
      });
    }
  }

  for (const delegated of delegatedTokenUsage(session).slice(0, 3)) {
    const merged = delegated.contextEstimatedTokens ? ` · merged ~${formatInteger(delegated.contextEstimatedTokens)} tokens` : "";
    items.push({
      label: delegated.kind === "focused_task" ? "Focused task tokens" : "Subagent tokens",
      value: formatTokenUsageDetail(delegated.tokenUsage),
      detail: `${delegated.title}${merged}`,
    });
  }

  return { items };
}

export function workbenchContextUsageSummary(usage?: WorkbenchContextUsageView): string | undefined {
  const sessionTokens = usage?.items.find((item) => item.label === "Session tokens");
  if (!sessionTokens) return undefined;
  return compactTokenValue(sessionTokens.value);
}

export function workbenchContextEvidenceText(input: WorkbenchContextEvidenceInput): string {
  const lines = [
    "Workbench context evidence",
    `State: ${input.overview.stateLabel || "Ready"}`,
    `Headline: ${input.overview.headline}`,
    `Detail: ${workbenchContextStatusDetail(input)}`,
  ];
  const metrics = displaySessionOverviewMetrics(input.overview.metrics);
  for (const metric of metrics) lines.push(`${metric.label}: ${metric.value}`);
  if (input.workspace?.path) lines.push(`Workspace path: ${input.workspace.path}`);
  if (input.workspace?.lastAgentCwd) lines.push(`Last agent cwd: ${input.workspace.lastAgentCwd}`);
  if (input.workspace?.latestCommandCwd) lines.push(`Latest command cwd: ${input.workspace.latestCommandCwd}`);
  for (const item of input.usage?.items ?? []) {
    lines.push(`${item.label}: ${item.value}${item.detail ? ` · ${item.detail}` : ""}`);
  }
  for (const item of buildWorkbenchContextEvidence(input)) lines.push(`${item.label}: ${item.summary} · ${item.detail}`);
  if (input.automationTitle) {
    lines.push(`Automation: ${input.automationTitle}${input.automationDetail ? ` · ${input.automationDetail}` : ""}`);
  }
  return lines.filter((line) => line.trim()).join("\n");
}

export function workbenchContextEvidenceDraft(input: WorkbenchContextEvidenceInput): string {
  return `Use this current chat context in the next step:\n${workbenchContextEvidenceText(input)}`;
}

function latestTurnWithUsage(session: SessionState): TurnState | undefined {
  return [...session.turns].reverse().find((turn) => !!turn.usage);
}

function latestCheckpointUsage(summary?: SessionSummary): { inputTokens: number; outputTokens: number } | undefined {
  const state = summary?.loop_protocol?.state ?? summary?.loop_state;
  const inputTokens = state?.last_turn_input_tokens ?? 0;
  const outputTokens = state?.last_turn_output_tokens ?? 0;
  return tokenTotal(inputTokens, outputTokens) > 0 ? { inputTokens, outputTokens } : undefined;
}

function delegatedTokenUsage(session: SessionState): Array<ExecutionTreeNode & { tokenUsage: NonNullable<ExecutionTreeNode["tokenUsage"]> }> {
  const nodes: Array<ExecutionTreeNode & { tokenUsage: NonNullable<ExecutionTreeNode["tokenUsage"]> }> = [];
  for (const turn of session.turns) {
    for (const node of buildExecutionTree(turn)) collectDelegatedTokenUsage(node, nodes);
  }
  return nodes.reverse();
}

function collectDelegatedTokenUsage(
  node: ExecutionTreeNode,
  nodes: Array<ExecutionTreeNode & { tokenUsage: NonNullable<ExecutionTreeNode["tokenUsage"]> }>,
) {
  if ((node.kind === "subagent" || node.kind === "focused_task") && node.tokenUsage) {
    nodes.push(node as ExecutionTreeNode & { tokenUsage: NonNullable<ExecutionTreeNode["tokenUsage"]> });
  }
  for (const child of node.children) collectDelegatedTokenUsage(child, nodes);
}

function formatTokenSplit(inputTokens: number, outputTokens: number): string {
  const total = tokenTotal(inputTokens, outputTokens);
  return `${formatInteger(total)} ${total === 1 ? "token" : "tokens"} (${formatInteger(inputTokens)} in / ${formatInteger(outputTokens)} out)`;
}

function tokenTotal(inputTokens: number, outputTokens: number): number {
  return inputTokens + outputTokens;
}

function formatInteger(value: number): string {
  return value.toLocaleString("en-US");
}

function compactTokenValue(value: string): string {
  return value.replace(/\s*\(.+\)\s*$/, "");
}
