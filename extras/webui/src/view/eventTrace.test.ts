import { describe, expect, it } from "vitest";
import { normalizeEvents } from "../normalize/normalizeEvent";
import { buildEventTraceItems, buildEventTraceModel, filterEventTraceEvents, streamSummary } from "./eventTrace";

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
          context_bytes: 2048,
          context_omitted_bytes: 1024,
          result_artifact_path: ".affent/artifacts/c1.txt",
        },
      },
      {
        id: 3,
        type: "loop.protocol_feed",
        data: {
          turn_id: "t1",
          loop_id: "longrun",
          status: "running",
          mode: "digest",
          feed_number: 4,
          protocol_feeds: 4,
          calibration_answers: 1,
          last_calibration_answer_preview: "Stop when source evidence is weak.",
          protocol_path: ".affent/loops/longrun/LOOP.md",
          current_situation_preview: "current intent: verify browser evidence; current risk: dynamic page needs network refs",
          plan_label: "plan:1/3:active",
          plan_current_step_index: 2,
          plan_current_step_status: "in_progress",
          plan_current_step: "verify browser evidence",
        },
      },
      {
        id: 4,
        type: "loop.protocol_calibration_request",
        data: {
          loop_id: "longrun",
          status: "draft",
          calibration_questions: 1,
          last_calibration_question_preview: "What stop condition should pause this loop?",
          protocol_path: ".affent/loops/longrun/LOOP.md",
          event_seq: 2,
        },
      },
      {
        id: 5,
        type: "loop.protocol_calibration",
        data: {
          loop_id: "longrun",
          status: "draft",
          calibration_questions: 1,
          last_calibration_question_preview: "What stop condition should pause this loop?",
          calibration_answers: 1,
          last_calibration_answer_preview: "Stop when source evidence is weak.",
          protocol_path: ".affent/loops/longrun/LOOP.md",
          event_seq: 3,
        },
      },
      {
        id: 6,
        type: "loop.protocol_activate",
        data: {
          turn_id: "t1",
          loop_id: "longrun",
          status: "running",
          protocol_updates: 3,
          protocol_path: ".affent/loops/longrun/LOOP.md",
          event_seq: 4,
        },
      },
      {
        id: 7,
        type: "runtime.surface",
        data: {
          turn_id: "t1",
          tool_count: 16,
          capabilities: { web_search: true, memory: true, subagent: true, focused_tasks: true, workspace_tools: ["read_file"] },
          max_turn_steps: 12,
          max_tool_calls: 8,
          max_turn_input_tokens: 300000,
        },
      },
      {
        id: 8,
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
        id: 9,
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
        id: 10,
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
    const [request, result, feed, question, calibration, activation, surface, decision, compacted, finished] = model.items;

    expect(model.metadata).toHaveLength(1);
    expect(model.metadata[0].type).toBe("trace.meta");
    expect(model.items).toHaveLength(10);
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
        meta: ["Request 1", "16 tools", "12 turns", "8 tool cap", "300,000 input cap"],
        badges: ["web search", "memory", "subagent", "focused tasks", "workspace: read_file"],
      },
    });
    expect(result).toMatchObject({
      kind: "event",
      display: {
        label: "Action failed",
        meta: ["read_file", "42 ms", "file missing", "tool context 2 KiB, 1 KiB omitted", "artifact c1.txt (1 KiB, 3 KiB omitted)"],
        badges: ["context trimmed", "truncated", "full output"],
      },
    });
    expect(feed).toMatchObject({
      kind: "event",
      display: {
        label: "Loop protocol fed",
        meta: ["Request 1", "longrun", "feed 4", "calibration 1 · Stop when source evidence is weak.", "situation · current intent: verify browser evidence; current risk: dynamic page needs network refs", "plan plan:1/3:active · step 2 in_progress · verify browser evidence", ".affent/loops/longrun/LOOP.md"],
        badges: ["digest", "running"],
      },
    });
    expect(question).toMatchObject({
      kind: "event",
      display: {
        label: "Loop calibration asked",
        meta: ["longrun", "question 1", "What stop condition should pause this loop?", "event 2", ".affent/loops/longrun/LOOP.md"],
        badges: ["draft"],
      },
    });
    expect(calibration).toMatchObject({
      kind: "event",
      display: {
        label: "Loop calibration recorded",
        meta: ["longrun", "calibration 1", "Stop when source evidence is weak.", "event 3", ".affent/loops/longrun/LOOP.md"],
        badges: ["draft"],
      },
    });
    expect(activation).toMatchObject({
      kind: "event",
      display: {
        label: "Loop activated",
        meta: ["Request 1", "longrun", "3 updates", "event 4", ".affent/loops/longrun/LOOP.md"],
        badges: ["running"],
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

  it("filters trace events by display text, raw payload, and unclassified status", () => {
    const events = normalizeEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      { id: 4, type: "turn.start", data: { turn_id: "t2" } },
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t2",
          call_id: "c1",
          tool: "read_file",
          args: { path: "README.md" },
        },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t2",
          call_id: "c1",
          exit_code: 1,
          result_summary: "file missing",
        },
      },
      { id: 3, type: "future.event", data: { detail: "new server field" } },
    ]);

    expect(filterEventTraceEvents(events, "read_file").map((event) => event.id)).toEqual([1, 2]);
    expect(filterEventTraceEvents(events, "README.md").map((event) => event.id)).toEqual([1]);
    expect(filterEventTraceEvents(events, "file missing").map((event) => event.id)).toEqual([2]);
    expect(filterEventTraceEvents(events, "schema v1").map((event) => event.id)).toEqual([0]);
    expect(filterEventTraceEvents(events, "unclassified").map((event) => event.id)).toEqual([3]);
    expect(filterEventTraceEvents(events, "request:1").map((event) => event.id)).toEqual([4, 1, 2]);
    expect(filterEventTraceEvents(events, "req:t2").map((event) => event.id)).toEqual([4, 1, 2]);
    expect(filterEventTraceEvents(events, "request:99")).toHaveLength(0);
  });

  it("labels research checkpoint loop decisions in event trace metadata", () => {
    const model = buildEventTraceModel(normalizeEvents([
      {
        id: 1,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          kind: "research_checkpoint",
          decision: "trigger",
          confidence: "medium",
          reason: "High-impact loop design review needs external calibration.",
          visible_in_ui: true,
        },
      },
    ]));

    expect(model.items[0]).toMatchObject({
      kind: "event",
      display: {
        label: "Loop decision",
        meta: ["Request 1", "research checkpoint", "trigger", "High-impact loop design review needs external calibration."],
        badges: ["medium", "visible"],
      },
    });
  });

  it("labels budget loop decisions in event trace metadata", () => {
    const model = buildEventTraceModel(normalizeEvents([
      {
        id: 1,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          kind: "input_budget",
          decision: "defer",
          confidence: "high",
          reason: "Projected next request would exceed this turn budget.",
          token_budget: 300000,
          observed_input_tokens: 120000,
          projected_input_tokens: 480000,
          visible_in_ui: true,
        },
      },
      {
        id: 2,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          kind: "tool_context_budget",
          decision: "defer",
          confidence: "high",
          reason: "Tool result context budget exhausted.",
          budget_bytes: 32768,
          visible_in_ui: true,
        },
      },
    ]));

    expect(model.items[0]).toMatchObject({
      kind: "event",
      display: {
        label: "Loop decision",
        meta: ["Request 1", "input budget", "defer", "300,000 tokens", "projected 480,000 / 300,000 input tokens", "Projected next request would exceed this turn budget."],
        badges: ["high", "300,000 tokens", "visible"],
      },
    });
    expect(model.items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Loop decision",
        meta: ["Request 1", "context budget", "defer", "32 KiB", "Tool result context budget exhausted."],
        badges: ["high", "32 KiB", "visible"],
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
                message_idx: 4,
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
          "message 4",
          "matched alpha, coast, inventory",
          "adjacent context",
          "snippet user: Alpha Coast assistant: inventory-drag",
          "also 1 unshown history hit",
        ],
      },
    });
  });

  it("surfaces loop guard guidance on blocked tool result rows", () => {
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "web_fetch" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          failure_kinds: ["blocked", "loop_guard_repeated_failed_input"],
          result: "loop_guard: blocked repeated failed call to \"web_fetch\" with the same effective URL after previous Failure kind=blocked.\nNext: do not retry the same failing URL; choose a different source, use another available inspection tool, or answer with clearly marked gaps.\nFailure: kind=loop_guard_repeated_failed_input",
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action failed",
        meta: [
          "web_fetch",
          "loop guard repeated failed input",
          "guard blocked repeated failed call to \"web_fetch\" with the same effective URL after previous Failure ...",
          "next do not retry the same failing URL; choose a different source, use another available inspection ...",
        ],
        badges: ["blocked", "loop_guard_repeated_failed_input"],
      },
    });
  });

  it("surfaces failed tool Next guidance on result rows", () => {
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "read_file" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          failure_kind: "not_found",
          result_summary: "read failed\nNext: check the path from rg --files before retrying\nFailure: kind=not_found",
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action failed",
        meta: [
          "read_file",
          "next check the path from rg --files before retrying",
          "read failed Next: check the path from rg --files before retrying Failure: kind=not_found",
        ],
        badges: ["not_found"],
      },
    });
  });

  it("surfaces weak context compaction summary states", () => {
    const model = buildEventTraceModel(normalizeEvents([
      {
        id: 1,
        type: "context.compacted",
        data: {
          turn_id: "t1",
          before_messages: 50,
          after_messages: 12,
          removed_messages: 38,
          reactive: true,
          reason: "context_overflow",
          summary_present: false,
          summary_bytes: 0,
          summary_preview: "",
        },
      },
      {
        id: 2,
        type: "context.compacted",
        data: {
          turn_id: "t2",
          before_messages: 30,
          after_messages: 14,
          removed_messages: 16,
          reactive: false,
          reason: "threshold",
          summary_present: true,
          summary_bytes: 0,
          summary_preview: "",
        },
      },
    ]));

    expect(model.items[0]).toMatchObject({
      kind: "event",
      display: {
        label: "Context compacted",
        meta: ["Request 1", "context_overflow", "50 -> 12 messages", "38 removed", "summary missing"],
        badges: ["reactive", "summary missing"],
      },
    });
    expect(model.items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Context compacted",
        meta: ["Request 2", "threshold", "30 -> 14 messages", "16 removed", "summary empty"],
        badges: ["proactive", "summary empty"],
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

  it("surfaces non-normal message modes in request trace records", () => {
    const items = buildEventTraceItems(normalizeEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "market monitor", display_text: "Set up loop: market monitor", mode: "loop_setup" } },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]));

    expect(items[0]).toMatchObject({
      kind: "eventGroup",
      label: "Request trace",
      meta: ["Request 1", "loop setup", "Set up loop: market monitor", "completed"],
      badges: ["loop_setup"],
    });
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

  it("surfaces confirmed memory update details on tool result rows", () => {
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "mem1", tool: "memory", args: { action: "replace", topic: "markets" } },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "mem1",
          exit_code: 0,
          result: "{\"ok\":true}",
          memory_update: {
            action: "replace",
            target: "memory",
            topic: "markets",
            location: "memory:markets",
            preview: "old dashboard rule -> prefer browser_network_read evidence",
            previous_preview: "old dashboard rule",
            next_preview: "prefer browser_network_read evidence",
          },
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action finished",
        meta: [
          "memory",
          "Updated memory",
          "memory:markets",
          "old dashboard rule -> prefer browser_network_read evidence",
        ],
        badges: ["memory replace"],
      },
    });
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
        meta: ["browser_navigate", "42 ms", "partial source", "https://taostats.io/subnets/120", "preview PAGE TEXT: Market Cap"],
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
          result_summary: "SourceAccess: browser_network_url=https://api.taostats.io/subnets/120; requested_url=https://app.taostats.io/subnets/120; ref=n2; status=200; content_type=application/json; source_method=network_xhr_fetch",
          result: "SourceAccess: browser_network_url=https://api.taostats.io/subnets/120; requested_url=https://app.taostats.io/subnets/120; ref=n2; status=200; content_type=application/json; source_method=network_xhr_fetch\n{\"price\":\"0.06342 T\"}",
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
          "ref n2",
          "http 200",
          "application/json",
          "preview {\"price\":\"0.06342 T\"}",
        ],
        badges: ["network"],
      },
    });
  });

  it("surfaces browser scroll telemetry on tool result rows", () => {
    const nextHint = "scrolling did not move the page; use browser_network/browser_network_read for hidden XHR/fetch data.";
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "browser_scroll" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 18,
          result_summary: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence",
          result: [
            "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence",
            "PAGE TEXT:",
            "Market Cap",
            "SCROLL: direction=down before_y=1200 after_y=1200 max_y=1200 movement=none boundary=bottom",
            `Next: ${nextHint}`,
          ].join("\n"),
          result_truncated: false,
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action finished",
        meta: [
          "browser_scroll",
          "18 ms",
          "scroll down no movement at bottom y 1200/1200",
          `next ${streamSummary(nextHint)}`,
          "partial source",
          "https://taostats.io/subnets/120",
          "preview PAGE TEXT: Market Cap",
        ],
        badges: ["dynamic_partial", "scroll no movement", "scroll bottom"],
      },
    });
  });

  it("surfaces browser network ref guidance before read evidence exists", () => {
    const resultSummary = [
      "BROWSER_NETWORK: status=matches page_url=https://taostats.io/subnets/120 query=\"market cap\" refs=n2",
      "ref n2 url=https://api.taostats.io/subnets/120 preview={\"price\":\"0.06342 T\"}",
      "Next: call browser_network_read with the most relevant ref and json_path before citing values.",
    ].join("\n");
    const items = buildEventTraceItems(normalizeEvents([
      {
        id: 1,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "c1", tool: "browser_network" },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 22,
          result_summary: resultSummary,
        },
      },
    ]));

    expect(items[1]).toMatchObject({
      kind: "event",
      display: {
        label: "Action finished",
        meta: [
          "browser_network",
          "22 ms",
          "next call browser_network_read with the most relevant ref and json_path before citing values.",
          streamSummary(resultSummary),
        ],
        badges: [],
      },
    });
  });

  it("collapses whitespace and truncates long summaries", () => {
    expect(streamSummary("  line one\n\tline two  ")).toBe("line one line two");
    expect(streamSummary("x".repeat(120))).toBe(`${"x".repeat(95)}...`);
    expect(streamSummary("   ")).toBe("(empty output)");
  });
});
