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

export interface SessionAutomationQueueItem {
  id: string;
  label: string;
  title: string;
  detail: string;
  meta?: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export function SessionAutomationPanel({
  title,
  detail,
  metrics = [],
  focus,
  queue = [],
  actions,
  defaultOpen = false,
  testId = "session-automation-panel",
  children,
}: {
  title: string;
  detail: string;
  metrics?: readonly SessionAutomationMetric[];
  focus?: SessionAutomationFocus;
  queue?: readonly SessionAutomationQueueItem[];
  actions?: ReactNode;
  defaultOpen?: boolean;
  testId?: string;
  children: ReactNode;
}) {
  const visibleQueue = queue.filter((item) => !automationQueueItemCoveredByFocus(item, focus));
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
        <div className="session-automation-details" data-testid="session-automation-details">
          {children}
        </div>
        {visibleQueue.length > 0 ? (
          <section className="session-automation-queue" data-testid="session-automation-queue" aria-label="Automation execution queue">
            <header>
              <span>Execution queue</span>
              <strong>{visibleQueue.length} {visibleQueue.length === 1 ? "item" : "items"}</strong>
            </header>
            <ol>
              {visibleQueue.map((item) => (
                <li key={item.id} data-tone={item.tone ?? "neutral"}>
                  <div className="session-automation-queue-main">
                    <span>{item.label}</span>
                    <strong>{item.title}</strong>
                    <p>{item.detail}</p>
                  </div>
                  {item.meta ? <code>{item.meta}</code> : null}
                </li>
              ))}
            </ol>
          </section>
        ) : null}
      </div>
    </details>
  );
}

function automationQueueItemCoveredByFocus(item: SessionAutomationQueueItem, focus?: SessionAutomationFocus): boolean {
  if (!focus) return false;
  if (focus.action === "answer" && item.id === "loop-calibration") return true;
  if (focus.action === "review" && item.id === "loop-review") return true;
  return item.title === focus.title && item.detail === focus.detail;
}
