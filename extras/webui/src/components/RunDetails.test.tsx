import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { SessionOverviewMetric } from "../view/sessionOverview";
import { RunDetails } from "./RunDetails";

describe("RunDetails", () => {
  it("shows the first metric inline so high-value artifacts can surface in the chat context", () => {
    const metrics: SessionOverviewMetric[] = [
      { label: "Artifact", value: "1 file (8 KiB, 1 MiB omitted)" },
      { label: "Work", value: "1 action · 1 source" },
      { label: "Tokens", value: "138" },
    ];

    render(
      <RunDetails
        metrics={metrics}
        className="chat-context-details"
        testId="session-metrics"
        ariaLabel="Session metrics"
        summaryLabel="Session metrics"
        inlineLimit={1}
      />,
    );

    const details = screen.getByTestId("session-metrics");
    expect(within(details).getByText("Artifact 1 file (8 KiB, 1 MiB omitted)")).toBeVisible();
    expect(within(details).getByLabelText("Session metrics: 2 more metrics")).toBeInTheDocument();
  });
});
