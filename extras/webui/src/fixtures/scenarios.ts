import type { RawEvent } from "../api/events";

// Schema-faithful fixtures for the core UI states webui-architecture.md
// requires coverage for. Hand-authored; to be cross-checked against real
// captured events.jsonl. Small on purpose — each isolates one behavior.

/** A tool that fails: exit_code != 0, error text in the result. */
export const toolError: RawEvent[] = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "build it" } },
  {
    id: 3,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "c1",
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
      call_id: "c1",
      exit_code: 2,
      duration_ms: 340,
      result_summary: "make: *** No rule to make target. Stop.",
      result: "make: *** No rule to make target. Stop.\nNext: check the Makefile path",
      result_truncated: false,
      result_bytes: 70,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
    },
  },
  { id: 5, type: "message.done", data: { turn_id: "t1", text: "The build failed.", finish_reason: "stop" } },
  { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, tool_errors: 1, tool_duration_ms: 340 } } },
];

/** The model's tool name/args were repaired before dispatch. */
export const argsRepaired: RawEvent[] = [
  { id: 0, type: "turn.start", data: { turn_id: "t1" } },
  {
    id: 1,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "c1",
      tool: "read_file",
      args: { path: "main.go" },
      args_truncated: false,
      args_bytes: 18,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
      original_tool: "readFile",
      original_args_summary: "{\"filename\":\"main.go\"}",
      canonicalized: true,
      args_repaired: true,
      repair_notes: ["renamed readFile -> read_file", "coerced filename -> path"],
    },
  },
  {
    id: 2,
    type: "tool.result",
    data: {
      call_id: "c1",
      exit_code: 0,
      duration_ms: 5,
      result_summary: "package main",
      result: "package main",
      result_truncated: false,
      result_bytes: 12,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
    },
  },
  { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 1, tool_name_canonicalized: 1, tool_args_repaired: 1 } } },
];

/** A large tool result that got capped, with a full-output artifact. */
export const resultTruncated: RawEvent[] = [
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
      context_bytes: 4096,
      context_omitted_bytes: 4096,
      context_estimated_tokens: 1024,
      result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
    },
  },
  { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
];

/** Turn cancelled mid-stream. */
export const cancelledTurn: RawEvent[] = [
  { id: 0, type: "turn.start", data: { turn_id: "t1" } },
  { id: 1, type: "user.message", data: { turn_id: "t1", text: "do a long thing" } },
  { id: 2, type: "message.delta", data: { turn_id: "t1", delta: "Starting" } },
  { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "cancelled" } },
];

/** Loop hit the step budget while still issuing tool calls. */
export const maxTurns: RawEvent[] = [
  { id: 0, type: "turn.start", data: { turn_id: "t1" } },
  {
    id: 1,
    type: "tool.request",
    data: { turn_id: "t1", call_id: "c1", tool: "shell", args: { command: "ls" }, args_truncated: false, args_bytes: 16, args_omitted_bytes: 0, args_cap_bytes: 8192 },
  },
  { id: 2, type: "tool.result", data: { call_id: "c1", exit_code: 0, duration_ms: 3, result_summary: "a", result: "a", result_truncated: false, result_bytes: 1, result_omitted_bytes: 0, result_cap_bytes: 8192 } },
  { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "max_turns", tool_stats: { tool_requests: 1, forced_no_tools: 1 } } },
];

/** A recoverable upstream error surfaced mid-turn. */
export const turnError: RawEvent[] = [
  { id: 0, type: "turn.start", data: { turn_id: "t1" } },
  { id: 1, type: "user.message", data: { turn_id: "t1", text: "hi" } },
  { id: 2, type: "error", data: { turn_id: "t1", code: "upstream_5xx", message: "provider returned 503", recoverable: true } },
  { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "error" } },
];

export const runningSubagent: RawEvent[] = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "use a subagent to inspect docs" } },
  {
    id: 3,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "c1",
      tool: "subagent_run",
      args: { mode: "explore", task: "Inspect docs for WebUI trace requirements", max_turns: 4 },
      args_truncated: false,
      args_bytes: 82,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
];

export const completedSubagentTree: RawEvent[] = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "delegate docs inspection" } },
  {
    id: 3,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "c1",
      tool: "subagent_run",
      args: { mode: "explore", task: "Find the WebUI trace requirements", max_turns: 4 },
      args_truncated: false,
      args_bytes: 76,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
  {
    id: 4,
    type: "tool.result",
    data: {
      call_id: "c1",
      exit_code: 0,
      duration_ms: 1480,
      result_summary: "Subagent found WebUI trace requirements and inspected three tools.",
      result: JSON.stringify({
        report: "Conclusion:\nWebUI must render trace details as expandable agent structure.\nEvidence:\n- docs/webui-product-design.md requires tool args, result, repair and truncation metadata.\n- docs/focused-tasks.md requires focused task timelines and child tool calls.",
        ok: true,
        turn_end_reason: "completed",
        mode: "explore",
        child_session_id: "subagent_01",
        depth: 1,
        max_depth: 2,
        usage: { input_tokens: 310, output_tokens: 82 },
        tool_calls: [
          { tool: "list_files", args: { path: "docs" }, exit_code: 0 },
          { tool: "read_file", args: { path: "docs/webui-product-design.md", max_bytes: 4096 }, exit_code: 0 },
          { tool: "MCP_search", args: { query: "webui trace" }, exit_code: 0 },
          { tool: "subagent_run", args: { mode: "explore", task: "Check focused task docs" }, exit_code: 0 },
        ],
      }),
      result_truncated: false,
      result_bytes: 742,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
      context_bytes: 742,
      context_omitted_bytes: 0,
      context_estimated_tokens: 186,
    },
  },
  {
    id: 5,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "c2",
      tool: "run_task",
      args: { task_type: "verify", objective: "Verify trace tree requirements" },
      args_truncated: false,
      args_bytes: 68,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
  {
    id: 6,
    type: "tool.result",
    data: {
      call_id: "c2",
      exit_code: 0,
      duration_ms: 920,
      result_summary: "Focused task verified the relevant docs.",
      result: JSON.stringify({
        task_type: "verify",
        ok: true,
        summary: "Trace UI needs hierarchical detail for focused tasks and subagents.",
        findings: [
          {
            claim: "Focused task WebUI should show child tool calls.",
            evidence: "docs/focused-tasks.md lists task type, child tool calls, final findings and warnings.",
            source: "docs/focused-tasks.md",
            confidence: "high",
          },
        ],
        not_found: [],
        warnings: ["No dedicated focused_task.* event exists yet; use structured tool result until the event contract grows."],
        suggested_next: ["Replace result parsing with explicit child trace events when backend exposes them."],
        objective: "Verify trace tree requirements",
        child_session_id: "focused_verify_01",
        turn_end_reason: "completed",
        depth: 1,
        usage: { input_tokens: 220, output_tokens: 58 },
        tool_calls: [{ tool: "read_file", args: { path: "docs/focused-tasks.md" }, exit_code: 0 }],
      }),
      result_truncated: false,
      result_bytes: 780,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
      context_bytes: 780,
      context_omitted_bytes: 0,
      context_estimated_tokens: 195,
    },
  },
  { id: 7, type: "message.done", data: { turn_id: "t1", text: "The delegated checks found the WebUI trace requirements.", finish_reason: "stop" } },
  { id: 8, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: { tool_requests: 2, tool_duration_ms: 2400 } } },
];
