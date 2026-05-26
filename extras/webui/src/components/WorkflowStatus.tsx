import type { SessionOverview } from "../view/sessionOverview";
import { RunDetails } from "./RunDetails";

export function WorkflowStatus({
  overview,
}: {
  overview: SessionOverview;
}) {
  const artifactMetric = overview.metrics.find((metric) => metric.label === "Artifact" || metric.label === "Artifacts");
  return (
    <details className="workflow-status" data-active={overview.active} data-tone={overview.tone} data-testid="workflow-status">
      <summary className="workflow-line">
        <span className="pulse-dot" data-status={dotStatus(overview)} />
        <div className="workflow-title">
          <h2>{overview.headline}</h2>
        </div>
        <span className="state-pill" data-tone={overview.tone}>
          {overview.stateLabel}
        </span>
        {artifactMetric ? (
          <span className="state-pill" data-tone="artifact">
            {artifactMetric.value}
          </span>
        ) : null}
      </summary>
      <div className="workflow-status-body">
        <p>{overview.detail}</p>
        <RunDetails
          metrics={overview.metrics}
          className="workflow-details"
          testId="workflow-details"
          ariaLabel="Session metrics"
          summaryLabel="Work metrics"
          inlineLimit={1}
        />
      </div>
    </details>
  );
}

function dotStatus(overview: SessionOverview): string {
  if (overview.active) return "running";
  if (overview.tone === "error") return "error";
  if (overview.tone === "warning") return "warning";
  if (overview.tone === "success") return "completed";
  return "cancelled";
}
