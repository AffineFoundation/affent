import { useMemo, useState } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { filterEventTraceEvents } from "../view/eventTrace";
import {
  sessionTraceDraft,
  sessionTraceEvidenceText,
  type SessionTraceView,
} from "../view/sessionTrace";
import type { DraftSource } from "../view/draftSource";
import { CopyButton } from "./CopyButton";
import { EventTrace } from "./EventTrace";

export function SessionTracePanel({
  trace,
  events,
  defaultOpen = false,
  onOpenArtifact,
  onUseAsDraft,
}: {
  trace: SessionTraceView;
  events: readonly NormalizedEvent[];
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: (draft: string, source: DraftSource) => void;
}) {
  const [query, setQuery] = useState("");
  const trimmedQuery = query.trim();
  const visibleEvents = useMemo(
    () => (trimmedQuery ? filterEventTraceEvents(events, trimmedQuery) : events),
    [events, trimmedQuery],
  );

  return (
    <details className="session-skills-panel session-trace-panel" data-testid="session-trace-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Trace</span>
        <strong>{trace.summary}</strong>
        <span>{trace.detail}</span>
      </summary>
      <div className="session-skills-body session-trace-body">
        {trace.eventCount > 0 ? (
          <>
            <div className="session-trace-actions">
              <CopyButton label="Copy trace evidence" value={sessionTraceEvidenceText(trace)} className="node-action" />
              {onUseAsDraft ? (
                <button type="button" className="node-action" onClick={() => onUseAsDraft(sessionTraceDraft(trace), "trace")}>
                  Use trace as draft
                </button>
              ) : null}
            </div>
            {events.length > 1 ? (
              <div className="session-skills-controls">
                <label className="session-skills-search">
                  <span>Search trace</span>
                  <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="event type, tool, turn, path, or raw payload" />
                </label>
                {trimmedQuery ? (
                  <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                    Clear
                  </button>
                ) : null}
              </div>
            ) : null}
            <div className="session-trace-metrics" data-testid="session-trace-metrics">
              <span><strong>Entries</strong>{trace.eventCount}</span>
              <span><strong>Records</strong>{trace.recordCount}</span>
              {trimmedQuery ? <span><strong>Matching</strong>{visibleEvents.length}</span> : null}
              {trace.schemaVersion ? <span><strong>Schema</strong>v{trace.schemaVersion}</span> : null}
              {trace.unknownCount > 0 ? <span data-tone="warning"><strong>Unclassified</strong>{trace.unknownCount}</span> : null}
            </div>
            {!trimmedQuery && trace.latest ? (
              <div className="session-trace-latest" data-testid="session-trace-latest">
                <strong>{trace.latest.label}</strong>
                <span>{trace.latest.detail}</span>
              </div>
            ) : null}
            {visibleEvents.length > 0 ? (
              <EventTrace events={visibleEvents} onOpenArtifact={onOpenArtifact} />
            ) : (
              <div className="session-skills-empty">No trace entries matching "{trimmedQuery}".</div>
            )}
          </>
        ) : (
          <div className="session-skills-empty" data-testid="session-trace-empty">No persisted trace loaded for this chat.</div>
        )}
      </div>
    </details>
  );
}
