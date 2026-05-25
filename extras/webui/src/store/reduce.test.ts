import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import {
  argsRepaired,
  cancelledTurn,
  maxTurns,
  resultTruncated,
  toolError,
  turnError,
} from "../fixtures/scenarios";
import { normalizeEvents } from "../normalize/normalizeEvent";
import { applyEvent, reduceRawEvents } from "./reduce";
import { initialSessionState } from "./sessionState";

describe("reduce — completed turn", () => {
  const s = reduceRawEvents(completedTurn);

  it("records the schema version and one completed turn", () => {
    expect(s.schemaVersion).toBe(1);
    expect(s.turns).toHaveLength(1);
    expect(s.status).toBe("completed");
    expect(s.turns[0].status).toBe("completed");
    expect(s.turns[0].endReason).toBe("completed");
  });

  it("assembles user, thinking, assistant text and finish reason", () => {
    const t = s.turns[0];
    expect(t.userText).toBe("list the files");
    expect(t.thinkingText).toBe("I should list files.");
    // message.done replaces accumulated deltas with the final text.
    expect(t.assistantText).toBe("There are two files.");
    expect(t.finishReason).toBe("stop");
    expect(t.thinkingStreaming).toBe(false);
    expect(t.messageStreaming).toBe(false);
  });

  it("links tool.result to its tool.request by call_id", () => {
    const c = s.turns[0].toolCalls[0];
    expect(c.callId).toBe("c1");
    expect(c.tool).toBe("list_files");
    expect(c.status).toBe("success");
    expect(c.exitCode).toBe(0);
    expect(c.durationMs).toBe(12);
  });

  it("totals usage across the turn", () => {
    expect(s.totalUsage).toEqual({ inputTokens: 120, outputTokens: 18 });
    expect(s.turns[0].usage).toEqual({ inputTokens: 120, outputTokens: 18 });
  });
});

describe("reduce — message deltas accumulate before done", () => {
  it("shows streaming text mid-turn, then the final text", () => {
    const evs = normalizeEvents(completedTurn);
    // Apply up to the second message.delta (id 8), before message.done.
    let state = initialSessionState();
    for (const ev of evs) {
      state = applyEvent(state, ev);
      if (ev.id === 8) break;
    }
    expect(state.turns[0].assistantText).toBe("There are two files.");
    expect(state.turns[0].messageStreaming).toBe(true);
    expect(state.status).toBe("running");
  });
});

describe("reduce — tool error", () => {
  it("marks the tool call as error and preserves the Next: hint", () => {
    const s = reduceRawEvents(toolError);
    const c = s.turns[0].toolCalls[0];
    expect(c.status).toBe("error");
    expect(c.exitCode).toBe(2);
    expect(c.result).toContain("Next: check the Makefile path");
    expect(s.turns[0].toolStats?.tool_errors).toBe(1);
    // A failing tool does not by itself fail the turn.
    expect(s.turns[0].status).toBe("completed");
  });
});

describe("reduce — repaired args", () => {
  it("surfaces canonicalization and repair notes", () => {
    const s = reduceRawEvents(argsRepaired);
    const c = s.turns[0].toolCalls[0];
    expect(c.canonicalized).toBe(true);
    expect(c.argsRepaired).toBe(true);
    expect(c.originalTool).toBe("readFile");
    expect(c.repairNotes).toEqual(["renamed readFile -> read_file", "coerced filename -> path"]);
  });

  it("does not treat unchanged original tool metadata as a repair", () => {
    const s = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          original_tool: "web_fetch",
          original_args_summary: "{\"url\":\"https://example.com\"}",
          args: { url: "https://example.com" },
        },
      },
    ]);
    const c = s.turns[0].toolCalls[0];

    expect(c.originalTool).toBeUndefined();
    expect(c.originalArgsSummary).toBeUndefined();
    expect(c.argsRepaired).toBe(false);
    expect(c.canonicalized).toBe(false);
  });
});

describe("reduce — truncated result", () => {
  it("flags truncation and exposes the artifact path", () => {
    const s = reduceRawEvents(resultTruncated);
    const c = s.turns[0].toolCalls[0];
    expect(c.resultTruncated).toBe(true);
    expect(c.resultArtifactPath).toBe(".affent/artifacts/tool-results/000001-c1.txt");
  });
});

describe("reduce — terminal statuses", () => {
  it("maps cancelled / max_turns / error reasons to turn + session status", () => {
    expect(reduceRawEvents(cancelledTurn).status).toBe("cancelled");
    expect(reduceRawEvents(maxTurns).status).toBe("max_turns");

    const errState = reduceRawEvents(turnError);
    expect(errState.status).toBe("error");
    expect(errState.turns[0].error).toEqual({
      code: "upstream_5xx",
      message: "provider returned 503",
      recoverable: true,
    });
  });

  it("treats non-recoverable error events as terminal even before turn.end arrives", () => {
    const errState = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "user.message", data: { turn_id: "t1", text: "hi" } },
      { id: 2, type: "message.delta", data: { turn_id: "t1", delta: "partial" } },
      { id: 3, type: "error", data: { turn_id: "t1", code: "llm_request", message: "provider unavailable", recoverable: false } },
    ]);

    expect(errState.status).toBe("error");
    expect(errState.turns[0]).toMatchObject({
      status: "error",
      messageStreaming: false,
      error: {
        code: "llm_request",
        message: "provider unavailable",
        recoverable: false,
      },
    });
  });
});

describe("reduce — robustness", () => {
  it("counts unknown event types without crashing or dropping turns", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "turn.checkpoint", data: { turn_id: "t1", note: "future event" } },
      { id: 2, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    expect(s.unknownEventCount).toBe(1);
    expect(s.turns).toHaveLength(1);
    expect(s.status).toBe("completed");
  });

  it("ignores a tool.result for an unknown call_id instead of throwing", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "tool.result", data: { call_id: "ghost", exit_code: 0, result_summary: "", result: "", result_truncated: false, result_bytes: 0, result_omitted_bytes: 0, result_cap_bytes: 8192 } },
      { id: 2, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    expect(s.turns[0].toolCalls).toHaveLength(0);
  });

  it("is idempotent on a repeated turn.start (replay overlapping live)", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
    ]);
    expect(s.turns).toHaveLength(1);
  });
});
