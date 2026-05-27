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

describe("reduce — user display text", () => {
  it("uses display_text for generated control prompts", () => {
    const s = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "user.message",
        data: {
          turn_id: "t1",
          text: "internal loop setup prompt with detailed tool instructions",
          display_text: "Set up loop: market monitor",
        },
      },
    ]);

    expect(s.turns[0].userText).toBe("Set up loop: market monitor");
  });
});

describe("reduce — context injections", () => {
  it("attaches pre-turn injected context to the matching turn", () => {
    const s = reduceRawEvents([
      {
        id: 1,
        type: "context.injected",
        data: {
          turn_id: "t1",
          source: "account_access",
          title: "Account access context injected",
          summary: "Account hints were made available.",
          preview: "GITHUB_TOKEN",
          bytes: 120,
          estimated_tokens: 30,
        },
      },
      { id: 2, type: "turn.start", data: { turn_id: "t1" } },
    ]);

    expect(s.contextInjections).toHaveLength(1);
    expect(s.turns[0].contextInjections?.[0]).toMatchObject({
      eventId: 1,
      source: "account_access",
      preview: "GITHUB_TOKEN",
    });
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

  it("preserves structured tool failure kinds for replay and filtering", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://taostats.io/subnets/120" },
          args_truncated: false,
          args_bytes: 42,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          failure_kind: "dynamic_shell",
          failure_kinds: ["dynamic_shell", "no_verified_source"],
          result_summary: "Only a dynamic shell was available.",
          result: "Failure: kind=dynamic_shell",
          result_truncated: false,
          result_bytes: 28,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
    ]);

    const c = s.turns[0].toolCalls[0];
    expect(c.failureKind).toBe("dynamic_shell");
    expect(c.failureKinds).toEqual(["dynamic_shell", "no_verified_source"]);
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
    expect(c.contextBytes).toBe(4096);
    expect(c.contextOmittedBytes).toBe(4096);
    expect(c.contextEstimatedTokens).toBe(1024);
  });

  it("preserves structured memory update metadata from tool results", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "m1",
          tool: "memory",
          args: { __affent_truncated: "tool request args exceeded cap" },
          args_truncated: true,
          args_bytes: 17000,
          args_omitted_bytes: 12000,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 2,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "m1",
          exit_code: 0,
          result_summary: "{\"ok\":true}",
          result: "{\"ok\":true}",
          result_truncated: false,
          result_bytes: 11,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
          memory_update: {
            action: "add",
            target: "memory",
            topic: "markets",
            location: "memory:markets",
            preview: "Alpha Coast reports use marker MEM-STOCK-73.",
            next_preview: "Alpha Coast reports use marker MEM-STOCK-73.",
          },
        },
      },
    ]);

    expect(s.turns[0].toolCalls[0].memoryUpdate).toMatchObject({
      action: "add",
      location: "memory:markets",
      preview: "Alpha Coast reports use marker MEM-STOCK-73.",
    });
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
  it("records runtime surface on the turn without counting it as unknown", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "runtime.surface",
        data: {
          turn_id: "t1",
          tool_count: 3,
          tools: [{ name: "web_fetch", group: "Research" }, { name: "web_search", group: "Research" }, { name: "memory", group: "Memory" }],
          capabilities: { web_fetch: true, web_search: true, memory: true },
          max_turn_steps: 12,
          tool_result_event_cap_bytes: 262144,
          tool_result_context_budget_bytes: 32768,
        },
      },
      { id: 2, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    expect(s.unknownEventCount).toBe(0);
    expect(s.turns[0].runtimeSurface?.tool_count).toBe(3);
    expect(s.turns[0].runtimeSurface?.capabilities.web_search).toBe(true);
    expect(s.turns[0].runtimeSurface?.tools?.map((tool) => tool.name)).toEqual(["web_fetch", "web_search", "memory"]);
  });

  it("records loop decisions without counting them as unknown", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          loop_id: "affent-runtime",
          decision_id: "d1",
          kind: "evidence_quality",
          trigger: "dynamic_page_shell",
          decision: "defer",
          confidence: "high",
          reason: "Only a dynamic shell was captured.",
          required_action: "Use browser_network before citing values.",
          visible_in_ui: true,
        },
      },
      { id: 2, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    expect(s.unknownEventCount).toBe(0);
    expect(s.loopDecisions).toEqual([
      {
        eventId: 1,
        turn_id: "t1",
        loop_id: "affent-runtime",
        decision_id: "d1",
        kind: "evidence_quality",
        trigger: "dynamic_page_shell",
        decision: "defer",
        confidence: "high",
        reason: "Only a dynamic shell was captured.",
        required_action: "Use browser_network before citing values.",
        visible_in_ui: true,
      },
    ]);
    expect(s.turns[0].loopDecisions).toEqual(s.loopDecisions);
  });

  it("records context compactions on both session and owning turn", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "context.compacted",
        data: {
          turn_id: "t1",
          before_messages: 90,
          after_messages: 18,
          removed_messages: 72,
          reactive: true,
          reason: "context_overflow",
          summary_present: true,
          summary_bytes: 4096,
          summary_preview: "USER_CONTEXT: preserve code review state.",
        },
      },
      { id: 2, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    expect(s.unknownEventCount).toBe(0);
    expect(s.contextCompactions).toEqual([
      {
        eventId: 1,
        turn_id: "t1",
        before_messages: 90,
        after_messages: 18,
        removed_messages: 72,
        reactive: true,
        reason: "context_overflow",
        summary_present: true,
        summary_bytes: 4096,
        summary_preview: "USER_CONTEXT: preserve code review state.",
      },
    ]);
    expect(s.turns[0].contextCompactions).toEqual(s.contextCompactions);
  });

  it("accepts loop calibration events without counting them as unknown", () => {
    const s = reduceRawEvents([
      { id: 0, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 1,
        type: "loop.protocol_calibration_request",
        data: {
          loop_id: "longrun",
          status: "draft",
          calibration_questions: 1,
          last_calibration_question_preview: "What should pause this loop?",
        },
      },
      {
        id: 2,
        type: "loop.protocol_calibration",
        data: {
          loop_id: "longrun",
          status: "draft",
          calibration_questions: 1,
          calibration_answers: 1,
          last_calibration_answer_preview: "Pause on weak evidence.",
        },
      },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    expect(s.unknownEventCount).toBe(0);
    expect(s.turns).toHaveLength(1);
  });

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
