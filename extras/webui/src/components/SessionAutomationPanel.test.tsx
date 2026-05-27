import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { SessionAutomationPanel } from "./SessionAutomationPanel";

describe("SessionAutomationPanel", () => {
  it("keeps loop and timer content under one disclosure", () => {
    render(
      <SessionAutomationPanel title="Loop waiting · 1 timer pending" detail="Answer setup question before LOOP.md can run." defaultOpen>
        <section data-testid="loop-section">Loop section</section>
        <section data-testid="timer-section">Timer section</section>
      </SessionAutomationPanel>,
    );

    const panel = screen.getByTestId("session-automation-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("Loop waiting · 1 timer pending");
    expect(screen.getByTestId("loop-section")).toBeInTheDocument();
    expect(screen.getByTestId("timer-section")).toBeInTheDocument();
  });
});
