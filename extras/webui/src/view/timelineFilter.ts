import type { NormalizedEvent } from "../normalize/normalizeEvent";
import type { ToolCallState, TurnState } from "../store/sessionState";
import { buildExecutionTree, searchableExecutionNodeText } from "./executionTree";
import { describeMemoryUpdate } from "./memoryUpdate";
import { describeSourceAccess } from "./sourceAccess";

export type TimelineFilterMode =
  | "all"
  | "errors"
  | "tools"
  | "messages"
  | "evidence"
  | "recall"
  | "guard"
  | "context"
  | "artifacts"
  | "memory"
  | "truncated"
  | "repaired";

export interface TimelineFilter {
  mode: TimelineFilterMode;
  query: string;
}

export function turnMatchesFilter(
  turn: TurnState,
  events: readonly NormalizedEvent[],
  filter: TimelineFilter,
): boolean {
  if (!matchesMode(turn, filter.mode)) return false;
  const query = normalizeQuery(filter.query);
  if (!query) return true;
  return searchableTurnText(turn, events).includes(query);
}

export function countMatchingTurns(
  turns: readonly TurnState[],
  events: readonly NormalizedEvent[],
  filter: TimelineFilter,
): number {
  return turns.reduce((count, turn) => count + (turnMatchesFilter(turn, events, filter) ? 1 : 0), 0);
}

export function countTurnsByMode(
  turns: readonly TurnState[],
  events: readonly NormalizedEvent[],
  modes: readonly TimelineFilterMode[],
  query: string,
): Record<TimelineFilterMode, number> {
  const counts = Object.fromEntries(modes.map((mode) => [mode, 0])) as Record<TimelineFilterMode, number>;
  for (const mode of modes) {
    counts[mode] = countMatchingTurns(turns, events, { mode, query });
  }
  return counts;
}

function matchesMode(turn: TurnState, mode: TimelineFilterMode): boolean {
  switch (mode) {
    case "all":
      return true;
    case "errors":
      return turn.status === "error" || turn.status === "max_turns" || !!turn.error || turn.toolCalls.some((tool) => tool.status === "error");
    case "tools":
      return turn.toolCalls.length > 0;
    case "messages":
      return !!(turn.userText || turn.assistantText || turn.thinkingText);
    case "evidence":
      return turn.toolCalls.some((tool) => !!describeSourceAccess(tool.result ?? tool.resultSummary));
    case "recall":
      return (turn.toolStats?.session_search_calls ?? 0) > 0 ||
        (turn.toolStats?.session_search_results ?? 0) > 0 ||
        turn.toolCalls.some((tool) => tool.tool === "session_search");
    case "guard":
      return (turn.toolStats?.loop_guard_interventions ?? 0) > 0 ||
        (turn.toolStats?.forced_no_tools ?? 0) > 0 ||
        (turn.loopDecisions ?? []).some((decision) => decision.visible_in_ui !== false);
    case "context":
      return (turn.contextCompactions?.length ?? 0) > 0;
    case "artifacts":
      return turn.toolCalls.some((tool) => !!tool.resultArtifactPath);
    case "memory":
      return turn.toolCalls.some((tool) => !!describeMemoryUpdate(tool));
    case "truncated":
      return turn.toolCalls.some((tool) => tool.argsTruncated || tool.resultTruncated);
    case "repaired":
      return turn.toolCalls.some((tool) => tool.argsRepaired || tool.canonicalized || !!tool.repairNotes?.length || !!tool.originalTool);
  }
}

function searchableTurnText(turn: TurnState, events: readonly NormalizedEvent[]): string {
  const chunks = [
    turn.id,
    turn.status,
    turn.endReason,
    turn.userText,
    turn.thinkingText,
    turn.assistantText,
    turn.finishReason,
    turn.error?.code,
    turn.error?.message,
    ...(turn.loopDecisions ?? []).flatMap(searchableLoopDecisionText),
    ...(turn.contextCompactions ?? []).flatMap(searchableContextCompactionText),
    ...searchableSessionRecallText(turn),
    ...buildExecutionTree(turn).flatMap(searchableExecutionNodeText),
    ...turn.toolCalls.flatMap(searchableToolText),
    ...events.filter((event) => eventBelongsToTurn(event, turn)).map((event) => JSON.stringify(event.raw)),
  ];
  return normalizeQuery(chunks.filter(Boolean).join("\n"));
}

function searchableSessionRecallText(turn: TurnState): string[] {
  const stats = turn.toolStats;
  if (!stats || ((stats.session_search_calls ?? 0) <= 0 && (stats.session_search_results ?? 0) <= 0)) return [];
  return [
    "recall",
    "session history",
    "history search",
    `${stats.session_search_results ?? 0} hits`,
    `${stats.session_search_context_hits ?? 0} context`,
    `${stats.session_search_matched_terms ?? 0} terms`,
  ];
}

function searchableContextCompactionText(compaction: NonNullable<TurnState["contextCompactions"]>[number]): string[] {
  return [
    "context",
    "compaction",
    compaction.reactive ? "reactive" : "scheduled",
    compaction.reason,
    String(compaction.before_messages),
    String(compaction.after_messages),
    String(compaction.removed_messages),
  ].filter((item): item is string => !!item);
}

function searchableLoopDecisionText(decision: NonNullable<TurnState["loopDecisions"]>[number]): string[] {
  return [
    decision.kind,
    decision.trigger,
    decision.decision,
    decision.confidence,
    decision.reason,
    decision.required_action,
  ].filter((item): item is string => !!item);
}

function searchableToolText(tool: ToolCallState): string[] {
  return [
    tool.callId,
    tool.tool,
    tool.originalTool,
    tool.originalArgsSummary,
    tool.failureKind,
    JSON.stringify(tool.args),
    tool.resultSummary,
    tool.result,
    describeSourceAccess(tool.result ?? tool.resultSummary)?.status,
    tool.resultArtifactPath,
    ...(tool.failureKinds ?? []),
    ...(tool.repairNotes ?? []),
  ].filter((item): item is string => !!item);
}

function eventBelongsToTurn(event: NormalizedEvent, turn: TurnState): boolean {
  if (event.turnId === turn.id) return true;
  return turn.toolCalls.some((call) => eventReferencesTool(event, call.callId));
}

function eventReferencesTool(event: NormalizedEvent, callId: ToolCallState["callId"]): boolean {
  return !!event.data && typeof event.data === "object" && (event.data as { call_id?: unknown }).call_id === callId;
}

function normalizeQuery(query: string): string {
  return query.trim().toLowerCase();
}
