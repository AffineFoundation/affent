import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { WorkbenchContextPanel } from "./WorkbenchContextPanel";
import type { SessionOverview } from "../view/sessionOverview";

describe("WorkbenchContextPanel", () => {
  it("opens on current chat context without promoting low-signal token counts", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({
          headline: "Fix failing checkout tests",
          detail: "Tests failed after the payment route changed.",
          stateLabel: "Review needed",
          tone: "warning",
          metrics: [
            { label: "Recovery", value: "rerun checkout spec", tone: "warning" },
            { label: "Tokens", value: "12k" },
            { label: "Artifact", value: "1 file (8 KiB)" },
          ],
        })}
      />,
    );

    const panel = screen.getByTestId("workbench-context-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("Context");
    expect(panel).toHaveTextContent("Review needed");
    expect(panel).toHaveTextContent("Fix failing checkout tests");
    expect(screen.getByTestId("workbench-context-details")).toHaveTextContent("Recovery rerun checkout spec");
    expect(screen.getByTestId("workbench-context-details")).toHaveTextContent("Artifact 1 file");
    expect(screen.getByTestId("workbench-context-details")).not.toHaveTextContent("Tokens 12k");
  });

  it("links automation only when the current session has automation attention", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({ headline: "Runtime monitor", detail: "Loop waits for calibration." })}
        automationTitle="Loop waiting · 1 timer pending"
        automationDetail="Answer setup question before LOOP.md can run."
      />,
    );

    expect(screen.getByTestId("workbench-context-automation")).toHaveTextContent("Loop waiting · 1 timer pending");
    expect(screen.getByTestId("workbench-context-automation")).toHaveTextContent("Answer setup question before LOOP.md can run.");
  });

  it("keeps fresh-task Workbench context short and actionable", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession={false}
        overview={overview({ headline: "Start a chat", detail: "Describe the outcome you want; Affent will create the chat." })}
      />,
    );

    const panel = screen.getByTestId("workbench-context-panel");
    expect(within(panel).getByText("Fresh task")).toBeVisible();
    expect(panel).toHaveTextContent("Start a task or open a saved chat");
  });
});

function overview(overrides: Partial<SessionOverview>): SessionOverview {
  return {
    headline: "Start a chat",
    detail: "Describe the outcome you want.",
    stateLabel: "Ready",
    tone: "ready",
    active: false,
    metrics: [],
    ...overrides,
  };
}
