import { useState, type ReactNode } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { buildEventTraceModel, streamSummary } from "../view/eventTrace";
import { EventTrace } from "./EventTrace";

export function TraceDisclosure({
  events,
  className,
  label = "Raw trace",
  onOpenArtifact,
  children,
}: {
  events: readonly NormalizedEvent[];
  className: string;
  label?: string;
  onOpenArtifact?: (path: string) => void;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const model = buildEventTraceModel(events);
  const recordCount = model.items.length + (model.metadata.length > 0 ? 1 : 0);
  const summary = traceDisclosureSummary(label, model, recordCount);

  return (
    <details className={className} onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>
        <span>{label}</span>
        <span className="subtle-count">{summary}</span>
      </summary>
      {open ? (
        <>
          {children}
          <EventTrace events={events} onOpenArtifact={onOpenArtifact} />
        </>
      ) : null}
    </details>
  );
}

function recordCountLabel(count: number): string {
  return `${count} trace entr${count === 1 ? "y" : "ies"}`;
}

function traceDisclosureSummary(label: string, model: ReturnType<typeof buildEventTraceModel>, recordCount: number): string {
  if (recordCount === 0) return "No trace entries";
  const leadItem = pickTraceLeadItem(model.items);
  const lead = leadItem ? summarizeTraceItem(leadItem) : model.metadata.length > 0 ? "Metadata" : label;
  return `${recordCountLabel(recordCount)} · ${lead}`;
}

type TraceItem = ReturnType<typeof buildEventTraceModel>["items"][number];

function pickTraceLeadItem(items: ReadonlyArray<TraceItem>): TraceItem | undefined {
  let best: { item: TraceItem; score: number } | undefined;
  for (const item of items) {
    const score = scoreTraceItem(item);
    if (!best || score > best.score) best = { item, score };
  }
  return best?.item;
}

function scoreTraceItem(item: TraceItem): number {
  if (item.kind === "eventGroup") return 70;
  if (item.kind === "deltaGroup") return 30;

  if (item.display.label === "Action failed") return hasArtifactMeta(item.display.meta) ? 100 : 90;
  if (item.display.label === "Action finished") return hasArtifactMeta(item.display.meta) ? 95 : 85;
  if (item.display.label === "Request finished") return 60;
  if (item.display.label === "Assistant answer saved") return 55;
  if (item.display.label === "User message") return 50;
  if (item.display.label === "Started action") return 40;
  if (item.display.label === "Thinking saved") return 35;
  if (item.display.label === "Token usage") return 20;
  return 10;
}

function summarizeTraceItem(item: TraceItem): string {
  if (item.kind === "event") {
    return [item.display.label, summarizeTraceMeta(item.display)].filter(Boolean).join(" · ");
  }
  if (item.kind === "eventGroup") {
    return [item.label, item.meta[0]].filter(Boolean).join(" · ");
  }
  return [item.label, item.turnLabel, streamSummary(item.text)].filter(Boolean).join(" · ");
}

function summarizeTraceMeta(display: { meta: readonly string[]; label: string }): string | undefined {
  if (display.label === "Action finished" || display.label === "Action failed") {
    return display.meta.find((meta) => meta.startsWith("artifact ")) ?? display.meta[2] ?? display.meta[0];
  }
  if (display.label === "Started action") {
    return display.meta[2] ?? display.meta[1] ?? display.meta[0];
  }
  return display.meta[0];
}

function hasArtifactMeta(meta: readonly string[]): boolean {
  return meta.some((item) => item.startsWith("artifact "));
}
