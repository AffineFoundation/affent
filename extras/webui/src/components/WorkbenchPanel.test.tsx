import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { WorkbenchNavItem } from "../view/workbenchNav";
import { WorkbenchEmpty, WorkbenchPanel } from "./WorkbenchPanel";

const navItems: WorkbenchNavItem[] = [
  { key: "context", label: "Context", detail: "Result ready", scope: "current" },
  { key: "run", label: "Run", detail: "1 failed", scope: "current", badge: "1", tone: "error" },
  { key: "config", label: "Config", detail: "1 env configured", scope: "platform", badge: "1" },
];

describe("WorkbenchPanel", () => {
  it("renders a stable section nav and switches tabs through the owner", async () => {
    const user = userEvent.setup();
    const onSelectTab = vi.fn();
    const onClose = vi.fn();

    render(
      <WorkbenchPanel
        title="Workbench"
        subtitle="Global runtime console"
        attachment={{
          label: "Attached chat",
          title: "Fix checkout tests",
          detail: "session-123",
          metrics: ["Live", "affent · main", "0.0015M tokens"],
          tone: "live",
        }}
        navItems={navItems}
        activeTab="context"
        onSelectTab={onSelectTab}
        onClose={onClose}
      >
        <div data-testid="active-tab">Context content</div>
      </WorkbenchPanel>,
    );

    expect(screen.getByTestId("workbench-panel")).toHaveAccessibleName("Workbench");
    expect(screen.getByText("Global runtime console")).toBeInTheDocument();
    expect(screen.getByTestId("workbench-attachment")).toHaveTextContent("Attached chat");
    expect(screen.getByTestId("workbench-attachment")).toHaveTextContent("Fix checkout tests");
    expect(screen.getByTestId("workbench-attachment")).toHaveTextContent("0.0015M tokens");
    expect(screen.getByText("Current chat")).toBeInTheDocument();
    expect(screen.getByText("Platform")).toBeInTheDocument();
    expect(screen.getByTestId("active-tab")).toHaveTextContent("Context content");
    expect(within(screen.getByRole("navigation", { name: "Workbench sections" })).getByRole("button", { name: /^Run\b/ })).toHaveAttribute("data-tone", "error");

    await user.click(screen.getByRole("button", { name: /^Run\b/ }));
    expect(onSelectTab).toHaveBeenCalledWith("run");

    await user.click(screen.getByRole("button", { name: "Close Workbench" }));
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it("keeps empty states short and factual", () => {
    render(<WorkbenchEmpty title="No active automation" detail="Loop and timer controls appear here when this chat has long-running work." />);

    expect(screen.getByTestId("workbench-empty")).toHaveTextContent("No active automation");
    expect(screen.getByTestId("workbench-empty")).not.toHaveTextContent("Workbench lets you");
  });
});
