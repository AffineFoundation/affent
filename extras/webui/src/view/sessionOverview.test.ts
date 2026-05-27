import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { completedSubagentTree, runningSubagent } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { deriveWorkflowStatus } from "../store/workflowStatus";
import { buildSessionOverview, displaySessionOverviewMetrics } from "./sessionOverview";

describe("buildSessionOverview", () => {
  it("keeps the no-session state task-first", () => {
    const session = reduceRawEvents([]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
    });

    expect(overview).toMatchObject({
      headline: "Start a chat",
      stateLabel: "Ready",
      tone: "ready",
    });
    expect(overview.detail).toContain("create the chat");
  });

  it("shows a summarized submitted task while waiting for the first live event", () => {
    const session = reduceRawEvents([]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
      pendingTask: "summarize the repository architecture",
    });

    expect(overview).toMatchObject({
      headline: "repository architecture",
      stateLabel: "Sending",
      tone: "running",
      active: true,
    });
    expect(overview.detail).toContain("Creating chat");
  });

  it("does not use a full feedback sentence as the pending chat title", () => {
    const session = reduceRawEvents([]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
      pendingTask: "会话的标题最好是经过总结的，而不是把第一句话的输入当做标题",
    });

    expect(overview).toMatchObject({
      headline: "会话标题摘要",
      stateLabel: "Sending",
      tone: "running",
    });
  });

  it("shows a submitted follow-up as the current context even when history exists", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      pendingTask: "explain main.go",
    });

    expect(overview).toMatchObject({
      headline: "main.go",
      stateLabel: "Sending",
      tone: "running",
      active: true,
    });
    expect(overview.detail).toContain("next update");
    expect(overview.metrics).not.toContainEqual({ label: "Work", value: "1 action" });
  });

  it("labels pending live guidance as an intervention", () => {
    const session = reduceRawEvents(runningSubagent);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      pendingGuidance: "Guidance for current run: inspect tests first",
    });

    expect(overview).toMatchObject({
      headline: "use a subagent to inspect docs",
      stateLabel: "Sending guidance",
      detail: "Applying your guidance to the current run.",
      tone: "running",
      active: true,
    });
  });

  it("uses the latest user task as the headline after a turn exists", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.headline).toBe("list the files");
    expect(overview.stateLabel).toBe("Result ready");
    expect(overview.detail).toBe("README.md main.go");
    expect(overview.metrics).toEqual([
      { label: "Work", value: "1 action", tone: undefined },
      { label: "Tokens", value: "138" },
    ]);
    expect(displaySessionOverviewMetrics(overview.metrics)).toEqual([
      { label: "Work", value: "1 action", tone: undefined },
    ]);
  });

  it("keeps warning end reasons in display metrics but drops plain token counts", () => {
    expect(displaySessionOverviewMetrics([
      { label: "Turn tokens", value: "1.2k" },
      { label: "Chat tokens", value: "1.7k" },
      { label: "End", value: "max_turns", tone: "warning" },
    ])).toEqual([
      { label: "End", value: "max_turns", tone: "warning" },
    ]);
  });

  it("surfaces session context pressure from the server summary", () => {
    const session = reduceRawEvents([]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      contextSummary: {
        message_count: 192,
        compact_trigger: 240,
        compact_percent: 80,
        messages_until_compact: 48,
      },
    });

    expect(overview.metrics).toContainEqual({ label: "Context", value: "192/240 · 80%", tone: "warning" });
  });

  it("does not understate context pressure when local events exceed the session summary", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "inspect" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "shell", args: { command: "ls" } } },
      { id: 4, type: "tool.result", data: { turn_id: "t1", call_id: "c1", exit_code: 0, result_summary: "ok", result: "ok" } },
      { id: 5, type: "message.done", data: { turn_id: "t1", text: "done" } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      contextSummary: {
        message_count: 1,
        compact_trigger: 4,
        compact_percent: 25,
        messages_until_compact: 3,
      },
    });

    expect(overview.metrics).toContainEqual({ label: "Context", value: "4/4 · 100%", tone: "error" });
  });

  it("surfaces artifact output in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize the repo" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 42,
          result_summary: "saved output",
          result: "saved output",
          result_truncated: true,
          result_bytes: 8192,
          result_omitted_bytes: 1048576,
          result_cap_bytes: 262144,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Artifact", value: "1 file (8 KiB, 1 MiB omitted)" },
      { label: "Work", value: "1 action · 1 source", tone: undefined },
    ]));
  });

  it("surfaces visible loop decisions in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "inspect taostats subnet 19" } },
      {
        id: 3,
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
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Loop", value: "1 decision defer", tone: undefined },
    ]));
  });

  it("surfaces loop protocol feed checkpoints in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "continue active loop" } },
      {
        id: 3,
        type: "loop.protocol_feed",
        data: {
          turn_id: "t1",
          mode: "full",
          feed_number: 1,
          protocol_path: ".affent/loops/plan-loop/LOOP.md",
          current_situation_preview: "current risk: browser values need network refs",
          plan_label: "plan:1/3:active",
          plan_current_step_index: 2,
          plan_current_step_status: "in_progress",
          plan_current_step: "verify browser network evidence",
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Loop", value: "1 feed full plan:1/3:active step 2 in_progress situation current risk: browser values need network refs", tone: undefined },
    ]));
  });

  it("labels research checkpoint decisions in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          kind: "research_checkpoint",
          trigger: "external_calibration_requested",
          decision: "trigger",
          visible_in_ui: true,
        },
      },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Loop", value: "1 research checkpoint trigger", tone: undefined },
    ]));
  });

  it("surfaces context compactions in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "continue long run" } },
      {
        id: 3,
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
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Compaction", value: "1 · reactive · -72 msgs · 4 KiB summary", tone: "warning" },
    ]));
  });

  it("surfaces weak context compaction summaries in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "context.compacted",
        data: {
          turn_id: "t1",
          before_messages: 90,
          after_messages: 18,
          removed_messages: 72,
          reactive: true,
          reason: "context_overflow",
          summary_present: false,
          summary_bytes: 0,
          summary_preview: "",
        },
      },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Compaction", value: "1 · reactive · -72 msgs · summary missing", tone: "error" },
    ]));
  });

  it("surfaces confirmed memory updates in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "remember market policy" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "memory",
          args: {
            action: "add",
            target: "memory",
            topic: "markets",
            content: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
          },
          args_truncated: false,
          args_bytes: 64,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          result_summary: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\"}",
          result: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\"}",
          result_truncated: false,
          result_bytes: 48,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, memory_updates: 1, memory_update_add: 1 } } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Memory", value: "1 update · memory:markets: Alpha Coast market reports use marker MEM-STOCK...", tone: "success" },
    ]));
  });

  it("surfaces source evidence quality in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "verify dynamic dashboard facts" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "completed",
          tool_stats: {
            source_access_results: 4,
            source_access_verified: 1,
            source_access_network: 1,
            source_access_dynamic_partial: 1,
            source_access_discovery_only: 1,
          },
        },
      },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Evidence", value: "1/4 verified · 1 network · 1 partial · 1 discovery", tone: "warning" },
    ]));
  });

  it("surfaces loop guard interventions in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "recover repeated tool calls" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "max_turns",
          tool_stats: {
            loop_guard_interventions: 2,
            forced_no_tools: 1,
          },
        },
      },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Loop", value: "1 max-turn · 2 guards · 1 no-tools", tone: "warning" },
    ]));
  });

  it("surfaces tool result context trimming in the session overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "inspect large logs" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "completed",
          tool_stats: {
            tool_context_truncated: 1,
            tool_context_omitted_bytes: 2048,
          },
        },
      },
      { id: 4, type: "turn.start", data: { turn_id: "t2" } },
      {
        id: 5,
        type: "turn.end",
        data: {
          turn_id: "t2",
          reason: "completed",
          tool_stats: {
            tool_context_truncated: 2,
            tool_context_omitted_bytes: 512,
          },
        },
      },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Tool context", value: "3 trims · 3 KiB omitted", tone: "warning" },
    ]));
  });

  it("surfaces the persisted plan step summary in the session overview", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      planSummary: {
        label: "plan:1/3:active",
        total_steps: 3,
        completed_steps: 1,
        active: true,
        blocked: false,
        done: false,
        current_step: "verify browser evidence",
        current_step_index: 2,
        current_step_status: "in_progress",
        last_completed_step: "inspect plan state",
        last_completed_step_index: 1,
        error: false,
      },
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Plan", value: "1/3 · step 2 active", tone: undefined },
    ]));
  });

  it("marks blocked persisted plan steps as warning context", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      planSummary: {
        label: "plan:1/2:blocked",
        total_steps: 2,
        completed_steps: 1,
        active: false,
        blocked: true,
        done: false,
        current_step: "wait for approval",
        current_step_index: 2,
        current_step_status: "blocked",
        last_completed_step: "prepare patch",
        last_completed_step_index: 1,
        blocked_step: "wait for approval",
        blocked_step_index: 2,
        error: false,
      },
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Plan", value: "1/2 · step 2 blocked", tone: "warning" },
    ]));
  });

  it("uses the generated session title for loaded chats when the API provides one", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      sessionTitle: "Repository file listing",
    });

    expect(overview.headline).toBe("Repository file listing");
    expect(overview.detail).toBe("README.md main.go");
  });

  it("keeps a newly submitted task ahead of an older generated session title", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      pendingTask: "explain main.go",
      sessionTitle: "Repository file listing",
    });

    expect(overview.headline).toBe("main.go");
    expect(overview.stateLabel).toBe("Sending");
  });

  it("adds evidence and usage metrics for completed delegated work", () => {
    const session = reduceRawEvents(completedSubagentTree);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Work", value: "2 actions · 4 sources", tone: undefined },
    ]));
    expect(overview.detail).toBe("WebUI must render trace details as expandable agent structure.");
  });

  it("keeps replayed turns task-first even without a selected live session", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
    });

    expect(overview.headline).toBe("list the files");
    expect(overview.stateLabel).toBe("Result ready");
  });

  it("shows prior work as task metrics after a no-tool finalization turn", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "usage", data: { turn_id: "t1", input_tokens: 400, output_tokens: 80 } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 7, type: "turn.start", data: { turn_id: "t2" } },
      { id: 8, type: "user.message", data: { turn_id: "t2", text: "continue and summarize" } },
      { id: 9, type: "message.done", data: { turn_id: "t2", text: "Here is the report." } },
      { id: 10, type: "usage", data: { turn_id: "t2", input_tokens: 1000, output_tokens: 200 } },
      { id: 11, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.headline).toBe("Affine");
    expect(overview.stateLabel).toBe("Result ready");
    expect(overview.tone).toBe("success");
    expect(overview.metrics).toEqual([
      { label: "Loop", value: "1 max-turn", tone: "warning" },
      { label: "Tool issue", value: "1", tone: "warning" },
      { label: "Earlier work", value: "1 action" },
      { label: "Turn tokens", value: "1.2k" },
      { label: "Chat tokens", value: "1.7k" },
    ]);
    expect(overview.metrics).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Work" }),
    ]));
  });

  it("surfaces the latest failed tool recovery hint in the overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "read the missing config" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "read_file",
          args: { path: "config/missing.yaml" },
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          result_summary: "file not found\nNext: run rg --files config before retrying\nFailure: kind=not_found",
          result: "file not found\nNext: run rg --files config before retrying\nFailure: kind=not_found",
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, tool_errors: 1 } } },
    ]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toContainEqual({ label: "Issue", value: "1", tone: "error" });
    expect(overview.metrics).toContainEqual({ label: "Recovery", value: "run rg --files config before retrying", tone: "warning" });
  });

  it("uses durable recovery hints when selected history has not loaded a failing turn", () => {
    const session = reduceRawEvents([]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      recoveryHint: "check the browser network panel before retrying the taostats value",
    });

    expect(overview.metrics).toContainEqual({
      label: "Recovery",
      value: "check the browser network panel before retrying the taostats value",
      tone: "warning",
    });
  });

  it("prefers live recovery hints over durable summary recovery hints", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "read the missing config" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "read_file",
          args: { path: "config/missing.yaml" },
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          result_summary: "file not found\nNext: run rg --files config before retrying\nFailure: kind=not_found",
          result: "file not found\nNext: run rg --files config before retrying\nFailure: kind=not_found",
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, tool_errors: 1 } } },
    ]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      recoveryHint: "stale durable recovery hint",
    });

    expect(overview.metrics.filter((metric) => metric.label === "Recovery")).toEqual([
      { label: "Recovery", value: "run rg --files config before retrying", tone: "warning" },
    ]);
  });

  it("carries source counts into the header when a final report uses earlier tool evidence", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "真实收集 Affine 的相关信息" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://www.affine.io/" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 42,
          result_summary: "AFFINE subnet 120",
          result: "AFFINE subnet 120",
          result_truncated: false,
          result_bytes: 20,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      {
        id: 5,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c2",
          tool: "web_fetch",
          args: { url: "https://missing.invalid/" },
          args_truncated: false,
          args_bytes: 34,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 6,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c2",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 7, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 8, type: "turn.start", data: { turn_id: "t2" } },
      { id: 9, type: "user.message", data: { turn_id: "t2", text: "不要再调用任何工具。直接基于本 session 前面结果输出最终报告。" } },
      {
        id: 10,
        type: "message.done",
        data: { turn_id: "t2", text: "# Affine（Bittensor Subnet 120）调研报告\n\n基于已查阅来源整理。" },
      },
      { id: 11, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Tool issue", value: "1", tone: "warning" },
      { label: "Earlier work", value: "2 actions · 1 source" },
    ]));
    expect(overview.metrics).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Work" }),
    ]));
  });

  it("surfaces unknown events as an unclassified metric", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "future.event", data: { turn_id: "t1", payload: "kept" } },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toContainEqual({ label: "Unclassified", value: "1", tone: "warning" });
  });

  it("uses explicit chat token wording when only aggregate usage is available", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "usage", data: { input_tokens: 1800, output_tokens: 200 } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual([{ label: "Chat tokens", value: "2.0k" }]);
  });

  it("shows current context usage against the compaction trigger", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "inspect docs" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "shell", args: { command: "ls" } } },
      { id: 4, type: "tool.result", data: { turn_id: "t1", call_id: "c1", exit_code: 0, result_summary: "a", result: "a", result_truncated: false, result_bytes: 1, result_omitted_bytes: 0, result_cap_bytes: 262144 } },
      { id: 5, type: "message.done", data: { turn_id: "t1", text: "done" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      contextSummary: { message_count: 1, compact_trigger: 20, compact_percent: 5, messages_until_compact: 19 },
    });

    expect(overview.metrics[0]).toEqual({ label: "Context", value: "4/20 · 20%" });
  });

  it("keeps the original research topic after a finalization prompt", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "真实收集 Affine 的相关信息" } },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 4, type: "turn.start", data: { turn_id: "t2" } },
      { id: 5, type: "user.message", data: { turn_id: "t2", text: "不要再调用任何工具。直接基于本 session 前两轮结果输出最终报告。" } },
      {
        id: 6,
        type: "message.done",
        data: { turn_id: "t2", text: "# Affine（Bittensor Subnet 120）调研报告\n\nAffine 是 SN120。" },
      },
      { id: 7, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.headline).toBe("Affine");
    expect(overview.detail).toBe("Affine（Bittensor Subnet 120）调研报告: Affine 是 SN120。");
  });

  it("labels completed tool failures as tool issues in the header metrics", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "message.done", data: { turn_id: "t1", text: "I still found enough to answer." } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Tool issue", value: "1", tone: "warning" },
      { label: "Work", value: "1 action", tone: "warning" },
    ]));
    expect(overview.detail).toBe("I still found enough to answer.");
    expect(overview.metrics).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Issue" }),
    ]));
  });

  it("marks a completed chat with tool failures as a warning in the overview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "message.done", data: { turn_id: "t1", text: "I still found enough to answer." } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.tone).toBe("warning");
    expect(overview.stateLabel).toBe("Result ready");
    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Tool issue", value: "1", tone: "warning" },
      { label: "Work", value: "1 action", tone: "warning" },
    ]));
  });

  it("uses plain text previews for markdown answers in the header", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize affine" } },
      {
        id: 3,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: "## Affine（Bittensor 子网）介绍\n\n**Reason Mining** uses [`TAOstats`](https://taostats.io/).\n\n---\n\n1. Registered as subnet 120.",
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.detail).toBe("Affine（Bittensor 子网）介绍: Reason Mining uses TAOstats.");
    expect(overview.detail).not.toContain("##");
    expect(overview.detail).not.toContain("**");
    expect(overview.detail).not.toContain("---");
  });

  it("keeps markdown tables out of the header preview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize affine" } },
      {
        id: 3,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: [
            "现在我收集到了充分的数据。让我整理最终报告。",
            "",
            "# Affine（Bittensor Subnet 120）公开信息调查报告",
            "",
            "## 重要前提说明：两个 Affine",
            "",
            "经过查阅，存在两个同名项目需要区分：",
            "",
            "| 项目 | 域名 | 性质 |",
            "|------|------|------|",
            "| AFFiNE | affine.pro | 开源知识管理平台 |",
            "| Affine Subnet | affine.io | Bittensor 子网 #120 |",
            "",
            "本报告仅针对后者。",
          ].join("\n"),
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.detail).toBe("Affine（Bittensor Subnet 120）公开信息调查报告: 经过查阅，存在两个同名项目需要区分：");
    expect(overview.detail).not.toContain("|");
    expect(overview.detail).not.toContain("现在我收集到了充分的数据");
  });

  it("skips generic answer preambles in the header preview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "introduce affine" } },
      {
        id: 3,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: "我现在有了足够的信息来给你一个全面、诚实的回答。以下是基于我实际查阅的公开来源的整理：\n\n## Affine（Bittensor 子网）介绍\n\nAffine 是 Reason Mining 子网。",
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.detail).toBe("Affine（Bittensor 子网）介绍: Affine 是 Reason Mining 子网。");
    expect(overview.detail).not.toContain("我现在有了足够的信息");
    expect(overview.detail).not.toContain("以下是基于");
  });
});
