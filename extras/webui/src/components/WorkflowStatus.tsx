import { displaySessionOverviewMetrics, type SessionOverview, type SessionOverviewMetric } from "../view/sessionOverview";
import type { UseAsDraft } from "../view/draftSource";
import { RunDetails } from "./RunDetails";

export function WorkflowStatus({
  overview,
  onUseAsDraft,
}: {
  overview: SessionOverview;
  onUseAsDraft?: UseAsDraft;
}) {
  const metrics = displaySessionOverviewMetrics(overview.metrics);
  const pinnedMetrics = pinnedWorkflowMetrics(metrics);
  const recoveryMetric = recoveryWorkflowMetric(metrics);
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
        {recoveryMetric && onUseAsDraft ? (
          <button
            type="button"
            className="secondary-action workflow-recovery-action"
            onClick={() => onUseAsDraft(recoveryDraft(recoveryMetric), "tool_guidance")}
          >
            Use recovery
          </button>
        ) : null}
        <RunDetails
          metrics={metrics}
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

function recoveryWorkflowMetric(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric | undefined {
  return metrics.find((metric) => metric.label === "Recovery" && metric.value.trim() !== "");
}

function recoveryDraft(metric: SessionOverviewMetric): string {
  return `Continue: ${metric.value.trim()}`;
}

function pinnedWorkflowMetrics(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric[] {
  const pinned: SessionOverviewMetric[] = [];
  const context = metrics.find((metric) => metric.label === "Context");
  const loop = metrics.find((metric) => metric.label === "Loop");
  const memory = metrics.find((metric) => metric.label === "Memory");
  const recall = metrics.find((metric) => metric.label === "Recall");
  const compaction = metrics.find((metric) => metric.label === "Compaction" || metric.label === "Compactions");
  const artifact = metrics.find((metric) => metric.label === "Artifact" || metric.label === "Artifacts");
  if (context && context.tone) pinned.push(context);
  if (loop) pinned.push(loop);
  if (memory) pinned.push(memory);
  if (recall) pinned.push(recall);
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
