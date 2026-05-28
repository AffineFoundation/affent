import type { SessionState } from "../store/sessionState";
import { buildEventTraceModel, streamSummary, type EventTraceItem } from "./eventTrace";

export interface SessionTraceView {
  summary: string;
  detail: string;
  eventCount: number;
  recordCount: number;
  metadataCount: number;
  unknownCount: number;
  schemaVersion?: number;
  latest?: {
    label: string;
    detail: string;
  };
}

export function buildSessionTrace(session: SessionState): SessionTraceView {
  const model = buildEventTraceModel(session.events);
  const metadataCount = model.metadata.length;
  const recordCount = model.items.length + (metadataCount > 0 ? 1 : 0);
  const latest = latestTraceRecord(model.items);
  const eventCount = session.events.length;
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
    recordCount,
    metadataCount,
    unknownCount: session.unknownEventCount,
    schemaVersion: session.schemaVersion,
    latest,
  };
}

export function sessionTraceEvidenceText(trace: SessionTraceView): string {
  const lines = [
    "Session trace evidence",
    `Entries: ${trace.eventCount}`,
    `Grouped records: ${trace.recordCount}`,
  ];
  if (trace.schemaVersion) lines.push(`Schema: v${trace.schemaVersion}`);
  if (trace.unknownCount > 0) lines.push(`Unclassified events: ${trace.unknownCount}`);
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
