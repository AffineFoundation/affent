import type { SessionOverviewMetric } from "../view/sessionOverview";

export function RunDetails({
  metrics,
  className,
  testId,
  ariaLabel = "Activity metrics",
  summaryLabel = "Metrics",
  valueFirst = false,
  inlineLimit = 3,
}: {
  metrics: readonly SessionOverviewMetric[];
  className: string;
  testId: string;
  ariaLabel?: string;
  summaryLabel?: string;
  valueFirst?: boolean;
  inlineLimit?: number;
}) {
  if (metrics.length === 0) return null;
  const visibleMetrics = metrics.slice(0, inlineLimit);
  const overflowMetrics = metrics.slice(inlineLimit);
  return (
    <div className={className} data-testid={testId} aria-label={ariaLabel}>
      <div className="run-detail-inline">
        {visibleMetrics.map((metric) => (
          <MetricChip key={`${metric.label}-${metric.value}`} metric={metric} valueFirst={valueFirst} />
        ))}
        {overflowMetrics.length > 0 ? (
          <details className="run-detail-overflow">
            <summary aria-label={`${summaryLabel}: ${overflowMetrics.length} more`}>
              +{overflowMetrics.length}
            </summary>
            <div className="run-detail-metrics" aria-label={ariaLabel}>
              {overflowMetrics.map((metric) => (
                <MetricChip key={`${metric.label}-${metric.value}`} metric={metric} valueFirst={valueFirst} />
              ))}
            </div>
          </details>
        ) : null}
      </div>
    </div>
  );
}

function MetricChip({
  metric,
  valueFirst,
}: {
  metric: SessionOverviewMetric;
  valueFirst: boolean;
}) {
  return (
    <span data-tone={metric.tone}>
      {valueFirst ? (
        <>
          <b>{metric.value}</b> {formatMetricLabel(metric.label, metric.value)}
        </>
      ) : (
        <>
          {metric.label} {metric.value}
        </>
      )}
    </span>
  );
}

function formatMetricLabel(label: string, value: string): string {
  const normalized = label.toLowerCase();
  if (value === "1" && normalized.endsWith("s")) return normalized.slice(0, -1);
  return normalized;
}
