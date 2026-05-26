import type { SessionOverview, SessionOverviewMetric } from "../view/sessionOverview";
import { RunDetails } from "./RunDetails";

export function WorkflowStatus({
  overview,
}: {
  overview: SessionOverview;
}) {
  const pinnedMetrics = pinnedWorkflowMetrics(overview.metrics);
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
        {pinnedMetrics.map((metric) => (
          <span key={`${metric.label}:${metric.value}`} className="state-pill" data-tone={pinnedMetricTone(metric)}>
            {metric.value}
          </span>
        ))}
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

function pinnedWorkflowMetrics(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric[] {
  const pinned: SessionOverviewMetric[] = [];
  const context = metrics.find((metric) => metric.label === "Context");
  const compaction = metrics.find((metric) => metric.label === "Compaction" || metric.label === "Compactions");
  const artifact = metrics.find((metric) => metric.label === "Artifact" || metric.label === "Artifacts");
  if (context && context.tone) pinned.push(context);
  if (compaction) pinned.push(compaction);
  if (artifact) pinned.push(artifact);
  return pinned.slice(0, 2);
}

function pinnedMetricTone(metric: SessionOverviewMetric): SessionOverviewMetric["tone"] | "artifact" | undefined {
  if (metric.label === "Artifact" || metric.label === "Artifacts") return "artifact";
  return metric.tone;
}

function dotStatus(overview: SessionOverview): string {
  if (overview.active) return "running";
  if (overview.tone === "error") return "error";
  if (overview.tone === "warning") return "warning";
  if (overview.tone === "success") return "completed";
  return "cancelled";
}
