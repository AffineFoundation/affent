import { describe, expect, it } from "vitest";
import type { SessionPlanSummary } from "../api/sessions";
import { decidePlanRunContinuation, nextIssuedPlanRunState, type PlanRunState } from "./planRun";

describe("planRun", () => {
  it("waits for the matching plan fetch after a plan mutation", () => {
    expect(decidePlanRunContinuation({
      state: runState({ lastPlanMutationCount: 1, waitingForProgress: true }),
      sessionId: "s1",
      busy: false,
      sessionRunning: false,
      hasPendingMessage: false,
      planLoading: false,
      planMutationCount: 2,
      fetchedPlanKey: "s1:1",
      summary: summary({ completed_steps: 1, current_step_index: 2 }),
    })).toEqual({ action: "wait" });
  });

  it("continues when the active plan advanced", () => {
    expect(decidePlanRunContinuation({
      state: runState({ lastCompletedSteps: 1, lastCurrentStepIndex: 2, waitingForProgress: true }),
      sessionId: "s1",
      busy: false,
      sessionRunning: false,
      hasPendingMessage: false,
      planLoading: false,
      planMutationCount: 2,
      fetchedPlanKey: "s1:2",
      summary: summary({ completed_steps: 2, current_step_index: 3 }),
    })).toEqual({ action: "continue" });
  });

  it("pauses when a step turn finished without plan progress", () => {
    expect(decidePlanRunContinuation({
      state: runState({ lastCompletedSteps: 1, lastCurrentStepIndex: 2, waitingForProgress: true }),
      sessionId: "s1",
      busy: false,
      sessionRunning: false,
      hasPendingMessage: false,
      planLoading: false,
      planMutationCount: 2,
      fetchedPlanKey: "s1:2",
      summary: summary({ completed_steps: 1, current_step_index: 2 }),
    })).toEqual({
      action: "pause",
      detail: "plan paused because the current step did not advance",
    });
  });

  it("clears an automatic run when the plan is done or blocked", () => {
    expect(decidePlanRunContinuation({
      state: runState(),
      sessionId: "s1",
      busy: false,
      sessionRunning: false,
      hasPendingMessage: false,
      planLoading: false,
      planMutationCount: 2,
      fetchedPlanKey: "s1:2",
      summary: summary({ done: true, active: false }),
    })).toEqual({ action: "clear" });

    expect(decidePlanRunContinuation({
      state: runState(),
      sessionId: "s1",
      busy: false,
      sessionRunning: false,
      hasPendingMessage: false,
      planLoading: false,
      planMutationCount: 2,
      fetchedPlanKey: "s1:2",
      summary: summary({ blocked: true, active: false }),
    })).toEqual({ action: "clear" });
  });

  it("stops at the step safety limit", () => {
    expect(decidePlanRunContinuation({
      state: runState({ issuedSteps: 5, maxSteps: 5 }),
      sessionId: "s1",
      busy: false,
      sessionRunning: false,
      hasPendingMessage: false,
      planLoading: false,
      planMutationCount: 2,
      fetchedPlanKey: "s1:2",
      summary: summary({ completed_steps: 2, current_step_index: 3 }),
    })).toEqual({
      action: "limit",
      detail: "plan run stopped at the step safety limit",
    });
  });

  it("records the baseline for each issued execute-plan turn", () => {
    expect(nextIssuedPlanRunState({
      sessionId: "s1",
      summary: summary({ completed_steps: 1, current_step_index: 2 }),
      stepIndex: 2,
      maxSteps: 4,
      planMutationCount: 3,
    })).toMatchObject({
      sessionId: "s1",
      issuedSteps: 1,
      maxSteps: 4,
      lastCompletedSteps: 1,
      lastCurrentStepIndex: 2,
      lastPlanMutationCount: 3,
      waitingForProgress: true,
    });
  });
});

function runState(overrides: Partial<PlanRunState> = {}): PlanRunState {
  return {
    sessionId: "s1",
    issuedSteps: 1,
    maxSteps: 4,
    lastCompletedSteps: 0,
    lastCurrentStepIndex: 1,
    lastPlanMutationCount: 1,
    waitingForProgress: false,
    ...overrides,
  };
}

function summary(overrides: Partial<SessionPlanSummary> = {}): SessionPlanSummary {
  return {
    label: "plan:1/3:active",
    total_steps: 3,
    completed_steps: 1,
    current_step_index: 2,
    current_step_status: "in_progress",
    active: true,
    blocked: false,
    done: false,
    error: false,
    ...overrides,
  };
}
