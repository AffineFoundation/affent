import { describe, expect, it } from "vitest";
import type { ToolCallState } from "../store/sessionState";
import { describeMemoryUpdate } from "./memoryUpdate";

describe("describeMemoryUpdate", () => {
  it("summarizes memory additions from tool args", () => {
    expect(describeMemoryUpdate(memoryCall({
      action: "add",
      target: "memory",
      topic: "markets",
      content: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
    }))).toEqual({
      action: "add",
      label: "Saved memory",
      target: "memory",
      topic: "markets",
      location: "memory:markets",
      preview: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
    });
  });

  it("summarizes replacements and removals without treating searches as updates", () => {
    expect(describeMemoryUpdate(memoryCall({
      action: "replace",
      topic: "deploy",
      content: "Use canary deploys for dashboard refresh changes.",
    }))?.label).toBe("Updated memory");

    expect(describeMemoryUpdate(memoryCall({
      action: "remove",
      topic: "deploy",
      old_text: "stale deploy instruction",
    }))?.preview).toBe("stale deploy instruction");

    expect(describeMemoryUpdate(memoryCall({ action: "search", query: "deploy" }))).toBeUndefined();
  });

  it("defaults omitted target and topic to the memory general bucket", () => {
    const summary = describeMemoryUpdate(memoryCall({ action: "add", content: "Remember the local test command." }));

    expect(summary?.location).toBe("memory:general");
  });
});

function memoryCall(args: Record<string, unknown>): ToolCallState {
  return {
    callId: "c1",
    tool: "memory",
    args,
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    exitCode: 0,
    resultSummary: "{}",
    result: "{}",
    resultTruncated: false,
  };
}
