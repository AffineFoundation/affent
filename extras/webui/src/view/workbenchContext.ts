import type { SessionContextSummary, SessionSummary, SessionTaskStateSummary } from "../api/sessions";
import { EventType } from "../api/events";
import type { SessionState, TurnState } from "../store/sessionState";
import { buildExecutionTree, type ExecutionTokenUsage, type ExecutionTreeNode } from "./executionTree";
import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import { displaySessionOverviewMetrics, type SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import type { SessionWorkspaceView } from "./sessionWorkspace";
import { isToolResultStoragePath } from "./toolResultDisplay";
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

export interface WorkbenchContextUsageTrendPoint {
  label: string;
  value: number;
  valueLabel: string;
  detail?: string;
}

export interface WorkbenchContextUsageView {
  items: WorkbenchContextUsageItem[];
  trend: WorkbenchContextUsageTrendPoint[];
  totalTokens: number;
}

export interface WorkbenchRequestModeView {
  raw: string;
  label: string;
  detail?: string;
  turnId?: string;
  source?: string;
}

export interface WorkbenchConversationContextView extends SessionContextSummary {
  estimated?: boolean;
}

export interface WorkbenchAttachment {
  label: string;
  title: string;
  detail?: string;
  metrics?: readonly string[];
  tone?: "live" | "saved" | "none";
}

export interface WorkbenchAttachmentInput {
  selectedSessionId?: string;
  selectedSessionTitle?: string;
  selectedSession?: Pick<SessionSummary, "active" | "durable">;
  workspace?: Pick<SessionWorkspaceView, "hasData" | "shortStatus">;
  usage?: WorkbenchContextUsageView;
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
  requestMode?: WorkbenchRequestModeView;
  taskState?: SessionTaskStateSummary;
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
  const detail = input.attention?.target === "context" ? input.attention.detail : input.overview.detail;
  return isLowSignalStatusDetail(detail) ? input.overview.headline : detail;
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
    items.push({
      target: "artifacts",
      label: "Artifacts",
      summary: `${artifacts.length} ${artifacts.length === 1 ? "artifact" : "artifacts"}`,
      detail: workbenchArtifactContextDetail(artifacts),
    });
  }
  return items;
}

export function workbenchArtifactContextDetail(artifacts: readonly TurnArtifact[]): string {
  const latest = artifacts.at(-1);
  if (!latest) return "Generated files available";
  const kind = artifactIsFullOutput(latest) ? "full output" : "deliverable";
  const title = artifactContextTitle(latest);
  const origin = compact([
    latest.turnNumber != null ? `turn ${latest.turnNumber}` : undefined,
    latest.tool,
  ]).join(" · ");
  const source = compactSource(latest.source);
  const parts = [
    `Latest ${kind}: ${title}`,
    origin || undefined,
    source ? `from ${source}` : undefined,
  ];
  return compact(parts).join(" · ");
}

export function buildWorkbenchContextUsage(session: SessionState, summary?: SessionSummary): WorkbenchContextUsageView {
  const items: WorkbenchContextUsageItem[] = [];
  const traceTotal = tokenTotal(session.totalUsage.inputTokens, session.totalUsage.outputTokens);
  const summaryInput = summary?.usage?.input_tokens ?? 0;
  const summaryOutput = summary?.usage?.output_tokens ?? 0;
  const summaryTotal = tokenTotal(summaryInput, summaryOutput);
  const totalTokens = traceTotal > 0 ? traceTotal : summaryTotal;
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
    const merged = delegated.contextEstimatedTokens ? ` · merged ~${formatTokenCountMillions(delegated.contextEstimatedTokens)}` : "";
    items.push({
      label: delegated.kind === "focused_task" ? "Focused task tokens" : "Subagent tokens",
      value: formatExecutionTokenUsage(delegated.tokenUsage),
      detail: `${delegated.title}${merged}`,
    });
  }

  return { items, trend: usageTrend(session, summary), totalTokens };
}

export function buildConversationContextView(session: SessionState, summary?: SessionContextSummary): WorkbenchConversationContextView | undefined {
  if (summary && summary.compact_trigger > 0) return { ...summary };
  const messageCount = estimateModelContextMessages(session);
  if (messageCount <= 0) return undefined;
  const compactTrigger = 240;
  const messagesUntilCompact = Math.max(0, compactTrigger - messageCount);
  return {
    message_count: messageCount,
    compact_trigger: compactTrigger,
    compact_percent: Math.round((messageCount * 100) / compactTrigger),
    messages_until_compact: messagesUntilCompact,
    estimated: true,
  };
}

export function latestWorkbenchRequestMode(session?: SessionState): WorkbenchRequestModeView | undefined {
  const event = [...(session?.events ?? [])].reverse().find((candidate) => candidate.type === EventType.UserMessage);
  const raw = normalizeRequestMode(readString(event?.data, "mode"));
  if (!raw || raw === "normal") return undefined;
  const turnId = readString(event?.data, "turn_id") ?? event?.turnId;
  const source = readString(event?.data, "source");
  return {
    raw,
    label: requestModeLabel(raw),
    detail: compact([source === "schedule" ? "scheduled" : "latest request", turnId]).join(" · "),
    turnId,
    source,
  };
}

export function workbenchContextUsageSummary(usage?: WorkbenchContextUsageView): string | undefined {
  const sessionTokens = usage?.items.find((item) => item.label === "Session tokens");
  if (!sessionTokens) return undefined;
  return compactTokenValue(sessionTokens.value);
}

export function buildWorkbenchAttachment({
  selectedSessionId,
  selectedSessionTitle,
  selectedSession,
  workspace,
  usage,
}: WorkbenchAttachmentInput): WorkbenchAttachment {
  if (!selectedSessionId) {
    return {
      label: "Attached chat",
      title: "No chat attached",
      detail: "Fresh task",
      tone: "none",
    };
  }
  const metrics = [
    selectedSession?.active ? "Live" : selectedSession?.durable ? "Saved" : "Selected",
    workspace?.hasData ? workspace.shortStatus : undefined,
    workbenchContextUsageSummary(usage),
  ].filter((value): value is string => Boolean(value));
  return {
    label: "Attached chat",
    title: selectedSessionTitle ?? selectedSessionId,
    detail: selectedSessionId,
    metrics,
    tone: selectedSession?.active ? "live" : "saved",
  };
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
  if (input.requestMode) lines.push(`Request mode: ${input.requestMode.label}${input.requestMode.detail ? ` · ${input.requestMode.detail}` : ""}`);
  if (input.taskState?.objective) lines.push(`Task objective: ${input.taskState.objective}`);
  if (input.taskState?.status && input.taskState.status !== "unknown") lines.push(`Task status: ${input.taskState.status}`);
  if (input.taskState?.request_mode && input.taskState.request_mode !== "normal") lines.push(`Task request mode: ${input.taskState.request_mode}`);
  if (input.taskState?.request_source) {
    lines.push(`Task request source: ${[input.taskState.request_source, input.taskState.schedule_kind, input.taskState.schedule_id].filter(Boolean).join(" · ")}`);
  }
  if (input.taskState?.current_step) lines.push(`Current step: ${input.taskState.current_step}`);
  if (input.taskState?.next_step) lines.push(`Next step: ${input.taskState.next_step}`);
  if (input.taskState?.verification_state && input.taskState.verification_state !== "unknown") lines.push(`Verification: ${input.taskState.verification_state}`);
  for (const question of input.taskState?.open_questions?.slice(-3) ?? []) {
    lines.push(`Open question: ${question}`);
  }
  for (const file of input.taskState?.changed_files?.slice(-5) ?? []) {
    lines.push(`Changed file: ${[file.action, file.path].filter(Boolean).join(" ")}`);
  }
  for (const failure of input.taskState?.failed_actions?.slice(-3) ?? []) {
    lines.push(`Failed action: ${[failure.tool, failure.summary].filter(Boolean).join(" · ")}`);
  }
  for (const evidence of input.taskState?.evidence?.slice(-3) ?? []) {
    lines.push(`Task evidence: ${[evidence.source, evidence.summary].filter(Boolean).join(" · ")}`);
  }
  for (const item of buildWorkbenchContextEvidence(input)) lines.push(`${item.label}: ${item.summary} · ${item.detail}`);
  return lines.filter((line) => line.trim()).join("\n");
}

export function workbenchContextEvidenceDraft(input: WorkbenchContextEvidenceInput): string {
  return `Use this current chat context in the next step:\n${workbenchContextEvidenceText(input)}`;
}

function latestTurnWithUsage(session: SessionState): TurnState | undefined {
  return [...session.turns].reverse().find((turn) => !!turn.usage);
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

function readNumber(data: unknown, key: string): number | undefined {
  if (!data || typeof data !== "object") return undefined;
  const value = (data as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function readString(data: unknown, key: string): string | undefined {
  if (!data || typeof data !== "object") return undefined;
  const value = (data as Record<string, unknown>)[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function normalizeRequestMode(mode: string | undefined): string | undefined {
  return mode?.trim().toLowerCase();
}

function requestModeLabel(mode: string): string {
  if (mode === "loop_setup") return "Loop setup";
  if (mode === "plan_only") return "Plan only";
  if (mode === "execute_plan") return "Execute plan";
  return mode.replace(/_/g, " ");
}

function artifactIsFullOutput(artifact: TurnArtifact): boolean {
  return artifact.truncated || artifact.path.replace(/\\/g, "/").includes("/tool-results/");
}

function artifactContextTitle(artifact: TurnArtifact): string {
  if (isToolResultStoragePath(artifact.path) || isToolResultStoragePath(artifact.name)) return "Saved tool output";
  return artifact.name || artifactName(artifact.path);
}

function artifactName(path: string): string {
  return path.replace(/\\/g, "/").split("/").filter(Boolean).at(-1) ?? path;
}

function compactSource(source: string | undefined): string | undefined {
  const compacted = source?.replace(/\s+/g, " ").trim();
  if (!compacted) return undefined;
  if (compacted.length <= 72) return compacted;
  return `${compacted.slice(0, 69).trimEnd()}...`;
}

function isLowSignalStatusDetail(value: string | undefined): boolean {
  const normalized = value?.trim().toLowerCase() ?? "";
  return normalized.startsWith("continue from the current plan state") ||
    normalized.startsWith("execute the next concrete step") ||
    normalized.startsWith("the tool-step budget for this turn is exhausted") ||
    normalized.startsWith("tool-step budget for this turn is exhausted");
}

function usageTrend(session: SessionState, summary?: SessionSummary): WorkbenchContextUsageTrendPoint[] {
  const turnPoints = session.turns
    .map<WorkbenchContextUsageTrendPoint | undefined>((turn, index) => {
      const total = tokenTotal(turn.usage?.inputTokens ?? 0, turn.usage?.outputTokens ?? 0);
      if (total <= 0) return undefined;
      return {
        label: `Turn ${index + 1}`,
        value: total,
        valueLabel: formatTokenCountMillions(total),
        detail: turn.id,
      };
    })
    .filter((point): point is WorkbenchContextUsageTrendPoint => Boolean(point));
  if (turnPoints.length > 0) return turnPoints.slice(-12);

  const summaryTotal = tokenTotal(summary?.usage?.input_tokens ?? 0, summary?.usage?.output_tokens ?? 0);
  if (summaryTotal > 0) {
    const turns = summary?.usage?.turns ?? 0;
    return [{
      label: turns > 0 ? `${turns} ${turns === 1 ? "turn" : "turns"}` : "Session",
      value: summaryTotal,
      valueLabel: formatTokenCountMillions(summaryTotal),
      detail: "from session index",
    }];
  }

  const checkpoint = latestCheckpointUsage(summary);
  if (checkpoint) {
    const total = tokenTotal(checkpoint.inputTokens, checkpoint.outputTokens);
    return [{ label: "Latest turn", value: total, valueLabel: formatTokenCountMillions(total), detail: "from loop checkpoint" }];
  }

  return [];
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
  return `${formatTokenCountMillions(total)} (${formatTokenMillions(inputTokens)} in / ${formatTokenMillions(outputTokens)} out)`;
}

function formatExecutionTokenUsage(usage: ExecutionTokenUsage): string {
  const input = usage.inputTokens ?? 0;
  const output = usage.outputTokens ?? 0;
  const split = usage.inputTokens != null || usage.outputTokens != null
    ? ` (${formatTokenMillions(input)} in / ${formatTokenMillions(output)} out)`
    : "";
  return `${formatTokenCountMillions(usage.totalTokens)}${split}`;
}

function tokenTotal(inputTokens: number, outputTokens: number): number {
  return inputTokens + outputTokens;
}

function formatTokenMillions(value: number): string {
  const millions = value / 1_000_000;
  if (value < 10_000) return `${millions.toFixed(4)}M`;
  if (value < 100_000) return `${millions.toFixed(3)}M`;
  return `${millions.toFixed(2)}M`;
}

function formatTokenCountMillions(value: number): string {
  return `${formatTokenMillions(value)} tokens`;
}

function compactTokenValue(value: string): string {
  return value.replace(/\s*\(.+\)\s*$/, "");
}

function compact<T>(items: readonly (T | undefined | null | false)[]): T[] {
  return items.filter(Boolean) as T[];
}
