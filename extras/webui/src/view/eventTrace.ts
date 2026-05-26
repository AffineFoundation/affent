import { EventType } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { formatByteCount } from "./byteFormat";
import { artifactDisplayLabel, artifactName } from "./turnArtifacts";

export type EventTraceItem =
  | { kind: "event"; event: NormalizedEvent; display: EventDisplay }
  | { kind: "eventGroup"; key: string; label: string; meta: string[]; badges: string[]; events: NormalizedEvent[] }
  | {
    kind: "deltaGroup";
    key: string;
    type: string;
    label: string;
    turnId?: string;
    turnLabel?: string;
    events: NormalizedEvent[];
    updateCount: number;
    text: string;
  };

export interface EventDisplay {
  label: string;
  meta: string[];
  badges: string[];
}

export interface EventTraceModel {
  metadata: NormalizedEvent[];
  items: EventTraceItem[];
}

export function buildEventTraceModel(events: readonly NormalizedEvent[]): EventTraceModel {
  const items: EventTraceItem[] = [];
  const metadata = events.filter((event) => event.type === EventType.TraceMeta);
  const context = buildDisplayContext(events);
  const deltaGroups = collectDeltaGroups(events, context);
  const recordGroups = collectRequestRecordGroups(events, context);
  const renderedDeltaGroups = new Set<string>();
  const renderedRecordGroups = new Set<string>();

  for (const event of events) {
    if (event.type === EventType.TraceMeta) continue;
    if (isDoneForDeltaGroup(event) && deltaGroups.has(doneDeltaGroupKey(event))) continue;

    const recordKey = requestRecordKey(event);
    if (recordKey && recordGroups.has(recordKey)) {
      if (!renderedRecordGroups.has(recordKey)) {
        renderedRecordGroups.add(recordKey);
        items.push(recordGroups.get(recordKey)!);
      }
      continue;
    }

    if (!isGroupableDelta(event)) {
      items.push({ kind: "event", event, display: eventDisplay(event, context) });
      continue;
    }

    const key = deltaGroupKey(event);
    if (renderedDeltaGroups.has(key)) continue;
    const group = deltaGroups.get(key);
    if (!group) continue;
    renderedDeltaGroups.add(key);
    items.push(group);
  }

  return { metadata, items };
}

export function buildEventTraceItems(events: readonly NormalizedEvent[]): EventTraceItem[] {
  return buildEventTraceModel(events).items;
}

interface DisplayContext {
  turnLabels: Map<string, string>;
  callTools: Map<string, string>;
}

function buildDisplayContext(events: readonly NormalizedEvent[]): DisplayContext {
  const turnLabels = new Map<string, string>();
  const callTools = new Map<string, string>();

  for (const event of events) {
    if (event.turnId && !turnLabels.has(event.turnId)) {
      turnLabels.set(event.turnId, `Request ${turnLabels.size + 1}`);
    }

    if (event.type === EventType.ToolRequest) {
      const callId = readString(event.data, "call_id");
      const tool = readString(event.data, "tool");
      if (callId && tool) callTools.set(callId, tool);
    }
  }

  return { turnLabels, callTools };
}

export function streamSummary(text: string): string {
  const summary = text.replace(/\s+/g, " ").trim();
  if (!summary) return "(empty output)";
  return summary.length > 96 ? `${summary.slice(0, 95)}...` : summary;
}

function collectDeltaGroups(
  events: readonly NormalizedEvent[],
  context: DisplayContext,
): Map<string, Extract<EventTraceItem, { kind: "deltaGroup" }>> {
  const groups = new Map<string, Extract<EventTraceItem, { kind: "deltaGroup" }>>();

  for (const event of events) {
    if (!isGroupableDelta(event)) continue;
    const key = deltaGroupKey(event);
    const group = groups.get(key);
    if (group) {
      group.events.push(event);
      group.updateCount += 1;
      group.text += deltaText(event);
      continue;
    }
    groups.set(key, {
      kind: "deltaGroup",
      key,
      type: event.type,
      label: deltaGroupLabel(event.type),
      turnId: event.turnId,
      turnLabel: requestLabel(context, event.turnId),
      events: [event],
      updateCount: 1,
      text: deltaText(event),
    });
  }

  for (const event of events) {
    if (!isDoneForDeltaGroup(event)) continue;
    const group = groups.get(doneDeltaGroupKey(event));
    if (!group) continue;
    group.events.push(event);
    group.text = readString(event.data, "text") ?? group.text;
  }

  return groups;
}

function collectRequestRecordGroups(
  events: readonly NormalizedEvent[],
  context: DisplayContext,
): Map<string, Extract<EventTraceItem, { kind: "eventGroup" }>> {
  const lifecycleEventsByTurn = new Map<string, NormalizedEvent[]>();

  for (const event of events) {
    const key = requestRecordKey(event);
    if (!key) continue;
    const group = lifecycleEventsByTurn.get(key) ?? [];
    group.push(event);
    lifecycleEventsByTurn.set(key, group);
  }

  const groups = new Map<string, Extract<EventTraceItem, { kind: "eventGroup" }>>();
  for (const [key, groupEvents] of lifecycleEventsByTurn) {
    if (groupEvents.length < 2 || !hasRequestBoundary(groupEvents)) continue;
    groups.set(key, requestRecordGroup(key, groupEvents, context));
  }

  return groups;
}

function hasRequestBoundary(events: readonly NormalizedEvent[]): boolean {
  return events.some((event) => event.type === EventType.TurnStart || event.type === EventType.UserMessage);
}

function requestRecordGroup(
  key: string,
  events: NormalizedEvent[],
  context: DisplayContext,
): Extract<EventTraceItem, { kind: "eventGroup" }> {
  const turnId = events[0]?.turnId;
  const userMessage = events.find((event) => event.type === EventType.UserMessage);
  const usage = events.find((event) => event.type === EventType.Usage);
  const end = events.find((event) => event.type === EventType.TurnEnd);
  const tokenTotal = (readNumber(usage?.data, "input_tokens") ?? 0) + (readNumber(usage?.data, "output_tokens") ?? 0);

  return {
    kind: "eventGroup",
    key,
    label: "Request trace",
    meta: compact([
      requestLabel(context, turnId),
      streamSummary(readString(userMessage?.data, "text") ?? ""),
      readString(end?.data, "reason"),
      tokenTotal > 0 ? `${tokenTotal} tokens` : undefined,
    ]),
    badges: [],
    events,
  };
}

function requestRecordKey(event: NormalizedEvent): string | undefined {
  if (!event.turnId) return undefined;
  if (
    event.type !== EventType.TurnStart
    && event.type !== EventType.UserMessage
    && event.type !== EventType.RuntimeSurface
    && event.type !== EventType.Usage
    && event.type !== EventType.TurnEnd
  ) {
    return undefined;
  }
  return `request:${event.turnId}`;
}

function deltaGroupKey(event: NormalizedEvent): string {
  return `${event.type}:${event.turnId ?? "session"}`;
}

function isGroupableDelta(event: NormalizedEvent): boolean {
  return event.type === EventType.MessageDelta || event.type === EventType.ThinkingDelta;
}

function isDoneForDeltaGroup(event: NormalizedEvent): boolean {
  return event.type === EventType.MessageDone || event.type === EventType.ThinkingDone;
}

function doneDeltaGroupKey(event: NormalizedEvent): string {
  if (event.type === EventType.MessageDone) return `${EventType.MessageDelta}:${event.turnId ?? "session"}`;
  if (event.type === EventType.ThinkingDone) return `${EventType.ThinkingDelta}:${event.turnId ?? "session"}`;
  return deltaGroupKey(event);
}

function deltaGroupLabel(type: string): string {
  if (type === EventType.MessageDelta) return "Assistant output";
  if (type === EventType.ThinkingDelta) return "Thinking notes";
  return "Text output";
}

function deltaText(event: NormalizedEvent): string {
  return readString(event.data, "delta") ?? "";
}

function eventDisplay(event: NormalizedEvent, context: DisplayContext): EventDisplay {
  const request = requestLabel(context, event.turnId);

  switch (event.type) {
    case EventType.TraceMeta:
      return {
        label: "Trace loaded",
        meta: [],
        badges: schemaVersion(event) ? [`schema v${schemaVersion(event)}`] : [],
      };
    case EventType.TurnStart:
      return { label: "Started request", meta: compact([request]), badges: [] };
    case EventType.UserMessage:
      return { label: "User message", meta: compact([request, streamSummary(readString(event.data, "text") ?? "")]), badges: [] };
    case EventType.RuntimeSurface:
      return { label: "Runtime surface", meta: runtimeSurfaceMeta(event, request), badges: runtimeSurfaceBadges(event) };
    case EventType.MessageDone:
      return {
        label: "Assistant answer saved",
        meta: compact([request, finishReason(event)]),
        badges: [],
      };
    case EventType.ThinkingDone:
      return { label: "Thinking saved", meta: compact([request]), badges: [] };
    case EventType.ToolRequest:
      return { label: "Started action", meta: toolRequestMeta(event, request), badges: toolRequestBadges(event) };
    case EventType.ToolResult:
      return { label: toolResultLabel(event), meta: toolResultMeta(event, context), badges: toolResultBadges(event) };
    case EventType.Usage:
      return { label: "Token usage", meta: usageMeta(event, request), badges: [] };
    case EventType.TurnEnd:
      return { label: turnEndLabel(event), meta: turnEndMeta(event, request), badges: [] };
    case EventType.LoopDecision:
      return { label: "Loop decision", meta: loopDecisionMeta(event, request), badges: loopDecisionBadges(event) };
    case EventType.ContextCompacted:
      return { label: "Context compacted", meta: contextCompactedMeta(event, request), badges: contextCompactedBadges(event) };
    case EventType.Error:
      return { label: "Error", meta: errorMeta(event, request), badges: readBoolean(event.data, "recoverable") ? ["recoverable"] : [] };
    default:
      return { label: event.type, meta: fallbackMeta(event, context), badges: [] };
  }
}

function runtimeSurfaceMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  const toolCount = readNumber(event.data, "tool_count");
  const maxSteps = readNumber(event.data, "max_turn_steps");
  const maxCalls = readNumber(event.data, "max_tool_calls");
  return compact([
    turn,
    typeof toolCount === "number" ? `${toolCount} tools` : undefined,
    typeof maxSteps === "number" ? `${maxSteps} turns` : undefined,
    typeof maxCalls === "number" && maxCalls > 0 ? `${maxCalls} tool cap` : undefined,
  ]);
}

function runtimeSurfaceBadges(event: NormalizedEvent): string[] {
  const caps = readObject(event.data, "capabilities");
  const workspaceTools = readStringArray(caps, "workspace_tools");
  return compact([
    readBoolean(caps, "web_search") ? "web search" : readBoolean(caps, "web_fetch") ? "web fetch" : undefined,
    readBoolean(caps, "browser") ? "browser" : undefined,
    readBoolean(caps, "memory") ? "memory" : undefined,
    readBoolean(caps, "subagent") ? "subagent" : undefined,
    readBoolean(caps, "focused_tasks") ? "focused tasks" : undefined,
    readBoolean(caps, "builtins") ? "workspace tools" : workspaceTools.length > 0 ? `workspace: ${workspaceTools.join(", ")}` : undefined,
  ]);
}

function contextCompactedMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  const before = readNumber(event.data, "before_messages");
  const after = readNumber(event.data, "after_messages");
  const removed = readNumber(event.data, "removed_messages");
  const summaryBytes = readNumber(event.data, "summary_bytes");
  return compact([
    turn,
    readString(event.data, "reason"),
    typeof before === "number" && typeof after === "number" ? `${before} -> ${after} messages` : undefined,
    typeof removed === "number" ? `${removed} removed` : undefined,
    typeof summaryBytes === "number" && summaryBytes > 0 ? `${formatByteCount(summaryBytes)} summary` : undefined,
  ]);
}

function contextCompactedBadges(event: NormalizedEvent): string[] {
  return compact([
    readBoolean(event.data, "reactive") ? "reactive" : "proactive",
    readBoolean(event.data, "summary_present") ? "summary" : undefined,
  ]);
}

function loopDecisionMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  return compact([
    turn,
    readString(event.data, "kind"),
    readString(event.data, "decision"),
    streamSummary(readString(event.data, "reason") ?? readString(event.data, "required_action") ?? ""),
  ]);
}

function loopDecisionBadges(event: NormalizedEvent): string[] {
  return compact([
    readString(event.data, "confidence"),
    readBoolean(event.data, "visible_in_ui") ? "visible" : undefined,
  ]);
}

function toolRequestMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  const argsSummary = readString(event.data, "original_args_summary");
  return compact([
    turn,
    readString(event.data, "tool"),
    argsSummary ? streamSummary(argsSummary) : undefined,
  ]);
}

function toolRequestBadges(event: NormalizedEvent): string[] {
  return compact([
    readBoolean(event.data, "canonicalized") ? "renamed" : undefined,
    readBoolean(event.data, "args_repaired") ? "repaired" : undefined,
    readBoolean(event.data, "args_truncated") ? "truncated" : undefined,
  ]);
}

function toolResultLabel(event: NormalizedEvent): string {
  const exitCode = readNumber(event.data, "exit_code");
  return typeof exitCode === "number" && exitCode !== 0 ? "Action failed" : "Action finished";
}

function toolResultMeta(event: NormalizedEvent, context: DisplayContext): string[] {
  const duration = readNumber(event.data, "duration_ms");
  const tool = context.callTools.get(readString(event.data, "call_id") ?? "");
  const artifactPath = readString(event.data, "result_artifact_path");
  const resultBytes = readNumber(event.data, "result_bytes");
  const omittedBytes = readNumber(event.data, "result_omitted_bytes");
  const capBytes = readNumber(event.data, "result_cap_bytes");
  const resultTruncated = readBoolean(event.data, "result_truncated");
  return compact([
    tool,
    typeof duration === "number" ? formatDuration(duration) : undefined,
    streamSummary(readString(event.data, "result_summary") ?? readString(event.data, "result") ?? ""),
    artifactPath
      ? `artifact ${artifactDisplayLabel({
          path: artifactPath,
          name: artifactName(artifactPath),
          source: "",
          truncated: resultTruncated,
          bytes: resultBytes,
          omittedBytes,
          capBytes,
        })}`
      : undefined,
  ]);
}

function toolResultBadges(event: NormalizedEvent): string[] {
  return compact([
    readBoolean(event.data, "result_truncated") ? "truncated" : undefined,
    readString(event.data, "result_artifact_path") ? "full output" : undefined,
  ]);
}

function usageMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  const input = readNumber(event.data, "input_tokens");
  const output = readNumber(event.data, "output_tokens");
  return compact([
    turn,
    typeof input === "number" ? `${input} in` : undefined,
    typeof output === "number" ? `${output} out` : undefined,
  ]);
}

function turnEndLabel(event: NormalizedEvent): string {
  const reason = readString(event.data, "reason");
  if (reason === "completed") return "Request finished";
  if (reason === "cancelled") return "Request cancelled";
  if (reason === "max_turns") return "Stopped at limit";
  if (reason === "error") return "Request ended with error";
  return "Request ended";
}

function turnEndMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  const stats = event.data && typeof event.data === "object"
    ? (event.data as { tool_stats?: unknown }).tool_stats
    : undefined;
  const toolStats = stats && typeof stats === "object" ? stats as Record<string, unknown> : undefined;
  const toolRequests = typeof toolStats?.tool_requests === "number" ? toolStats.tool_requests : undefined;
  const toolErrors = typeof toolStats?.tool_errors === "number" ? toolStats.tool_errors : undefined;
  const toolDuration = typeof toolStats?.tool_duration_ms === "number" ? toolStats.tool_duration_ms : undefined;
  const verifiedSources = typeof toolStats?.source_access_verified === "number" ? toolStats.source_access_verified : undefined;
  const networkSources = typeof toolStats?.source_access_network === "number" ? toolStats.source_access_network : undefined;
  const dynamicPartialSources = typeof toolStats?.source_access_dynamic_partial === "number" ? toolStats.source_access_dynamic_partial : undefined;

  return compact([
    turn,
    readString(event.data, "reason"),
    typeof toolRequests === "number" ? `${toolRequests} actions` : undefined,
    typeof toolErrors === "number" && toolErrors > 0 ? `${toolErrors} failed` : undefined,
    typeof verifiedSources === "number" && verifiedSources > 0 ? `${verifiedSources} sources` : undefined,
    typeof networkSources === "number" && networkSources > 0 ? `${networkSources} network` : undefined,
    typeof dynamicPartialSources === "number" && dynamicPartialSources > 0 ? `${dynamicPartialSources} partial` : undefined,
    typeof toolDuration === "number" ? formatDuration(toolDuration) : undefined,
  ]);
}

function errorMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  return compact([
    turn,
    readString(event.data, "code"),
    streamSummary(readString(event.data, "message") ?? ""),
  ]);
}

function fallbackMeta(event: NormalizedEvent, context: DisplayContext): string[] {
  return compact([
    requestLabel(context, event.turnId),
    context.callTools.get(readString(event.data, "call_id") ?? ""),
  ]);
}

function requestLabel(context: DisplayContext, turnId: string | undefined): string | undefined {
  return turnId ? context.turnLabels.get(turnId) ?? turnId : undefined;
}

function finishReason(event: NormalizedEvent): string | undefined {
  const finish = readString(event.data, "finish_reason");
  return finish ? `finish ${finish}` : undefined;
}

function formatDuration(durationMs: number): string {
  if (durationMs < 1000) return `${durationMs} ms`;
  return `${(durationMs / 1000).toFixed(durationMs < 10_000 ? 1 : 0)} s`;
}

function compact(values: Array<string | undefined>): string[] {
  return values.filter((value): value is string => !!value);
}

function schemaVersion(event: NormalizedEvent): number | undefined {
  const value = event.type === "trace.meta" && event.data && typeof event.data === "object"
    ? (event.data as { schema_version?: unknown }).schema_version
    : undefined;
  return typeof value === "number" ? value : undefined;
}

function readString(data: unknown, key: string): string | undefined {
  if (!data || typeof data !== "object") return undefined;
  const value = (data as Record<string, unknown>)[key];
  return typeof value === "string" && value !== "" ? value : undefined;
}

function readObject(data: unknown, key: string): Record<string, unknown> | undefined {
  if (!data || typeof data !== "object") return undefined;
  const value = (data as Record<string, unknown>)[key];
  return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function readStringArray(data: unknown, key: string): string[] {
  if (!data || typeof data !== "object") return [];
  const value = (data as Record<string, unknown>)[key];
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is string => typeof item === "string" && item !== "");
}

function readNumber(data: unknown, key: string): number | undefined {
  if (!data || typeof data !== "object") return undefined;
  const value = (data as Record<string, unknown>)[key];
  return typeof value === "number" ? value : undefined;
}

function readBoolean(data: unknown, key: string): boolean {
  if (!data || typeof data !== "object") return false;
  return (data as Record<string, unknown>)[key] === true;
}
