import { useEffect, useState } from "react";
import type { SessionLoopProtocolSummary, SessionLoopState } from "../api/sessions";
import { CopyButton } from "./CopyButton";

export function SessionLoopPanel({
  summary,
  state,
  disabling = false,
  protocol,
  loadingProtocol = false,
  protocolError,
  defaultGoal,
  starting = false,
  onDisable,
  onStart,
  onLoadProtocol,
  onUseAsDraft,
}: {
  summary?: SessionLoopProtocolSummary;
  state?: SessionLoopState;
  disabling?: boolean;
  protocol?: string;
  loadingProtocol?: boolean;
  protocolError?: string;
  defaultGoal?: string;
  starting?: boolean;
  onDisable?: () => Promise<void> | void;
  onStart?: (goal: string) => Promise<void> | void;
  onLoadProtocol?: () => Promise<void> | void;
  onUseAsDraft?: () => void;
}) {
  const [setupGoal, setSetupGoal] = useState(defaultGoal ?? "");
  useEffect(() => {
    if (!summary && !state) setSetupGoal(defaultGoal ?? "");
  }, [defaultGoal, state, summary]);

  if (!summary && !state) {
    const goal = compact(setupGoal);
    return (
      <details className="session-plan-panel session-loop-panel" data-testid="session-loop-panel" open>
        <summary className="session-plan-summary">
          <span className="session-plan-kicker">Loop</span>
          <strong>Off</strong>
          <span>Draft first · asks before activation</span>
        </summary>
        <div className="session-plan-body session-loop-body">
          <form
            className="session-loop-setup"
            onSubmit={(event) => {
              event.preventDefault();
              if (goal && onStart) void onStart(goal);
            }}
          >
            <label>
              <span>Goal</span>
              <input
                value={setupGoal}
                onChange={(event) => setSetupGoal(event.target.value)}
                placeholder="Long-running objective"
                disabled={starting}
              />
            </label>
            <button type="submit" className="secondary-action" disabled={!goal || starting || !onStart}>
              {starting ? "Starting loop" : "Set up loop"}
            </button>
          </form>
        </div>
      </details>
    );
  }

  const status = compact(summary?.status) || compact(state?.status) || "unknown";
  const goal = compact(state?.initial_goal_preview);
  const path = compact(summary?.path) || compact(state?.protocol_path);
  const preview = compact(summary?.preview);
  const feeds = state?.protocol_feeds ?? 0;
  const updates = state?.protocol_updates ?? 0;
  const event = compact(state?.last_event_summary);
  const disabled = status === "disabled";
  const title = disabled ? "Disabled" : statusLabel(status);
  const detail = loopDetail({ goal, feeds, updates, event });

  return (
    <details className="session-plan-panel session-loop-panel" data-testid="session-loop-panel" open={!disabled}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Loop</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body session-loop-body">
        <div className="session-loop-grid">
          {goal ? <LoopField label="Goal" value={goal} /> : null}
          {path ? <LoopField label="File" value={path} mono /> : null}
          {feeds > 0 ? <LoopField label="Feeds" value={String(feeds)} /> : null}
          {updates > 0 ? <LoopField label="Updates" value={String(updates)} /> : null}
          {compact(state?.last_decision_kind) ? (
            <LoopField label="Decision" value={[state?.last_decision_kind, state?.last_decision].filter(Boolean).join(":")} />
          ) : null}
          {event ? <LoopField label="Latest" value={event} /> : null}
        </div>
        {preview ? <p className="session-loop-preview">{preview}</p> : null}
        {protocol ? (
          <pre className="session-loop-protocol" data-testid="session-loop-protocol">{protocol}</pre>
        ) : null}
        {protocolError ? (
          <div className="session-plan-empty error" role="alert">
            {protocolError}
          </div>
        ) : null}
        <div className="session-loop-actions">
          {onLoadProtocol ? (
            <button type="button" className="ghost-action" disabled={loadingProtocol} onClick={() => void onLoadProtocol()}>
              {loadingProtocol ? "Loading LOOP.md" : protocol ? "Refresh LOOP.md" : "View LOOP.md"}
            </button>
          ) : null}
          {protocol ? <CopyButton label="Copy LOOP.md" value={protocol} className="ghost-action" /> : null}
          {onUseAsDraft && !disabled ? (
            <button type="button" className="ghost-action" onClick={onUseAsDraft}>
              Continue setup
            </button>
          ) : null}
          {!disabled && onDisable ? (
            <button type="button" className="ghost-action danger-action" disabled={disabling} onClick={() => void onDisable()}>
              {disabling ? "Disabling loop" : "Disable loop"}
            </button>
          ) : null}
        </div>
      </div>
    </details>
  );
}

function LoopField({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="session-loop-field">
      <span>{label}</span>
      {mono ? <code>{value}</code> : <strong>{value}</strong>}
    </div>
  );
}

function loopDetail({
  goal,
  feeds,
  updates,
  event,
}: {
  goal?: string;
  feeds: number;
  updates: number;
  event?: string;
}) {
  if (goal) return goal;
  const parts: string[] = [];
  if (feeds > 0) parts.push(`${feeds} ${feeds === 1 ? "feed" : "feeds"}`);
  if (updates > 0) parts.push(`${updates} ${updates === 1 ? "update" : "updates"}`);
  if (event) parts.push(event);
  return parts.length > 0 ? parts.join(" | ") : "No loop activity yet";
}

function statusLabel(status: string) {
  if (status === "running") return "Running";
  if (status === "draft") return "Draft";
  if (status === "paused") return "Paused";
  return status;
}

function compact(value?: string): string | undefined {
  const next = value?.replace(/\s+/g, " ").trim();
  return next || undefined;
}
