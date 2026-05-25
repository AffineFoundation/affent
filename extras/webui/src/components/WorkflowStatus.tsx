import type { SessionOverview } from "../view/sessionOverview";
import { RunDetails } from "./RunDetails";

export function WorkflowStatus({
  overview,
}: {
  overview: SessionOverview;
}) {
  return (
    <section className="workflow-status" data-active={overview.active} data-tone={overview.tone} data-testid="workflow-status">
      <div className="workflow-line" aria-label="Current chat state">
        <span className="pulse-dot" data-status={dotStatus(overview)} />
        <div className="workflow-title">
          <h2>{overview.headline}</h2>
          <p>{overview.detail}</p>
        </div>
        <span className="state-pill" data-tone={overview.tone}>
          {overview.stateLabel}
        </span>
        <RunDetails
          metrics={overview.metrics}
          className="workflow-details"
          testId="workflow-details"
          valueFirst
        />
      </div>
    </section>
  );
}

function dotStatus(overview: SessionOverview): string {
  if (overview.active) return "running";
  if (overview.tone === "error") return "error";
  if (overview.tone === "warning") return "max_turns";
  if (overview.tone === "success") return "completed";
  return "cancelled";
}
