import type { SessionOverview } from "../view/sessionOverview";

export function WorkflowStatus({
  overview,
}: {
  overview: SessionOverview;
}) {
  return (
    <section className="workflow-status" data-active={overview.active} data-tone={overview.tone} data-testid="workflow-status">
      <div className="workflow-line" aria-label="Current chat state">
        <span className="pulse-dot" data-status={dotStatus(overview)} />
        <div className="workflow-title">
          <h2>{overview.headline}</h2>
          <p>{overview.detail}</p>
        </div>
        <span className="state-pill" data-tone={overview.tone}>
          {overview.stateLabel}
        </span>
        {overview.metrics.length > 0 ? <WorkflowDetails metrics={overview.metrics} /> : null}
      </div>
    </section>
  );
}

function WorkflowDetails({ metrics }: { metrics: SessionOverview["metrics"] }) {
  return (
    <details className="workflow-details" data-testid="workflow-details">
      <summary>Run details</summary>
      <div className="workflow-metrics" aria-label="Run details">
        {metrics.map((metric) => (
          <span key={metric.label} data-tone={metric.tone}>
            <b>{metric.value}</b> {formatMetricLabel(metric.label, metric.value)}
          </span>
        ))}
      </div>
    </details>
  );
}

function formatMetricLabel(label: string, value: string): string {
  const normalized = label.toLowerCase();
  if (value === "1" && normalized.endsWith("s")) return normalized.slice(0, -1);
  return normalized;
}

function dotStatus(overview: SessionOverview): string {
  if (overview.active) return "running";
  if (overview.tone === "error") return "error";
  if (overview.tone === "warning") return "max_turns";
  if (overview.tone === "success") return "completed";
  return "cancelled";
}
