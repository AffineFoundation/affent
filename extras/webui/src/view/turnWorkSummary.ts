import type { ToolCallState, TurnState } from "../store/sessionState";
import { detectConstraintDeviations } from "./constraintDeviation";
import { artifactCountLabel, buildTurnArtifacts } from "./turnArtifacts";

export type WorkSummaryTone = "muted" | "running" | "error" | "warning" | "info" | "artifact";

export interface WorkSummaryItem {
  label: string;
  tone: WorkSummaryTone;
}

export interface TurnWorkSummary {
  actionLabel: string;
  items: WorkSummaryItem[];
  headlineItems: WorkSummaryItem[];
}

export function buildTurnWorkSummary(
  turn: TurnState,
  opts: { continuedAfterLimit?: boolean } = {},
): TurnWorkSummary {
  return buildTurnWorkSummaryWithOptions(turn, opts);
}

export function buildTurnWorkSummaryWithOptions(
  turn: TurnState,
  opts: { continuedAfterLimit?: boolean } = {},
): TurnWorkSummary {
  const calls = turn.toolCalls;
  const failed = calls.filter((call) => call.status === "error").length;
  const running = calls.filter((call) => call.status === "running").length;
  const repaired = calls.filter(hasRepair).length;
  const truncated = calls.filter((call) => call.argsTruncated || call.resultTruncated).length;
  const artifacts = buildTurnArtifacts(turn);
  const durationMs = turn.toolStats?.tool_duration_ms ?? sumDurations(calls);
  const verifiedSources = turn.toolStats?.source_access_verified ?? 0;
  const networkSources = turn.toolStats?.source_access_network ?? 0;
  const dynamicPartialSources = turn.toolStats?.source_access_dynamic_partial ?? 0;
  const recallCalls = turn.toolStats?.session_search_calls ?? 0;
  const recallHits = turn.toolStats?.session_search_results ?? 0;
  const actionLabel = actionSummary(calls);
  const items: WorkSummaryItem[] = [];
  const finalAnswerReady = turn.status === "completed" && Boolean(turn.assistantText.trim());
  const constraintDeviations = detectConstraintDeviations(turn);

  if (constraintDeviations.length > 0) items.push({ label: constraintDeviations.length === 1 ? "constraint" : `${constraintDeviations.length} constraints`, tone: "warning" });
  if (failed && !opts.continuedAfterLimit) items.push({ label: finalAnswerReady ? toolIssueLabel(failed) : `${failed} failed`, tone: finalAnswerReady ? "warning" : "error" });
  if (running) items.push({ label: `${running} running`, tone: "running" });
  if (repaired) items.push({ label: `${repaired} repaired`, tone: "warning" });
  if (verifiedSources > 0) items.push({ label: `${verifiedSources} verified ${pluralize("source", verifiedSources)}`, tone: "info" });
  if (networkSources > 0) items.push({ label: `${networkSources} network ${pluralize("source", networkSources)}`, tone: "info" });
  if (dynamicPartialSources > 0) items.push({ label: `${dynamicPartialSources} partial ${pluralize("source", dynamicPartialSources)}`, tone: "warning" });
  if (recallCalls > 0 || recallHits > 0) items.push({ label: `${recallHits} recall ${pluralize("hit", recallHits)}`, tone: recallHits > 0 ? "info" : "warning" });
  if (truncated) items.push({ label: `${truncated} truncated`, tone: "info" });
  if (artifacts.length) items.push({ label: artifactCountLabel(artifacts) ?? `${artifacts.length} file${artifacts.length === 1 ? "" : "s"}`, tone: "artifact" });
  if (durationMs != null && durationMs > 0) items.push({ label: formatDuration(durationMs), tone: "muted" });

  return {
    actionLabel,
    items,
    headlineItems: selectHeadlineWorkSummaryItems(items),
  };
}

export function selectHeadlineWorkSummaryItems(items: readonly WorkSummaryItem[], visibleCount = 3): WorkSummaryItem[] {
  const visible = items.filter((item) => item.tone !== "muted").slice(0, visibleCount);
  const artifact = items.find((item) => item.tone === "artifact");
  if (!artifact || visible.some((item) => item === artifact)) return visible;
  if (visible.length < visibleCount) return [...visible, artifact];
  const replacementIndex = [...visible].reverse().findIndex((item) => item.tone !== "artifact");
  if (replacementIndex < 0) return visible;
  const actualIndex = visible.length - 1 - replacementIndex;
  const next = [...visible];
  next[actualIndex] = artifact;
  return next;
}

function hasRepair(call: ToolCallState): boolean {
  return !!(call.argsRepaired || call.canonicalized || call.originalTool || call.repairNotes?.length);
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

function toolIssueLabel(count: number): string {
  return `${count} tool issue${count === 1 ? "" : "s"}`;
}

function pluralize(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function actionSummary(calls: readonly ToolCallState[]): string {
  if (calls.length !== 1) return `${calls.length} action${calls.length === 1 ? "" : "s"}`;
  return summarize(singleActionLabel(calls[0]) ?? "1 action", 72);
}

function singleActionLabel(call: ToolCallState): string | undefined {
  const task = stringArg(call, "task") ?? stringArg(call, "objective");
  if (task) return task;
  const command = stringArg(call, "command");
  if (command) return command;
  const path = stringArg(call, "path") ?? stringArg(call, "file") ?? stringArg(call, "filename");
  if (path) return actionWithTarget(call.tool, path);
  return toolLabel(call.tool);
}

function actionWithTarget(tool: string, target: string): string {
  const label = toolLabel(tool);
  return label ? `${label}: ${target}` : target;
}

function toolLabel(tool: string): string | undefined {
  switch (tool) {
    case "list_files":
      return "List files";
    case "read_file":
      return "Read file";
    case "write_file":
      return "Write file";
    case "edit_file":
      return "Edit file";
    case "subagent_run":
      return "Run subagent";
    case "run_task":
      return "Run focused task";
    default:
      return tool || undefined;
  }
}

function stringArg(call: ToolCallState, key: string): string | undefined {
  const value = call.args[key];
  return typeof value === "string" && value.trim() !== "" ? value.trim() : undefined;
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}

function formatDuration(ms: number): string {
  if (ms < 1000) return `${ms}ms`;
  const s = ms / 1000;
  return `${s.toFixed(s < 10 ? 2 : 1)}s`;
}
