// Faithful TypeScript mirror of internal/sse/types.go and event.go.
//
// This is the frontend's single source of truth for the Affent event
// contract. It MUST stay in parity with the Go structs — every field
// name here matches a Go `json:"..."` tag. A parity guard (added with
// the generator/test in a later step) fails CI if the two drift.
//
// Live SSE and persisted trace/history replay carry the SAME events, so
// one set of types serves both modes.

/** Canonical event type strings (internal/sse/types.go). */
export const EventType = {
  TraceMeta: "trace.meta",
  TurnStart: "turn.start",
  UserMessage: "user.message",
  RuntimeSurface: "runtime.surface",
  MessageDelta: "message.delta",
  MessageDone: "message.done",
  ThinkingDelta: "thinking.delta",
  ThinkingDone: "thinking.done",
  ToolRequest: "tool.request",
  ToolResult: "tool.result",
  Usage: "usage",
  TurnEnd: "turn.end",
  LoopProtocolFeed: "loop.protocol_feed",
  LoopProtocolCalibration: "loop.protocol_calibration",
  LoopDecision: "loop.decision",
  ContextCompacted: "context.compacted",
  Error: "error",
} as const;

export type EventTypeName = (typeof EventType)[keyof typeof EventType];

/** turn.end reason values (internal/sse/types.go). */
export const TurnEndReason = {
  Completed: "completed",
  Cancelled: "cancelled",
  Error: "error",
  MaxTurns: "max_turns",
} as const;

export type TurnEndReasonName = (typeof TurnEndReason)[keyof typeof TurnEndReason];

export interface TraceMetaPayload {
  schema_version: number;
}

export interface TurnStartPayload {
  turn_id: string;
}

export interface UserMessagePayload {
  turn_id: string;
  text: string;
  display_text?: string;
  source?: string;
  schedule_id?: string;
  schedule_kind?: string;
}

export interface RuntimeSurfacePayload {
  turn_id: string;
  tool_count: number;
  tools?: RuntimeSurfaceTool[];
  capabilities: RuntimeCapabilities;
  max_turn_steps?: number;
  max_tool_calls?: number;
  tool_result_event_cap_bytes?: number;
  tool_result_context_max_bytes?: number;
  tool_result_context_budget_bytes?: number;
  tool_result_artifact_prefix?: string;
  turn_tool_override?: boolean;
}

export interface RuntimeSurfaceTool {
  name: string;
  raw_name?: string;
  group?: string;
  source?: string;
}

export interface RuntimeCapabilities {
  builtins?: boolean;
  workspace_tools?: string[];
  memory?: boolean;
  plan?: boolean;
  session_search?: boolean;
  web_fetch?: boolean;
  web_search?: boolean;
  browser?: boolean;
  subagent?: boolean;
  focused_tasks?: boolean;
  skill?: boolean;
  mcp?: boolean;
}

export interface DelegationMeta {
  kind: string;
  task_type?: string;
  mode?: string;
}

export interface MemoryUpdateMeta {
  action: "add" | "replace" | "remove";
  target: string;
  topic?: string;
  location: string;
  preview: string;
  previous_preview?: string;
  next_preview?: string;
}

export interface MessageDeltaPayload {
  turn_id: string;
  delta: string;
}

export interface MessageDonePayload {
  turn_id: string;
  text: string;
  /** "stop" | "length" | "tool_calls" | "content_filter" | provider ext. */
  finish_reason?: string;
}

export interface ThinkingDeltaPayload {
  turn_id: string;
  delta: string;
}

export interface ThinkingDonePayload {
  turn_id: string;
  text: string;
}

export interface ToolRequestPayload {
  turn_id: string;
  call_id: string;
  tool: string;
  args: Record<string, unknown>;
  args_truncated: boolean;
  args_bytes: number;
  args_omitted_bytes: number;
  args_cap_bytes: number;
  original_tool?: string;
  original_args_summary?: string;
  canonicalized?: boolean;
  args_repaired?: boolean;
  repair_notes?: string[];
  delegation?: DelegationMeta;
}

export interface ToolResultPayload {
  turn_id?: string;
  call_id: string;
  exit_code: number;
  failure_kind?: string;
  failure_kinds?: string[];
  duration_ms?: number;
  /** Short UI-friendly preview; may be ellipsis-truncated, NOT JSON-safe. */
  result_summary: string;
  /** Output capped by the event budget; tolerate a trailing truncation marker. */
  result: string;
  result_truncated: boolean;
  result_bytes: number;
  result_omitted_bytes: number;
  result_cap_bytes: number;
  context_bytes?: number;
  context_omitted_bytes?: number;
  context_estimated_tokens?: number;
  /** Workspace-relative path to the complete output when truncated. */
  result_artifact_path?: string;
  delegation?: DelegationMeta;
  memory_update?: MemoryUpdateMeta;
}

export interface UsagePayload {
  turn_id: string;
  input_tokens: number;
  output_tokens: number;
}

export interface ToolRuntimeStats {
  tool_requests?: number;
  tool_name_canonicalized?: number;
  tool_args_repaired?: number;
  tool_repair_calls?: number;
  tool_repair_succeeded?: number;
  tool_repair_failed?: number;
  tool_repair_notes?: number;
  tool_repair_by_kind?: Record<string, number>;
  tool_failure_by_kind?: Record<string, number>;
  tool_errors?: number;
  tool_duration_ms?: number;
  loop_guard_interventions?: number;
  forced_no_tools?: number;
  source_access_results?: number;
  source_access_verified?: number;
  source_access_discovery_only?: number;
  source_access_network?: number;
  source_access_dynamic_partial?: number;
  session_search_calls?: number;
  session_search_results?: number;
  session_search_context_hits?: number;
  session_search_matched_terms?: number;
  memory_updates?: number;
  memory_update_add?: number;
  memory_update_replace?: number;
  memory_update_remove?: number;
  tool_context_truncated?: number;
  tool_context_omitted_bytes?: number;
}

export interface TurnEndPayload {
  turn_id: string;
  reason: string;
  tool_stats?: ToolRuntimeStats;
}

export interface LoopProtocolFeedPayload {
  turn_id?: string;
  loop_id?: string;
  status?: string;
  mode: string;
  feed_number: number;
  protocol_feeds?: number;
  calibration_answers?: number;
  last_calibration_answer_preview?: string;
  protocol_path?: string;
  plan_label?: string;
  plan_current_step_index?: number;
  plan_current_step_status?: string;
  plan_current_step?: string;
}

export interface LoopProtocolCalibrationPayload {
  loop_id?: string;
  status?: string;
  calibration_answers?: number;
  last_calibration_answer_preview?: string;
  protocol_path?: string;
  event_seq?: number;
}

export interface LoopDecisionPayload {
  turn_id?: string;
  loop_id?: string;
  decision_id?: string;
  kind: string;
  trigger?: string;
  decision: string;
  confidence?: string;
  reason?: string;
  required_action?: string;
  token_budget?: number;
  visible_in_ui?: boolean;
}

export interface ContextCompactedPayload {
  turn_id?: string;
  before_messages: number;
  after_messages: number;
  removed_messages: number;
  reactive: boolean;
  reason: string;
  summary_present?: boolean;
  summary_bytes?: number;
  summary_preview?: string;
}

export interface ErrorPayload {
  turn_id: string;
  code: string;
  message: string;
  failure_kind?: string;
  recoverable: boolean;
}

/** Maps each event type to its payload shape. */
export interface PayloadByType {
  [EventType.TraceMeta]: TraceMetaPayload;
  [EventType.TurnStart]: TurnStartPayload;
  [EventType.UserMessage]: UserMessagePayload;
  [EventType.RuntimeSurface]: RuntimeSurfacePayload;
  [EventType.MessageDelta]: MessageDeltaPayload;
  [EventType.MessageDone]: MessageDonePayload;
  [EventType.ThinkingDelta]: ThinkingDeltaPayload;
  [EventType.ThinkingDone]: ThinkingDonePayload;
  [EventType.ToolRequest]: ToolRequestPayload;
  [EventType.ToolResult]: ToolResultPayload;
  [EventType.Usage]: UsagePayload;
  [EventType.TurnEnd]: TurnEndPayload;
  [EventType.LoopProtocolFeed]: LoopProtocolFeedPayload;
  [EventType.LoopProtocolCalibration]: LoopProtocolCalibrationPayload;
  [EventType.LoopDecision]: LoopDecisionPayload;
  [EventType.ContextCompacted]: ContextCompactedPayload;
  [EventType.Error]: ErrorPayload;
}

/**
 * Wire shape of one event (internal/sse/event.go). `id` is a per-session
 * monotonic sequence; over a server restart ids can repeat, which is why
 * the history API paginates by line number, not id.
 */
export interface RawEvent {
  id: number;
  type: string;
  data: unknown;
}

/** Response shape of GET /v1/sessions/{id}/history. */
export interface SessionHistoryResponse {
  session_id: string;
  events: RawEvent[];
  next_after: number;
  has_more: boolean;
  trace_schema_version?: number;
  trace_schema_detected: boolean;
}

/** The trace schema version this build was written against. */
export const TRACE_SCHEMA_VERSION = 1;
