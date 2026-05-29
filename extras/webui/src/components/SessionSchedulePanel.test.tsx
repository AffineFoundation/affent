import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionSchedulePanel } from "./SessionSchedulePanel";

describe("SessionSchedulePanel", () => {
  it("blocks resuming paused loop ticks until LOOP.md is running", () => {
    render(
      <SessionSchedulePanel
        loopStatus="draft"
        defaultOpen
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
        defaultOpen
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
        defaultOpen
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
        defaultOpen
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
        defaultOpen
        onScheduleCheckIn={() => undefined}
        onScheduleLoopTick={() => undefined}
        onScheduleDaily={() => undefined}
      />,
    );

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel).toHaveTextContent("Off");
    expect(panel).toHaveTextContent("No scheduled follow-ups for this chat.");
    expect(screen.getByRole("button", { name: "Schedule 1h check-in" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Schedule 30m loop tick" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Schedule daily check-in" })).toBeInTheDocument();
  });

  it("keeps timer controls fully folded by default", () => {
    render(<SessionSchedulePanel summary={{ count: 0, enabled: 0 }} />);

    expect(screen.getByTestId("session-schedule-panel")).not.toHaveAttribute("open");
  });

  it("can render inside the unified automation surface without a nested disclosure", () => {
    render(<SessionSchedulePanel summary={{ count: 0, enabled: 0 }} embedded />);

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel.tagName).toBe("SECTION");
    expect(panel).toHaveTextContent("No scheduled follow-ups for this chat.");
    expect(panel).not.toHaveAttribute("open");
  });

  it("separates unloaded timer details from an empty timer state", () => {
    render(<SessionSchedulePanel defaultOpen onLoadSchedules={() => undefined} />);

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel).toHaveTextContent("Timer details needed");
    expect(panel).toHaveTextContent("Load details before pausing, resuming, or deleting timers.");
    expect(screen.getByRole("button", { name: "Load timer details" })).toBeInTheDocument();
    expect(panel).not.toHaveTextContent("Off");
    expect(panel).not.toHaveTextContent("No scheduled follow-ups for this chat.");
  });

  it("loads timer details without presenting Timers as a separate entry", () => {
    const onLoadSchedules = () => undefined;
    const { rerender } = render(<SessionSchedulePanel summary={{ count: 1, enabled: 1 }} defaultOpen onLoadSchedules={onLoadSchedules} />);

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel).toHaveTextContent("1 active");
    expect(panel).toHaveTextContent("1 active timer; load details to inspect the next run.");
    expect(panel).not.toHaveTextContent("No scheduled follow-ups for this chat.");
    expect(screen.getByRole("button", { name: "Load timer details" })).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "View timers" })).toBeNull();

    rerender(<SessionSchedulePanel summary={{ count: 1, enabled: 1 }} defaultOpen loading onLoadSchedules={onLoadSchedules} />);
    expect(screen.getByRole("button", { name: "Loading timer details" })).toBeDisabled();

    rerender(
      <SessionSchedulePanel
        summary={{ count: 1, enabled: 1 }}
        defaultOpen
        onLoadSchedules={onLoadSchedules}
        schedules={[
          {
            id: "sched_checkin",
            kind: "checkin",
            prompt: "Check runtime health",
            enabled: true,
            next_run_at: "2026-05-27T14:00:00Z",
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
        ]}
      />,
    );
    expect(screen.getByRole("button", { name: "Refresh timer details" })).toBeInTheDocument();
  });

  it("keeps paused timer summaries actionable when next-run metadata is absent", () => {
    render(<SessionSchedulePanel summary={{ count: 2, enabled: 0 }} defaultOpen onLoadSchedules={() => undefined} />);

    const panel = screen.getByTestId("session-schedule-panel");
    expect(panel).toHaveTextContent("2 paused");
    expect(panel).toHaveTextContent("2 paused timers; load details before resuming or deleting.");
    expect(panel).not.toHaveTextContent("No scheduled follow-ups for this chat.");
  });

  it("confirms before deleting a timer", async () => {
    const user = userEvent.setup();
    const onDeleteSchedule = vi.fn().mockResolvedValue(undefined);
    render(
      <SessionSchedulePanel
        defaultOpen
        onDeleteSchedule={onDeleteSchedule}
        summary={{ count: 1, enabled: 1 }}
        schedules={[
          {
            id: "sched_checkin",
            kind: "checkin",
            prompt: "Check runtime health",
            display_text: "Check in 1h: runtime health",
            enabled: true,
            next_run_at: "2026-05-27T14:00:00Z",
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
        ]}
      />,
    );

    const list = screen.getByTestId("session-schedule-list");
    await user.click(within(list).getByRole("button", { name: "Delete" }));
    expect(onDeleteSchedule).not.toHaveBeenCalled();
    const firstConfirm = within(list).getByRole("group", { name: "Confirm delete Check-in timer" });
    expect(firstConfirm).toHaveTextContent("Delete this timer?");
    await user.click(within(firstConfirm).getByRole("button", { name: "Cancel" }));
    expect(within(list).queryByRole("group", { name: "Confirm delete Check-in timer" })).toBeNull();

    await user.click(within(list).getByRole("button", { name: "Delete" }));
    const confirm = within(list).getByRole("group", { name: "Confirm delete Check-in timer" });
    await user.click(within(confirm).getByRole("button", { name: "Confirm" }));
    expect(onDeleteSchedule).toHaveBeenCalledWith("sched_checkin");
  });

  it("orders timers by action priority before next-run time", () => {
    render(
      <SessionSchedulePanel
        loopStatus="draft"
        defaultOpen
        summary={{ count: 4, enabled: 3, pending_loop_ticks: 1 }}
        schedules={[
          {
            id: "sched_active_later",
            kind: "checkin",
            prompt: "Later check-in",
            display_text: "Later check-in",
            enabled: true,
            next_run_at: "2026-05-27T18:00:00Z",
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
          {
            id: "sched_paused_early",
            kind: "checkin",
            prompt: "Paused early check-in",
            display_text: "Paused early check-in",
            enabled: false,
            next_run_at: "2026-05-27T12:00:00Z",
            created_at: "2026-05-27T11:30:00Z",
            updated_at: "2026-05-27T11:30:00Z",
          },
          {
            id: "sched_pending_loop",
            kind: "loop_tick",
            prompt: "Pending loop tick",
            display_text: "Pending loop tick",
            enabled: true,
            next_run_at: "2026-05-27T17:00:00Z",
            repeat_interval_seconds: 1800,
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
          },
          {
            id: "sched_failed",
            kind: "daily_checkin",
            prompt: "Failed daily check-in",
            display_text: "Failed daily check-in",
            enabled: true,
            next_run_at: "2026-05-27T19:00:00Z",
            repeat_interval_seconds: 86400,
            created_at: "2026-05-27T13:30:00Z",
            updated_at: "2026-05-27T13:30:00Z",
            last_error: "network unavailable",
          },
        ]}
      />,
    );

    const rows = within(screen.getByTestId("session-schedule-list")).getAllByRole("listitem");
    expect(rows.map((row) => row.textContent)).toEqual([
      expect.stringContaining("Failed daily check-in"),
      expect.stringContaining("Pending loop tick"),
      expect.stringContaining("Later check-in"),
      expect.stringContaining("Paused early check-in"),
    ]);
  });
});
