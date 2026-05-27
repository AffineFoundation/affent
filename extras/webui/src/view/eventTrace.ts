import { EventType } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { formatByteCount } from "./byteFormat";
import { describeSourceAccess, sourceEvidenceLabel } from "./sourceAccess";
import { artifactDisplayLabel, artifactName } from "./turnArtifacts";
import { contextCompactionSummaryLabel } from "./contextCompaction";

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
      userMessageOriginMeta(userMessage),
      streamSummary(userMessageDisplayText(userMessage) ?? ""),
      readString(end?.data, "reason"),
      ...toolRuntimeStatsMeta(readToolStats(end)),
      tokenTotal > 0 ? `${tokenTotal} tokens` : undefined,
    ]),
    badges: userMessageBadges(userMessage),
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
      return {
        label: userMessageLabel(event),
        meta: compact([request, userMessageOriginMeta(event), streamSummary(userMessageDisplayText(event) ?? "")]),
        badges: userMessageBadges(event),
      };
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
    case EventType.LoopProtocolFeed:
      return { label: "Loop protocol fed", meta: loopProtocolFeedMeta(event, request), badges: loopProtocolFeedBadges(event) };
    case EventType.LoopProtocolCalibrationRequest:
      return { label: "Loop calibration asked", meta: loopProtocolCalibrationRequestEventMeta(event), badges: loopProtocolCalibrationBadges(event) };
    case EventType.LoopProtocolCalibration:
      return { label: "Loop calibration recorded", meta: loopProtocolCalibrationEventMeta(event), badges: loopProtocolCalibrationBadges(event) };
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

function userMessageDisplayText(event: NormalizedEvent | undefined): string | undefined {
  return readString(event?.data, "display_text") ?? readString(event?.data, "text");
}

function userMessageLabel(event: NormalizedEvent | undefined): string {
  return readString(event?.data, "source") === "schedule" ? "Scheduled message" : "User message";
}

function userMessageOriginMeta(event: NormalizedEvent | undefined): string | undefined {
  const source = readString(event?.data, "source");
  if (source === "schedule") {
    const scheduleID = readString(event?.data, "schedule_id");
    const kind = scheduleKindLabel(readString(event?.data, "schedule_kind"));
    return compact([kind ?? "timer", scheduleID]).join(" ");
  }
  return undefined;
}

function userMessageBadges(event: NormalizedEvent | undefined): string[] {
  const source = readString(event?.data, "source");
  if (source === "schedule") return compact(["scheduled", readString(event?.data, "schedule_kind"), readString(event?.data, "schedule_id")]);
  return [];
}

function scheduleKindLabel(kind: string | undefined): string | undefined {
  if (kind === "loop_tick") return "loop tick";
  if (kind === "daily_checkin") return "daily check-in";
  if (kind === "checkin") return "check-in";
  if (kind === "custom") return "timer";
  return undefined;
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
    contextCompactionSummaryLabel(event.data),
    readString(event.data, "summary_preview") ? `summary: ${streamSummary(readString(event.data, "summary_preview") ?? "")}` : undefined,
  ]);
}

function contextCompactedBadges(event: NormalizedEvent): string[] {
  return compact([
    readBoolean(event.data, "reactive") ? "reactive" : "proactive",
    contextCompactionSummaryLabel(event.data) ?? (readBoolean(event.data, "summary_present") ? "summary" : undefined),
  ]);
}

function loopDecisionMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  return compact([
    turn,
    loopDecisionKindLabel(readString(event.data, "kind")),
    readString(event.data, "decision"),
    streamSummary(readString(event.data, "reason") ?? readString(event.data, "required_action") ?? ""),
  ]);
}

function loopDecisionKindLabel(kind: string | undefined): string | undefined {
  if (kind === "research_checkpoint") return "research checkpoint";
  return kind;
}

function loopProtocolFeedMeta(event: NormalizedEvent, turn: string | undefined): string[] {
  const feedNumber = readNumber(event.data, "feed_number");
  const feeds = readNumber(event.data, "protocol_feeds");
  return compact([
    turn,
    readString(event.data, "loop_id"),
    feedNumber ? `feed ${feedNumber}` : undefined,
    feeds && feeds !== feedNumber ? `${feeds} total` : undefined,
    loopProtocolFeedCalibrationMeta(event),
    loopProtocolFeedSituationMeta(event),
    loopProtocolPlanMeta(event),
    readString(event.data, "protocol_path"),
  ]);
}

function loopProtocolFeedCalibrationMeta(event: NormalizedEvent): string | undefined {
  const answers = readNumber(event.data, "calibration_answers");
  const preview = readString(event.data, "last_calibration_answer_preview");
  if (!answers && !preview) return undefined;
  const label = answers ? `calibration ${answers}` : "calibration";
  return preview ? `${label} · ${streamSummary(preview)}` : label;
}

function loopProtocolFeedSituationMeta(event: NormalizedEvent): string | undefined {
  const situation = readString(event.data, "current_situation_preview");
  return situation ? `situation · ${streamSummary(situation)}` : undefined;
}

function loopProtocolCalibrationEventMeta(event: NormalizedEvent): string[] {
  const answers = readNumber(event.data, "calibration_answers");
  const preview = readString(event.data, "last_calibration_answer_preview");
  const seq = readNumber(event.data, "event_seq");
  return compact([
    readString(event.data, "loop_id"),
    answers ? `calibration ${answers}` : "calibration",
    preview ? streamSummary(preview) : undefined,
    seq ? `event ${seq}` : undefined,
    readString(event.data, "protocol_path"),
  ]);
}

function loopProtocolCalibrationRequestEventMeta(event: NormalizedEvent): string[] {
  const questions = readNumber(event.data, "calibration_questions");
  const preview = readString(event.data, "last_calibration_question_preview");
  const seq = readNumber(event.data, "event_seq");
  return compact([
    readString(event.data, "loop_id"),
    questions ? `question ${questions}` : "question",
    preview ? streamSummary(preview) : undefined,
    seq ? `event ${seq}` : undefined,
    readString(event.data, "protocol_path"),
  ]);
}

function loopProtocolCalibrationBadges(event: NormalizedEvent): string[] {
  return compact([
    readString(event.data, "status"),
  ]);
}

function loopProtocolPlanMeta(event: NormalizedEvent): string | undefined {
  const label = readString(event.data, "plan_label");
  const index = readNumber(event.data, "plan_current_step_index");
  const status = readString(event.data, "plan_current_step_status");
  const step = readString(event.data, "plan_current_step");
  if (!label && !index && !step) return undefined;
  const current = index ? `step ${index}${status ? ` ${status}` : ""}` : status;
  return compact([
    label ? `plan ${label}` : "plan",
    current,
    step ? streamSummary(step) : undefined,
  ]).join(" · ");
}

function loopProtocolFeedBadges(event: NormalizedEvent): string[] {
  return compact([
    readString(event.data, "mode"),
    readString(event.data, "status"),
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
  const exitCode = readNumber(event.data, "exit_code");
  const artifactPath = readString(event.data, "result_artifact_path");
  const resultBytes = readNumber(event.data, "result_bytes");
  const omittedBytes = readNumber(event.data, "result_omitted_bytes");
  const capBytes = readNumber(event.data, "result_cap_bytes");
  const resultTruncated = readBoolean(event.data, "result_truncated");
  const contextBytes = readNumber(event.data, "context_bytes");
  const contextOmittedBytes = readNumber(event.data, "context_omitted_bytes");
  const fullResultText = readString(event.data, "result") ?? readString(event.data, "result_summary") ?? "";
  const sourceAccess = describeSourceAccess(fullResultText);
  const memoryUpdate = tool === "memory" ? memoryUpdateMeta(event) : [];
  const sessionSearchPayload = tool === "session_search" ? parseJSONRecord(readString(event.data, "result")) : undefined;
  const sessionSearch = sessionSearchPayload ? sessionSearchMeta(sessionSearchPayload) : [];
  const resultText = readString(event.data, "result_summary") ?? fullResultText;
  const nextHint = shouldSurfaceToolResultNextHint(tool, exitCode, sourceAccess) ? toolResultNextHint(event) : undefined;
  const loopGuard = loopGuardMeta(event, fullResultText || resultText);
  const scrollTelemetry = tool === "browser_scroll" ? browserScrollTelemetryMeta(fullResultText) : undefined;
  const resultPreview = sessionSearchPayload
    ? readString(sessionSearchPayload, "message")
    : memoryUpdate.length > 0
      ? ""
    : resultText;
  return compact([
    tool,
    typeof duration === "number" ? formatDuration(duration) : undefined,
    ...memoryUpdate,
    ...sessionSearch,
    ...loopGuard,
    scrollTelemetry,
    !loopGuard.length && nextHint ? `next ${streamSummary(nextHint)}` : undefined,
    sourceAccess ? sourceEvidenceLabel(sourceAccess) : undefined,
    sourceAccess ? sourceAccess.accessedUrl : !loopGuard.length && resultPreview ? streamSummary(resultPreview) : undefined,
    sourceAccess?.requestedUrl && sourceAccess.requestedUrl !== sourceAccess.accessedUrl ? `from ${sourceAccess.requestedUrl}` : undefined,
    sourceAccess?.ref ? `ref ${sourceAccess.ref}` : undefined,
    sourceAccess?.httpStatus ? `http ${sourceAccess.httpStatus}` : undefined,
    sourceAccess?.contentType,
    sourceAccess?.jsonPath ? `json path ${sourceAccess.jsonPath}` : undefined,
    sourceAccess?.resultPreview ? `preview ${sourceAccess.resultPreview}` : undefined,
    toolContextMeta(contextBytes, contextOmittedBytes),
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

function toolResultNextHint(event: NormalizedEvent): string | undefined {
  const summary = readString(event.data, "result_summary");
  const result = readString(event.data, "result");
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const next = match?.[1]?.trim();
  return next || undefined;
}

function shouldSurfaceToolResultNextHint(
  tool: string | undefined,
  exitCode: number | undefined,
  sourceAccess: ReturnType<typeof describeSourceAccess>,
): boolean {
  if (typeof exitCode === "number" && exitCode !== 0) return true;
  if (tool === "browser_network" || tool === "browser_scroll") return true;
  return sourceAccess?.status === "dynamic_partial" || sourceAccess?.status === "discovery_only";
}

function loopGuardMeta(event: NormalizedEvent, resultText: string): string[] {
  const kinds = eventFailureKinds(event).filter((kind) => kind.startsWith("loop_guard"));
  if (kinds.length === 0 && !/\bloop_guard:/.test(resultText)) return [];
  return compact([
    kinds.length > 0 ? `loop guard ${kinds.map(loopGuardKindLabel).join(", ")}` : "loop guard",
    loopGuardMessage(resultText),
    loopGuardNext(resultText),
  ]);
}

function loopGuardKindLabel(kind: string): string {
  return kind.replace(/^loop_guard_?/, "").replace(/_/g, " ");
}

function loopGuardMessage(text: string): string | undefined {
  const match = text.match(/\bloop_guard:\s*([\s\S]*?)(?:\nNext:|\nFailure:|$)/);
  const message = match?.[1]?.trim();
  return message ? `guard ${streamSummary(message)}` : undefined;
}

function loopGuardNext(text: string): string | undefined {
  const match = text.match(/\nNext:\s*([\s\S]*?)(?:\nFailure:|$)/);
  const next = match?.[1]?.trim();
  return next ? `next ${streamSummary(next)}` : undefined;
}

function toolContextMeta(contextBytes?: number, contextOmittedBytes?: number): string | undefined {
  if ((!contextBytes || contextBytes <= 0) && (!contextOmittedBytes || contextOmittedBytes <= 0)) return undefined;
  const parts: string[] = [];
  if (contextBytes && contextBytes > 0) parts.push(formatByteCount(contextBytes));
  if (contextOmittedBytes && contextOmittedBytes > 0) parts.push(`${formatByteCount(contextOmittedBytes)} omitted`);
  return `tool context ${parts.join(", ")}`;
}

function toolResultBadges(event: NormalizedEvent): string[] {
  const sourceAccess = describeSourceAccess(readString(event.data, "result") ?? readString(event.data, "result_summary"));
  const memoryAction = memoryUpdateAction(event);
  const resultText = readString(event.data, "result") ?? readString(event.data, "result_summary") ?? "";
  return compact([
    ...eventFailureKinds(event),
    memoryAction ? `memory ${memoryAction}` : undefined,
    sourceAccess ? sourceAccess.status : undefined,
    ...browserScrollTelemetryBadges(resultText),
    (readNumber(event.data, "context_omitted_bytes") ?? 0) > 0 ? "context trimmed" : undefined,
    readBoolean(event.data, "result_truncated") ? "truncated" : undefined,
    readString(event.data, "result_artifact_path") ? "full output" : undefined,
  ]);
}

interface BrowserScrollTelemetry {
  direction?: string;
  beforeY?: string;
  afterY?: string;
  maxY?: string;
  movement?: string;
  boundary?: string;
}

function browserScrollTelemetryMeta(text: string): string | undefined {
  const telemetry = parseBrowserScrollTelemetry(text);
  if (!telemetry) return undefined;
  const direction = telemetry.direction ? `scroll ${telemetry.direction}` : "scroll";
  const movement = telemetry.movement === "none" ? "no movement" : telemetry.movement;
  const boundary = telemetry.boundary ? `at ${telemetry.boundary}` : undefined;
  const position = telemetry.afterY && telemetry.maxY ? `y ${telemetry.afterY}/${telemetry.maxY}` : undefined;
  return compact([direction, movement, boundary, position]).join(" ");
}

function browserScrollTelemetryBadges(text: string): string[] {
  const telemetry = parseBrowserScrollTelemetry(text);
  if (!telemetry) return [];
  return compact([
    telemetry.movement === "none" ? "scroll no movement" : undefined,
    telemetry.boundary ? `scroll ${telemetry.boundary}` : undefined,
  ]);
}

function parseBrowserScrollTelemetry(text: string): BrowserScrollTelemetry | undefined {
  const line = text.split("\n").map((part) => part.trim()).find((part) => part.startsWith("SCROLL:"));
  if (!line) return undefined;
  const telemetry: BrowserScrollTelemetry = {};
  for (const field of line.replace(/^SCROLL:\s*/, "").split(/\s+/)) {
    const [key, value] = field.split("=", 2);
    if (!key || !value) continue;
    if (key === "direction") telemetry.direction = value;
    if (key === "before_y") telemetry.beforeY = value;
    if (key === "after_y") telemetry.afterY = value;
    if (key === "max_y") telemetry.maxY = value;
    if (key === "movement") telemetry.movement = value;
    if (key === "boundary") telemetry.boundary = value;
  }
  return telemetry;
}

function memoryUpdateMeta(event: NormalizedEvent): string[] {
  const update = readObject(event.data, "memory_update");
  if (!update) return [];
  const action = memoryUpdateAction(event);
  const location = readString(update, "location");
  const preview = readString(update, "preview");
  return compact([
    action ? memoryUpdateLabel(action) : undefined,
    location,
    preview ? streamSummary(preview) : undefined,
  ]);
}

function memoryUpdateAction(event: NormalizedEvent): string | undefined {
  const update = readObject(event.data, "memory_update");
  const action = readString(update, "action");
  return action === "add" || action === "replace" || action === "remove" ? action : undefined;
}

function memoryUpdateLabel(action: string): string {
  if (action === "add") return "Saved memory";
  if (action === "replace") return "Updated memory";
  if (action === "remove") return "Removed memory";
  return "Memory update";
}

function sessionSearchMeta(payload: Record<string, unknown>): string[] {
  const total = readNumber(payload, "total");
  const results = readObjectArray(payload, "results");
  const first = results[0];
  const matchedTerms = first ? readStringArray(first, "matched_terms") : [];
  const snippet = first ? readString(first, "snippet") : undefined;
  const visibleTotal = total ?? results.length;
  const extra = Math.max(0, visibleTotal - 1);
  return compact([
    typeof total === "number" ? `${total} history hit${total === 1 ? "" : "s"}` : undefined,
    first ? readString(first, "session_id") : undefined,
    first && typeof readNumber(first, "turn_idx") === "number" ? `turn ${readNumber(first, "turn_idx")}` : undefined,
    first && typeof readNumber(first, "message_idx") === "number" ? `message ${readNumber(first, "message_idx")}` : undefined,
    matchedTerms.length > 0 ? `matched ${matchedTerms.slice(0, 8).join(", ")}` : undefined,
    first && readBoolean(first, "context_included") ? "adjacent context" : undefined,
    snippet ? `snippet ${streamSummary(snippet)}` : undefined,
    extra > 0 ? `${extra} more history hit${extra === 1 ? "" : "s"}` : undefined,
  ]);
}

function eventFailureKinds(event: NormalizedEvent): string[] {
  const seen = new Set<string>();
  return [
    ...readStringArray(event.data, "failure_kinds"),
    readString(event.data, "failure_kind"),
  ].filter((kind): kind is string => {
    if (!kind || seen.has(kind)) return false;
    seen.add(kind);
    return true;
  });
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
  return compact([
    turn,
    readString(event.data, "reason"),
    ...toolRuntimeStatsMeta(readToolStats(event)),
  ]);
}

function readToolStats(event: NormalizedEvent | undefined): Record<string, unknown> | undefined {
  const stats = event?.data && typeof event.data === "object"
    ? (event.data as { tool_stats?: unknown }).tool_stats
    : undefined;
  return stats && typeof stats === "object" && !Array.isArray(stats) ? stats as Record<string, unknown> : undefined;
}

function toolRuntimeStatsMeta(toolStats: Record<string, unknown> | undefined): string[] {
  const toolRequests = readNumber(toolStats, "tool_requests");
  const toolErrors = readNumber(toolStats, "tool_errors");
  const loopGuard = readNumber(toolStats, "loop_guard_interventions");
  const forcedNoTools = readNumber(toolStats, "forced_no_tools");
  const memoryUpdates = readNumber(toolStats, "memory_updates");
  const sessionSearchCalls = readNumber(toolStats, "session_search_calls");
  const sessionSearchResults = readNumber(toolStats, "session_search_results");
  const verifiedSources = readNumber(toolStats, "source_access_verified");
  const networkSources = readNumber(toolStats, "source_access_network");
  const dynamicPartialSources = readNumber(toolStats, "source_access_dynamic_partial");
  const toolDuration = readNumber(toolStats, "tool_duration_ms");

  return compact([
    typeof toolRequests === "number" ? `${toolRequests} actions` : undefined,
    typeof toolErrors === "number" && toolErrors > 0 ? `${toolErrors} failed` : undefined,
    typeof loopGuard === "number" && loopGuard > 0 ? `Guard ${loopGuard}` : undefined,
    typeof forcedNoTools === "number" && forcedNoTools > 0 ? `${forcedNoTools} no-tools` : undefined,
    typeof memoryUpdates === "number" && memoryUpdates > 0 ? memoryUpdateStatsMeta(toolStats, memoryUpdates) : undefined,
    typeof sessionSearchResults === "number" && (sessionSearchResults > 0 || (sessionSearchCalls ?? 0) > 0) ? sessionSearchStatsMeta(toolStats, sessionSearchResults) : undefined,
    typeof verifiedSources === "number" && verifiedSources > 0 ? `${verifiedSources} sources` : undefined,
    typeof networkSources === "number" && networkSources > 0 ? `${networkSources} network` : undefined,
    typeof dynamicPartialSources === "number" && dynamicPartialSources > 0 ? `${dynamicPartialSources} partial` : undefined,
    typeof toolDuration === "number" ? formatDuration(toolDuration) : undefined,
  ]);
}

function sessionSearchStatsMeta(toolStats: Record<string, unknown> | undefined, results: number): string {
  const calls = readNumber(toolStats, "session_search_calls") ?? 0;
  const contextHits = readNumber(toolStats, "session_search_context_hits") ?? 0;
  const matchedTerms = readNumber(toolStats, "session_search_matched_terms") ?? 0;
  const parts = [`Recall ${results} hit${results === 1 ? "" : "s"}`];
  if (calls > 1 || results === 0) parts.push(`${calls} search${calls === 1 ? "" : "es"}`);
  if (contextHits > 0) parts.push(`${contextHits} context`);
  if (matchedTerms > 0) parts.push(`${matchedTerms} terms`);
  return parts.join(", ");
}

function memoryUpdateStatsMeta(toolStats: Record<string, unknown> | undefined, total: number): string {
  const parts = [
    countLabel(readNumber(toolStats, "memory_update_add"), "add"),
    countLabel(readNumber(toolStats, "memory_update_replace"), "replace"),
    countLabel(readNumber(toolStats, "memory_update_remove"), "remove"),
  ].filter(Boolean);
  const label = `${total} memory update${total === 1 ? "" : "s"}`;
  return parts.length > 0 ? `${label} (${parts.join(", ")})` : label;
}

function countLabel(count: number | undefined, label: string): string | undefined {
  return typeof count === "number" && count > 0 ? `${count} ${label}` : undefined;
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

function readObjectArray(data: unknown, key: string): Array<Record<string, unknown>> {
  if (!data || typeof data !== "object") return [];
  const value = (data as Record<string, unknown>)[key];
  if (!Array.isArray(value)) return [];
  return value.filter((item): item is Record<string, unknown> => !!item && typeof item === "object" && !Array.isArray(item));
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

function parseJSONRecord(text: string | undefined): Record<string, unknown> | undefined {
  if (!text) return undefined;
  try {
    const value: unknown = JSON.parse(text);
    return value && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
  } catch {
    return undefined;
  }
}
