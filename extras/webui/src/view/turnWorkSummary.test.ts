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
      { label: "88ms", tone: "muted" },
    ]);
  });

  it("keeps tool-result storage files out of headline summaries", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "shell",
          args: { command: "cat big.log" },
          args_truncated: false,
          args_bytes: 24,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 88,
          result_summary: "line 1\nline 2\n…(truncated)",
          result: "line 1\nline 2\n… [output truncated]",
          result_truncated: true,
          result_bytes: 8192,
          result_omitted_bytes: 1048576,
          result_cap_bytes: 8192,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c2",
          tool: "shell",
          args: { command: "make" },
          args_truncated: false,
          args_bytes: 18,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c2",
          exit_code: 2,
          duration_ms: 40,
          result_summary: "make: *** No rule to make target. Stop.",
          result: "make: *** No rule to make target. Stop.",
          result_truncated: false,
          result_bytes: 39,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 2, tool_errors: 1, tool_duration_ms: 128 } } },
    ]).turns[0]);

    expect(summary.items).toEqual([
      { label: "1 failed", tone: "error" },
      { label: "1 truncated", tone: "info" },
      { label: "128ms", tone: "muted" },
    ]);
    expect(summary.headlineItems).toEqual([
      { label: "1 failed", tone: "error" },
      { label: "1 truncated", tone: "info" },
    ]);
  });

  it("uses the concrete action name for a single tool call", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(completedTurn).turns[0]);

    expect(summary.actionLabel).toBe("List files: .");
  });

  it("shows completed tool failures as tool issues when a final answer exists", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents(toolError).turns[0]);

    expect(summary.items).toContainEqual({ label: "1 tool issue", tone: "warning" });
    expect(summary.headlineItems).toEqual([{ label: "1 tool issue", tone: "warning" }]);
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

  it("surfaces verified source and network evidence stats", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "browser_network_read",
          args: { ref: "net:1" },
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 64,
          result_summary: "validated dynamic metrics",
          result_truncated: false,
        },
      },
      {
        id: 4,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "completed",
          tool_stats: {
            tool_requests: 1,
            source_access_verified: 2,
            source_access_network: 1,
            source_access_dynamic_partial: 1,
            tool_duration_ms: 64,
          },
        },
      },
    ]).turns[0]);

    expect(summary.items).toContainEqual({ label: "2 verified sources", tone: "info" });
    expect(summary.items).toContainEqual({ label: "1 network source", tone: "info" });
    expect(summary.items).toContainEqual({ label: "1 partial source", tone: "warning" });
    expect(summary.headlineItems).toEqual([
      { label: "2 verified sources", tone: "info" },
      { label: "1 network source", tone: "info" },
      { label: "1 partial source", tone: "warning" },
    ]);
  });

  it("surfaces session recall hits as visible work", () => {
    const summary = buildTurnWorkSummary(reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "session_search",
          args: { query: "Alpha Coast marker" },
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 24,
          result_summary: "{\"total\":2}",
          result_truncated: false,
        },
      },
      {
        id: 4,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "completed",
          tool_stats: {
            tool_requests: 1,
            session_search_calls: 1,
            session_search_results: 2,
            session_search_context_hits: 1,
            session_search_matched_terms: 3,
            tool_duration_ms: 24,
          },
        },
      },
    ]).turns[0]);

    expect(summary.items).toContainEqual({ label: "2 recall hits", tone: "info" });
    expect(summary.headlineItems).toEqual([{ label: "2 recall hits", tone: "info" }]);
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
