import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { argsRepaired, resultTruncated, toolError } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { buildTurnWorkSummary } from "./turnWorkSummary";

describe("buildTurnWorkSummary", () => {
  it("keeps successful work quiet while preserving duration", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(resultTruncated).turns[0]);

    expect(summary.actionLabel).toBe("cat big.log");
    expect(summary.items).toEqual([
      { label: "1 truncated", tone: "info" },
      { label: "1 file", tone: "artifact" },
      { label: "88ms", tone: "muted" },
    ]);
  });

  it("uses the concrete action name for a single tool call", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(completedTurn).turns[0]);

    expect(summary.actionLabel).toBe("List files: .");
  });

  it("shows completed tool failures as handled attempts when a final answer exists", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(toolError).turns[0]);

    expect(summary.items).toContainEqual({ label: "1 handled", tone: "warning" });
    expect(summary.headlineItems).toEqual([{ label: "1 handled", tone: "warning" }]);
  });

  it("hides failed attempt chips after a later message continues the work", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(toolError.slice(0, 5)).turns[0], { continuedAfterLimit: true });

    expect(summary.items).not.toContainEqual({ label: "1 failed", tone: "error" });
    expect(summary.headlineItems).toEqual([]);
  });

  it("keeps failed work urgent when no final answer exists", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(toolError.slice(0, 5)).turns[0]);

    expect(summary.items).toContainEqual({ label: "1 failed", tone: "error" });
    expect(summary.headlineItems).toEqual([{ label: "1 failed", tone: "error" }]);
  });

  it("marks repaired calls without exposing raw repair fields in the component", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(argsRepaired).turns[0]);

    expect(summary.items).toContainEqual({ label: "1 repaired", tone: "warning" });
  });

  it("does not count unchanged original tool metadata as repaired work", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents([
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
    ]).turns[0]);

    expect(summary.items).not.toContainEqual({ label: "1 repaired", tone: "warning" });
  });

  it("marks tool use that contradicts an explicit no-tool instruction", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "不要再调用任何工具。直接输出最终报告。" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "shell",
          args: { command: "curl https://example.com" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
    ]).turns[0]);

    expect(summary.items).toContainEqual({ label: "constraint", tone: "warning" });
    expect(summary.headlineItems[0]).toEqual({ label: "constraint", tone: "warning" });
  });
});
