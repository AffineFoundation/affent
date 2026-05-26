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
  return (
    <div className={className} data-testid={testId} aria-label={ariaLabel}>
      <div className="run-detail-inline">
        <span className="run-detail-line" data-tone={summarizeTone(visibleMetrics)}>
          {visibleText}
        </span>
        {overflowMetrics.length > 0 ? (
          <details className="run-detail-overflow">
            <summary aria-label={`${summaryLabel}: ${overflowMetrics.length} more ${metricNoun(overflowMetrics.length)}`}>
              +{overflowMetrics.length} more
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

function summarizeTone(metrics: readonly SessionOverviewMetric[]): SessionOverviewMetric["tone"] | undefined {
  return metrics.find((metric) => metric.tone === "error")?.tone
    ?? metrics.find((metric) => metric.tone === "warning")?.tone
    ?? metrics.find((metric) => metric.tone === "running")?.tone
    ?? metrics.find((metric) => metric.tone === "success")?.tone;
}

function metricNoun(count: number): string {
  return count === 1 ? "metric" : "metrics";
}
