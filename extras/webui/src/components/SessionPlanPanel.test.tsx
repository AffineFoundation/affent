import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SessionPlanPanel } from "./SessionPlanPanel";

describe("SessionPlanPanel", () => {
  it("surfaces step execution actions for active plans", () => {
    const onExecuteCurrentStep = vi.fn();
    const onRunRemaining = vi.fn();
    render(
      <SessionPlanPanel
        summary={{
          label: "plan:0/2:active",
          total_steps: 2,
          completed_steps: 0,
          current_step_index: 1,
          current_step_status: "in_progress",
          active: true,
          blocked: false,
          done: false,
          error: false,
        }}
        plan={{ steps: [{ text: "Fix failing test", status: "in_progress" }] }}
        onExecuteCurrentStep={onExecuteCurrentStep}
        onRunRemaining={onRunRemaining}
      />,
    );

    const panel = screen.getByTestId("session-plan-panel");
    expect(panel).not.toHaveAttribute("open");
    fireEvent.click(panel.querySelector("summary")!);

    fireEvent.click(screen.getByRole("button", { name: "Run current step" }));
    fireEvent.click(screen.getByRole("button", { name: "Run remaining steps" }));

    expect(onExecuteCurrentStep).toHaveBeenCalledTimes(1);
    expect(onRunRemaining).toHaveBeenCalledTimes(1);
  });

  it("shows stop control while a remaining-plan run is active", () => {
    const onStopRunRemaining = vi.fn();
    render(
      <SessionPlanPanel
        summary={{
          label: "plan:1/3:active",
          total_steps: 3,
          completed_steps: 1,
          current_step_index: 2,
          current_step_status: "in_progress",
          active: true,
          blocked: false,
          done: false,
          error: false,
        }}
        plan={{ steps: [{ text: "Run migration", status: "in_progress" }] }}
        runRemainingActive
        onStopRunRemaining={onStopRunRemaining}
      />,
    );

    fireEvent.click(screen.getByTestId("session-plan-panel").querySelector("summary")!);
    fireEvent.click(screen.getByRole("button", { name: "Stop after this step" }));

    expect(onStopRunRemaining).toHaveBeenCalledTimes(1);
    expect(screen.queryByRole("button", { name: "Run remaining steps" })).toBeNull();
  });

  it("opens automatically for loading and error states", () => {
    const { rerender } = render(<SessionPlanPanel loading />);

    expect(screen.getByTestId("session-plan-panel")).toHaveAttribute("open");

    rerender(<SessionPlanPanel error="Plan could not be read" />);

    expect(screen.getByTestId("session-plan-panel")).toHaveAttribute("open");
  });
});
