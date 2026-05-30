import { type ReactNode, useState } from "react";
import { EventType } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { buildEventTraceModel, streamSummary, type EventTraceItem } from "../view/eventTrace";
import { describeSourceAccess, sourceEvidenceLabel } from "../view/sourceAccess";
import { CopyButton } from "./CopyButton";

export function EventTrace({
  events,
  onOpenArtifact,
  showCount = true,
}: {
  events: readonly NormalizedEvent[];
  onOpenArtifact?: (path: string) => void;
  showCount?: boolean;
}) {
  const model = buildEventTraceModel(events);

  return (
    <div className="event-trace" data-testid="event-trace">
      {events.length > 0 ? (
        <div className="event-trace-actions" data-show-count={showCount ? "true" : "false"}>
          {showCount ? <span className="event-trace-count">{events.length} trace entries</span> : null}
          <CopyButton label="Copy trace" value={copyHistoryText(events)} className="event-action" />
        </div>
      ) : null}
      {model.metadata.length > 0 ? renderMetadata(model.metadata) : null}
      {model.items.map((item) => {
        if (item.kind === "deltaGroup") return renderDeltaGroup(item);
        if (item.kind === "eventGroup") return renderEventGroup(item);
        return renderEvent(item, onOpenArtifact);
      })}
    </div>
  );
}

function renderMetadata(events: readonly NormalizedEvent[]) {
  const schemaVersions = events
    .map((event) => schemaVersion(event))
    .filter((version): version is number => typeof version === "number");
  const summary = schemaVersions.length > 0 ? `schema v${schemaVersions[schemaVersions.length - 1]}` : `${events.length} ${events.length === 1 ? "entry" : "entries"}`;

  return (
    <EventDisclosure
      key="event-log-metadata"
      className="event-log-metadata"
      dataKnown={true}
      summary={
        <>
          <span className="event-id">meta</span>
          <span className="event-copy">
            <span className="event-kind-label">Metadata</span>
            <EventMeta meta={[summary]} />
          </span>
          <span className="event-badges" aria-hidden="true" />
        </>
      }
    >
      <div className="event-body">
        <div className="event-actions event-stream-actions">
          <span className="event-stream-stats">{events.length} {events.length === 1 ? "entry" : "entries"}</span>
          <CopyButton
            label="Copy metadata"
            value={JSON.stringify(events.map((event) => event.raw), null, 2)}
            className="event-action"
          />
        </div>
        <pre className="code">{JSON.stringify(events.map((event) => event.raw), null, 2)}</pre>
      </div>
    </EventDisclosure>
  );
}

function renderEvent(item: Extract<EventTraceItem, { kind: "event" }>, onOpenArtifact?: (path: string) => void) {
  const artifactPath = artifactPathForEvent(item.event);
  const evidence = toolResultEvidence(item.event);
  return (
    <EventDisclosure
      key={`${item.event.id}-${item.event.type}`}
      className={evidence ? "event-chip event-chip-evidence" : "event-chip"}
      dataKnown={item.event.known}
      summary={
        <>
          <span className="event-id">{item.event.id}</span>
          <span className="event-copy">
            <span className="event-kind-label">{item.display.label}</span>
            <EventMeta meta={item.display.meta} maxParts={item.event.type === "tool.request" ? 4 : 3} />
          </span>
          <span className="event-badges">
            {!item.event.known ? <span className="badge" data-kind="error">unclassified</span> : null}
            {item.display.badges.map((badge) => <span key={badge} className="badge" data-kind="schema">{badge}</span>)}
          </span>
          {evidence ? <ToolResultEvidenceView evidence={evidence} /> : null}
        </>
      }
    >
      <div className="event-body">
        <div className="event-actions">
          {artifactPath && onOpenArtifact ? (
            <button type="button" className="event-action" onClick={() => onOpenArtifact(artifactPath)}>
              Open artifact
            </button>
          ) : null}
          <CopyButton label="Copy event" value={JSON.stringify(item.event.raw, null, 2)} className="event-action" />
        </div>
        <EventDetailList details={item.display.meta} />
        <pre className="code">{JSON.stringify(item.event.raw, null, 2)}</pre>
      </div>
    </EventDisclosure>
  );
}

interface ToolResultEvidence {
  tone: "error" | "ok" | "source";
  label: string;
  summary: string;
  next?: string;
  facts: string[];
}

function ToolResultEvidenceView({ evidence }: { evidence: ToolResultEvidence }) {
  return (
    <span className="event-evidence-preview" data-tone={evidence.tone}>
      <span>{evidence.label}</span>
      <strong>{evidence.summary}</strong>
      {evidence.next ? <small><b>Next</b>{evidence.next}</small> : null}
      {evidence.facts.length > 0 ? (
        <span className="event-evidence-facts">
          {evidence.facts.map((fact) => <b key={fact}>{fact}</b>)}
        </span>
      ) : null}
    </span>
  );
}

function renderEventGroup(item: Extract<EventTraceItem, { kind: "eventGroup" }>) {
  const first = item.events[0];
  const last = item.events[item.events.length - 1];
  const idLabel = first.id === last.id ? `${first.id}` : `${first.id}-${last.id}`;

  return (
    <EventDisclosure
      key={`event-group-${item.key}-${first.id}-${last.id}`}
      className="event-chip event-chip-group"
      dataKnown={true}
      summary={
        <>
          <span className="event-id">{idLabel}</span>
          <span className="event-copy">
            <span className="event-kind-label">{item.label}</span>
            <EventMeta meta={item.meta} maxParts={6} />
          </span>
          <span className="event-badges">
            {item.badges.map((badge) => <span key={badge} className="badge" data-kind="schema">{badge}</span>)}
          </span>
        </>
      }
    >
      <div className="event-body">
        <div className="event-actions event-stream-actions">
          <span className="event-stream-stats">{item.events.length} events</span>
          <CopyButton
            label="Copy events"
            value={JSON.stringify(item.events.map((event) => event.raw), null, 2)}
            className="event-action"
          />
        </div>
        <EventDetailList details={item.meta} />
        <pre className="code">{JSON.stringify(item.events.map((event) => event.raw), null, 2)}</pre>
      </div>
    </EventDisclosure>
  );
}

function renderDeltaGroup(item: Extract<EventTraceItem, { kind: "deltaGroup" }>) {
  const first = item.events[0];
  const last = item.events[item.events.length - 1];
  const idLabel = first.id === last.id ? `${first.id}` : `${first.id}-${last.id}`;
  const meta = [
    item.turnLabel,
    streamSummary(item.text),
  ].filter((part): part is string => Boolean(part));

  return (
    <EventDisclosure
      key={`delta-${item.type}-${first.id}-${last.id}`}
      className="event-chip event-chip-group"
      dataKnown={true}
      summary={
        <>
          <span className="event-id">{idLabel}</span>
          <span className="event-copy">
            <span className="event-stream-label">{item.label}</span>
            <EventMeta meta={meta} />
          </span>
          <span className="event-badges" aria-hidden="true" />
        </>
      }
    >
      <div className="event-body">
        <div className="event-actions event-stream-actions">
          <span className="event-stream-stats">{item.updateCount} updates · {item.text.length} chars</span>
          <CopyButton
            label="Copy events"
            value={JSON.stringify(item.events.map((event) => event.raw), null, 2)}
            className="event-action"
          />
        </div>
        <pre className="code delta-preview">{item.text || "(empty delta stream)"}</pre>
        <details className="nested-raw event-group-raw">
          <summary>{item.events.length} trace entries</summary>
          <pre className="code">{JSON.stringify(item.events.map((event) => event.raw), null, 2)}</pre>
        </details>
      </div>
    </EventDisclosure>
  );
}

function EventMeta({ meta, maxParts = 3 }: { meta: readonly string[]; maxParts?: number }) {
  const compact = eventSummaryMeta(meta, maxParts);
  return (
    <span className="event-meta" title={meta.join(" · ")}>
      {compact || "Open for details"}
    </span>
  );
}

function EventDetailList({ details }: { details: readonly string[] }) {
  const visible = details.map((detail) => detail.trim()).filter(Boolean);
  if (visible.length === 0) return null;
  return (
    <div className="event-detail-list" aria-label="Event summary details">
      {visible.map((detail) => (
        <p key={detail}>{detail}</p>
      ))}
    </div>
  );
}

function eventSummaryMeta(meta: readonly string[], maxParts: number): string {
  const useful = meta
    .map((part) => compactEventMetaPart(part))
    .filter(Boolean)
    .slice(0, maxParts);
  return useful.join(" · ");
}

function compactEventMetaPart(part: string): string {
  const compact = part.replace(/\s+/g, " ").trim();
  if (!compact) return "";
  return compact.length > 112 ? `${compact.slice(0, 111)}...` : compact;
}

function EventDisclosure({
  className,
  dataKnown,
  summary,
  children,
}: {
  className: string;
  dataKnown: boolean;
  summary: ReactNode;
  children: ReactNode;
}) {
  const [open, setOpen] = useState(false);

  return (
    <details className={className} data-known={dataKnown} onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>{summary}</summary>
      {open ? children : null}
    </details>
  );
}

function schemaVersion(event: NormalizedEvent): number | undefined {
  const value = event.type === "trace.meta" && event.data && typeof event.data === "object"
    ? (event.data as { schema_version?: unknown }).schema_version
    : undefined;
  return typeof value === "number" ? value : undefined;
}

function artifactPathForEvent(event: NormalizedEvent): string | undefined {
  if (!event.data || typeof event.data !== "object") return undefined;
  const value = (event.data as { result_artifact_path?: unknown }).result_artifact_path;
  return typeof value === "string" && value.trim() ? value : undefined;
}

function toolResultEvidence(event: NormalizedEvent): ToolResultEvidence | undefined {
  if (event.type !== EventType.ToolResult || !event.data || typeof event.data !== "object") return undefined;
  const data = event.data as Record<string, unknown>;
  const exitCode = typeof data.exit_code === "number" ? data.exit_code : undefined;
  const failureKinds = [
    ...(Array.isArray(data.failure_kinds) ? data.failure_kinds.filter((value): value is string => typeof value === "string") : []),
    typeof data.failure_kind === "string" ? data.failure_kind : undefined,
  ].filter(Boolean);
  const failed = (exitCode != null && exitCode !== 0) || failureKinds.length > 0;
  const resultText = [
    typeof data.result_summary === "string" ? data.result_summary : undefined,
    typeof data.result === "string" && data.result !== data.result_summary ? data.result : undefined,
  ].filter(Boolean).join("\n");
  const sourceText = typeof data.result === "string" ? data.result : typeof data.result_summary === "string" ? data.result_summary : undefined;
  const sourceAccess = describeSourceAccess(sourceText);
  if (sourceAccess) {
    return {
      tone: "source",
      label: sourceEvidenceTitle(sourceEvidenceLabel(sourceAccess)),
      summary: sourceAccess.accessedUrl,
      facts: [
        sourceAccess.requestedUrl && sourceAccess.requestedUrl !== sourceAccess.accessedUrl ? `from ${sourceAccess.requestedUrl}` : undefined,
        sourceAccess.ref ? `ref ${sourceAccess.ref}` : undefined,
        sourceAccess.httpStatus ? `http ${sourceAccess.httpStatus}` : undefined,
        sourceAccess.contentType,
        sourceAccess.jsonPath ? `json ${sourceAccess.jsonPath}` : undefined,
        sourceAccess.resultPreview ? `preview ${streamSummary(sourceAccess.resultPreview)}` : undefined,
      ].filter((fact): fact is string => Boolean(fact)),
    };
  }
  const summary = cleanToolResultEvidenceSummary(resultText);
  const next = toolResultNextEvidence(resultText);
  const artifactPath = typeof data.result_artifact_path === "string" && data.result_artifact_path.trim() ? data.result_artifact_path : undefined;
  const facts = [
    exitCode != null ? `exit ${exitCode}` : undefined,
    typeof data.duration_ms === "number" ? formatEvidenceDuration(data.duration_ms) : undefined,
    artifactPath ? "artifact" : undefined,
  ].filter((fact): fact is string => Boolean(fact));

  if (!failed && !summary && !artifactPath) return undefined;
  return {
    tone: failed ? "error" : "ok",
    label: failed ? "Failure output" : "Result output",
    summary: summary || (failed ? "Tool call failed without a captured summary." : "Tool call completed."),
    next,
    facts,
  };
}

function sourceEvidenceTitle(value: string): string {
  return value
    .split(/\s+/)
    .map((part) => part ? `${part.charAt(0).toUpperCase()}${part.slice(1)}` : part)
    .join(" ");
}

function cleanToolResultEvidenceSummary(text: string): string {
  const lines = text
    .split(/\r?\n/)
    .map((line) => line.trim())
    .filter(Boolean)
    .filter((line) => !/^Next:/i.test(line))
    .filter((line) => !/^Failure:/i.test(line));
  const summary = lines[0] ?? "";
  return summary ? streamSummary(summary) : "";
}

function toolResultNextEvidence(text: string): string | undefined {
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const next = match?.[1]?.replace(/\s+/g, " ").trim();
  return next ? streamSummary(next) : undefined;
}

function formatEvidenceDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
}

function copyHistoryText(events: readonly NormalizedEvent[]): string {
  return events.map((event) => JSON.stringify(event.raw)).join("\n");
}
