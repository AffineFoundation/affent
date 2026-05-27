import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionSchedulePanel } from "./SessionSchedulePanel";

describe("SessionSchedulePanel", () => {
  it("blocks resuming paused loop ticks until LOOP.md is running", () => {
    render(
      <SessionSchedulePanel
        loopStatus="draft"
        onUpdateSchedule={() => undefined}
        summary={{ count: 1, enabled: 0, enabled_loop_ticks: 0 }}
        schedules={[
          {
            id: "sched_paused_loop",
            kind: "loop_tick",
            prompt: "Scheduled loop tick for session: runtime",
            display_text: "Loop every 30m: runtime",
            enabled: false,
            next_run_at: "2026-05-27T14:00:00Z",
            repeat_interval_seconds: 1800,
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
            last_error: "LOOP.md not running; answer calibration first",
          },
        ]}
      />,
    );

    const button = screen.getByRole("button", { name: "Activate loop first" });
    expect(button).toBeDisabled();
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Paused");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("LOOP.md not running");
  });

  it("allows resuming paused loop ticks once LOOP.md is running", () => {
    render(
      <SessionSchedulePanel
        loopStatus="running"
        onUpdateSchedule={() => undefined}
        summary={{ count: 1, enabled: 0, enabled_loop_ticks: 0 }}
        schedules={[
          {
            id: "sched_paused_loop",
            kind: "loop_tick",
            prompt: "Scheduled loop tick for session: runtime",
            enabled: false,
            next_run_at: "2026-05-27T14:00:00Z",
            repeat_interval_seconds: 1800,
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
        ]}
      />,
    );

    expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
  });

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
            display_text: "Loop every 30m: long running runtime improvement",
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
    expect(screen.getByTestId("session-schedule-callout")).toHaveTextContent("Calibration pending");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Loop tick");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Loop every 30m: long running runtime improvement");
    expect(screen.getByTestId("session-schedule-list")).not.toHaveTextContent("Scheduled loop tick for session");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("Pending calibration");
    expect(screen.getByTestId("session-schedule-list")).toHaveTextContent("waiting for LOOP.md activation");
  });

  it("shows loop ticks as active when LOOP.md is running", () => {
    render(
      <SessionSchedulePanel
        loopStatus="running"
        summary={{ count: 1, enabled: 1, pending_loop_ticks: 1, next_prompt_preview: "Scheduled loop tick for session: runtime" }}
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

  it("labels timer creation as a calibration-first chat action", () => {
    render(
      <SessionSchedulePanel
        summary={{ count: 0, enabled: 0 }}
        onScheduleCheckIn={() => undefined}
        onScheduleLoopTick={() => undefined}
        onScheduleDaily={() => undefined}
      />,
    );

    const callout = screen.getByTestId("session-schedule-callout");
    expect(callout).toHaveTextContent("Calibration first");
    expect(callout).toHaveTextContent("opens chat");
    expect(screen.getByRole("button", { name: "Check in 1h" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Loop every 30m" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Daily check-in" })).toBeInTheDocument();
  });
});
