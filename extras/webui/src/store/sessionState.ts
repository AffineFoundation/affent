import type { ContextCompactedPayload, DelegationMeta, LoopDecisionPayload, LoopProtocolFeedPayload, MemoryUpdateMeta, RuntimeSurfacePayload, ToolRuntimeStats } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";

// The structured view of a session that the reducer builds from the
// event stream. Live SSE and replay produce identical state from
// identical events — the bug-analysis order in webui-architecture.md
// starts here: "does the reducer produce the right TurnView / ToolCallView?"

export type ToolCallStatus = "running" | "success" | "error";
export type TurnStatus = "running" | "completed" | "cancelled" | "error" | "max_turns";
export type SessionStatus = "idle" | "running" | "completed" | "cancelled" | "error" | "max_turns";

export interface ToolCallState {
  callId: string;
  tool: string;
  /** Pre-canonicalization name the model emitted, when it was rewritten. */
  originalTool?: string;
  /** Bounded preview of the original model-emitted args before repair. */
  originalArgsSummary?: string;
  args: Record<string, unknown>;
  argsTruncated: boolean;
  argsBytes?: number;
  argsOmittedBytes?: number;
  argsCapBytes?: number;
  argsRepaired: boolean;
  canonicalized: boolean;
  repairNotes?: string[];
  status: ToolCallStatus;
  exitCode?: number;
  failureKind?: string;
  failureKinds?: string[];
  durationMs?: number;
  resultSummary?: string;
  result?: string;
  resultTruncated: boolean;
  resultBytes?: number;
  resultOmittedBytes?: number;
  resultCapBytes?: number;
  contextBytes?: number;
  contextOmittedBytes?: number;
  contextEstimatedTokens?: number;
  /** Workspace-relative path to the full output when the result was capped. */
  resultArtifactPath?: string;
  delegation?: DelegationMeta;
  memoryUpdate?: MemoryUpdateMeta;
}

export interface TurnError {
  code: string;
  message: string;
  failureKind?: string;
  recoverable: boolean;
}

export interface TurnUsage {
  inputTokens: number;
  outputTokens: number;
}

export interface LoopDecisionState extends LoopDecisionPayload {
  eventId: number;
}

export interface LoopProtocolFeedState extends LoopProtocolFeedPayload {
  eventId: number;
}

export interface ContextCompactionState extends ContextCompactedPayload {
  eventId: number;
}

export interface TurnState {
  id: string;
  status: TurnStatus;
  userText?: string;
  thinkingText: string;
  thinkingStreaming: boolean;
  assistantText: string;
  messageStreaming: boolean;
  runtimeSurface?: RuntimeSurfacePayload;
  finishReason?: string;
  toolCalls: ToolCallState[];
  loopProtocolFeeds?: LoopProtocolFeedState[];
  loopDecisions?: LoopDecisionState[];
  contextCompactions?: ContextCompactionState[];
  usage?: TurnUsage;
  toolStats?: ToolRuntimeStats;
  endReason?: string;
  error?: TurnError;
}

export interface SessionState {
  /** From trace.meta; lets the UI flag an incompatible trace. */
  schemaVersion?: number;
  status: SessionStatus;
  /** Append-only archive for inline trace drill-down and live/replay parity. */
  events: NormalizedEvent[];
  turns: TurnState[];
  totalUsage: TurnUsage;
  /** Structured loop/checkpoint decisions surfaced outside assistant text. */
  loopProtocolFeeds: LoopProtocolFeedState[];
  loopDecisions: LoopDecisionState[];
  /** Context compactions that rewrote model history while preserving trace replay. */
  contextCompactions: ContextCompactionState[];
  /** Events whose type this build doesn't know — kept visible, never fatal. */
  unknownEventCount: number;
}

export function initialSessionState(): SessionState {
  return {
    status: "idle",
    events: [],
    turns: [],
    totalUsage: { inputTokens: 0, outputTokens: 0 },
    loopProtocolFeeds: [],
    loopDecisions: [],
    contextCompactions: [],
    unknownEventCount: 0,
  };
}
