import { type ReactNode, useState } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { buildEventTraceModel, streamSummary, type EventTraceItem } from "../view/eventTrace";
import { CopyButton } from "./CopyButton";

export function EventTrace({ events, onOpenArtifact }: { events: readonly NormalizedEvent[]; onOpenArtifact?: (path: string) => void }) {
  const model = buildEventTraceModel(events);

  return (
    <div className="event-trace" data-testid="event-trace">
      {events.length > 0 ? (
        <div className="event-trace-actions">
          <span className="event-trace-count">{events.length} trace entries</span>
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
            <span className="event-meta">{summary}</span>
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
  return (
    <EventDisclosure
      key={`${item.event.id}-${item.event.type}`}
      className="event-chip"
      dataKnown={item.event.known}
      summary={
        <>
          <span className="event-id">{item.event.id}</span>
          <span className="event-copy">
            <span className="event-kind-label">{item.display.label}</span>
            <span className="event-meta">{item.display.meta.join(" · ")}</span>
          </span>
          <span className="event-badges">
            {!item.event.known ? <span className="badge" data-kind="error">unclassified</span> : null}
            {item.display.badges.map((badge) => <span key={badge} className="badge" data-kind="schema">{badge}</span>)}
          </span>
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
        <pre className="code">{JSON.stringify(item.event.raw, null, 2)}</pre>
      </div>
    </EventDisclosure>
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
            <span className="event-meta">{item.meta.join(" · ")}</span>
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
  ].filter(Boolean).join(" · ");

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
            <span className="event-meta">{meta}</span>
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

function copyHistoryText(events: readonly NormalizedEvent[]): string {
  return events.map((event) => JSON.stringify(event.raw)).join("\n");
}
