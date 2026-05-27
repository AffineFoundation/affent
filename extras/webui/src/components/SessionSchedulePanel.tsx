import type { SessionSchedule, SessionSchedulesSummary } from "../api/sessions";

export function SessionSchedulePanel({
  summary,
  schedules,
  busy,
  disabled = false,
  loading = false,
  error,
  deletingId,
  updatingId,
  loopStatus,
  onLoadSchedules,
  onUpdateSchedule,
  onDeleteSchedule,
  onScheduleLoopTick,
  onScheduleCheckIn,
  onScheduleDaily,
}: {
  summary?: SessionSchedulesSummary;
  schedules?: SessionSchedule[];
  busy?: "loop" | "checkin" | "daily";
  disabled?: boolean;
  loading?: boolean;
  error?: string;
  deletingId?: string;
  updatingId?: string;
  loopStatus?: string;
  onLoadSchedules?: () => Promise<void> | void;
  onUpdateSchedule?: (scheduleId: string, enabled: boolean) => Promise<void> | void;
  onDeleteSchedule?: (scheduleId: string) => Promise<void> | void;
  onScheduleLoopTick?: () => Promise<void> | void;
  onScheduleCheckIn?: () => Promise<void> | void;
  onScheduleDaily?: () => Promise<void> | void;
}) {
  const count = summary?.count ?? 0;
  const enabled = summary?.enabled ?? 0;
  const next = summary?.next_run_at ? formatScheduleTime(summary.next_run_at) : undefined;
  const preview = compact(summary?.next_prompt_preview);
  const lastError = compact(summary?.last_error);
  const pendingLoopTimers = pendingLoopTimerCount(schedules, summary, loopStatus);
  const title = pendingLoopTimers > 0 ? `${pendingLoopTimers} pending` : enabled > 0 ? `${enabled} active` : count > 0 ? `${count} paused` : "None";
  const detail = lastError
    ? `${summary?.error_count ?? 1} error${summary?.error_count === 1 ? "" : "s"} · ${lastError}`
    : pendingLoopTimers > 0 ? "Loop timer waits for LOOP.md activation"
    : next ? `Next ${next}${preview ? ` · ${preview}` : ""}` : "No scheduled prompts";

  return (
    <details className="session-plan-panel session-schedule-panel" data-testid="session-schedule-panel" open={count === 0 || !!schedules?.length || loading || !!error}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Timers</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body session-schedule-body">
        <div className="session-schedule-grid">
          <ScheduleField label="Enabled" value={String(enabled)} />
          <ScheduleField label="Total" value={String(count)} />
          {summary?.error_count ? <ScheduleField label="Errors" value={String(summary.error_count)} /> : null}
          {next ? <ScheduleField label="Next" value={next} /> : null}
        </div>
        {preview ? <p className="session-loop-preview">{preview}</p> : null}
        {error ? (
          <div className="session-plan-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {schedules && schedules.length > 0 ? (
          <ol className="session-schedule-list" data-testid="session-schedule-list">
            {schedules.map((schedule) => (
              <li key={schedule.id} className="session-schedule-item" data-enabled={schedule.enabled ? "true" : "false"}>
                <div className="session-schedule-item-main">
                  <strong>{scheduleKindLabel(schedule.kind)} · {scheduleStatusLabel(schedule, loopStatus)} · {formatScheduleTime(schedule.next_run_at)}</strong>
                  <p>{schedule.prompt}</p>
                  <small>{scheduleMeta(schedule, loopStatus)}</small>
                </div>
                <div className="session-schedule-actions">
                  {onUpdateSchedule ? (
                    <button
                      type="button"
                      className="ghost-action"
                      disabled={scheduleUpdateDisabled(schedule, loopStatus, deletingId, updatingId)}
                      onClick={() => void onUpdateSchedule(schedule.id, !schedule.enabled)}
                    >
                      {scheduleUpdateLabel(schedule, loopStatus, updatingId)}
                    </button>
                  ) : null}
                  {onDeleteSchedule ? (
                    <button
                      type="button"
                      className="ghost-action danger-action"
                      disabled={!!deletingId || !!updatingId}
                      onClick={() => void onDeleteSchedule(schedule.id)}
                    >
                      {deletingId === schedule.id ? "Deleting" : "Delete"}
                    </button>
                  ) : null}
                </div>
              </li>
            ))}
          </ol>
        ) : null}
        <div className="session-loop-actions">
          {onLoadSchedules && count > 0 ? (
            <button
              type="button"
              className="ghost-action"
              disabled={loading}
              onClick={() => void onLoadSchedules()}
            >
              {loading ? "Loading timers" : schedules ? "Refresh timers" : "View timers"}
            </button>
          ) : null}
          {onScheduleCheckIn ? (
            <button
              type="button"
              className="ghost-action"
              disabled={disabled || !!busy}
              onClick={() => void onScheduleCheckIn()}
            >
              {busy === "checkin" ? "Scheduling" : "1h check-in"}
            </button>
          ) : null}
          {onScheduleLoopTick ? (
            <button
              type="button"
              className="ghost-action"
              disabled={disabled || !!busy}
              onClick={() => void onScheduleLoopTick()}
            >
              {busy === "loop" ? "Scheduling" : "30m loop tick"}
            </button>
          ) : null}
          {onScheduleDaily ? (
            <button
              type="button"
              className="ghost-action"
              disabled={disabled || !!busy}
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

function scheduleMeta(schedule: SessionSchedule, loopStatus?: string): string {
  const parts: string[] = [schedule.repeat_interval_seconds ? `Repeats every ${formatDuration(schedule.repeat_interval_seconds)}` : "One-time"];
  if (loopTimerPendingCalibration(schedule, loopStatus)) parts.push("waiting for LOOP.md activation");
  if (schedule.run_count && schedule.run_count > 0) parts.push(`${schedule.run_count} run${schedule.run_count === 1 ? "" : "s"}`);
  if (schedule.last_run_at) parts.push(`last ${formatScheduleTime(schedule.last_run_at)}`);
  if (schedule.last_error) parts.push(`error ${schedule.last_error}`);
  return parts.join(" · ");
}

function scheduleStatusLabel(schedule: SessionSchedule, loopStatus?: string): string {
  if (loopTimerPendingCalibration(schedule, loopStatus)) return "Pending calibration";
  return schedule.enabled ? "Active" : "Paused";
}

function scheduleUpdateDisabled(schedule: SessionSchedule, loopStatus?: string, deletingId?: string, updatingId?: string): boolean {
  return !!deletingId || !!updatingId || loopTimerResumeNeedsActivation(schedule, loopStatus);
}

function scheduleUpdateLabel(schedule: SessionSchedule, loopStatus?: string, updatingId?: string): string {
  if (updatingId === schedule.id) return "Updating";
  if (schedule.enabled) return "Pause";
  if (loopTimerResumeNeedsActivation(schedule, loopStatus)) return "Activate loop first";
  return "Resume";
}

function loopTimerResumeNeedsActivation(schedule: SessionSchedule, loopStatus?: string): boolean {
  return schedule.kind === "loop_tick" && !schedule.enabled && !loopProtocolRunning(loopStatus);
}

function pendingLoopTimerCount(schedules?: SessionSchedule[], summary?: SessionSchedulesSummary, loopStatus?: string): number {
  if ((summary?.pending_loop_ticks ?? 0) > 0) return summary?.pending_loop_ticks ?? 0;
  if (loopProtocolRunning(loopStatus)) return 0;
  const visible = schedules?.filter((schedule) => loopTimerPendingCalibration(schedule, loopStatus)).length ?? 0;
  if (visible > 0) return visible;
  if ((summary?.enabled ?? 0) > 0 && summary?.next_schedule_kind === "loop_tick") return summary?.enabled ?? 1;
  const preview = compact(summary?.next_prompt_preview)?.toLowerCase() ?? "";
  if ((summary?.enabled ?? 0) > 0 && preview.includes("scheduled loop tick")) return summary?.enabled ?? 1;
  return 0;
}

function loopTimerPendingCalibration(schedule: SessionSchedule, loopStatus?: string): boolean {
  return schedule.kind === "loop_tick" && schedule.enabled && !loopProtocolRunning(loopStatus);
}

function loopProtocolRunning(loopStatus?: string): boolean {
  return compact(loopStatus)?.toLowerCase() === "running";
}

function scheduleKindLabel(kind: SessionSchedule["kind"]): string {
  if (kind === "loop_tick") return "Loop tick";
  if (kind === "daily_checkin") return "Daily check-in";
  if (kind === "checkin") return "Check-in";
  return "Timer";
}

function formatDuration(seconds: number): string {
  if (seconds % 86400 === 0) {
    const days = seconds / 86400;
    return `${days}d`;
  }
  if (seconds % 3600 === 0) {
    const hours = seconds / 3600;
    return `${hours}h`;
  }
  if (seconds % 60 === 0) {
    const minutes = seconds / 60;
    return `${minutes}m`;
  }
  return `${seconds}s`;
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
