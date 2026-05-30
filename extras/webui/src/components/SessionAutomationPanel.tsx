import { useEffect, useState, type ReactNode } from "react";
import { automationActionLabel } from "../view/automationActions";

export interface SessionAutomationMetric {
  label: string;
  value: string;
  detail?: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export interface SessionAutomationFocus {
  label: string;
  title: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
  action?: "answer" | "review";
}

export interface SessionAutomationQueueItem {
  id: string;
  label: string;
  title: string;
  detail: string;
  meta?: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export interface SessionAutomationOverviewItem {
  id: string;
  label: string;
  value: string;
  detail?: string;
  meta?: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
  actionLabel?: string;
  actionBusy?: boolean;
  actionDisabled?: boolean;
  onAction?: () => Promise<void> | void;
}

export function SessionAutomationPanel({
  title,
  detail,
  overview = [],
  metrics = [],
  focus,
  queue = [],
  actions,
  empty = false,
  defaultGoal,
  starting = false,
  scheduling,
  disabled = false,
  onStartLoop,
  onScheduleCheckIn,
  onScheduleDaily,
  defaultOpen = false,
  testId = "session-automation-panel",
  children,
}: {
  title: string;
  detail: string;
  overview?: readonly SessionAutomationOverviewItem[];
  metrics?: readonly SessionAutomationMetric[];
  focus?: SessionAutomationFocus;
  queue?: readonly SessionAutomationQueueItem[];
  actions?: ReactNode;
  empty?: boolean;
  defaultGoal?: string;
  starting?: boolean;
  scheduling?: "checkin" | "daily";
  disabled?: boolean;
  onStartLoop?: (goal: string) => Promise<void> | void;
  onScheduleCheckIn?: () => Promise<void> | void;
  onScheduleDaily?: () => Promise<void> | void;
  defaultOpen?: boolean;
  testId?: string;
  children: ReactNode;
}) {
  const visibleFocus = shouldShowAutomationFocus(focus);
  const visibleMetrics = focus?.tone === "danger"
    ? []
    : metrics
      .filter(automationMetricHasSignal)
      .filter((metric) => !automationMetricCoveredByFocus(metric, focus))
      .slice(0, 3);
  const visibleQueue = focus?.tone === "danger" ? [] : queue.filter((item) => !automationQueueItemCoveredByFocus(item, focus)).slice(0, 3);
  const headerDetail = empty || visibleFocus || overview.length > 0 ? undefined : detail;
  const [loopGoal, setLoopGoal] = useState(defaultGoal ?? "");
  useEffect(() => {
    if (empty) setLoopGoal(defaultGoal ?? "");
  }, [defaultGoal, empty]);
  const automationHeader = (
    <header className="session-automation-head">
      <span className="session-plan-kicker">Automation</span>
      <strong>{empty ? "Manual mode" : title}</strong>
      {headerDetail ? <span>{headerDetail}</span> : null}
    </header>
  );
  const trimmedGoal = loopGoal.trim();
  if (empty) {
    return (
      <section className="session-plan-panel session-automation-panel" data-testid={testId} data-surface="true">
        {automationHeader}
        <div className="session-plan-body session-automation-body">
          <section className="session-automation-empty" data-testid="session-automation-empty" aria-label="Automation starters">
            <form
              className="session-automation-loop-starter"
              onSubmit={(event) => {
                event.preventDefault();
                if (trimmedGoal && onStartLoop) void onStartLoop(trimmedGoal);
              }}
            >
              <label>
                <span>Loop goal</span>
                <input
                  value={loopGoal}
                  onChange={(event) => setLoopGoal(event.target.value)}
                  placeholder="Track this task across future turns"
                  disabled={disabled || starting}
                />
              </label>
              <button type="submit" className="secondary-action" disabled={disabled || starting || !trimmedGoal || !onStartLoop}>
                {starting ? automationActionLabel("loop_setup", true) : "Set up loop"}
              </button>
            </form>
            <div className="session-automation-empty-actions">
              {onScheduleCheckIn ? (
                <button type="button" className="ghost-action" disabled={disabled || !!scheduling} onClick={() => void onScheduleCheckIn()}>
                  {scheduling === "checkin" ? automationActionLabel("checkin", true) : "Check in 1h"}
                </button>
              ) : null}
              {onScheduleDaily ? (
                <button type="button" className="ghost-action" disabled={disabled || !!scheduling} onClick={() => void onScheduleDaily()}>
                  {scheduling === "daily" ? automationActionLabel("daily", true) : "Daily check-in"}
                </button>
              ) : null}
            </div>
          </section>
        </div>
      </section>
    );
  }
  return (
    <section className="session-plan-panel session-automation-panel" data-testid={testId} data-surface="true" {...(defaultOpen ? { "data-open": "true" } : {})}>
      {automationHeader}
      <div className="session-plan-body session-automation-body">
        {overview.length > 0 ? (
          <div className="session-automation-overview" data-testid="session-automation-overview" aria-label="Automation status">
            {overview.map((item) => (
              <section key={item.id} className="session-automation-overview-card" data-testid={`session-automation-overview-${item.id}`} data-tone={item.tone ?? "neutral"}>
                <div className="session-automation-overview-main">
                  <span>{item.label}</span>
                  <strong>{item.value}</strong>
                  {item.detail ? <small>{item.detail}</small> : null}
                  {item.meta ? <code>{item.meta}</code> : null}
                </div>
                {item.actionLabel && item.onAction ? (
                  <button type="button" className="ghost-action" disabled={item.actionDisabled || item.actionBusy} onClick={() => void item.onAction?.()}>
                    {item.actionBusy ? `${item.actionLabel}...` : item.actionLabel}
                  </button>
                ) : null}
              </section>
            ))}
          </div>
        ) : null}
        {visibleFocus || actions ? (
          <section className="session-automation-focus" data-tone={visibleFocus?.tone ?? "neutral"} data-testid="session-automation-focus" aria-label="Automation focus">
            {visibleFocus ? (
              <div className="session-automation-focus-main">
                <span>{visibleFocus.label}</span>
                <strong>{visibleFocus.title}</strong>
                <small>{visibleFocus.detail}</small>
              </div>
            ) : null}
            {actions ? <div className="session-automation-focus-actions">{actions}</div> : null}
          </section>
        ) : null}
        {visibleMetrics.length > 0 ? (
          <div className="session-automation-statusbar" data-testid="session-automation-statusbar" aria-label="Automation status">
            {visibleMetrics.map((metric) => (
              <div key={metric.label} className="session-automation-metric" data-tone={metric.tone ?? "neutral"}>
                <span>{metric.label}</span>
                <strong>{metric.value}</strong>
                {metric.detail ? <small>{metric.detail}</small> : null}
              </div>
            ))}
          </div>
        ) : null}
        <div className="session-automation-details" data-testid="session-automation-details">
          {children}
        </div>
        {visibleQueue.length > 0 ? (
          <section className="session-automation-queue" data-testid="session-automation-queue" aria-label="Upcoming automation">
            <header>
              <span>Upcoming</span>
              <strong>{visibleQueue.length} {visibleQueue.length === 1 ? "item" : "items"}</strong>
            </header>
            <ol>
              {visibleQueue.map((item) => (
                <li key={item.id} data-tone={item.tone ?? "neutral"}>
                  <div className="session-automation-queue-main">
                    <span>{item.label}</span>
                    <strong>{item.title}</strong>
                    <p>{item.detail}</p>
                  </div>
                  {item.meta ? <code>{item.meta}</code> : null}
                </li>
              ))}
            </ol>
          </section>
        ) : null}
      </div>
    </section>
  );
}

function shouldShowAutomationFocus(focus?: SessionAutomationFocus): SessionAutomationFocus | undefined {
  if (!focus) return undefined;
  if (focus.tone === "danger") return focus;
  if (focus.action === "answer") return focus;
  return undefined;
}

function automationMetricHasSignal(metric: SessionAutomationMetric): boolean {
  if (metric.tone === "danger" || metric.tone === "attention") return true;
  const value = metric.value.trim().toLowerCase();
  if (value === "off" || value === "none" || value === "manual") return false;
  return true;
}

function automationMetricCoveredByFocus(metric: SessionAutomationMetric, focus?: SessionAutomationFocus): boolean {
  if (!focus) return false;
  const label = normalizeAutomationText(metric.label);
  const value = normalizeAutomationText(metric.value);
  const detail = normalizeAutomationText(metric.detail);
  const focusLabel = normalizeAutomationText(focus.label);
  const focusTitle = normalizeAutomationText(focus.title);
  const focusDetail = normalizeAutomationText(focus.detail);
  const focusText = [focusLabel, focusTitle, focusDetail].filter(Boolean).join(" ");
  if (label.includes("timer") && focusLabel.includes("timer")) return true;
  if (label.includes("next") && focusDetail.includes(value) && value.length > 3) return true;
  if (value.length > 3 && (focusTitle.includes(value) || focusDetail.includes(value))) return true;
  if (detail.length > 8 && focusText.includes(detail)) return true;
  return false;
}

function normalizeAutomationText(value?: string): string {
  return (value ?? "").trim().toLowerCase().replace(/\s+/g, " ");
}

function automationQueueItemCoveredByFocus(item: SessionAutomationQueueItem, focus?: SessionAutomationFocus): boolean {
  if (!focus) return false;
  if (focus.action === "answer" && item.id === "loop-calibration") return true;
  if (focus.action === "review" && item.id === "loop-review") return true;
  return item.title === focus.title && item.detail === focus.detail;
}
