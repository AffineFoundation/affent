import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { WorkbenchContextPanel } from "./WorkbenchContextPanel";
import type { SessionOverview } from "../view/sessionOverview";

describe("WorkbenchContextPanel", () => {
  it("opens on current chat context without promoting low-signal token counts", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        onUseAsDraft={onUseAsDraft}
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
        workspace={{
          hasData: true,
          summary: "affent",
          shortStatus: "affent · main · dirty",
          detail: "/work/affent · branch main · dirty",
          label: "affent",
          path: "/work/affent",
          branch: "main",
          dirtyState: "dirty",
        }}
        changes={{
          summary: "2 changed files",
          detail: "2 changed",
          files: [
            { path: "src/payments.ts", operation: "edit", status: "changed", turnNumber: 1, actionCount: 1 },
            { path: "src/routes.ts", operation: "edit", status: "changed", turnNumber: 1, actionCount: 1 },
          ],
        }}
        files={{
          summary: "3 file references",
          detail: "2 read · 1 changed",
          items: [
            { path: "src/payments.ts", actions: ["read", "changed"], status: "available", turnNumber: 1, actionCount: 2 },
            { path: "src/routes.ts", actions: ["read"], status: "available", turnNumber: 1, actionCount: 1 },
            { path: "README.md", actions: ["read"], status: "available", turnNumber: 1, actionCount: 1 },
          ],
        }}
        run={{
          summary: "1 failed command",
          detail: "1 failed",
          tone: "error",
          commands: [
            { command: "npm test -- checkout.spec.ts", status: "failed", turnNumber: 1, exitCode: 1 },
          ],
        }}
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
    expect(screen.getByTestId("workbench-context-runtime")).toHaveTextContent("Workspace path");
    expect(screen.getByTestId("workbench-context-runtime")).toHaveTextContent("/work/affent");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Workspace");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("affent");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Changes");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("2 changed files");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Files");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("3 file references");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Run");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("1 failed command");

    await user.click(screen.getByRole("button", { name: "Copy context" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Workbench context evidence"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Run: 1 failed command"));
    await user.click(screen.getByRole("button", { name: "Use context as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Use this current chat context in the next step:"), "evidence");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Recovery: rerun checkout spec"), "evidence");

    await user.click(screen.getByRole("button", { name: "Open Run" }));
    expect(onSelectSection).toHaveBeenCalledWith("run");
  });

  it("shows session, turn, and delegated token evidence in Workbench context", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();
    const onUseAsDraft = vi.fn();

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        onUseAsDraft={onUseAsDraft}
        overview={overview({ headline: "Inspect runtime evidence", detail: "Context has loaded." })}
        workspace={{
          hasData: true,
          summary: "affent",
          shortStatus: "affent · main",
          detail: "/home/claudeuser/work/affent · branch main",
          label: "affent",
          path: "/home/claudeuser/work/affent",
          branch: "main",
        }}
        usage={{
          items: [
            { label: "Session tokens", value: "1,540 tokens (1,200 in / 340 out)", detail: "1 turn from loaded trace" },
            { label: "Latest turn tokens", value: "1,540 tokens (1,200 in / 340 out)", detail: "t1" },
            { label: "Subagent tokens", value: "392 tokens (310 in / 82 out)", detail: "Find WebUI requirements · merged ~186 tokens" },
          ],
        }}
      />,
    );

    const runtime = screen.getByTestId("workbench-context-runtime");
    expect(runtime).toHaveTextContent("Workspace path");
    expect(runtime).toHaveTextContent("/home/claudeuser/work/affent");
    expect(runtime).toHaveTextContent("Session tokens");
    expect(runtime).toHaveTextContent("1,540 tokens (1,200 in / 340 out)");
    expect(runtime).toHaveTextContent("Latest turn tokens");
    expect(runtime).toHaveTextContent("Subagent tokens");

    await user.click(screen.getByRole("button", { name: "Open workspace" }));
    expect(onSelectSection).toHaveBeenCalledWith("workspace");
    await user.click(screen.getByRole("button", { name: "Use context as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Workspace path: /home/claudeuser/work/affent"), "evidence");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Subagent tokens: 392 tokens (310 in / 82 out)"), "evidence");
  });

  it("links automation only when the current session has automation attention", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        overview={overview({ headline: "Runtime monitor", detail: "Loop waits for calibration." })}
        automationTitle="Loop waiting · 1 timer pending"
        automationDetail="Answer setup question before LOOP.md can run."
      />,
    );

    expect(screen.getByTestId("workbench-context-automation")).toHaveTextContent("Loop waiting · 1 timer pending");
    expect(screen.getByTestId("workbench-context-automation")).toHaveTextContent("Answer setup question before LOOP.md can run.");

    await user.click(screen.getByTestId("workbench-context-automation"));
    expect(onSelectSection).toHaveBeenCalledWith("automation");
  });

  it("uses the context attention detail as the status evidence", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({
          headline: "Fix failing checkout tests",
          detail: "npm test -- checkout.spec.ts: Next: update payment route then rerun",
          tone: "error",
          metrics: [{ label: "Issue", value: "1", tone: "error" }],
        })}
        attention={{
          label: "Issue: checkout spec failed · View context",
          detail: "checkout spec failed · Next: update payment route then rerun",
          tone: "error",
          target: "context",
        }}
      />,
    );

    expect(screen.getByTestId("workbench-context-status")).toHaveTextContent("checkout spec failed");
    expect(screen.getByTestId("workbench-context-status")).not.toHaveTextContent("npm test -- checkout.spec.ts: Next");
  });

  it("keeps the completed chat state in the collapsed summary", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({
          headline: "Checkout route inspected",
          detail: "Read src/payments.ts and found the route handler.",
          stateLabel: "Result ready",
          tone: "success",
        })}
      />,
    );

    const summary = within(screen.getByTestId("workbench-context-panel")).getByText("Context").closest("summary");
    expect(summary).toHaveTextContent("Result ready");
    expect(summary).toHaveTextContent("Checkout route inspected");
    expect(summary).not.toHaveTextContent("Chat ready");
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
    expect(panel).toHaveTextContent("Start or open a chat");
    expect(panel).toHaveTextContent("Start a task or open a saved chat before inspecting session evidence.");
    expect(panel).not.toHaveTextContent("run evidence, changes, memory, and automation");
    expect(panel).not.toHaveTextContent("No chat selected");
    expect(screen.queryByRole("button", { name: "Copy context" })).toBeNull();
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
