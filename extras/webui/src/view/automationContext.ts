import type {
  SessionLoopProtocolResponse,
  SessionSchedule,
  SessionSchedulesSummary,
  SessionSummary,
} from "../api/sessions";

export type AutomationLoopPanelState =
  | { state: "idle" }
  | { state: "loading"; sessionId: string }
  | { state: "ready"; sessionId: string; protocol: SessionLoopProtocolResponse }
  | { state: "error"; sessionId: string; error: string };

export type AutomationSchedulePanelState =
  | { state: "idle" }
  | { state: "loading"; sessionId: string }
  | { state: "ready"; sessionId: string; schedules: SessionSchedule[] }
  | { state: "error"; sessionId: string; error: string; schedules?: SessionSchedule[] };

export interface AutomationContextView {
  title: string;
  detail: string;
}

export function shouldShowLoopContext(
  session: SessionSummary | undefined,
  state: SessionSummary["loop_state"] | undefined,
  panelState: AutomationLoopPanelState,
  busy: boolean,
): boolean {
  if (busy || panelState.state !== "idle") return true;
  if (session?.has_loop_protocol || session?.loop_protocol) return true;
  const status = compactStatus(state?.status);
  return !!status && status !== "off";
}

export function shouldShowScheduleContext(
  session: SessionSummary | undefined,
  panelState: AutomationSchedulePanelState,
  busy: "loop" | "checkin" | "daily" | undefined,
  deletingId: string | undefined,
  updatingId: string | undefined,
): boolean {
  if (busy || deletingId || updatingId || panelState.state === "loading" || panelState.state === "error") return true;
  if (panelState.state === "ready" && panelState.schedules.length > 0) return true;
  if (session?.has_schedules && !session.schedules) return true;
  const summary = session?.schedules;
  if (!summary) return false;
  if (summary.count > 0 || summary.enabled > 0 || (summary.pending_loop_ticks ?? 0) > 0) return true;
  return (summary.error_count ?? 0) > 0 || !!summary.last_error;
}

export function buildAutomationContext(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  loopPanelState: AutomationLoopPanelState,
  schedulePanelState: AutomationSchedulePanelState,
): AutomationContextView {
  const titleParts = [
    loopAutomationLabel(session, loopState, loopPanelState),
    scheduleAutomationLabel(session, schedulePanelState),
  ].filter((part): part is string => !!part);
  const detailParts = [
    loopAutomationDetail(session, loopState, loopPanelState),
    scheduleAutomationDetail(session, schedulePanelState),
  ].filter((part): part is string => !!part);
  return {
    title: titleParts.length > 0 ? titleParts.join(" · ") : "Attention",
    detail: detailParts.length > 0 ? detailParts.join(" · ") : "Open Automation for loop and timer state.",
  };
}

function loopAutomationLabel(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  panelState: AutomationLoopPanelState,
): string | undefined {
  if (panelState.state === "loading") return "Loop loading";
  if (panelState.state === "error") return "Loop error";
  const status = compactStatus(loopState?.status ?? session?.loop_protocol?.status);
  if (!status || status === "off") return undefined;
  if (status === "draft") {
    const answers = loopState?.calibration_answers ?? session?.loop_protocol?.state?.calibration_answers ?? 0;
    return answers > 0 ? "Loop review" : "Loop waiting";
  }
  return `Loop ${status}`;
}

function loopAutomationDetail(
  session: SessionSummary | undefined,
  loopState: SessionSummary["loop_state"] | undefined,
  panelState: AutomationLoopPanelState,
): string | undefined {
  if (panelState.state === "loading") return "Loading LOOP.md and event state.";
  if (panelState.state === "error") return `LOOP.md failed: ${compact(panelState.error) ?? "unknown error"}`;
  const status = compactStatus(loopState?.status ?? session?.loop_protocol?.status);
  if (!status || status === "off") return undefined;
  if (status === "draft") {
    const answers = loopState?.calibration_answers ?? session?.loop_protocol?.state?.calibration_answers ?? 0;
    const questions = loopState?.calibration_questions ?? session?.loop_protocol?.state?.calibration_questions ?? 0;
    if (answers > 0) return "Review recorded calibration before activating LOOP.md.";
    if (questions > 0) return "Answer the setup question before LOOP.md can run.";
    return "Wait for Affent to ask the required setup question.";
  }
  if (status === "running") return "LOOP.md is active; use chat for durable protocol changes.";
  if (status === "disabled") return "LOOP.md is disabled for this chat.";
  return `Review LOOP.md status: ${status}.`;
}

function scheduleAutomationLabel(
  session: SessionSummary | undefined,
  panelState: AutomationSchedulePanelState,
): string | undefined {
  if (panelState.state === "loading") return "Timers loading";
  if (panelState.state === "error") return "Timers error";
  const visibleSchedules = panelState.state === "ready" ? panelState.schedules.length : 0;
  const visibleEnabled = panelState.state === "ready" ? panelState.schedules.filter((schedule) => schedule.enabled).length : 0;
  const summary = session?.schedules;
  const pending = summary?.pending_loop_ticks ?? 0;
  if (pending > 0) return `${pending} timer pending`;
  if ((summary?.error_count ?? 0) > 0 || summary?.last_error) return "Timer failed";
  const enabled = Math.max(summary?.enabled ?? 0, visibleEnabled);
  if (enabled > 0) return `${enabled} timer${enabled === 1 ? "" : "s"} active`;
  const count = Math.max(summary?.count ?? 0, visibleSchedules);
  if (count > 0) return `${count} timer${count === 1 ? "" : "s"} paused`;
  if (session?.has_schedules) return "Timers available";
  return undefined;
}

function scheduleAutomationDetail(
  session: SessionSummary | undefined,
  panelState: AutomationSchedulePanelState,
): string | undefined {
  if (panelState.state === "loading") return "Loading saved timer details.";
  if (panelState.state === "error") return `Timers failed: ${compact(panelState.error) ?? "unknown error"}`;
  const summary = session?.schedules;
  const visibleSchedules = panelState.state === "ready" ? panelState.schedules : [];
  const pending = summary?.pending_loop_ticks ?? 0;
  if (pending > 0) return `${pending} loop timer${pending === 1 ? "" : "s"} queued until LOOP.md activation.`;
  const error = scheduleErrorDetail(summary);
  if (error) return error;
  if (summary?.next_run_at) return `Next ${formatScheduleTime(summary.next_run_at)}${compact(summary.next_prompt_preview) ? ` · ${compact(summary.next_prompt_preview)}` : ""}`;
  const enabled = Math.max(summary?.enabled ?? 0, visibleSchedules.filter((schedule) => schedule.enabled).length);
  if (enabled > 0) return `${enabled} timer${enabled === 1 ? "" : "s"} enabled; open Timers to inspect the next run.`;
  const count = Math.max(summary?.count ?? 0, visibleSchedules.length);
  if (count > 0) return `${count} timer${count === 1 ? "" : "s"} paused; resume before the next needed check-in or delete it.`;
  if (session?.has_schedules) return "Open Timers to load saved schedule details.";
  return undefined;
}

function scheduleErrorDetail(summary?: SessionSchedulesSummary): string | undefined {
  if (!summary || ((summary.error_count ?? 0) <= 0 && !summary.last_error)) return undefined;
  const count = summary.error_count ?? 1;
  const error = compact(summary.last_error);
  return `${count} timer error${count === 1 ? "" : "s"}${error ? `: ${error}` : ""}`;
}

function compactStatus(value: string | undefined): string | undefined {
  const compacted = value?.replace(/\s+/g, " ").trim().toLowerCase();
  return compacted || undefined;
}

function compact(value?: string): string | undefined {
  const next = value?.replace(/\s+/g, " ").trim();
  return next || undefined;
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
