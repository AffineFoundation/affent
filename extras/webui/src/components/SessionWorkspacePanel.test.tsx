import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import { SessionWorkspacePanel } from "./SessionWorkspacePanel";

describe("SessionWorkspacePanel", () => {
  it("renders workspace binding, cwd, mismatch issue, and copy actions", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    const onUseAsDraft = vi.fn();
    const onVerifyWorkspace = vi.fn().mockResolvedValue(undefined);
    const onOpenWorkspacePath = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    render(<SessionWorkspacePanel defaultOpen workspace={workspace} onVerifyWorkspace={onVerifyWorkspace} onOpenWorkspacePath={onOpenWorkspacePath} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("Workspace mismatch");
    expect(panel).toHaveTextContent("Latest command cwd is outside the session workspace.");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("Session workspace");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("/repo/affent");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("Latest command cwd");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("/tmp/extras/webui");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Binding");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Recorded");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Agent cwd");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Outside");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("Recorded agent cwd");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("/tmp");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("Branch");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("main");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("State");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("dirty");

    await user.click(within(panel).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith("/repo/affent");
    await user.click(within(panel).getByRole("button", { name: "Copy cwd" }));
    expect(writeText).toHaveBeenCalledWith("/tmp/extras/webui");
    await user.click(within(panel).getByRole("button", { name: "Copy workspace evidence" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Issue: Latest command cwd is outside the session workspace."));
    await user.click(within(panel).getByRole("button", { name: "Browse root" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith(".");
    expect(within(panel).queryByRole("button", { name: "Open cwd" })).toBeNull();
    await user.click(within(panel).getByRole("button", { name: "Verify workspace" }));
    expect(onVerifyWorkspace).toHaveBeenCalledWith({
      command: "pwd; git status --short --branch 2>/dev/null || true",
      cwd: "/repo/affent",
    });
    await user.click(within(panel).getByRole("button", { name: "Ask to verify" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      expect.stringContaining("Verify this workspace mismatch before making more file changes or running commands"),
      "workspace",
    );
  });

  it("does not mark historical cwd-only sessions as verified and can draft verification", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    render(<SessionWorkspacePanel defaultOpen workspace={{
      hasData: true,
      summary: "Workspace binding missing",
      shortStatus: "Workspace binding missing",
      detail: "cwd /workspace/sessions/sess_123",
      verification: "missing_binding",
      lastAgentCwd: "/workspace/sessions/sess_123",
    }} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(panel).toHaveTextContent("Binding missing");
    expect(panel).toHaveTextContent("Recorded cwd only");
    expect(screen.getByTestId("session-workspace-card")).toHaveAttribute("data-tone", "warning");
    expect(panel).not.toHaveTextContent("Boundary verified");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("Session workspace");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("Not recorded");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("Last agent cwd");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("/workspace/sessions/sess_123");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Binding");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Missing");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("historical cwd");
    expect(panel).toHaveTextContent("Use cwd in chat");
    await user.click(within(panel).getByRole("button", { name: "Draft verification" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      expect.stringContaining("Working directory: /workspace/sessions/sess_123"),
      "run_command",
    );
  });

  it("labels command-cwd verification honestly when no workspace binding exists", async () => {
    const user = userEvent.setup();
    const onVerifyWorkspace = vi.fn();
    render(<SessionWorkspacePanel defaultOpen workspace={{
      hasData: true,
      summary: "Workspace binding missing",
      shortStatus: "Workspace binding missing",
      detail: "cwd /workspace/sessions/sess_123",
      verification: "missing_binding",
      latestCommandCwd: "/workspace/sessions/sess_123",
    }} onVerifyWorkspace={onVerifyWorkspace} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(within(panel).queryByRole("button", { name: "Verify workspace" })).toBeNull();
    await user.click(within(panel).getByRole("button", { name: "Verify cwd" }));
    expect(onVerifyWorkspace).toHaveBeenCalledWith({
      command: "pwd; git status --short --branch 2>/dev/null || true",
      cwd: "/workspace/sessions/sess_123",
    });
  });

  it("labels missing bindings with latest command cwd evidence as command cwd only", () => {
    render(<SessionWorkspacePanel defaultOpen workspace={{
      hasData: true,
      summary: "Workspace binding missing",
      shortStatus: "Workspace binding missing",
      detail: "cwd /workspace/sessions/sess_123",
      verification: "missing_binding",
      lastAgentCwd: "/tmp/old",
      latestCommandCwd: "/workspace/sessions/sess_123",
    }} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(panel).toHaveTextContent("Command cwd only");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("Latest command cwd");
    expect(screen.getByTestId("session-workspace-boundary")).toHaveTextContent("/workspace/sessions/sess_123");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("Recorded agent cwd");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("/tmp/old");
  });

  it("opens the latest in-workspace command cwd from the workspace boundary", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    render(<SessionWorkspacePanel defaultOpen workspace={{
      hasData: true,
      summary: "affent",
      shortStatus: "affent · main",
      detail: "/repo/affent · branch main · cwd /repo/affent/extras/webui",
      verification: "verified",
      label: "affent",
      path: "/repo/affent",
      branch: "main",
      lastAgentCwd: "/repo/affent/extras/webui",
      latestCommandCwd: "/repo/affent/extras/webui",
    }} onOpenWorkspacePath={onOpenWorkspacePath} />);

    const panel = screen.getByTestId("session-workspace-panel");
    await user.click(within(panel).getByRole("button", { name: "Open cwd" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("extras/webui");
  });

  it("makes a missing workspace binding actionable without pretending it exists", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    render(<SessionWorkspacePanel defaultOpen workspace={{
      hasData: false,
      summary: "No workspace evidence",
      shortStatus: "No workspace evidence",
      detail: "No workspace binding or command cwd recorded.",
      verification: "unknown",
    }} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(panel).toHaveTextContent("No workspace evidence");
    expect(panel).toHaveTextContent("Not recorded");
    expect(panel).toHaveTextContent("Locate workspace");
    expect(panel).not.toHaveTextContent("Use workspace in chat");
    await user.click(within(panel).getByRole("button", { name: "Locate workspace" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      expect.stringContaining("Identify the current workspace before making file changes or running project commands"),
      "workspace",
    );
  });

  it("shows runtime workspace path mode and root entries", () => {
    render(<SessionWorkspacePanel defaultOpen workspace={{
      hasData: true,
      summary: "Runtime workspace",
      shortStatus: "Runtime workspace",
      detail: "workspace-relative",
      verification: "runtime",
      runtimeDefaultCwd: "workspace_root",
      runtimePathMode: "workspace_relative",
      runtimeRoot: ".",
      runtimeRootEntryCount: 3,
      runtimeRootEntries: [
        { name: "README.md", kind: "file" },
        { name: "src", kind: "dir" },
        { name: "package.json", kind: "file" },
      ],
    }} />);

    const panel = screen.getByTestId("session-workspace-panel");
    expect(panel).toHaveTextContent("Runtime workspace");
    expect(panel).toHaveTextContent("Workspace-relative runtime");
    expect(panel).toHaveTextContent("Workspace tools start at `.`");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("Runtime path mode");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("workspace-relative");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("Default cwd");
    expect(screen.getByLabelText("Workspace fields")).toHaveTextContent("workspace root");
    expect(screen.getByLabelText("Workspace review facts")).toHaveTextContent("Runtime paths");
    expect(screen.getByTestId("session-workspace-root-entries")).toHaveTextContent("Root entries");
    expect(screen.getByTestId("session-workspace-root-entries")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-root-entries")).toHaveTextContent("src");
    expect(screen.getByTestId("session-workspace-root-entries")).toHaveTextContent("dir");
  });
});

const workspace: SessionWorkspaceView = {
  hasData: true,
  summary: "Workspace mismatch",
  shortStatus: "Workspace mismatch",
  detail: "/repo/affent · branch main · dirty · cwd /tmp",
  verification: "mismatch",
  tone: "warning",
  label: "affent",
  path: "/repo/affent",
  branch: "main",
  dirtyState: "dirty",
  lastAgentCwd: "/tmp",
  latestCommandCwd: "/tmp/extras/webui",
  issue: "Latest command cwd is outside the session workspace.",
};
