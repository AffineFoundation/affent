import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { argsRepaired, resultTruncated, toolError } from "../fixtures/scenarios";
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
    const modes: TimelineFilterMode[] = ["artifacts", "truncated", "repaired", "errors"];

    for (const mode of modes) {
      expect(turnMatchesFilter(session.turns[0], session.events, { mode, query: "" })).toBe(false);
    }
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
});

function namespaceEvents(raws: typeof resultTruncated, suffix: string, idOffset: number): typeof resultTruncated {
  return raws.map((event) => ({
    ...event,
    id: event.id + idOffset,
    data: namespacePayload(event.data, suffix),
  }));
}

function namespacePayload(data: unknown, suffix: string): unknown {
  if (!data || typeof data !== "object" || Array.isArray(data)) return data;
  const copy: Record<string, unknown> = { ...(data as Record<string, unknown>) };
  if (typeof copy.turn_id === "string") copy.turn_id = `${copy.turn_id}_${suffix}`;
  if (typeof copy.call_id === "string") copy.call_id = `${copy.call_id}_${suffix}`;
  return copy;
}
