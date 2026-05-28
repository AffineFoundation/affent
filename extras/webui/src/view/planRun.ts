import type { SessionPlanSummary } from "../api/sessions";

export interface PlanRunState {
  sessionId: string;
  issuedSteps: number;
  maxSteps: number;
  lastCompletedSteps: number;
  lastCurrentStepIndex: number;
  lastPlanMutationCount: number;
  waitingForProgress: boolean;
}

export type PlanRunContinuationDecision =
  | { action: "idle" }
  | { action: "wait" }
  | { action: "clear" }
  | { action: "pause"; detail: string }
  | { action: "limit"; detail: string }
  | { action: "continue" };

export interface PlanRunContinuationInput {
  state?: PlanRunState;
  sessionId?: string;
  busy: boolean;
  sessionRunning: boolean;
  hasPendingMessage: boolean;
  planLoading: boolean;
  planMutationCount: number;
  fetchedPlanKey: string;
  summary?: SessionPlanSummary;
}

export function decidePlanRunContinuation(input: PlanRunContinuationInput): PlanRunContinuationDecision {
  const state = input.state;
  if (!state || state.sessionId !== input.sessionId) return { action: "idle" };
  if (input.busy || input.sessionRunning || input.hasPendingMessage || input.planLoading) return { action: "wait" };
  if (
    state.waitingForProgress
    && input.planMutationCount > state.lastPlanMutationCount
    && input.fetchedPlanKey !== `${input.sessionId}:${input.planMutationCount}`
  ) {
    return { action: "wait" };
  }

  const summary = input.summary;
  if (!summary || summary.done || summary.blocked || !summary.active) return { action: "clear" };

  const currentStepIndex = summary.current_step_index ?? 0;
  const progressed = summary.completed_steps > state.lastCompletedSteps
    || (currentStepIndex > 0 && currentStepIndex !== state.lastCurrentStepIndex);
  if (state.waitingForProgress && !progressed) {
    return { action: "pause", detail: "plan paused because the current step did not advance" };
  }
  if (state.issuedSteps >= state.maxSteps) {
    return { action: "limit", detail: "plan run stopped at the step safety limit" };
  }
  return { action: "continue" };
}

export function nextIssuedPlanRunState({
  current,
  sessionId,
  summary,
  stepIndex,
  maxSteps,
  planMutationCount,
}: {
  current?: PlanRunState;
  sessionId: string;
  summary?: SessionPlanSummary;
  stepIndex: number;
  maxSteps: number;
  planMutationCount: number;
}): PlanRunState {
  const base = current?.sessionId === sessionId
    ? current
    : {
        sessionId,
        issuedSteps: 0,
        maxSteps,
        lastCompletedSteps: summary?.completed_steps ?? 0,
        lastCurrentStepIndex: stepIndex,
        lastPlanMutationCount: planMutationCount,
        waitingForProgress: false,
      };
  return {
    ...base,
    maxSteps: Math.max(base.maxSteps, maxSteps),
    issuedSteps: base.issuedSteps + 1,
    lastCompletedSteps: summary?.completed_steps ?? base.lastCompletedSteps,
    lastCurrentStepIndex: stepIndex || base.lastCurrentStepIndex,
    lastPlanMutationCount: planMutationCount,
    waitingForProgress: true,
  };
}
