import type { SessionSchedulesSummary } from "../api/sessions";

export function SessionSchedulePanel({
  summary,
  busy,
  onScheduleCheckIn,
  onScheduleDaily,
}: {
  summary?: SessionSchedulesSummary;
  busy?: "checkin" | "daily";
  onScheduleCheckIn?: () => Promise<void> | void;
  onScheduleDaily?: () => Promise<void> | void;
}) {
  const count = summary?.count ?? 0;
  const enabled = summary?.enabled ?? 0;
  const next = summary?.next_run_at ? formatScheduleTime(summary.next_run_at) : undefined;
  const preview = compact(summary?.next_prompt_preview);
  const title = enabled > 0 ? `${enabled} active` : count > 0 ? `${count} paused` : "None";
  const detail = next ? `Next ${next}${preview ? ` · ${preview}` : ""}` : "No scheduled prompts";

  return (
    <details className="session-plan-panel session-schedule-panel" data-testid="session-schedule-panel" open={count === 0}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Timers</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body session-schedule-body">
        <div className="session-schedule-grid">
          <ScheduleField label="Enabled" value={String(enabled)} />
          <ScheduleField label="Total" value={String(count)} />
          {next ? <ScheduleField label="Next" value={next} /> : null}
        </div>
        {preview ? <p className="session-loop-preview">{preview}</p> : null}
        <div className="session-loop-actions">
          {onScheduleCheckIn ? (
            <button
              type="button"
              className="ghost-action"
              disabled={!!busy}
              onClick={() => void onScheduleCheckIn()}
            >
              {busy === "checkin" ? "Scheduling" : "1h check-in"}
            </button>
          ) : null}
          {onScheduleDaily ? (
            <button
              type="button"
              className="ghost-action"
              disabled={!!busy}
              onClick={() => void onScheduleDaily()}
            >
              {busy === "daily" ? "Scheduling" : "Daily check-in"}
            </button>
          ) : null}
        </div>
      </div>
    </details>
  );
}

function ScheduleField({ label, value }: { label: string; value: string }) {
  return (
    <div className="session-loop-field">
      <span>{label}</span>
      <strong>{value}</strong>
    </div>
  );
}

function formatScheduleTime(value: string): string {
  const time = Date.parse(value);
  if (!Number.isFinite(time)) return value;
  return new Intl.DateTimeFormat(undefined, {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  }).format(new Date(time));
}

function compact(value?: string): string | undefined {
  const next = value?.replace(/\s+/g, " ").trim();
  return next || undefined;
}
