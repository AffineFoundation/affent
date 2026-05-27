import { useEffect, useState } from "react";
import type { SessionLoopEvent, SessionLoopProtocolSummary, SessionLoopState } from "../api/sessions";
import { CopyButton } from "./CopyButton";

export function SessionLoopPanel({
  summary,
  state,
  disabling = false,
  protocol,
  events,
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
  events?: SessionLoopEvent[];
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
          <span>Draft first · calibration required</span>
        </summary>
        <div className="session-plan-body session-loop-body">
          <LoopStatusCallout status="off" />
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
  const memory = loopMemoryUpdate(state);
  const disabled = status === "disabled";
  const draft = status === "draft";
  const title = disabled ? "Disabled" : statusLabel(status);
  const detail = draft ? "Waiting for calibration answer" : loopDetail({ goal, feeds, updates, event });

  return (
    <details className="session-plan-panel session-loop-panel" data-testid="session-loop-panel" open={!disabled}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Loop</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body session-loop-body">
        <LoopStatusCallout status={disabled ? "disabled" : draft ? "draft" : status === "running" ? "running" : "unknown"} />
        <div className="session-loop-grid">
          {goal ? <LoopField label="Goal" value={goal} /> : null}
          {path ? <LoopField label="File" value={path} mono /> : null}
          {feeds > 0 ? <LoopField label="Feeds" value={String(feeds)} /> : null}
          {updates > 0 ? <LoopField label="Updates" value={String(updates)} /> : null}
          {memory ? <LoopField label="Memory" value={memory} /> : null}
          {compact(state?.last_decision_kind) ? (
            <LoopField label="Decision" value={[state?.last_decision_kind, state?.last_decision].filter(Boolean).join(":")} />
          ) : null}
          {event ? <LoopField label="Latest" value={event} /> : null}
        </div>
        {preview ? <p className="session-loop-preview">{preview}</p> : null}
        {protocol ? (
          <pre className="session-loop-protocol" data-testid="session-loop-protocol">{protocol}</pre>
        ) : null}
        {events && events.length > 0 ? <LoopEvents events={events} /> : null}
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
              Update via chat
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

function LoopEvents({ events }: { events: SessionLoopEvent[] }) {
  const recent = events.slice(-5).reverse();
  return (
    <ol className="session-loop-events" data-testid="session-loop-events" aria-label="Recent loop protocol events">
      {recent.map((event) => {
        const detail = loopEventDetail(event);
        return (
          <li key={`${event.seq}:${event.type}`}>
            <strong>{event.summary || loopEventLabel(event.type)}</strong>
            {detail ? <span>{detail}</span> : null}
          </li>
        );
      })}
    </ol>
  );
}

function loopEventDetail(event: SessionLoopEvent): string | undefined {
  const parts = [
    event.type,
    event.reason ? `reason ${event.reason}` : undefined,
    event.sections_changed && event.sections_changed.length > 0 ? `sections ${event.sections_changed.join(", ")}` : undefined,
    event.memory_preview ? `memory ${event.memory_preview}` : undefined,
    event.decision ? `decision ${event.decision}` : undefined,
    event.turn_end_reason ? `turn ${event.turn_end_reason}` : undefined,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : undefined;
}

function loopEventLabel(type: string): string {
  if (type === "loop.protocol_init") return "Initialized LOOP.md";
  if (type === "loop.protocol_update") return "Updated LOOP.md";
  if (type === "loop.protocol_activate") return "Activated LOOP.md";
  if (type === "loop.protocol_disable") return "Disabled LOOP.md";
  return type;
}

function LoopStatusCallout({ status }: { status: "off" | "draft" | "running" | "disabled" | "unknown" }) {
  const copy = loopStatusCopy(status);
  return (
    <div className={`session-loop-callout ${status}`} data-testid="session-loop-callout">
      <strong>{copy.title}</strong>
      <span>{copy.detail}</span>
    </div>
  );
}

function loopStatusCopy(status: "off" | "draft" | "running" | "disabled" | "unknown") {
  if (status === "draft") {
    return {
      title: "Setup pending",
      detail: "Affent must ask, update LOOP.md, then activate after your answer.",
    };
  }
  if (status === "running") {
    return {
      title: "Running protocol",
      detail: "LOOP.md is active and will be fed into future long-run turns.",
    };
  }
  if (status === "disabled") {
    return {
      title: "Loop disabled",
      detail: "This session will not receive LOOP.md guidance until setup runs again.",
    };
  }
  if (status === "off") {
    return {
      title: "Draft setup",
      detail: "Starting creates LOOP.md first; Affent asks before it begins running.",
    };
  }
  return {
    title: "Loop state",
    detail: "Review LOOP.md before continuing long-run work.",
  };
}

function LoopField({ label, value, mono = false }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="session-loop-field">
      <span>{label}</span>
      {mono ? <code>{value}</code> : <strong>{value}</strong>}
    </div>
  );
}

function loopMemoryUpdate(state?: SessionLoopState): string | undefined {
  if (!state) return undefined;
  const action = compact(state.last_memory_update_action);
  const location = compact(state.last_memory_update_location) || memoryLocation(state);
  const preview = compact(state.last_memory_update_preview) || compact(state.last_memory_update_next_preview) || compact(state.last_memory_update_previous_preview);
  const parts = [action ? memoryActionLabel(action) : undefined, location, preview].filter(Boolean);
  if (parts.length > 0) return parts.join(" · ");
  const count = state.memory_update_events ?? 0;
  return count > 0 ? `${count} memory ${count === 1 ? "update" : "updates"}` : undefined;
}

function memoryLocation(state: SessionLoopState): string | undefined {
  const target = compact(state.last_memory_update_target);
  const topic = compact(state.last_memory_update_topic);
  if (target && topic) return `${target}:${topic}`;
  return target || topic;
}

function memoryActionLabel(action: string): string {
  if (action === "add") return "Added";
  if (action === "replace") return "Replaced";
  if (action === "remove") return "Removed";
  return action;
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
