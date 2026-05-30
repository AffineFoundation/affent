import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionAutomationPanel } from "./SessionAutomationPanel";

describe("SessionAutomationPanel", () => {
  it("keeps loop and timer content under one automation surface", () => {
    render(
      <SessionAutomationPanel title="Loop waiting · 1 timer pending" detail="Answer setup question before LOOP.md can run." defaultOpen>
        <section data-testid="loop-section">Loop section</section>
        <section data-testid="timer-section">Timer section</section>
      </SessionAutomationPanel>,
    );

    const panel = screen.getByTestId("session-automation-panel");
    expect(panel).toHaveAttribute("data-surface", "true");
    expect(panel).toHaveTextContent("Loop waiting · 1 timer pending");
    expect(screen.getByTestId("session-automation-details")).toContainElement(screen.getByTestId("loop-section"));
    expect(screen.getByTestId("session-automation-details")).toContainElement(screen.getByTestId("timer-section"));
    expect(screen.getByTestId("loop-section")).toBeInTheDocument();
    expect(screen.getByTestId("timer-section")).toBeInTheDocument();
  });

  it("shows a compact statusbar when Workbench has real automation state", () => {
    render(
      <SessionAutomationPanel
        title="Loop waiting"
        detail="Answer setup question before LOOP.md can run."
        metrics={[
          { label: "Loop", value: "Draft", detail: "Answer setup question", tone: "attention" },
          { label: "Timers", value: "Off", detail: "No scheduled follow-ups" },
          { label: "Next", value: "Loop waiting", detail: "Answer setup question before LOOP.md can run.", tone: "attention" },
          { label: "Timer", value: "1 active", detail: "Next May 27, 02:00 PM", tone: "ok" },
        ]}
        focus={{
          label: "Required action",
          title: "Answer setup question",
          detail: "Before LOOP.md can run.",
          tone: "attention",
          action: "answer",
        }}
        queue={[
          {
            id: "loop-calibration",
            label: "Required",
            title: "Answer loop calibration",
            detail: "LOOP.md is still a draft.",
            tone: "attention",
            meta: ".affent/loops/demo/LOOP.md",
          },
          {
            id: "timer-next",
            label: "Check-in",
            title: "Next May 27, 02:00 PM",
            detail: "Repeats every 1h.",
            tone: "ok",
            meta: "sched_1",
          },
        ]}
        actions={<button type="button">Answer setup</button>}
        defaultOpen
      >
        <section>Loop section</section>
      </SessionAutomationPanel>,
    );

    const focus = screen.getByTestId("session-automation-focus");
    expect(focus).toHaveTextContent("Required action");
    expect(focus).toHaveTextContent("Answer setup question");
    expect(screen.getByRole("button", { name: "Answer setup" })).toBeInTheDocument();
    const queue = screen.getByTestId("session-automation-queue");
    expect(queue).toHaveTextContent("Upcoming");
    expect(queue).toHaveTextContent("1 item");
    expect(queue).not.toHaveTextContent("Answer loop calibration");
    expect(queue).not.toHaveTextContent(".affent/loops/demo/LOOP.md");
    expect(queue).toHaveTextContent("Next May 27, 02:00 PM");
    const statusbar = screen.getByTestId("session-automation-statusbar");
    expect(statusbar).toHaveTextContent("Timer");
    expect(statusbar).toHaveTextContent("1 active");
    expect(statusbar).not.toHaveTextContent("Loop");
    expect(statusbar).not.toHaveTextContent("Draft");
    expect(statusbar).not.toHaveTextContent("Loop waiting");
    expect(screen.getByTestId("session-automation-details")).toHaveTextContent("Loop section");
    expect(
      screen.getByTestId("session-automation-details").compareDocumentPosition(queue) & Node.DOCUMENT_POSITION_FOLLOWING,
    ).toBeTruthy();
  });

  it("keeps non-critical timer focus out of the main automation surface", () => {
    render(
      <SessionAutomationPanel
        title="1 timer active"
        detail="Next May 29, 10:00 PM"
        metrics={[
          { label: "Timers", value: "1 active", detail: "Next May 29, 10:00 PM", tone: "ok" },
          { label: "Next run", value: "May 29, 10:00 PM", detail: "Check WebUI automation health", tone: "ok" },
        ]}
        focus={{
          label: "Timer active",
          title: "1 scheduled follow-up",
          detail: "Next May 29, 10:00 PM",
          tone: "ok",
        }}
        defaultOpen
      >
        <section>Timer list</section>
      </SessionAutomationPanel>,
    );

    expect(screen.queryByTestId("session-automation-focus")).toBeNull();
    expect(screen.queryByTestId("session-automation-statusbar")).toBeNull();
    expect(screen.queryByTestId("session-automation-dashboard")).toBeNull();
    expect(screen.getByTestId("session-automation-panel")).toHaveTextContent("1 timer active");
    expect(screen.getByTestId("session-automation-details")).toHaveTextContent("Timer list");
  });

  it("does not repeat the focused loop action in the execution queue", () => {
    render(
      <SessionAutomationPanel
        title="Loop waiting"
        detail="Answer setup question before LOOP.md can run."
        focus={{
          label: "Required action",
          title: "Answer setup question",
          detail: "Before LOOP.md can run.",
          tone: "attention",
          action: "answer",
        }}
        queue={[
          {
            id: "loop-calibration",
            label: "Required",
            title: "Answer loop calibration",
            detail: "LOOP.md is still a draft.",
            tone: "attention",
            meta: ".affent/loops/demo/LOOP.md",
          },
          {
            id: "timer-next",
            label: "Check-in",
            title: "Next May 27, 02:00 PM",
            detail: "Repeats every 1h.",
            tone: "ok",
            meta: "sched_1",
          },
        ]}
        defaultOpen
      >
        <section>Loop section</section>
      </SessionAutomationPanel>,
    );

    const queue = screen.getByTestId("session-automation-queue");
    expect(queue).toHaveTextContent("1 item");
    expect(queue).not.toHaveTextContent("Answer loop calibration");
    expect(queue).toHaveTextContent("Next May 27, 02:00 PM");
  });

  it("collapses empty automation into starter actions without dashboard noise", () => {
    render(
      <SessionAutomationPanel
        title="No automation"
        detail="Start a loop or schedule a check-in when this chat needs follow-up."
        empty
        defaultGoal="Investigate runtime behavior"
        metrics={[
          { label: "Loop", value: "Off", detail: "No LOOP.md" },
          { label: "Timers", value: "Off", detail: "No scheduled follow-ups" },
        ]}
        queue={[{
          id: "automation-off",
          label: "Manual",
          title: "No loop or timer armed",
          detail: "Start setup or schedule a check-in only when this session needs durable follow-up.",
        }]}
      >
        <section data-testid="loop-section">Loop section</section>
      </SessionAutomationPanel>,
    );

    const empty = screen.getByTestId("session-automation-empty");
    expect(empty).toHaveTextContent("Loop goal");
    expect(screen.getByRole("button", { name: "Set up loop" })).toBeInTheDocument();
    expect(screen.getByDisplayValue("Investigate runtime behavior")).toBeVisible();
    expect(screen.queryByTestId("session-automation-statusbar")).toBeNull();
    expect(screen.queryByTestId("session-automation-dashboard")).toBeNull();
    expect(screen.queryByTestId("session-automation-queue")).toBeNull();
    expect(screen.queryByTestId("session-automation-details")).toBeNull();
    expect(screen.queryByText("No loop or timer armed")).toBeNull();
    expect(screen.queryByText("No LOOP.md")).toBeNull();
  });
});
