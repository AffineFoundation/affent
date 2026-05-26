import { describe, expect, it } from "vitest";
import { normalizeEvents } from "../normalize/normalizeEvent";
import { buildEventTraceItems, buildEventTraceModel, streamSummary } from "./eventTrace";

describe("eventTrace view model", () => {
  it("summarizes known protocol events as user-facing actions", () => {
    const model = buildEventTraceModel(normalizeEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "read_file",
          canonicalized: true,
          args_repaired: true,
          args_truncated: true,
          original_args_summary: "path README.md",
        },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "file missing",
          result_bytes: 1024,
          result_omitted_bytes: 3072,
          result_cap_bytes: 1024,
          result_truncated: true,
          result_artifact_path: ".affent/artifacts/c1.txt",
        },
      },
      {
        id: 4,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          kind: "evidence_quality",
          decision: "defer",
          confidence: "high",
          reason: "Dynamic page shell needs network evidence.",
          visible_in_ui: true,
        },
      },
      {
        id: 5,
        type: "context.compacted",
        data: {
          turn_id: "t1",
          before_messages: 42,
          after_messages: 13,
          removed_messages: 29,
          reactive: true,
          reason: "context_overflow",
          summary_present: true,
          summary_bytes: 2048,
        },
      },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "max_turns", tool_stats: { tool_requests: 2, tool_errors: 1, source_access_verified: 2, source_access_network: 1, source_access_dynamic_partial: 1, tool_duration_ms: 1200 } } },
    ]));
    const [request, result, decision, compacted, finished] = model.items;

    expect(model.metadata).toHaveLength(1);
    expect(model.metadata[0].type).toBe("trace.meta");
    expect(model.items).toHaveLength(5);
    expect(request).toMatchObject({
      kind: "event",
      display: {
        label: "Started action",
        meta: ["Request 1", "read_file", "path README.md"],
        badges: ["renamed", "repaired", "truncated"],
      },
    });
    expect(result).toMatchObject({
      kind: "event",
      display: {
        label: "Action failed",
        meta: ["read_file", "42 ms", "file missing", "artifact c1.txt (1 KiB, 3 KiB omitted)"],
        badges: ["truncated", "full output"],
      },
    });
    expect(decision).toMatchObject({
      kind: "event",
      display: {
        label: "Loop decision",
        meta: ["Request 1", "evidence_quality", "defer", "Dynamic page shell needs network evidence."],
        badges: ["high", "visible"],
      },
    });
    expect(compacted).toMatchObject({
      kind: "event",
      display: {
        label: "Context compacted",
        meta: ["Request 1", "context_overflow", "42 -> 13 messages", "29 removed", "2 KiB summary"],
        badges: ["reactive", "summary"],
      },
    });
    expect(finished).toMatchObject({
      kind: "event",
      display: {
        label: "Stopped at limit",
        meta: ["Request 1", "max_turns", "2 actions", "1 failed", "2 sources", "1 network", "1 partial", "1.2 s"],
      },
    });
  });

  it("groups interleaved delta events per turn and type", () => {
    const items = buildEventTraceItems(normalizeEvents([
      { id: 1, type: "message.delta", data: { turn_id: "t1", delta: "Hel" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "read_file" } },
      { id: 3, type: "message.delta", data: { turn_id: "t1", delta: "lo" } },
      { id: 4, type: "message.done", data: { turn_id: "t1", text: "Hello final", finish_reason: "stop" } },
      { id: 5, type: "thinking.delta", data: { turn_id: "t1", delta: "Plan" } },
      { id: 6, type: "thinking.done", data: { turn_id: "t1", text: "Plan final" } },
      { id: 7, type: "message.delta", data: { turn_id: "t2", delta: "Next" } },
    ]));

    expect(items.map((item) => {
      if (item.kind === "deltaGroup") return [item.label, item.turnLabel, item.text, item.events.map((event) => event.id)];
      if (item.kind === "eventGroup") return item.label;
      return item.display.label;
    })).toEqual([
      ["Assistant output", "Request 1", "Hello final", [1, 3, 4]],
      "Started action",
      ["Thinking notes", "Request 1", "Plan final", [5, 6]],
      ["Assistant output", "Request 2", "Next", [7]],
    ]);
  });

  it("groups request lifecycle events into one request record", () => {
    const items = buildEventTraceItems(normalizeEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize the repo" } },
      { id: 3, type: "message.delta", data: { turn_id: "t1", delta: "Done" } },
      { id: 4, type: "usage", data: { turn_id: "t1", input_tokens: 12, output_tokens: 5 } },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 6, type: "turn.end", data: { turn_id: "single", reason: "completed" } },
    ]));

    expect(items.map((item) => {
      if (item.kind === "eventGroup") return [item.label, item.meta, item.events.map((event) => event.id)];
      if (item.kind === "deltaGroup") return item.label;
      return item.display.label;
    })).toEqual([
      ["Request trace", ["Request 1", "summarize the repo", "completed", "17 tokens"], [1, 2, 4, 5]],
      "Assistant output",
      "Request finished",
    ]);
  });

  it("collapses whitespace and truncates long summaries", () => {
    expect(streamSummary("  line one\n\tline two  ")).toBe("line one line two");
    expect(streamSummary("x".repeat(120))).toBe(`${"x".repeat(95)}...`);
    expect(streamSummary("   ")).toBe("(empty output)");
  });
});
