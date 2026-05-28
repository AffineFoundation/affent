import type { NormalizedEvent } from "../normalize/normalizeEvent";
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
            <div className="session-trace-metrics" data-testid="session-trace-metrics">
              <span><strong>Entries</strong>{trace.eventCount}</span>
              <span><strong>Records</strong>{trace.recordCount}</span>
              {trace.schemaVersion ? <span><strong>Schema</strong>v{trace.schemaVersion}</span> : null}
              {trace.unknownCount > 0 ? <span data-tone="warning"><strong>Unclassified</strong>{trace.unknownCount}</span> : null}
            </div>
            {trace.latest ? (
              <div className="session-trace-latest" data-testid="session-trace-latest">
                <strong>{trace.latest.label}</strong>
                <span>{trace.latest.detail}</span>
              </div>
            ) : null}
            <EventTrace events={events} onOpenArtifact={onOpenArtifact} />
          </>
        ) : (
          <div className="session-skills-empty" data-testid="session-trace-empty">No persisted trace loaded for this chat.</div>
        )}
      </div>
    </details>
  );
}
