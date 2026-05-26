import type { ToolCallState, TurnState } from "../store/sessionState";

export type TurnBoundaryTone = "running" | "success" | "warning" | "error" | "muted";

export interface TurnBoundaryView {
  title: string;
  statusLabel: string;
  tone: TurnBoundaryTone;
  meta: string[];
  ariaLabel: string;
}

export function buildTurnBoundaryView({
  turn,
  turnNumber,
  artifactCount = 0,
  artifactLabel,
  continuedAfterLimit = false,
}: {
  turn: TurnState;
  turnNumber: number;
  artifactCount?: number;
  artifactLabel?: string;
  continuedAfterLimit?: boolean;
}): TurnBoundaryView {
  const title = turnTitle(turn);
  const statusLabel = boundaryStatus(turn, { continuedAfterLimit });
  const meta = buildBoundaryMeta(turn, artifactCount, artifactLabel);
  return {
    title,
    statusLabel,
    tone: boundaryTone(turn, { continuedAfterLimit }),
    meta,
    ariaLabel: [`Message ${turnNumber}: ${statusLabel}`, title, ...meta].filter(Boolean).join(". "),
  };
}

function boundaryStatus(turn: TurnState, opts: { continuedAfterLimit?: boolean } = {}): string {
  if (turn.status === "running") return "In progress";
  if (turn.status === "max_turns" && opts.continuedAfterLimit) return "Continued";
  if (turn.status === "max_turns") return "Needs final answer";
  if (turn.status === "error" || turn.error) return "Issue";
  if (turn.status === "cancelled") return "Cancelled";
  if (turn.status === "completed") return "Done";
  return turn.endReason ?? turn.status;
}

function boundaryTone(turn: TurnState, opts: { continuedAfterLimit?: boolean } = {}): TurnBoundaryTone {
  if (turn.status === "running") return "running";
  if (turn.status === "completed") return "success";
  if (turn.status === "max_turns" && opts.continuedAfterLimit) return "muted";
  if (turn.status === "max_turns") return "warning";
  if (turn.status === "error" || turn.error || turn.toolCalls.some((call) => call.status === "error")) return "error";
  return "muted";
}

function buildBoundaryMeta(turn: TurnState, artifactCount: number, artifactLabel?: string): string[] {
  const meta: string[] = [];
  const durationMs = turn.toolStats?.tool_duration_ms ?? sumDurations(turn.toolCalls);
  const tokenCount = turn.usage ? turn.usage.inputTokens + turn.usage.outputTokens : undefined;

  if (turn.toolCalls.length > 0) meta.push(`${turn.toolCalls.length} ${pluralize("action", turn.toolCalls.length)}`);
  if (artifactCount > 0) meta.push(artifactLabel ? artifactLabel : `${artifactCount} ${pluralize("file", artifactCount)}`);
  if (durationMs != null && durationMs > 0) meta.push(formatDuration(durationMs));
  if (tokenCount != null && tokenCount > 0) meta.push(`${tokenCount} tokens`);
  return meta;
}

function sumDurations(calls: readonly ToolCallState[]): number | undefined {
  let total = 0;
  let found = false;
  for (const call of calls) {
    if (call.durationMs == null) continue;
    total += call.durationMs;
    found = true;
  }
  return found ? total : undefined;
}

function turnTitle(turn: TurnState): string {
  if (turn.userText) return summarize(turn.userText, 72);
  if (turn.assistantText) return summarize(turn.assistantText, 72);
  return turn.id;
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}

function pluralize(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  return `${s.toFixed(s < 10 ? 2 : 1)}s`;
}
