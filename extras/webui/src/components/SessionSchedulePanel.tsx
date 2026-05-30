import { useEffect, useMemo, useState } from "react";
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
  const [selectedScheduleId, setSelectedScheduleId] = useState<string | undefined>();
  const count = summary?.count ?? 0;
  const enabled = summary?.enabled ?? 0;
  const next = summary?.next_run_at ? formatScheduleTime(summary.next_run_at) : undefined;
  const preview = compact(summary?.next_prompt_preview);
  const lastError = compact(summary?.last_error);
  const title = schedulePanelTitle(summary);
  const detail = schedulePanelDetail(summary, { lastError, next, preview });
  const orderedSchedules = useMemo(() => orderSchedules(schedules ?? []), [schedules]);
  const selectedSchedule = orderedSchedules.find((schedule) => schedule.id === selectedScheduleId) ?? orderedSchedules[0];
  const headerDetail = orderedSchedules.length > 0 ? "" : detail;
  const showSummaryFields = (count > 0 || Boolean(lastError)) && orderedSchedules.length === 0;
  const showLoadAction = Boolean(onLoadSchedules && (!summary || count > 0));
  const showCheckInAction = shouldShowCreateScheduleAction("checkin", busy, disabled, Boolean(onScheduleCheckIn));
  const showLoopTickAction = shouldShowCreateScheduleAction("loop", busy, disabled, Boolean(onScheduleLoopTick));
  const showDailyAction = shouldShowCreateScheduleAction("daily", busy, disabled, Boolean(onScheduleDaily));
  const toolbarDetail = scheduleToolbarDetail(summary, orderedSchedules, next, lastError);
  const scheduleActions = (
    <div className="session-loop-actions session-schedule-commandbar" data-testid="session-schedule-commandbar">
      <div className="session-schedule-commandbar-status">
        <span>Timers</span>
        <strong>{scheduleToolbarSummary(summary, orderedSchedules)}</strong>
        {toolbarDetail ? <small>{toolbarDetail}</small> : null}
      </div>
      <div className="session-schedule-commandbar-actions">
        {showLoadAction ? (
          <button
            type="button"
            className="ghost-action"
            aria-label={scheduleLoadLabel(loading, !!schedules)}
            disabled={loading}
            onClick={() => void onLoadSchedules?.()}
          >
            {scheduleLoadShortLabel(loading, !!schedules)}
          </button>
        ) : null}
        {showCheckInAction ? (
          <button
            type="button"
            className="ghost-action"
            aria-label={automationActionLabel("checkin", busy === "checkin")}
            disabled={disabled || !!busy}
            onClick={() => void onScheduleCheckIn?.()}
          >
            {busy === "checkin" ? automationActionLabel("checkin", true) : "1h"}
          </button>
        ) : null}
        {showLoopTickAction ? (
          <button
            type="button"
            className="ghost-action"
            aria-label={automationActionLabel("loop_tick", busy === "loop")}
            disabled={disabled || !!busy}
            onClick={() => void onScheduleLoopTick?.()}
          >
            {busy === "loop" ? automationActionLabel("loop_tick", true) : "30m"}
          </button>
        ) : null}
        {showDailyAction ? (
          <button
            type="button"
            className="ghost-action"
            aria-label={automationActionLabel("daily", busy === "daily")}
            disabled={disabled || !!busy}
            onClick={() => void onScheduleDaily?.()}
          >
            {busy === "daily" ? automationActionLabel("daily", true) : "Daily"}
          </button>
        ) : null}
      </div>
    </div>
  );
  const hasScheduleActions = Boolean(showLoadAction || showCheckInAction || showLoopTickAction || showDailyAction);

  async function deleteSchedule(scheduleId: string) {
    await onDeleteSchedule?.(scheduleId);
    setConfirmDeleteId(undefined);
  }

  useEffect(() => {
    if (orderedSchedules.length === 0) {
      setSelectedScheduleId(undefined);
      return;
    }
    if (!selectedScheduleId || !orderedSchedules.some((schedule) => schedule.id === selectedScheduleId)) {
      setSelectedScheduleId(orderedSchedules[0]?.id);
    }
  }, [orderedSchedules, selectedScheduleId]);

  return (
    <SessionPanelFrame
      className="session-plan-panel session-schedule-panel"
      testId="session-schedule-panel"
      embedded={embedded}
      defaultOpen={defaultOpen}
      kicker="Scheduled follow-ups"
      title={title}
      detail={headerDetail}
      embeddedHeader={embedded && orderedSchedules.length > 0 ? "hidden" : "default"}
    >
      <div className="session-plan-body session-schedule-body">
        {showSummaryFields ? (
          <div className="session-schedule-grid">
            <ScheduleField label="Enabled" value={String(enabled)} />
            <ScheduleField label="Total" value={String(count)} />
            {summary?.error_count ? <ScheduleField label="Errors" value={String(summary.error_count)} /> : null}
            {next ? <ScheduleField label="Next" value={next} /> : null}
          </div>
        ) : null}
        {preview && orderedSchedules.length === 0 ? <p className="session-loop-preview">{preview}</p> : null}
        {error ? (
          <div className="session-plan-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {orderedSchedules.length > 0 && hasScheduleActions ? scheduleActions : null}
        {orderedSchedules.length > 0 ? (
          <div className="session-schedule-workspace" data-testid="session-schedule-workspace">
            <ol className="session-schedule-list" data-testid="session-schedule-list" aria-label="Automation timers">
              {orderedSchedules.map((schedule) => (
                <li
                  key={schedule.id}
                  className="session-schedule-item"
                  data-enabled={schedule.enabled ? "true" : "false"}
                  data-selected={selectedSchedule?.id === schedule.id ? "true" : "false"}
                  data-tone={scheduleTone(schedule)}
                >
                  <button
                    type="button"
                    className="session-schedule-row-button"
                    onClick={() => {
                      setConfirmDeleteId(undefined);
                      setSelectedScheduleId(schedule.id);
                    }}
                  >
                    <div className="session-schedule-kind">
                      <span>{scheduleKindLabel(schedule.kind)}</span>
                      <strong>{scheduleStatusLabel(schedule)}</strong>
                    </div>
                    <div className="session-schedule-item-main">
                      <strong>{scheduleDisplayText(schedule)}</strong>
                      <p>{scheduleMeta(schedule)}</p>
                      {schedule.last_error ? <small data-tone="danger">{schedule.last_error}</small> : null}
                    </div>
                    <div className="session-schedule-next">
                      <span>Next</span>
                      <strong>{formatScheduleTime(schedule.next_run_at)}</strong>
                    </div>
                  </button>
                </li>
              ))}
            </ol>
            {selectedSchedule ? (
              <ScheduleInspector
                schedule={selectedSchedule}
                confirmingDelete={confirmDeleteId === selectedSchedule.id}
                deletingId={deletingId}
                updatingId={updatingId}
                onUpdateSchedule={onUpdateSchedule}
                canDelete={Boolean(onDeleteSchedule)}
                onDeleteRequest={() => setConfirmDeleteId(selectedSchedule.id)}
                onDeleteCancel={() => setConfirmDeleteId(undefined)}
                onDeleteConfirm={() => void deleteSchedule(selectedSchedule.id)}
              />
            ) : null}
          </div>
        ) : null}
        {orderedSchedules.length === 0 && hasScheduleActions ? scheduleActions : null}
      </div>
    </SessionPanelFrame>
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

function scheduleToolbarSummary(
  summary: SessionSchedulesSummary | undefined,
  schedules: readonly SessionSchedule[],
): string {
  if (schedules.length > 0) {
    const active = schedules.filter((schedule) => schedule.enabled).length;
    return `${schedules.length} ${plural("timer", schedules.length)} · ${active} active`;
  }
  if (!summary) return "Details needed";
  if (summary.count > 0) return `${summary.count} ${plural("timer", summary.count)} · ${summary.enabled} active`;
  return "No timers";
}

function scheduleToolbarDetail(
  summary: SessionSchedulesSummary | undefined,
  schedules: readonly SessionSchedule[],
  next?: string,
  lastError?: string,
): string | undefined {
  if (lastError) return `${summary?.error_count ?? 1} error${summary?.error_count === 1 ? "" : "s"}`;
  const nextSchedule = schedules.find((schedule) => schedule.enabled) ?? schedules[0];
  if (nextSchedule) return `Next ${formatScheduleTime(nextSchedule.next_run_at)}`;
  if (next) return `Next ${next}`;
  return summary ? undefined : "Load before pause, resume, or delete";
}

function ScheduleInspector({
  schedule,
  confirmingDelete,
  deletingId,
  updatingId,
  onUpdateSchedule,
  canDelete,
  onDeleteRequest,
  onDeleteCancel,
  onDeleteConfirm,
}: {
  schedule: SessionSchedule;
  confirmingDelete: boolean;
  deletingId?: string;
  updatingId?: string;
  onUpdateSchedule?: (scheduleId: string, enabled: boolean) => Promise<void> | void;
  canDelete: boolean;
  onDeleteRequest: () => void;
  onDeleteCancel: () => void;
  onDeleteConfirm: () => void;
}) {
  return (
    <aside className="session-schedule-inspector" data-tone={scheduleTone(schedule)} data-testid="session-schedule-inspector" aria-label="Selected automation timer">
      <div className="session-schedule-inspector-head">
        <span>{scheduleKindLabel(schedule.kind)}</span>
        <strong>{scheduleStatusLabel(schedule)}</strong>
      </div>
      <div className="session-schedule-inspector-title">
        <strong>{scheduleDisplayText(schedule)}</strong>
        {schedule.prompt !== scheduleDisplayText(schedule) ? <p>{schedule.prompt}</p> : null}
      </div>
      <dl className="session-schedule-inspector-facts">
        <div>
          <dt>Next</dt>
          <dd>{formatScheduleTime(schedule.next_run_at)}</dd>
        </div>
        <div>
          <dt>Repeat</dt>
          <dd>{schedule.repeat_interval_seconds ? formatDuration(schedule.repeat_interval_seconds) : "One-time"}</dd>
        </div>
        {schedule.last_run_at ? (
          <div>
            <dt>Last run</dt>
            <dd>{formatScheduleTime(schedule.last_run_at)}</dd>
          </div>
        ) : null}
        {schedule.run_count && schedule.run_count > 0 ? (
          <div>
            <dt>Runs</dt>
            <dd>{schedule.run_count}</dd>
          </div>
        ) : null}
        {schedule.last_error_kind ? (
          <div>
            <dt>Error kind</dt>
            <dd>{schedule.last_error_kind}</dd>
          </div>
        ) : null}
      </dl>
      {schedule.last_error ? (
        <div className="session-schedule-inspector-error" role="alert">
          <span>Last error</span>
          <strong>{schedule.last_error}</strong>
        </div>
      ) : null}
      <div className="session-schedule-actions">
        {onUpdateSchedule ? (
          <button
            type="button"
            className="ghost-action"
            disabled={scheduleUpdateDisabled(deletingId, updatingId)}
            onClick={() => void onUpdateSchedule(schedule.id, !schedule.enabled)}
          >
            {scheduleUpdateLabel(schedule, updatingId)}
          </button>
        ) : null}
        {canDelete ? confirmingDelete ? (
          <div className="session-schedule-delete-confirm" role="group" aria-label={`Confirm delete ${scheduleKindLabel(schedule.kind)} timer`}>
            <span>Delete this timer?</span>
            <button type="button" disabled={!!deletingId || !!updatingId} onClick={onDeleteCancel}>
              Cancel
            </button>
            <button type="button" className="danger" disabled={!!deletingId || !!updatingId} onClick={onDeleteConfirm}>
              {deletingId === schedule.id ? "Deleting" : "Confirm"}
            </button>
          </div>
        ) : (
          <button
            type="button"
            className="ghost-action danger-action"
            disabled={!!deletingId || !!updatingId}
            onClick={onDeleteRequest}
          >
            {deletingId === schedule.id ? "Deleting" : "Delete"}
          </button>
        ) : null}
      </div>
    </aside>
  );
}

function scheduleLoadLabel(loading: boolean, loaded: boolean): string {
  if (loading) return "Loading timer details";
  return loaded ? "Refresh timer details" : "Load timer details";
}

function scheduleLoadShortLabel(loading: boolean, loaded: boolean): string {
  if (loading) return "Loading";
  return loaded ? "Refresh" : "Load details";
}

function shouldShowCreateScheduleAction(
  kind: "loop" | "checkin" | "daily",
  busy: "loop" | "checkin" | "daily" | undefined,
  disabled: boolean,
  available: boolean,
): boolean {
  if (!available || disabled) return false;
  return !busy || busy === kind;
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
  const parts: string[] = [schedule.repeat_interval_seconds ? `Every ${formatDuration(schedule.repeat_interval_seconds)}` : "One-time"];
  if (schedule.run_count && schedule.run_count > 0) parts.push(`${schedule.run_count} run${schedule.run_count === 1 ? "" : "s"}`);
  if (schedule.last_run_at) parts.push(`last ${formatScheduleTime(schedule.last_run_at)}`);
  return parts.join(" · ");
}

function scheduleDisplayText(schedule: SessionSchedule): string {
  return compact(schedule.display_text) ?? schedule.prompt;
}

function scheduleStatusLabel(schedule: SessionSchedule): string {
  if (schedule.last_error) return `${schedule.enabled ? "Active" : "Paused"} · Error`;
  return schedule.enabled ? "Active" : "Paused";
}

function scheduleTone(schedule: SessionSchedule): "danger" | "ok" | "neutral" {
  if (schedule.last_error) return "danger";
  if (schedule.enabled) return "ok";
  return "neutral";
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
