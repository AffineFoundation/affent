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
  defaultOpen = false,
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
  defaultOpen?: boolean;
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
      <details className="session-plan-panel session-loop-panel" data-testid="session-loop-panel" {...(defaultOpen ? { open: true } : {})}>
        <summary className="session-plan-summary">
          <span className="session-plan-kicker">Loop</span>
          <strong>Off</strong>
          <span>Set up long-running work only when this chat needs it</span>
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
              <span>Long-run goal</span>
              <input
                value={setupGoal}
                onChange={(event) => setSetupGoal(event.target.value)}
                placeholder="Track subnet 19 evidence and prepare weekly updates"
                disabled={starting}
              />
            </label>
            <button type="submit" className="secondary-action" disabled={!goal || starting || !onStart}>
              {starting ? "Starting setup" : "Start setup"}
            </button>
            <p className="session-loop-setup-note">
              Creates a draft LOOP.md and asks one calibration question before any autonomous loop turn can run.
            </p>
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
  const compaction = loopCompaction(state);
  const calibrationQuestion = loopCalibrationQuestion(state);
  const calibration = loopCalibration(state);
  const calibrationQuestions = state?.calibration_questions ?? 0;
  const calibrationAnswers = state?.calibration_answers ?? 0;
  const disabled = status === "disabled";
  const draft = status === "draft";
  const title = disabled ? "Disabled" : statusLabel(status);
  const detail = draft
    ? calibrationAnswers > 0 ? "Calibration recorded; ready for activation review" : calibrationQuestions > 0 ? "Waiting for your calibration answer" : "Waiting for Affent to ask calibration"
    : loopDetail({ goal, feeds, updates, event });

  return (
    <details className="session-plan-panel session-loop-panel" data-testid="session-loop-panel" {...(defaultOpen ? { open: true } : {})}>
      <summary className="session-plan-summary">
        <span className="session-plan-kicker">Loop</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-plan-body session-loop-body">
        <LoopStatusCallout status={disabled ? "disabled" : draft ? "draft" : status === "running" ? "running" : "unknown"} calibrationAnswers={calibrationAnswers} />
        <LoopActivationChecklist status={disabled ? "disabled" : draft ? "draft" : status === "running" ? "running" : "unknown"} calibrationAnswers={calibrationAnswers} />
        <LoopNextStep
          status={disabled ? "disabled" : draft ? "draft" : status === "running" ? "running" : "unknown"}
          goal={goal}
          calibrationQuestion={calibrationQuestion}
          calibrationAnswers={calibrationAnswers}
          onUseAsDraft={onUseAsDraft}
        />
        <div className="session-loop-grid">
          {goal ? <LoopField label="Goal" value={goal} /> : null}
          {path ? <LoopField label="File" value={path} mono /> : null}
          {feeds > 0 ? <LoopField label="Feeds" value={String(feeds)} /> : null}
          {updates > 0 ? <LoopField label="Updates" value={String(updates)} /> : null}
          {calibrationQuestion ? <LoopField label="Setup question" value={calibrationQuestion} /> : null}
          {calibration ? <LoopField label="Calibration" value={calibration} /> : null}
          {memory ? <LoopField label="Memory" value={memory} /> : null}
          {compaction ? <LoopField label="Compaction" value={compaction} /> : null}
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

function LoopNextStep({
  status,
  goal,
  calibrationQuestion,
  calibrationAnswers = 0,
  onUseAsDraft,
}: {
  status: "off" | "draft" | "running" | "disabled" | "unknown";
  goal?: string;
  calibrationQuestion?: string;
  calibrationAnswers?: number;
  onUseAsDraft?: () => void;
}) {
  const copy = loopNextStepCopy(status, goal, calibrationQuestion, calibrationAnswers);
  if (!copy) return null;
  const actionLabel = loopNextStepActionLabel(status, calibrationAnswers);
  return (
    <div className={`session-loop-next ${status}`} data-testid="session-loop-next">
      <div className="session-loop-next-main">
        <span>{copy.kicker}</span>
        <strong>{copy.title}</strong>
        <p>{copy.detail}</p>
      </div>
      {onUseAsDraft && actionLabel ? (
        <button type="button" className="secondary-action session-loop-next-action" onClick={onUseAsDraft}>
          {actionLabel}
        </button>
      ) : null}
    </div>
  );
}

function loopNextStepCopy(
  status: "off" | "draft" | "running" | "disabled" | "unknown",
  goal?: string,
  calibrationQuestion?: string,
  calibrationAnswers = 0,
) {
  if (status === "off") {
    return {
      kicker: "Next",
      title: "Create draft, then answer Affent",
      detail: goal ? `Draft setup will use: ${goal}` : "Start setup creates LOOP.md as a draft and opens chat for calibration.",
    };
  }
  if (status === "draft" && calibrationAnswers <= 0) {
    return {
      kicker: "Waiting",
      title: "Answer the setup question",
      detail: calibrationQuestion || "Affent still needs one focused answer before LOOP.md can be activated.",
    };
  }
  if (status === "draft") {
    return {
      kicker: "Ready",
      title: "Review and activate in chat",
      detail: "Use the recorded answer to fill durable intent, stop conditions, recovery anchors, and only then mark LOOP.md running.",
    };
  }
  if (status === "running") {
    return {
      kicker: "Active",
      title: "Loop can receive timers",
      detail: "Use chat for durable protocol changes; concrete task progress should stay in plan state and trace.",
    };
  }
  if (status === "disabled") {
    return {
      kicker: "Stopped",
      title: "Loop guidance is disabled",
      detail: "Start setup again when this session needs a maintained LOOP.md.",
    };
  }
  return {
    kicker: "Check",
    title: "Review LOOP.md",
    detail: "Load the protocol before continuing long-running work.",
  };
}

function loopNextStepActionLabel(status: "off" | "draft" | "running" | "disabled" | "unknown", calibrationAnswers = 0): string | undefined {
  if (status === "draft" && calibrationAnswers <= 0) return "Open answer draft";
  if (status === "draft") return "Review in chat";
  if (status === "running") return "Update via chat";
  return undefined;
}

function LoopActivationChecklist({ status, calibrationAnswers = 0 }: { status: "off" | "draft" | "running" | "disabled" | "unknown"; calibrationAnswers?: number }) {
  const steps = loopActivationSteps(status, calibrationAnswers);
  if (steps.length === 0) return null;
  return (
    <ol className="session-loop-checklist" data-testid="session-loop-checklist" aria-label="Loop activation flow">
      {steps.map((step) => (
        <li key={step.label} data-state={step.state}>
          <span aria-hidden="true">{step.icon}</span>
          <div>
            <strong>{step.label}</strong>
            <small>{step.detail}</small>
          </div>
        </li>
      ))}
    </ol>
  );
}

function loopActivationSteps(status: "off" | "draft" | "running" | "disabled" | "unknown", calibrationAnswers = 0) {
  if (status === "disabled") return [];
  if (status === "running") {
    return [
      { label: "Protocol active", detail: "Future loop turns receive LOOP.md with recovery context.", state: "done", icon: "OK" },
      { label: "Maintain only durable changes", detail: "Task progress stays in plan state; LOOP.md stays compact.", state: "active", icon: "!" },
    ];
  }
  const draft = status === "draft";
  const answered = draft && calibrationAnswers > 0;
  return [
    { label: "Create draft", detail: draft ? "LOOP.md exists but is not running yet." : "Set up LOOP.md before autonomous work starts.", state: draft ? "done" : "active", icon: draft ? "OK" : "1" },
    { label: "Ask calibration", detail: answered ? "A calibration answer is recorded for this LOOP.md." : "Affent asks for intent, stop conditions, memory policy, and recovery expectations.", state: answered ? "done" : draft ? "active" : "pending", icon: answered ? "OK" : draft ? "!" : "2" },
    { label: "Activate after answer", detail: "Only then can the model complete_activation with Current Situation <= 1200 chars.", state: answered ? "active" : "pending", icon: answered ? "!" : "3" },
  ];
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
    event.reactive ? "reactive" : undefined,
    event.reason ? `reason ${event.reason}` : undefined,
    event.sections_changed && event.sections_changed.length > 0 ? `sections ${event.sections_changed.join(", ")}` : undefined,
    event.calibration_preview ? `calibration ${event.calibration_preview}` : undefined,
    event.memory_preview ? `memory ${event.memory_preview}` : undefined,
    event.decision ? `decision ${event.decision}` : undefined,
    event.turn_end_reason ? `turn ${event.turn_end_reason}` : undefined,
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : undefined;
}

function loopEventLabel(type: string): string {
  if (type === "loop.protocol_init") return "Initialized LOOP.md";
  if (type === "loop.protocol_update") return "Updated LOOP.md";
  if (type === "loop.protocol_calibration_request") return "Asked loop calibration question";
  if (type === "loop.protocol_calibration") return "Recorded calibration answer";
  if (type === "loop.protocol_activate") return "Activated LOOP.md";
  if (type === "loop.protocol_disable") return "Disabled LOOP.md";
  return type;
}

function LoopStatusCallout({ status, calibrationAnswers = 0 }: { status: "off" | "draft" | "running" | "disabled" | "unknown"; calibrationAnswers?: number }) {
  const copy = loopStatusCopy(status, calibrationAnswers);
  return (
    <div className={`session-loop-callout ${status}`} data-testid="session-loop-callout">
      <strong>{copy.title}</strong>
      <span>{copy.detail}</span>
    </div>
  );
}

function loopStatusCopy(status: "off" | "draft" | "running" | "disabled" | "unknown", calibrationAnswers = 0) {
  if (status === "draft") {
    if (calibrationAnswers > 0) {
      return {
        title: "Activation review",
        detail: "Affent has your calibration answer and should update LOOP.md before activating.",
      };
    }
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

function loopCalibration(state?: SessionLoopState): string | undefined {
  if (!state) return undefined;
  const count = state.calibration_answers ?? 0;
  const preview = compact(state.last_calibration_answer_preview);
  if (count <= 0 && !preview) return undefined;
  const parts = [count > 0 ? `${count} calibration ${count === 1 ? "answer" : "answers"}` : undefined, preview].filter(Boolean);
  return parts.join(" · ");
}

function loopCalibrationQuestion(state?: SessionLoopState): string | undefined {
  if (!state) return undefined;
  const count = state.calibration_questions ?? 0;
  const preview = compact(state.last_calibration_question_preview);
  if (count <= 0 && !preview) return undefined;
  const parts = [count > 0 ? `${count} setup ${count === 1 ? "question" : "questions"}` : undefined, preview].filter(Boolean);
  return parts.join(" · ");
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

function loopCompaction(state?: SessionLoopState): string | undefined {
  if (!state) return undefined;
  const count = state.context_compactions ?? 0;
  const parts = [
    count > 0 ? `${count} ${count === 1 ? "compaction" : "compactions"}` : undefined,
    state.last_context_compaction_reactive ? "reactive" : undefined,
    compact(state.last_context_compaction_reason),
  ].filter(Boolean);
  return parts.length > 0 ? parts.join(" · ") : undefined;
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
