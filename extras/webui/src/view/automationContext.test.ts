import { describe, expect, it } from "vitest";
import { buildAutomationContext, shouldShowLoopContext, shouldShowScheduleContext } from "./automationContext";

describe("automationContext view model", () => {
  it("keeps empty automation out of the surface", () => {
    expect(shouldShowLoopContext(undefined, undefined, { state: "idle" }, false)).toBe(false);
    expect(shouldShowScheduleContext(undefined, { state: "idle" }, undefined, undefined, undefined)).toBe(false);
  });

  it("summarizes draft loop and pending timer next actions", () => {
    const context = buildAutomationContext(
      {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        has_loop_protocol: true,
        loop_protocol: {
          bytes: 256,
          status: "draft",
          state: {
            version: 1,
            status: "draft",
            calibration_questions: 1,
            calibration_answers: 0,
          },
        },
        has_schedules: true,
        schedules: {
          count: 1,
          enabled: 1,
          pending_loop_ticks: 1,
        },
      },
      undefined,
      { state: "idle" },
      { state: "idle" },
    );

    expect(context).toEqual({
      title: "Loop waiting · 1 timer active",
      detail: "Answer the setup question before LOOP.md can run. · 1 timer enabled; open Automation to inspect the next run.",
    });
  });

  it("uses concrete timer failure and active-run details", () => {
    expect(buildAutomationContext(
      {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        has_schedules: true,
        schedules: {
          count: 2,
          enabled: 1,
          error_count: 1,
          last_error: "provider unavailable",
        },
      },
      { version: 1, status: "running" },
      { state: "idle" },
      { state: "idle" },
    )).toEqual({
      title: "Loop running · Timer failed",
      detail: "LOOP.md is active; use chat for durable protocol changes. · 1 timer error: provider unavailable",
    });
  });

  it("gives paused timers an explicit next action", () => {
    expect(buildAutomationContext(
      {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        has_schedules: true,
        schedules: {
          count: 2,
          enabled: 0,
        },
      },
      undefined,
      { state: "idle" },
      { state: "idle" },
    )).toEqual({
      title: "2 timers paused",
      detail: "2 timers paused; resume before the next needed check-in or delete it.",
    });
  });

  it("keeps timer next-run guidance under the unified Automation entry", () => {
    expect(buildAutomationContext(
      {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        has_schedules: true,
        schedules: {
          count: 1,
          enabled: 1,
        },
      },
      undefined,
      { state: "idle" },
      { state: "idle" },
    )).toEqual({
      title: "1 timer active",
      detail: "1 timer enabled; open Automation to inspect the next run.",
    });

    expect(buildAutomationContext(
      {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        has_schedules: true,
      },
      undefined,
      { state: "idle" },
      { state: "idle" },
    )).toEqual({
      title: "Timer details needed",
      detail: "Load timer details before pausing, resuming, or deleting timers.",
    });
  });
});
