import type { RawEvent } from "../api/events";

// A small, schema-faithful completed turn: the user asks, the model
// thinks, streams an answer, runs one tool, and the turn ends cleanly.
// Field names match internal/sse exactly. This is a hand-authored
// fixture for the data-layer unit tests; it will be cross-checked
// against a real captured events.jsonl once a live capture is wired in.
export const completedTurn: RawEvent[] = [
  { id: 0, type: "trace.meta", data: { schema_version: 1 } },
  { id: 1, type: "turn.start", data: { turn_id: "t1" } },
  { id: 2, type: "user.message", data: { turn_id: "t1", text: "list the files" } },
  { id: 3, type: "thinking.delta", data: { turn_id: "t1", delta: "I should " } },
  { id: 4, type: "thinking.done", data: { turn_id: "t1", text: "I should list files." } },
  {
    id: 5,
    type: "tool.request",
    data: {
      turn_id: "t1",
      call_id: "c1",
      tool: "list_files",
      args: { path: "." },
      args_truncated: false,
      args_bytes: 14,
      args_omitted_bytes: 0,
      args_cap_bytes: 8192,
    },
  },
  {
    id: 6,
    type: "tool.result",
    data: {
      call_id: "c1",
      exit_code: 0,
      duration_ms: 12,
      result_summary: "README.md\nmain.go",
      result: "README.md\nmain.go",
      result_truncated: false,
      result_bytes: 17,
      result_omitted_bytes: 0,
      result_cap_bytes: 8192,
    },
  },
  { id: 7, type: "message.delta", data: { turn_id: "t1", delta: "There are " } },
  { id: 8, type: "message.delta", data: { turn_id: "t1", delta: "two files." } },
  {
    id: 9,
    type: "message.done",
    data: { turn_id: "t1", text: "There are two files.", finish_reason: "stop" },
  },
  { id: 10, type: "usage", data: { turn_id: "t1", input_tokens: 120, output_tokens: 18 } },
  {
    id: 11,
    type: "turn.end",
    data: {
      turn_id: "t1",
      reason: "completed",
      tool_stats: { tool_requests: 1, tool_duration_ms: 12 },
    },
  },
];
