import { useMemo, useState } from "react";
import { EventType, type ToolResultPayload } from "../api/events";
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
  const [issueOnly, setIssueOnly] = useState(false);
  const trimmedQuery = query.trim();
  const visibleEvents = useMemo(
    () => {
      const source = issueOnly ? filterToolIssueEvents(events) : events;
      return trimmedQuery ? filterEventTraceEvents(source, trimmedQuery) : source;
    },
    [events, issueOnly, trimmedQuery],
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
                <button
                  type="button"
                  className="session-trace-filter"
                  aria-pressed={issueOnly}
                  disabled={trace.toolIssueCount === 0}
                  onClick={() => setIssueOnly((enabled) => !enabled)}
                >
                  Tool issues{trace.toolIssueCount > 0 ? ` ${trace.toolIssueCount}` : ""}
                </button>
              </div>
            ) : null}
            <div className="session-trace-metrics" data-testid="session-trace-metrics">
              <span><strong>Entries</strong>{trace.eventCount}</span>
              <span><strong>Records</strong>{trace.recordCount}</span>
              {trace.toolIssueCount > 0 ? <span data-tone="error"><strong>Tool issues</strong>{trace.toolIssueCount}</span> : null}
              {trimmedQuery ? <span><strong>Matching</strong>{visibleEvents.length}</span> : null}
              {issueOnly ? <span><strong>Filter</strong>Tool issues</span> : null}
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
              <div className="session-skills-empty">No trace entries matching {issueOnly ? "tool issues" : `"${trimmedQuery}"`}.</div>
            )}
          </>
        ) : (
          <div className="session-skills-empty" data-testid="session-trace-empty">No persisted trace loaded for this chat.</div>
        )}
      </div>
    </details>
  );
}

function filterToolIssueEvents(events: readonly NormalizedEvent[]): NormalizedEvent[] {
  const failedCallIds = new Set<string>();
  for (const event of events) {
    if (event.type !== EventType.ToolResult) continue;
    const data = event.data as ToolResultPayload;
    if ((data.exit_code ?? 0) !== 0 || data.failure_kind || data.failure_kinds?.length) failedCallIds.add(data.call_id);
  }
  return events.filter((event) => {
    if (event.type === EventType.ToolRequest || event.type === EventType.ToolResult) {
      const data = event.data as { call_id?: unknown };
      const callId = typeof data.call_id === "string" ? data.call_id : "";
      return failedCallIds.has(callId);
    }
    return false;
  });
}
