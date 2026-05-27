import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionSchedulePanel } from "./SessionSchedulePanel";

describe("SessionSchedulePanel", () => {
  it("shows enabled loop ticks as pending until LOOP.md is running", () => {
    render(
      <SessionSchedulePanel
        summary={{
          count: 1,
          enabled: 1,
          enabled_loop_ticks: 1,
          pending_loop_ticks: 1,
          next_run_at: "2026-05-27T14:00:00Z",
          next_schedule_id: "sched_loop",
          next_prompt_preview: "Scheduled loop tick for session: long running runtime improvement",
        }}
        schedules={[
          {
            id: "sched_loop",
            kind: "loop_tick",
            prompt: "Scheduled loop tick for session: long running runtime improvement",
            enabled: true,
            next_run_at: "2026-05-27T14:00:00Z",
            repeat_interval_seconds: 1800,
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
        ]}
      />,
    );

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel).toHaveTextContent("1 pending");
    expect(panel).toHaveTextContent("Loop timer waits for LOOP.md activation");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Loop tick");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Pending calibration");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("waiting for LOOP.md activation");
  });

  it("shows loop ticks as active when LOOP.md is running", () => {
    render(
      <SessionSchedulePanel
        loopStatus="running"
        summary={{ count: 1, enabled: 1, next_prompt_preview: "Scheduled loop tick for session: runtime" }}
        schedules={[
          {
            id: "sched_loop",
            kind: "loop_tick",
            prompt: "Scheduled loop tick for session: runtime",
            enabled: true,
            next_run_at: "2026-05-27T14:00:00Z",
            repeat_interval_seconds: 1800,
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
        ]}
      />,
    );

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel).toHaveTextContent("1 active");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Loop tick");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Active");
    expect(panel).not.toHaveTextContent("Pending calibration");
  });
});
