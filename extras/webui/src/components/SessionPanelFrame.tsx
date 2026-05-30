import type { ReactNode } from "react";

export function SessionPanelFrame({
  className,
  testId,
  embedded = false,
  defaultOpen = false,
  kicker,
  title,
  detail,
  embeddedHeader = "default",
  children,
}: {
  className: string;
  testId: string;
  embedded?: boolean;
  embeddedHeader?: "default" | "hidden";
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
      <section
        className={`${className} session-automation-section`}
        data-testid={testId}
        data-embedded-header={embeddedHeader}
        aria-label={kicker}
      >
        {embeddedHeader === "default" ? <div className="session-plan-summary session-automation-section-header">{header}</div> : null}
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
