import type { SessionState } from "../store/sessionState";
import { buildEventTraceModel, streamSummary, type EventTraceItem } from "./eventTrace";

export interface TraceToolRequestStats {
  total: number;
  admitted?: number;
  skipped?: number;
}

export interface SessionTraceView {
  summary: string;
  detail: string;
  eventCount: number;
  toolRequests: TraceToolRequestStats;
  toolIssueCount: number;
  toolIssues: TraceToolIssueView[];
  recordCount: number;
  metadataCount: number;
  unknownCount: number;
  schemaVersion?: number;
  latest?: {
    label: string;
    detail: string;
  };
}

export interface TraceToolIssueView {
  id: string;
  query: string;
  requestQuery: string;
  title: string;
  tool: string;
  detail: string;
  badges: string[];
  turnNumber: number;
  turnId?: string;
  exitCode?: number;
  durationMs?: number;
  artifactPath?: string;
  next?: string;
  occurrences: number;
}

export function buildSessionTrace(session: SessionState): SessionTraceView {
  const model = buildEventTraceModel(session.events);
  const metadataCount = model.metadata.length;
  const recordCount = model.items.length + (metadataCount > 0 ? 1 : 0);
  const latest = latestTraceRecord(model.items);
  const eventCount = session.events.length;
  const toolRequests = traceToolRequestStats(session);
  const toolIssues = buildTraceToolIssues(session);
  const toolIssueCount = toolIssues.reduce((sum, issue) => sum + issue.occurrences, 0);
  const summary = eventCount > 0
    ? `${eventCount} trace ${eventCount === 1 ? "entry" : "entries"}`
    : "No trace entries";
  const detailParts = [
    recordCount > 0 ? `${recordCount} grouped ${recordCount === 1 ? "record" : "records"}` : undefined,
    session.schemaVersion ? `schema v${session.schemaVersion}` : undefined,
    session.unknownEventCount > 0 ? `${session.unknownEventCount} unclassified` : undefined,
  ].filter((part): part is string => !!part);

  return {
    summary,
    detail: detailParts.join(" · ") || "No persisted trace loaded for this chat.",
    eventCount,
    toolRequests,
    toolIssueCount,
    toolIssues,
    recordCount,
    metadataCount,
    unknownCount: session.unknownEventCount,
    schemaVersion: session.schemaVersion,
    latest,
  };
}

function buildTraceToolIssues(session: SessionState): TraceToolIssueView[] {
  const issues: TraceToolIssueView[] = [];
  session.turns.forEach((turn, turnIndex) => {
    for (const call of turn.toolCalls) {
      if (!isToolIssue(call)) continue;
      const failureKinds = compactStrings([...(call.failureKinds ?? []), call.failureKind]);
      const summary = issueSummary(call.resultSummary, call.result);
      const detail = compactStrings([
        failureKinds.length > 0 ? failureKinds.join(", ") : call.exitCode != null && call.exitCode !== 0 ? `exit ${call.exitCode}` : "failed",
        summary ? streamSummary(summary) : undefined,
      ]).join(" · ");
      issues.push({
        id: call.callId,
        query: `call:${call.callId}`,
        requestQuery: turn.id ? `request:${turnIndex + 1}` : `call:${call.callId}`,
        title: `Request ${turnIndex + 1} · ${call.tool}`,
        tool: call.tool,
        detail,
        badges: compactStrings([
          call.exitCode != null && call.exitCode !== 0 ? `exit ${call.exitCode}` : undefined,
          ...failureKinds,
        ]),
        turnNumber: turnIndex + 1,
        turnId: turn.id,
        exitCode: call.exitCode,
        durationMs: call.durationMs,
        artifactPath: call.resultArtifactPath,
        next: issueNextHint(call.resultSummary, call.result),
        occurrences: 1,
      });
    }
  });
  return compactRepeatedToolIssues(issues);
}

function compactRepeatedToolIssues(issues: TraceToolIssueView[]): TraceToolIssueView[] {
  const bySignature = new Map<string, TraceToolIssueView>();
  const out: TraceToolIssueView[] = [];
  for (const issue of issues) {
    const signature = [
      issue.tool,
      issue.detail,
      issue.badges.join("|"),
      issue.exitCode ?? "",
      issue.next ?? "",
    ].join("\u0000");
    const previous = bySignature.get(signature);
    if (!previous) {
      bySignature.set(signature, issue);
      out.push(issue);
      continue;
    }
    previous.occurrences += 1;
    previous.badges = compactStrings([...previous.badges, `${previous.occurrences}x`]);
  }
  return out;
}

function isToolIssue(call: SessionState["turns"][number]["toolCalls"][number]): boolean {
  return call.status === "error" || (call.exitCode != null && call.exitCode !== 0) || !!call.failureKind || !!call.failureKinds?.length;
}

export function sessionTraceEvidenceText(trace: SessionTraceView): string {
  const lines = [
    "Session trace evidence",
    `Entries: ${trace.eventCount}`,
    `Grouped records: ${trace.recordCount}`,
  ];
  if (trace.schemaVersion) lines.push(`Schema: v${trace.schemaVersion}`);
  if (trace.unknownCount > 0) lines.push(`Unclassified events: ${trace.unknownCount}`);
  if (trace.toolRequests.total > 0) {
    lines.push(`Tool requests: ${traceToolRequestStatsText(trace.toolRequests)}`);
  }
  if (trace.toolIssues.length > 0) {
    lines.push(`Tool issues: ${trace.toolIssueCount}`);
    for (const issue of trace.toolIssues.slice(0, 3)) {
      const occurrences = issue.occurrences > 1 ? ` · ${issue.occurrences} occurrences` : "";
      lines.push(`Tool issue: ${issue.title}${issue.detail ? ` · ${issue.detail}` : ""}${occurrences}`);
    }
  }
  if (trace.latest) {
    lines.push(`Latest: ${trace.latest.label}`);
    if (trace.latest.detail) lines.push(`Latest detail: ${trace.latest.detail}`);
  }
  return lines.join("\n");
}

function traceToolRequestStats(session: SessionState): TraceToolRequestStats {
  const fromTurnEnd = session.events
    .map((event) => event.data && typeof event.data === "object" ? (event.data as { tool_stats?: unknown }).tool_stats : undefined)
    .filter((stats): stats is Record<string, unknown> => !!stats && typeof stats === "object" && !Array.isArray(stats))
    .reduce<TraceToolRequestStats>((acc, stats) => {
      const total = readNumber(stats, "tool_requests");
      const admitted = readNumber(stats, "tool_requests_admitted");
      const skipped = readNumber(stats, "tool_requests_skipped");
      if (typeof total === "number") acc.total += total;
      if (typeof admitted === "number") acc.admitted = (acc.admitted ?? 0) + admitted;
      if (typeof skipped === "number") acc.skipped = (acc.skipped ?? 0) + skipped;
      return acc;
    }, { total: 0 });
  if (fromTurnEnd.total > 0 || fromTurnEnd.admitted != null || fromTurnEnd.skipped != null) {
    return normalizeTraceToolRequestStats(fromTurnEnd);
  }

  let total = 0;
  let skipped = 0;
  for (const event of session.events) {
    if (event.type !== "tool.request") continue;
    total += 1;
    if (event.data && typeof event.data === "object" && (event.data as { skipped?: unknown }).skipped === true) skipped += 1;
  }
  return normalizeTraceToolRequestStats({ total, admitted: total > 0 && skipped > 0 ? total - skipped : undefined, skipped: skipped > 0 ? skipped : undefined });
}

function normalizeTraceToolRequestStats(stats: TraceToolRequestStats): TraceToolRequestStats {
  const total = Math.max(0, stats.total);
  const admitted = stats.admitted == null ? undefined : Math.max(0, stats.admitted);
  const skipped = stats.skipped == null ? undefined : Math.max(0, stats.skipped);
  return { total, admitted, skipped };
}

function traceToolRequestStatsText(stats: TraceToolRequestStats): string {
  const parts = [`${stats.total}`];
  if (stats.admitted != null || stats.skipped != null) {
    parts.push(`${stats.admitted ?? 0} admitted`);
    parts.push(`${stats.skipped ?? 0} skipped`);
  }
  return parts.join(" · ");
}

function readNumber(value: Record<string, unknown>, key: string): number | undefined {
  const n = value[key];
  return typeof n === "number" && Number.isFinite(n) ? n : undefined;
}

export function sessionTraceDraft(trace: SessionTraceView): string {
  return [
    "Inspect this session trace and identify the highest-value next action:",
    "",
    sessionTraceEvidenceText(trace),
  ].join("\n");
}

function latestTraceRecord(items: readonly EventTraceItem[]): SessionTraceView["latest"] {
  const item = [...items].reverse().find((candidate) => traceItemHasSignal(candidate));
  if (!item) return undefined;
  if (item.kind === "event") {
    return {
      label: item.display.label,
      detail: traceSummaryDetail(item.display.meta),
    };
  }
  if (item.kind === "eventGroup") {
    return {
      label: item.label,
      detail: traceSummaryDetail(item.meta),
    };
  }
  return {
    label: item.label,
    detail: traceSummaryDetail([item.turnLabel, stripTraceNextBlocks(streamSummary(item.text))]),
  };
}

function traceItemHasSignal(item: EventTraceItem): boolean {
  if (item.kind !== "event") return true;
  return item.display.label !== "Token usage";
}

function issueSummary(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const stripped = stripTraceNextBlocks(text)
    .split("\n")
    .map((line) => line.trim())
    .filter((line) => line && !/^Failure:/i.test(line))
    .join(" ");
  return compactWhitespace(stripped);
}

function issueNextHint(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const value = match?.[1]?.trim();
  return value ? streamSummary(value) : undefined;
}

function stripTraceNextBlocks(value: string): string {
  let out = value;
  let next = stripOneNextBlock(out);
  while (next !== out) {
    out = next;
    next = stripOneNextBlock(out);
  }
  return out;
}

function stripOneNextBlock(value: string): string {
  return value.replace(/(?:^|\n|\s)Next:\s*[\s\S]*?(?=\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/i, "\n");
}

function traceSummaryDetail(parts: Array<string | undefined>): string {
  return compactStrings(parts.map((part) => {
    const stripped = stripTraceNextBlocks(part ?? "");
    if (/^next\b/i.test(stripped.trim())) return undefined;
    return compactWhitespace(stripped);
  })).join(" · ");
}

function compactWhitespace(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function compactStrings(values: Array<string | undefined>): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const text = value?.trim();
    if (!text || seen.has(text)) continue;
    seen.add(text);
    out.push(text);
  }
  return out;
}
