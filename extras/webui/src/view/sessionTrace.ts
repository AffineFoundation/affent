import type { SessionState } from "../store/sessionState";
import { buildEventTraceModel, streamSummary, type EventTraceItem } from "./eventTrace";

export interface SessionTraceView {
  summary: string;
  detail: string;
  eventCount: number;
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
  title: string;
  detail: string;
  badges: string[];
}

export function buildSessionTrace(session: SessionState): SessionTraceView {
  const model = buildEventTraceModel(session.events);
  const metadataCount = model.metadata.length;
  const recordCount = model.items.length + (metadataCount > 0 ? 1 : 0);
  const latest = latestTraceRecord(model.items);
  const eventCount = session.events.length;
  const toolIssues = buildTraceToolIssues(session);
  const toolIssueCount = toolIssues.length;
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
      const next = nextHint(call.resultSummary, call.result);
      const summary = compactWhitespace(call.resultSummary || call.result || "");
      const detail = compactStrings([
        failureKinds.length > 0 ? failureKinds.join(", ") : call.exitCode != null && call.exitCode !== 0 ? `exit ${call.exitCode}` : "failed",
        next ? `Next: ${next}` : summary ? streamSummary(summary) : undefined,
      ]).join(" · ");
      issues.push({
        id: call.callId,
        query: `call:${call.callId}`,
        title: `Request ${turnIndex + 1} · ${call.tool}`,
        detail,
        badges: compactStrings([
          call.exitCode != null && call.exitCode !== 0 ? `exit ${call.exitCode}` : undefined,
          ...failureKinds,
        ]),
      });
    }
  });
  return issues;
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
  if (trace.toolIssues.length > 0) {
    lines.push(`Tool issues: ${trace.toolIssueCount}`);
    for (const issue of trace.toolIssues.slice(0, 3)) {
      lines.push(`Tool issue: ${issue.title}${issue.detail ? ` · ${issue.detail}` : ""}`);
    }
  }
  if (trace.latest) {
    lines.push(`Latest: ${trace.latest.label}`);
    if (trace.latest.detail) lines.push(`Latest detail: ${trace.latest.detail}`);
  }
  return lines.join("\n");
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
      detail: item.display.meta.join(" · "),
    };
  }
  if (item.kind === "eventGroup") {
    return {
      label: item.label,
      detail: item.meta.join(" · "),
    };
  }
  return {
    label: item.label,
    detail: [item.turnLabel, streamSummary(item.text)].filter(Boolean).join(" · "),
  };
}

function traceItemHasSignal(item: EventTraceItem): boolean {
  if (item.kind !== "event") return true;
  return item.display.label !== "Token usage";
}

function nextHint(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  return match?.[1]?.trim() || undefined;
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
