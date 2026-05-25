import type { SessionOverviewMetric } from "../view/sessionOverview";

export function RunDetails({
  metrics,
  className,
  testId,
  ariaLabel = "Work metrics",
  summaryLabel = "Metrics",
  valueFirst = false,
}: {
  metrics: readonly SessionOverviewMetric[];
  className: string;
  testId: string;
  ariaLabel?: string;
  summaryLabel?: string;
  valueFirst?: boolean;
}) {
  if (metrics.length === 0) return null;
  return (
    <details className={className} data-testid={testId}>
      <summary aria-label={ariaLabel}>{summaryLabel}</summary>
      <div className="run-detail-metrics" aria-label={ariaLabel}>
        {metrics.map((metric) => (
          <span key={`${metric.label}-${metric.value}`} data-tone={metric.tone}>
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
