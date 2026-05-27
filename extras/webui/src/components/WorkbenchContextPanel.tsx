import { displaySessionOverviewMetrics, type SessionOverview } from "../view/sessionOverview";
import { RunDetails } from "./RunDetails";

export function WorkbenchContextPanel({
  overview,
  hasSelectedSession,
  automationTitle,
  automationDetail,
  defaultOpen = false,
}: {
  overview: SessionOverview;
  hasSelectedSession: boolean;
  automationTitle?: string;
  automationDetail?: string;
  defaultOpen?: boolean;
}) {
  const metrics = displaySessionOverviewMetrics(overview.metrics);
  const summary = contextSummary(overview, hasSelectedSession);
  const detail = hasSelectedSession ? overview.headline : "No chat selected";

  return (
    <details className="session-skills-panel workbench-context-panel" data-testid="workbench-context-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Context</span>
        <strong>{summary}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="workbench-context-status" data-tone={overview.tone} data-testid="workbench-context-status">
          <div>
            <strong>{overview.headline}</strong>
            <span>{overview.detail}</span>
          </div>
          <span className="state-pill" data-tone={overview.tone}>
            {overview.stateLabel}
          </span>
        </div>
        <RunDetails
          metrics={metrics}
          className="workbench-context-details"
          testId="workbench-context-details"
          ariaLabel="Workbench context metrics"
          summaryLabel="Context metrics"
          inlineLimit={2}
        />
        {automationTitle ? (
          <div className="workbench-context-link" data-testid="workbench-context-automation">
            <strong>Automation</strong>
            <span>{automationTitle}</span>
            {automationDetail ? <small>{automationDetail}</small> : null}
          </div>
        ) : null}
        {!hasSelectedSession && metrics.length === 0 ? (
          <div className="session-skills-empty">Start a task or open a saved chat to inspect run evidence, changes, memory, and automation.</div>
        ) : null}
      </div>
    </details>
  );
}

function contextSummary(overview: SessionOverview, hasSelectedSession: boolean): string {
  if (overview.active) return overview.stateLabel;
  if (!hasSelectedSession) return "Fresh task";
  if (overview.tone === "error") return "Needs attention";
  if (overview.tone === "warning") return "Review needed";
  return "Chat ready";
}
