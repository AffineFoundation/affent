import type { ReactNode } from "react";

export function SessionAutomationPanel({
  title,
  detail,
  defaultOpen = false,
  testId = "session-automation-panel",
  children,
}: {
  title: string;
  detail: string;
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
        {children}
      </div>
    </details>
  );
}
