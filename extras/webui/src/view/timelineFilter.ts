import type { NormalizedEvent } from "../normalize/normalizeEvent";
import type { ToolCallState, TurnState } from "../store/sessionState";

export type TimelineFilterMode =
  | "all"
  | "errors"
  | "tools"
  | "messages"
  | "artifacts"
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
    case "artifacts":
      return turn.toolCalls.some((tool) => !!tool.resultArtifactPath);
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
    ...turn.toolCalls.flatMap(searchableToolText),
    ...events.filter((event) => eventBelongsToTurn(event, turn)).map((event) => JSON.stringify(event.raw)),
  ];
  return normalizeQuery(chunks.filter(Boolean).join("\n"));
}

function searchableToolText(tool: ToolCallState): string[] {
  return [
    tool.callId,
    tool.tool,
    tool.originalTool,
    tool.originalArgsSummary,
    JSON.stringify(tool.args),
    tool.resultSummary,
    tool.result,
    tool.resultArtifactPath,
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
