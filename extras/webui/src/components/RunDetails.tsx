import type { SessionOverviewMetric } from "../view/sessionOverview";

export function RunDetails({
  metrics,
  className,
  testId,
  ariaLabel = "Activity metrics",
  summaryLabel = "Metrics",
  inlineLimit = 3,
}: {
  metrics: readonly SessionOverviewMetric[];
  className: string;
  testId: string;
  ariaLabel?: string;
  summaryLabel?: string;
  inlineLimit?: number;
}) {
  if (metrics.length === 0) return null;
  const visibleMetrics = metrics.slice(0, inlineLimit);
  const overflowMetrics = metrics.slice(inlineLimit);
  const visibleText = visibleMetrics.map(formatMetric).join(" · ");
  const overflowLabel = overflowSummaryLabel(overflowMetrics);
  const overflowAria = overflowMetrics.map(formatMetric).join(" · ");
  return (
    <div className={className} data-testid={testId} aria-label={ariaLabel}>
      <div className="run-detail-inline">
        <span className="run-detail-line" data-tone={summarizeTone(visibleMetrics)}>
          {visibleText}
        </span>
        {overflowMetrics.length > 0 ? (
          <details className="run-detail-overflow">
            <summary aria-label={`${summaryLabel}: ${overflowAria}`} title={overflowAria}>
              {overflowLabel}
            </summary>
            <div className="run-detail-metrics" aria-label={ariaLabel}>
              <span className="run-detail-line" data-tone={summarizeTone(overflowMetrics)}>
                {overflowMetrics.map(formatMetric).join(" · ")}
              </span>
            </div>
          </details>
        ) : null}
      </div>
    </div>
  );
}

function formatMetric(metric: SessionOverviewMetric): string {
  return `${metric.label} ${metric.value}`;
}

function overflowSummaryLabel(metrics: readonly SessionOverviewMetric[]): string {
  if (metrics.length === 1) return formatMetric(metrics[0]);
  const visible = metrics.slice(0, 2).map((metric) => metric.label);
  const remaining = metrics.length - visible.length;
  return remaining > 0 ? `${visible.join(", ")} +${remaining}` : visible.join(", ");
}

function summarizeTone(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric["tone"] | undefined {
  return metrics.find((metric) => metric.tone === "error")?.tone
    ?? metrics.find((metric) => metric.tone === "warning")?.tone
    ?? metrics.find((metric) => metric.tone === "running")?.tone
    ?? metrics.find((metric) => metric.tone === "success")?.tone;
}
