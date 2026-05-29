import { useMemo, useState } from "react";
import type { SessionSchedule, SessionSchedulesSummary } from "../api/sessions";
import { automationActionLabel } from "../view/automationActions";
import { SessionPanelFrame } from "./SessionPanelFrame";

export function SessionSchedulePanel({
  summary,
  schedules,
  busy,
  disabled = false,
  defaultOpen = false,
  embedded = false,
  loading = false,
  error,
  deletingId,
  updatingId,
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
  defaultOpen?: boolean;
  embedded?: boolean;
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
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | undefined>();
  const count = summary?.count ?? 0;
  const enabled = summary?.enabled ?? 0;
  const next = summary?.next_run_at ? formatScheduleTime(summary.next_run_at) : undefined;
  const preview = compact(summary?.next_prompt_preview);
  const lastError = compact(summary?.last_error);
  const title = schedulePanelTitle(summary);
  const detail = schedulePanelDetail(summary, { lastError, next, preview });
  const orderedSchedules = useMemo(() => orderSchedules(schedules ?? []), [schedules]);

  async function deleteSchedule(scheduleId: string) {
    await onDeleteSchedule?.(scheduleId);
    setConfirmDeleteId(undefined);
  }

  return (
    <SessionPanelFrame
      className="session-plan-panel session-schedule-panel"
      testId="session-schedule-panel"
      embedded={embedded}
      defaultOpen={defaultOpen}
      kicker="Timers"
      title={title}
      detail={detail}
    >
      <div className="session-plan-body session-schedule-body">
        {count > 0 || lastError ? (
          <>
            <ScheduleCallout hasActions={!!(onScheduleLoopTick || onScheduleCheckIn || onScheduleDaily)} />
            <div className="session-schedule-grid">
              <ScheduleField label="Enabled" value={String(enabled)} />
              <ScheduleField label="Total" value={String(count)} />
              {summary?.error_count ? <ScheduleField label="Errors" value={String(summary.error_count)} /> : null}
              {next ? <ScheduleField label="Next" value={next} /> : null}
            </div>
          </>
        ) : null}
        {preview ? <p className="session-loop-preview">{preview}</p> : null}
        {error ? (
          <div className="session-plan-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {orderedSchedules.length > 0 ? (
          <ol className="session-schedule-list" data-testid="session-schedule-list">
            {orderedSchedules.map((schedule) => {
              const confirmingDelete = confirmDeleteId === schedule.id;
              return (
                <li key={schedule.id} className="session-schedule-item" data-enabled={schedule.enabled ? "true" : "false"}>
                  <div className="session-schedule-item-main">
                    <strong>{scheduleKindLabel(schedule.kind)} · {scheduleStatusLabel(schedule)} · {formatScheduleTime(schedule.next_run_at)}</strong>
                    <p>{scheduleDisplayText(schedule)}</p>
                    <small>{scheduleMeta(schedule)}</small>
                  </div>
                  <div className="session-schedule-actions">
                    {onUpdateSchedule ? (
                      <button
                        type="button"
                        className="ghost-action"
                        disabled={scheduleUpdateDisabled(deletingId, updatingId)}
                        onClick={() => {
                          setConfirmDeleteId(undefined);
                          void onUpdateSchedule(schedule.id, !schedule.enabled);
                        }}
                      >
                        {scheduleUpdateLabel(schedule, updatingId)}
                      </button>
                    ) : null}
                    {onDeleteSchedule ? confirmingDelete ? (
                      <div className="session-schedule-delete-confirm" role="group" aria-label={`Confirm delete ${scheduleKindLabel(schedule.kind)} timer`}>
                        <span>Delete this timer?</span>
                        <button type="button" disabled={!!deletingId || !!updatingId} onClick={() => setConfirmDeleteId(undefined)}>
                          Cancel
                        </button>
                        <button type="button" className="danger" disabled={!!deletingId || !!updatingId} onClick={() => void deleteSchedule(schedule.id)}>
                          {deletingId === schedule.id ? "Deleting" : "Confirm"}
                        </button>
                      </div>
                    ) : (
                      <button
                        type="button"
                        className="ghost-action danger-action"
                        disabled={!!deletingId || !!updatingId}
                        onClick={() => setConfirmDeleteId(schedule.id)}
                      >
                        {deletingId === schedule.id ? "Deleting" : "Delete"}
                      </button>
                    ) : null}
                  </div>
                </li>
              );
            })}
          </ol>
        ) : null}
        <div className="session-loop-actions">
          {onLoadSchedules && (!summary || count > 0) ? (
            <button
              type="button"
              className="ghost-action"
              disabled={loading}
              onClick={() => void onLoadSchedules()}
            >
              {scheduleLoadLabel(loading, !!schedules)}
            </button>
          ) : null}
          {onScheduleCheckIn ? (
            <button
              type="button"
              className="ghost-action"
              disabled={disabled || !!busy}
              onClick={() => void onScheduleCheckIn()}
            >
              {automationActionLabel("checkin", busy === "checkin")}
            </button>
          ) : null}
          {onScheduleLoopTick ? (
            <button
              type="button"
              className="ghost-action"
              disabled={disabled || !!busy}
              onClick={() => void onScheduleLoopTick()}
            >
              {automationActionLabel("loop_tick", busy === "loop")}
            </button>
          ) : null}
          {onScheduleDaily ? (
            <button
              type="button"
              className="ghost-action"
              disabled={disabled || !!busy}
              onClick={() => void onScheduleDaily()}
            >
              {automationActionLabel("daily", busy === "daily")}
            </button>
          ) : null}
        </div>
      </div>
    </SessionPanelFrame>
  );
}

function ScheduleCallout({
  hasActions,
}: {
  hasActions: boolean;
}) {
  if (!hasActions) return null;
  return (
    <div className="session-schedule-callout setup" data-testid="session-schedule-callout">
      <strong>Scheduler ready</strong>
      <span>Timers create scheduled turns. LOOP.md is only used when a separate long-running protocol is active.</span>
    </div>
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

function scheduleLoadLabel(loading: boolean, loaded: boolean): string {
  if (loading) return "Loading timer details";
  return loaded ? "Refresh timer details" : "Load timer details";
}

function schedulePanelTitle(summary: SessionSchedulesSummary | undefined): string {
  if (!summary) return "Timer details needed";
  if (summary.enabled > 0) return `${summary.enabled} active`;
  if (summary.count > 0) return `${summary.count} paused`;
  return "Off";
}

function schedulePanelDetail(
  summary: SessionSchedulesSummary | undefined,
  {
    lastError,
    next,
    preview,
  }: {
    lastError?: string;
    next?: string;
    preview?: string;
  },
): string {
  if (!summary) return "Load details before pausing, resuming, or deleting timers.";
  if (lastError) return `${summary.error_count ?? 1} error${summary.error_count === 1 ? "" : "s"} · ${lastError}`;
  if (next) return `Next ${next}${preview ? ` · ${preview}` : ""}`;
  if (summary.enabled > 0) return `${summary.enabled} active ${plural("timer", summary.enabled)}; load details to inspect the next run.`;
  if (summary.count > 0) return `${summary.count} paused ${plural("timer", summary.count)}; load details before resuming or deleting.`;
  return "No scheduled follow-ups for this chat.";
}

function scheduleMeta(schedule: SessionSchedule): string {
  const parts: string[] = [schedule.repeat_interval_seconds ? `Repeats every ${formatDuration(schedule.repeat_interval_seconds)}` : "One-time"];
  if (schedule.run_count && schedule.run_count > 0) parts.push(`${schedule.run_count} run${schedule.run_count === 1 ? "" : "s"}`);
  if (schedule.last_run_at) parts.push(`last ${formatScheduleTime(schedule.last_run_at)}`);
  if (schedule.last_error) parts.push(`error ${schedule.last_error}`);
  return parts.join(" · ");
}

function scheduleDisplayText(schedule: SessionSchedule): string {
  return compact(schedule.display_text) ?? schedule.prompt;
}

function scheduleStatusLabel(schedule: SessionSchedule): string {
  return schedule.enabled ? "Active" : "Paused";
}

function scheduleUpdateDisabled(deletingId?: string, updatingId?: string): boolean {
  return !!deletingId || !!updatingId;
}

function scheduleUpdateLabel(schedule: SessionSchedule, updatingId?: string): string {
  if (updatingId === schedule.id) return "Updating";
  if (schedule.enabled) return "Pause";
  return "Resume";
}

function orderSchedules(schedules: readonly SessionSchedule[]): SessionSchedule[] {
  return [...schedules].sort((left, right) => {
    const leftRank = schedulePriority(left);
    const rightRank = schedulePriority(right);
    if (leftRank !== rightRank) return leftRank - rightRank;
    const leftTime = Date.parse(left.next_run_at);
    const rightTime = Date.parse(right.next_run_at);
    if (Number.isFinite(leftTime) && Number.isFinite(rightTime) && leftTime !== rightTime) return leftTime - rightTime;
    if (Number.isFinite(leftTime) !== Number.isFinite(rightTime)) return Number.isFinite(leftTime) ? -1 : 1;
    return left.id.localeCompare(right.id);
  });
}

function schedulePriority(schedule: SessionSchedule): number {
  if (compact(schedule.last_error)) return 0;
  if (schedule.enabled) return 1;
  return 2;
}

function plural(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function scheduleKindLabel(kind: SessionSchedule["kind"]): string {
  if (kind === "loop_tick") return "30m timer";
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
