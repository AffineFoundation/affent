import { describe, expect, it } from "vitest";
import type { ToolCallState, TurnState } from "../store/sessionState";
import { describeMemoryUpdate, memoryUpdatesForTurn } from "./memoryUpdate";

describe("describeMemoryUpdate", () => {
  it("summarizes memory additions from tool args", () => {
    expect(describeMemoryUpdate(memoryCall({
      action: "add",
      target: "memory",
      topic: "markets",
      content: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
    }, { ok: true, target: "memory", topic: "markets" }))).toEqual({
      action: "add",
      label: "Saved memory",
      target: "memory",
      topic: "markets",
      location: "memory:markets",
      preview: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
      nextPreview: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
    });
  });

  it("summarizes replacements and removals without treating searches as updates", () => {
    expect(describeMemoryUpdate(memoryCall({
      action: "replace",
      topic: "deploy",
      old_text: "Use direct deploys for dashboard refresh changes.",
      content: "Use canary deploys for dashboard refresh changes.",
    }, { ok: true, target: "memory", topic: "deploy" }))).toMatchObject({
      label: "Updated memory",
      previousPreview: "Use direct deploys for dashboard refresh changes.",
      nextPreview: "Use canary deploys for dashboard refresh changes.",
      preview: "Use direct deploys for dashboard refresh changes. -> Use canary deploys for dashboard refresh changes.",
    });

    expect(describeMemoryUpdate(memoryCall({
      action: "remove",
      topic: "deploy",
      old_text: "stale deploy instruction",
    }, { ok: true, target: "memory", topic: "deploy" }))).toMatchObject({
      previousPreview: "stale deploy instruction",
      preview: "stale deploy instruction",
    });

    expect(describeMemoryUpdate(memoryCall({ action: "search", query: "deploy" }))).toBeUndefined();
  });

  it("defaults omitted target and topic to the memory general bucket", () => {
    const summary = describeMemoryUpdate(memoryCall({ action: "add", content: "Remember the local test command." }, { ok: true, target: "memory", topic: "general" }));

    expect(summary?.location).toBe("memory:general");
  });

  it("does not surface failed or unconfirmed memory writes as saved updates", () => {
    expect(describeMemoryUpdate(memoryCall(
      { action: "add", content: "blocked content" },
      { ok: false, target: "memory", topic: "general", message: "blocked" },
    ))).toBeUndefined();

    expect(describeMemoryUpdate(memoryCall({ action: "add", content: "missing result" }, null))).toBeUndefined();
  });

  it("collects every confirmed memory update in a turn", () => {
    expect(memoryUpdatesForTurn(turn([
      memoryCall({ action: "add", topic: "markets", content: "Remember market source policy." }, { ok: true, target: "memory", topic: "markets" }),
      memoryCall({ action: "search", query: "markets" }),
      memoryCall({ action: "remove", topic: "old", old_text: "stale note" }, { ok: true, target: "memory", topic: "old" }),
    ])).map((summary) => summary.preview)).toEqual([
      "Remember market source policy.",
      "stale note",
    ]);
  });
});

function turn(toolCalls: ToolCallState[]): TurnState {
  return {
    id: "t1",
    status: "completed",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls,
  };
}

function memoryCall(args: Record<string, unknown>, response: Record<string, unknown> | null = { ok: true }): ToolCallState {
  return {
    callId: "c1",
    tool: "memory",
    args,
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    exitCode: 0,
    resultSummary: response ? JSON.stringify(response) : undefined,
    result: response ? JSON.stringify(response) : undefined,
    resultTruncated: false,
  };
}
