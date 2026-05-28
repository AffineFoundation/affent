import type { ReactNode } from "react";

export interface SessionAutomationMetric {
  label: string;
  value: string;
  detail?: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export function SessionAutomationPanel({
  title,
  detail,
  metrics = [],
  defaultOpen = false,
  testId = "session-automation-panel",
  children,
}: {
  title: string;
  detail: string;
  metrics?: readonly SessionAutomationMetric[];
  defaultOpen?: boolean;
  testId?: string;
  children: ReactNode;
}) {
  return (
    <details className="session-plan-panel session-automation-panel" data-testid={testId} {...(defaultOpen ? { open: true } : {})}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Automation</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body session-automation-body">
        {metrics.length > 0 ? (
          <div className="session-automation-dashboard" data-testid="session-automation-dashboard">
            {metrics.map((metric) => (
              <div key={metric.label} className="session-automation-metric" data-tone={metric.tone ?? "neutral"}>
                <span>{metric.label}</span>
                <strong>{metric.value}</strong>
                {metric.detail ? <small>{metric.detail}</small> : null}
              </div>
            ))}
          </div>
        ) : null}
        {children}
      </div>
    </details>
  );
}
