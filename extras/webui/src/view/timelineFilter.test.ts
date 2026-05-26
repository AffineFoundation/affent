import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { argsRepaired, completedSubagentTree, resultTruncated, toolError } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { countMatchingTurns, countTurnsByMode, turnMatchesFilter, type TimelineFilterMode } from "./timelineFilter";

describe("timelineFilter", () => {
  it.each([
    ["artifacts", resultTruncated],
    ["truncated", resultTruncated],
    ["repaired", argsRepaired],
    ["errors", toolError],
  ] as const)("matches %s using structured runtime fields", (mode, raws) => {
    const session = reduceRawEvents(raws);

    expect(turnMatchesFilter(session.turns[0], session.events, { mode, query: "" })).toBe(true);
    expect(countMatchingTurns(session.turns, session.events, { mode, query: "" })).toBe(1);
  });

  it("does not match specialized runtime filters when a plain turn lacks those states", () => {
    const session = reduceRawEvents(completedTurn);
    const modes: TimelineFilterMode[] = ["artifacts", "memory", "truncated", "repaired", "errors"];

    for (const mode of modes) {
      expect(turnMatchesFilter(session.turns[0], session.events, { mode, query: "" })).toBe(false);
    }
  });

  it("matches only confirmed memory update turns", () => {
    const saved = reduceRawEvents(memoryUpdateTurn({ ok: true }));
    const rejected = reduceRawEvents(memoryUpdateTurn({ ok: false, message: "blocked" }));

    expect(turnMatchesFilter(saved.turns[0], saved.events, { mode: "memory", query: "" })).toBe(true);
    expect(turnMatchesFilter(saved.turns[0], saved.events, { mode: "memory", query: "MEM-STOCK-73" })).toBe(true);
    expect(turnMatchesFilter(rejected.turns[0], rejected.events, { mode: "memory", query: "" })).toBe(false);
  });

  it("counts every filter mode against the current search query", () => {
    const session = reduceRawEvents([...completedTurn, ...namespaceEvents(resultTruncated, "artifact", 100)]);

    expect(countTurnsByMode(session.turns, session.events, ["all", "tools", "artifacts", "truncated"], "")).toEqual({
      all: 2,
      tools: 2,
      artifacts: 1,
      truncated: 1,
    });
    expect(countTurnsByMode(session.turns, session.events, ["all", "tools", "artifacts", "truncated"], "big.log")).toEqual({
      all: 1,
      tools: 1,
      artifacts: 1,
      truncated: 1,
    });
  });

  it("matches the user-facing execution tree labels, not only raw tool names", () => {
    const session = reduceRawEvents(completedSubagentTree);

    expect(turnMatchesFilter(session.turns[0], session.events, { mode: "all", query: "External MCP service" })).toBe(true);
    expect(turnMatchesFilter(session.turns[0], session.events, { mode: "all", query: "Delegated worker" })).toBe(true);
  });
});

function namespaceEvents(raws: typeof resultTruncated, suffix: string, idOffset: number): typeof resultTruncated {
  return raws.map((event) => ({
    ...event,
    id: event.id + idOffset,
    data: namespacePayload(event.data, suffix),
  }));
}

function memoryUpdateTurn(response: Record<string, unknown>): typeof resultTruncated {
  return [
    { id: 0, type: "turn.start", data: { turn_id: "memory_turn" } },
    {
      id: 1,
      type: "tool.request",
      data: {
        turn_id: "memory_turn",
        call_id: "memory_call",
        tool: "memory",
        args: {
          action: "add",
          target: "memory",
          topic: "markets",
          content: "Alpha Coast reports use marker MEM-STOCK-73.",
        },
      },
    },
    {
      id: 2,
      type: "tool.result",
      data: {
        call_id: "memory_call",
        exit_code: 0,
        result_summary: JSON.stringify({ target: "memory", topic: "markets", ...response }),
        result: JSON.stringify({ target: "memory", topic: "markets", ...response }),
        result_truncated: false,
      },
    },
    { id: 3, type: "turn.end", data: { turn_id: "memory_turn", reason: "completed" } },
  ];
}

function namespacePayload(data: unknown, suffix: string): unknown {
  if (!data || typeof data !== "object" || Array.isArray(data)) return data;
  const copy: Record<string, unknown> = { ...(data as Record<string, unknown>) };
  if (typeof copy.turn_id === "string") copy.turn_id = `${copy.turn_id}_${suffix}`;
  if (typeof copy.call_id === "string") copy.call_id = `${copy.call_id}_${suffix}`;
  return copy;
}
