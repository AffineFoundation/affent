import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { normalizeEvent, normalizeEvents } from "./normalizeEvent";

describe("normalizeEvent", () => {
  it("marks Affent event types as known", () => {
    const norm = normalizeEvents(completedTurn);
    expect(norm.every((e) => e.known)).toBe(true);
  });

  it("marks loop decision events as known", () => {
    const decision = normalizeEvent({
      id: 100,
      type: "loop.decision",
      data: { turn_id: "t1", kind: "loop_stop", decision: "continue" },
    });

    expect(decision.known).toBe(true);
    expect(decision.turnId).toBe("t1");
  });

  it("marks context compaction events as known", () => {
    const compacted = normalizeEvent({
      id: 101,
      type: "context.compacted",
      data: { turn_id: "t1", before_messages: 40, after_messages: 12, removed_messages: 28, reactive: false, reason: "threshold" },
    });

    expect(compacted.known).toBe(true);
    expect(compacted.turnId).toBe("t1");
  });

  it("extracts turn_id where the payload carries one", () => {
    const turnStart = normalizeEvent(completedTurn[1]);
    expect(turnStart.turnId).toBe("t1");
  });

  it("leaves turnId undefined for payloads without turn_id (tool.result, trace.meta)", () => {
    const meta = normalizeEvent(completedTurn[0]);
    const toolResult = normalizeEvent(completedTurn[6]);
    expect(meta.turnId).toBeUndefined();
    expect(toolResult.turnId).toBeUndefined();
    expect(toolResult.type).toBe("tool.result");
  });

  it("preserves unknown event types instead of dropping them", () => {
    const future = normalizeEvent({ id: 99, type: "turn.checkpoint", data: { turn_id: "t1" } });
    expect(future.known).toBe(false);
    expect(future.type).toBe("turn.checkpoint");
    // A newer server's event must still be archived with its turn.
    expect(future.turnId).toBe("t1");
    expect(future.raw.id).toBe(99);
  });

  it("keeps the raw event for inline trace drill-down", () => {
    const ev = normalizeEvent(completedTurn[9]);
    expect(ev.raw).toBe(completedTurn[9]);
    expect(ev.data).toEqual({ turn_id: "t1", text: "There are two files.", finish_reason: "stop" });
  });
});
