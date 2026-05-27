import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import { SessionWorkspacePanel } from "./SessionWorkspacePanel";

describe("SessionWorkspacePanel", () => {
  it("renders workspace binding, cwd, mismatch issue, and copy actions", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    render(<SessionWorkspacePanel defaultOpen workspace={workspace} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("Workspace mismatch");
    expect(panel).toHaveTextContent("Latest command cwd is outside the session workspace.");
    expect(panel).toHaveTextContent("Path: /repo/affent");
    expect(panel).toHaveTextContent("Last agent cwd: /tmp");
    expect(panel).toHaveTextContent("Branch: main");
    expect(panel).toHaveTextContent("State: dirty");

    await user.click(within(panel).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith("/repo/affent");
    await user.click(within(panel).getByRole("button", { name: "Copy cwd" }));
    expect(writeText).toHaveBeenCalledWith("/tmp");
  });
});

const workspace: SessionWorkspaceView = {
  hasData: true,
  summary: "Workspace mismatch",
  detail: "/repo/affent · branch main · dirty · cwd /tmp",
  tone: "warning",
  label: "affent",
  path: "/repo/affent",
  branch: "main",
  dirtyState: "dirty",
  lastAgentCwd: "/tmp",
  issue: "Latest command cwd is outside the session workspace.",
};
