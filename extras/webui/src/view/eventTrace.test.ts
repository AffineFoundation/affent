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
        type: "runtime.surface",
        data: {
          turn_id: "t1",
          tool_count: 16,
          capabilities: { web_search: true, memory: true, subagent: true, focused_tasks: true, workspace_tools: ["read_file"] },
          max_turn_steps: 12,
          max_tool_calls: 8,
        },
      },
      {
        id: 5,
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
        id: 6,
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
          summary_preview: "USER_CONTEXT: keep exact source URLs and current plan state.",
        },
      },
      {
        id: 7,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "max_turns",
          tool_stats: {
            tool_requests: 2,
            tool_errors: 1,
            session_search_calls: 1,
            session_search_results: 2,
            session_search_context_hits: 1,
            session_search_matched_terms: 3,
            source_access_verified: 2,
            source_access_network: 1,
            source_access_dynamic_partial: 1,
            tool_duration_ms: 1200,
          },
        },
      },
    ]));
    const [request, result, surface, decision, compacted, finished] = model.items;

    expect(model.metadata).toHaveLength(1);
    expect(model.metadata[0].type).toBe("trace.meta");
    expect(model.items).toHaveLength(6);
    expect(request).toMatchObject({
      kind: "event",
      display: {
        label: "Started action",
        meta: ["Request 1", "read_file", "path README.md"],
        badges: ["renamed", "repaired", "truncated"],
      },
    });
    expect(surface).toMatchObject({
      kind: "event",
      display: {
        label: "Runtime surface",
        meta: ["Request 1", "16 tools", "12 turns", "8 tool cap"],
        badges: ["web search", "memory", "subagent", "focused tasks", "workspace: read_file"],
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
        meta: ["Request 1", "context_overflow", "42 -> 13 messages", "29 removed", "2 KiB summary", "summary: USER_CONTEXT: keep exact source URLs and current plan state."],
        badges: ["reactive", "summary"],
      },
    });
    expect(finished).toMatchObject({
      kind: "event",
      display: {
        label: "Stopped at limit",
        meta: ["Request 1", "max_turns", "2 actions", "1 failed", "Recall 2 hits, 1 context, 3 terms", "2 sources", "1 network", "1 partial", "1.2 s"],
      },
    });
  });

  it("summarizes session search match diagnostics", () => {
    const model = buildEventTraceModel(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "search-1",
          tool: "session_search",
          args: { query: "Alpha Coast inventory" },
        },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "search-1",
          exit_code: 0,
          duration_ms: 25,
          result: JSON.stringify({
            query: "Alpha Coast inventory",
            total: 2,
            results: [
              {
                session_id: "market-alpha",
                turn_idx: 2,
                role: "assistant",
                snippet: "user: Alpha Coast\nassistant: inventory-drag",
                score: 4.4,
                matched_terms: ["alpha", "coast", "inventory"],
                context_included: true,
              },
            ],
          }),
        },
      },
    ]));

    expect(model.items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action finished",
        meta: [
          "session_search",
          "25 ms",
          "2 history hits",
          "market-alpha",
          "turn 2",
          "matched alpha, coast, inventory",
          "adjacent context",
        ],
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
      { id: 3, type: "runtime.surface", data: { turn_id: "t1", tool_count: 3, capabilities: { web_fetch: true } } },
      { id: 4, type: "message.delta", data: { turn_id: "t1", delta: "Done" } },
      { id: 5, type: "usage", data: { turn_id: "t1", input_tokens: 12, output_tokens: 5 } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 7, type: "turn.end", data: { turn_id: "single", reason: "completed" } },
    ]));

    expect(items.map((item) => {
      if (item.kind === "eventGroup") return [item.label, item.meta, item.events.map((event) => event.id)];
      if (item.kind === "deltaGroup") return item.label;
      return item.display.label;
    })).toEqual([
      ["Request trace", ["Request 1", "summarize the repo", "completed", "17 tokens"], [1, 2, 3, 5, 6]],
      "Assistant output",
      "Request finished",
    ]);
  });

  it("promotes long-run runtime counters into request trace summaries", () => {
    const items = buildEventTraceItems(normalizeEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "recover repeated web failures" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "max_turns",
          tool_stats: {
            tool_requests: 4,
            tool_errors: 1,
            loop_guard_interventions: 2,
            forced_no_tools: 1,
            memory_updates: 3,
            memory_update_add: 2,
            memory_update_replace: 1,
            session_search_calls: 1,
            session_search_results: 2,
            session_search_context_hits: 1,
            session_search_matched_terms: 3,
            source_access_verified: 2,
            tool_duration_ms: 1250,
          },
        },
      },
      {
        id: 4,
        type: "turn.end",
        data: {
          turn_id: "single",
          reason: "completed",
          tool_stats: {
            loop_guard_interventions: 1,
            memory_updates: 1,
            memory_update_remove: 1,
          },
        },
      },
    ]));

    expect(items.map((item) => {
      if (item.kind === "eventGroup") return [item.label, item.meta];
      if (item.kind === "event") return [item.display.label, item.display.meta];
      return item.label;
    })).toEqual([
      [
        "Request trace",
        [
          "Request 1",
          "recover repeated web failures",
          "max_turns",
          "4 actions",
          "1 failed",
          "Guard 2",
          "1 no-tools",
          "3 memory updates (2 add, 1 replace)",
          "Recall 2 hits, 1 context, 3 terms",
          "2 sources",
          "1.3 s",
        ],
      ],
      ["Request finished", ["Request 2", "completed", "Guard 1", "1 memory update (1 remove)"]],
    ]);
  });

  it("surfaces source evidence status on tool result rows", () => {
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "browser_navigate" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 42,
          result_summary: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence",
          result: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence\nPAGE TEXT:\nMarket Cap",
          result_truncated: false,
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action finished",
        meta: ["browser_navigate", "42 ms", "partial source", "https://taostats.io/subnets/120"],
        badges: ["dynamic_partial"],
      },
    });
  });

  it("keeps requested source provenance visible for network evidence rows", () => {
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "browser_network_read" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 30,
          result_summary: "SourceAccess: browser_network_url=https://api.taostats.io/subnets/120; requested_url=https://app.taostats.io/subnets/120; source_method=network_xhr_fetch",
          result: "SourceAccess: browser_network_url=https://api.taostats.io/subnets/120; requested_url=https://app.taostats.io/subnets/120; source_method=network_xhr_fetch\n{\"price\":\"0.06342 T\"}",
          result_truncated: false,
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action finished",
        meta: [
          "browser_network_read",
          "30 ms",
          "network source",
          "https://api.taostats.io/subnets/120",
          "from https://app.taostats.io/subnets/120",
        ],
        badges: ["network"],
      },
    });
  });

  it("collapses whitespace and truncates long summaries", () => {
    expect(streamSummary("  line one\n\tline two  ")).toBe("line one line two");
    expect(streamSummary("x".repeat(120))).toBe(`${"x".repeat(95)}...`);
    expect(streamSummary("   ")).toBe("(empty output)");
  });
});
