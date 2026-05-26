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
  MessageDelta: "message.delta",
  MessageDone: "message.done",
  ThinkingDelta: "thinking.delta",
  ThinkingDone: "thinking.done",
  ToolRequest: "tool.request",
  ToolResult: "tool.result",
  Usage: "usage",
  TurnEnd: "turn.end",
  LoopDecision: "loop.decision",
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
}

export interface ToolResultPayload {
  call_id: string;
  exit_code: number;
  duration_ms?: number;
  /** Short UI-friendly preview; may be ellipsis-truncated, NOT JSON-safe. */
  result_summary: string;
  /** Output capped by the event budget; tolerate a trailing truncation marker. */
  result: string;
  result_truncated: boolean;
  result_bytes: number;
  result_omitted_bytes: number;
  result_cap_bytes: number;
  /** Workspace-relative path to the complete output when truncated. */
  result_artifact_path?: string;
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
  tool_errors?: number;
  tool_duration_ms?: number;
  loop_guard_interventions?: number;
  forced_no_tools?: number;
  source_access_results?: number;
  source_access_verified?: number;
  source_access_discovery_only?: number;
  source_access_network?: number;
  source_access_dynamic_partial?: number;
  memory_updates?: number;
  memory_update_add?: number;
  memory_update_replace?: number;
  memory_update_remove?: number;
}

export interface TurnEndPayload {
  turn_id: string;
  reason: string;
  tool_stats?: ToolRuntimeStats;
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

export interface ErrorPayload {
  turn_id: string;
  code: string;
  message: string;
  recoverable: boolean;
}

/** Maps each event type to its payload shape. */
export interface PayloadByType {
  [EventType.TraceMeta]: TraceMetaPayload;
  [EventType.TurnStart]: TurnStartPayload;
  [EventType.UserMessage]: UserMessagePayload;
  [EventType.MessageDelta]: MessageDeltaPayload;
  [EventType.MessageDone]: MessageDonePayload;
  [EventType.ThinkingDelta]: ThinkingDeltaPayload;
  [EventType.ThinkingDone]: ThinkingDonePayload;
  [EventType.ToolRequest]: ToolRequestPayload;
  [EventType.ToolResult]: ToolResultPayload;
  [EventType.Usage]: UsagePayload;
  [EventType.TurnEnd]: TurnEndPayload;
  [EventType.LoopDecision]: LoopDecisionPayload;
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
