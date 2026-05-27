import type { ReactNode } from "react";

export function SessionPanelFrame({
  className,
  testId,
  embedded = false,
  defaultOpen = false,
  kicker,
  title,
  detail,
  children,
}: {
  className: string;
  testId: string;
  embedded?: boolean;
  defaultOpen?: boolean;
  kicker: string;
  title: string;
  detail: string;
  children: ReactNode;
}) {
  const header = (
    <>
      <span className="session-plan-kicker">{kicker}</span>
      <strong>{title}</strong>
      <span>{detail}</span>
    </>
  );

  if (embedded) {
    return (
      <section className={`${className} session-automation-section`} data-testid={testId} aria-label={kicker}>
        <div className="session-plan-summary session-automation-section-header">{header}</div>
        {children}
      </section>
    );
  }

  return (
    <details className={className} data-testid={testId} {...(defaultOpen ? { open: true } : {})}>
      <summary className="session-plan-summary">{header}</summary>
      {children}
    </details>
  );
}
