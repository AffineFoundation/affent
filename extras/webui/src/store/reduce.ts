import {
  EventType,
  TurnEndReason,
  type ContextCompactedPayload,
  type ErrorPayload,
  type LoopDecisionPayload,
  type MessageDeltaPayload,
  type MessageDonePayload,
  type RawEvent,
  type RuntimeSurfacePayload,
  type ThinkingDeltaPayload,
  type ThinkingDonePayload,
  type ToolRequestPayload,
  type ToolResultPayload,
  type TraceMetaPayload,
  type TurnEndPayload,
  type TurnStartPayload,
  type UsagePayload,
  type UserMessagePayload,
} from "../api/events";
import { normalizeEvent, normalizeEvents, type NormalizedEvent } from "../normalize/normalizeEvent";
import {
  initialSessionState,
  type SessionState,
  type SessionStatus,
  type ToolCallState,
  type TurnState,
  type TurnStatus,
} from "./sessionState";

// applyEvent folds one normalized event into session state, returning a
// NEW state (the changed turn and the turns array are copied; untouched
// turns keep their references for cheap React diffing). It is pure and
// idempotent on turn.start, so replaying a history page that overlaps a
// live tail does not duplicate turns. Unknown turn/call ids are ignored
// rather than throwing — a late tool.result for an evicted turn must not
// crash the timeline.

function turnStatusFromReason(reason: string): TurnStatus {
  switch (reason) {
    case TurnEndReason.Completed:
      return "completed";
    case TurnEndReason.Cancelled:
      return "cancelled";
    case TurnEndReason.MaxTurns:
      return "max_turns";
    case TurnEndReason.Error:
      return "error";
    default:
      // Unknown reason from a newer server: the turn has clearly ended,
      // so don't leave it spinning as "running".
      return "completed";
  }
}

function deriveSessionStatus(turns: TurnState[]): SessionStatus {
  if (turns.length === 0) return "idle";
  return turns[turns.length - 1].status;
}

function updateTurn(
  state: SessionState,
  turnId: string,
  fn: (t: TurnState) => TurnState,
): SessionState {
  let changed = false;
  const turns = state.turns.map((t) => {
    if (t.id !== turnId) return t;
    changed = true;
    return fn(t);
  });
  if (!changed) return state;
  return { ...state, turns };
}

function updateToolCall(
  state: SessionState,
  callId: string,
  fn: (c: ToolCallState) => ToolCallState,
): SessionState {
  let changed = false;
  const turns = state.turns.map((t) => {
    const idx = t.toolCalls.findIndex((c) => c.callId === callId);
    if (idx === -1) return t;
    changed = true;
    const toolCalls = t.toolCalls.map((c, i) => (i === idx ? fn(c) : c));
    return { ...t, toolCalls };
  });
  if (!changed) return state;
  return { ...state, turns };
}

export function applyEvent(state: SessionState, ev: NormalizedEvent): SessionState {
  const withEvent = { ...state, events: [...state.events, ev] };
  return applyEventPayload(withEvent, ev);
}

function applyEventPayload(state: SessionState, ev: NormalizedEvent): SessionState {
  switch (ev.type) {
    case EventType.TraceMeta: {
      const p = ev.data as TraceMetaPayload;
      return { ...state, schemaVersion: p.schema_version };
    }
    case EventType.TurnStart: {
      const p = ev.data as TurnStartPayload;
      if (state.turns.some((t) => t.id === p.turn_id)) return state;
      const turn: TurnState = {
        id: p.turn_id,
        status: "running",
        thinkingText: "",
        thinkingStreaming: false,
        assistantText: "",
        messageStreaming: false,
        toolCalls: [],
        loopDecisions: [],
        contextCompactions: [],
      };
      return { ...state, turns: [...state.turns, turn], status: "running" };
    }
    case EventType.UserMessage: {
      const p = ev.data as UserMessagePayload;
      return updateTurn(state, p.turn_id, (t) => ({ ...t, userText: p.text }));
    }
    case EventType.RuntimeSurface: {
      const p = ev.data as RuntimeSurfacePayload;
      return updateTurn(state, p.turn_id, (t) => ({ ...t, runtimeSurface: p }));
    }
    case EventType.ThinkingDelta: {
      const p = ev.data as ThinkingDeltaPayload;
      return updateTurn(state, p.turn_id, (t) => ({
        ...t,
        thinkingText: t.thinkingText + p.delta,
        thinkingStreaming: true,
      }));
    }
    case EventType.ThinkingDone: {
      const p = ev.data as ThinkingDonePayload;
      return updateTurn(state, p.turn_id, (t) => ({
        ...t,
        thinkingText: p.text,
        thinkingStreaming: false,
      }));
    }
    case EventType.MessageDelta: {
      const p = ev.data as MessageDeltaPayload;
      return updateTurn(state, p.turn_id, (t) => ({
        ...t,
        assistantText: t.assistantText + p.delta,
        messageStreaming: true,
      }));
    }
    case EventType.MessageDone: {
      const p = ev.data as MessageDonePayload;
      return updateTurn(state, p.turn_id, (t) => ({
        ...t,
        assistantText: p.text,
        messageStreaming: false,
        finishReason: p.finish_reason,
      }));
    }
    case EventType.ToolRequest: {
      const p = ev.data as ToolRequestPayload;
      const originalTool = p.original_tool && p.original_tool !== p.tool ? p.original_tool : undefined;
      const repaired = !!(p.args_repaired || p.canonicalized || p.repair_notes?.length || originalTool);
      const call: ToolCallState = {
        callId: p.call_id,
        tool: p.tool,
        originalTool,
        originalArgsSummary: repaired ? p.original_args_summary : undefined,
        args: p.args ?? {},
        argsTruncated: !!p.args_truncated,
        argsBytes: p.args_bytes,
        argsOmittedBytes: p.args_omitted_bytes,
        argsCapBytes: p.args_cap_bytes,
        argsRepaired: !!p.args_repaired,
        canonicalized: !!p.canonicalized,
        repairNotes: p.repair_notes,
        status: "running",
        resultTruncated: false,
        delegation: p.delegation,
      };
      return updateTurn(state, p.turn_id, (t) => ({ ...t, toolCalls: [...t.toolCalls, call] }));
    }
    case EventType.ToolResult: {
      const p = ev.data as ToolResultPayload;
      return updateToolCall(state, p.call_id, (c) => ({
        ...c,
        status: p.exit_code === 0 ? "success" : "error",
        exitCode: p.exit_code,
        failureKind: p.failure_kind,
        failureKinds: p.failure_kinds,
        durationMs: p.duration_ms,
        resultSummary: p.result_summary,
        result: p.result,
        resultTruncated: !!p.result_truncated,
        resultBytes: p.result_bytes,
        resultOmittedBytes: p.result_omitted_bytes,
        resultCapBytes: p.result_cap_bytes,
        contextBytes: p.context_bytes,
        contextOmittedBytes: p.context_omitted_bytes,
        contextEstimatedTokens: p.context_estimated_tokens,
        resultArtifactPath: p.result_artifact_path,
        delegation: p.delegation ?? c.delegation,
      }));
    }
    case EventType.Usage: {
      const p = ev.data as UsagePayload;
      const next = updateTurn(state, p.turn_id, (t) => ({
        ...t,
        usage: { inputTokens: p.input_tokens, outputTokens: p.output_tokens },
      }));
      return {
        ...next,
        totalUsage: {
          inputTokens: next.totalUsage.inputTokens + p.input_tokens,
          outputTokens: next.totalUsage.outputTokens + p.output_tokens,
        },
      };
    }
    case EventType.TurnEnd: {
      const p = ev.data as TurnEndPayload;
      const next = updateTurn(state, p.turn_id, (t) => ({
        ...t,
        status: turnStatusFromReason(p.reason),
        endReason: p.reason,
        toolStats: p.tool_stats,
        thinkingStreaming: false,
        messageStreaming: false,
      }));
      return { ...next, status: deriveSessionStatus(next.turns) };
    }
    case EventType.LoopDecision: {
      const p = ev.data as LoopDecisionPayload;
      const next = p.turn_id
        ? updateTurn(state, p.turn_id, (t) => ({
            ...t,
            loopDecisions: [...(t.loopDecisions ?? []), { ...p, eventId: ev.id }],
          }))
        : state;
      return {
        ...next,
        loopDecisions: [...next.loopDecisions, { ...p, eventId: ev.id }],
      };
    }
    case EventType.ContextCompacted: {
      const p = ev.data as ContextCompactedPayload;
      const next = p.turn_id
        ? updateTurn(state, p.turn_id, (t) => ({
            ...t,
            contextCompactions: [...(t.contextCompactions ?? []), { ...p, eventId: ev.id }],
          }))
        : state;
      return {
        ...next,
        contextCompactions: [...next.contextCompactions, { ...p, eventId: ev.id }],
      };
    }
    case EventType.Error: {
      const p = ev.data as ErrorPayload;
      if (!p.turn_id) return state;
      const next = updateTurn(state, p.turn_id, (t) => ({
        ...t,
        status: p.recoverable ? t.status : "error",
        thinkingStreaming: p.recoverable ? t.thinkingStreaming : false,
        messageStreaming: p.recoverable ? t.messageStreaming : false,
        error: {
          code: p.code,
          message: p.message,
          ...(p.failure_kind ? { failureKind: p.failure_kind } : {}),
          recoverable: p.recoverable,
        },
      }));
      return p.recoverable ? next : { ...next, status: deriveSessionStatus(next.turns) };
    }
    default:
      return { ...state, unknownEventCount: state.unknownEventCount + 1 };
  }
}

/** Build session state from scratch (replay, imported trace, tests). */
export function reduceEvents(events: readonly NormalizedEvent[]): SessionState {
  return events.reduce(applyEvent, initialSessionState());
}

/** Apply one raw wire event to an existing live/replay state. */
export function applyRawEvent(state: SessionState, raw: RawEvent): SessionState {
  return applyEvent(state, normalizeEvent(raw));
}

/** Convenience: normalize + reduce raw wire events in one step. */
export function reduceRawEvents(raws: readonly RawEvent[]): SessionState {
  return reduceEvents(normalizeEvents(raws));
}
