import { useState, type ReactNode } from "react";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { buildEventTraceModel } from "../view/eventTrace";
import { EventTrace } from "./EventTrace";

export function TraceDisclosure({
  events,
  className,
  label = "Trace",
  children,
}: {
  events: readonly NormalizedEvent[];
  className: string;
  label?: string;
  children?: ReactNode;
}) {
  const [open, setOpen] = useState(false);
  const model = buildEventTraceModel(events);
  const recordCount = model.items.length + (model.metadata.length > 0 ? 1 : 0);

  return (
    <details className={className} onToggle={(event) => setOpen(event.currentTarget.open)}>
      <summary>
        <span>{label}</span>
        <span className="subtle-count">{recordCountLabel(recordCount)}</span>
      </summary>
      {open ? (
        <>
          {children}
          <EventTrace events={events} />
        </>
      ) : null}
    </details>
  );
}

function recordCountLabel(count: number): string {
  return `${count} record${count === 1 ? "" : "s"}`;
}
