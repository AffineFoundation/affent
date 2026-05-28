import type { ReactNode } from "react";

export interface SessionAutomationMetric {
  label: string;
  value: string;
  detail?: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export interface SessionAutomationFocus {
  label: string;
  title: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
  action?: "answer" | "review";
}

export function SessionAutomationPanel({
  title,
  detail,
  metrics = [],
  focus,
  actions,
  defaultOpen = false,
  testId = "session-automation-panel",
  children,
}: {
  title: string;
  detail: string;
  metrics?: readonly SessionAutomationMetric[];
  focus?: SessionAutomationFocus;
  actions?: ReactNode;
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
        {focus || actions ? (
          <section className="session-automation-focus" data-tone={focus?.tone ?? "neutral"} data-testid="session-automation-focus" aria-label="Automation focus">
            {focus ? (
              <div className="session-automation-focus-main">
                <span>{focus.label}</span>
                <strong>{focus.title}</strong>
                <small>{focus.detail}</small>
              </div>
            ) : null}
            {actions ? <div className="session-automation-focus-actions">{actions}</div> : null}
          </section>
        ) : null}
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
