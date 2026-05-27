import type { SessionPlanSummary } from "../api/sessions";

export function SessionPlanPanel({
  plan,
  summary,
  loading = false,
  error,
}: {
  plan?: unknown;
  summary?: SessionPlanSummary;
  loading?: boolean;
  error?: string;
}) {
  if (!summary && !loading && !error) return null;
  const steps = normalizedSteps(plan);
  const open = summary?.active || summary?.blocked || steps.length > 0;
  const title = summary
    ? `${summary.completed_steps}/${summary.total_steps} complete`
    : loading
      ? "Loading plan"
      : "Plan unavailable";
  const detail = planDetail(summary, steps.length);

  return (
    <details className="session-plan-panel" data-testid="session-plan-panel" open={open}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Plan</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body">
        {loading ? <div className="session-plan-empty">Loading plan...</div> : null}
        {!loading && error ? (
          <div className="session-plan-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {!loading && !error && steps.length > 0 ? (
          <ol className="session-plan-steps">
            {steps.map((step, index) => (
              <li key={`${index}:${step.text}`} className="session-plan-step" data-status={step.status}>
                <span className="session-plan-step-index">{index + 1}</span>
                <div className="session-plan-step-main">
                  <div className="session-plan-step-head">
                    <strong>{step.text}</strong>
                    <span className="session-plan-status">{statusLabel(step.status)}</span>
                  </div>
                  {step.note ? <p>{step.note}</p> : null}
                  {step.evidence.length > 0 ? (
                    <div className="session-plan-evidence">
                      {step.evidence.map((item) => (
                        <code key={item}>{item}</code>
                      ))}
                    </div>
                  ) : null}
                </div>
              </li>
            ))}
          </ol>
        ) : null}
        {!loading && !error && steps.length === 0 ? <div className="session-plan-empty">No active plan steps.</div> : null}
      </div>
    </details>
  );
}

interface NormalizedPlanStep {
  text: string;
  status: string;
  evidence: string[];
  note?: string;
}

function normalizedSteps(plan?: unknown): NormalizedPlanStep[] {
  const steps = isPlanSnapshot(plan) && Array.isArray(plan.steps) ? plan.steps : [];
  return steps
    .map(normalizedStep)
    .filter((step): step is NormalizedPlanStep => !!step);
}

function normalizedStep(step: unknown): NormalizedPlanStep | undefined {
  if (!step || typeof step !== "object") return undefined;
  const record = step as Record<string, unknown>;
  const text = compact(readString(record.text) ?? readString(record.step) ?? "");
  if (!text) return undefined;
  const status = normalizeStatus(readString(record.status));
  const evidence = Array.isArray(record.evidence) ? record.evidence.map((item) => compact(readString(item) ?? "")).filter(Boolean) : [];
  const note = compact(readString(record.note) ?? "");
  return { text, status, evidence, note: note || undefined };
}

function isPlanSnapshot(value: unknown): value is { steps?: unknown[] } {
  return !!value && typeof value === "object";
}

function readString(value: unknown): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function normalizeStatus(status?: string): string {
  const value = status?.trim().toLowerCase();
  if (value === "in_progress" || value === "completed" || value === "blocked" || value === "pending") return value;
  return "pending";
}

function statusLabel(status: string): string {
  if (status === "in_progress") return "Active";
  if (status === "completed") return "Done";
  if (status === "blocked") return "Blocked";
  return "Pending";
}

function planDetail(summary: SessionPlanSummary | undefined, stepCount: number): string {
  if (!summary) return stepCount > 0 ? `${stepCount} steps` : "No plan loaded";
  if (summary.error) return "Plan could not be read";
  if (summary.done) return "All steps completed";
  if (summary.blocked_step_index) return `Step ${summary.blocked_step_index} blocked`;
  if (summary.current_step_index) return `Step ${summary.current_step_index} ${statusLabel(summary.current_step_status ?? "pending").toLowerCase()}`;
  return `${summary.total_steps} ${summary.total_steps === 1 ? "step" : "steps"}`;
}

function compact(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}
