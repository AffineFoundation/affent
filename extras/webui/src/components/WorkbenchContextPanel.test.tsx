import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { WorkbenchContextPanel } from "./WorkbenchContextPanel";
import type { SessionOverview } from "../view/sessionOverview";
import { reduceRawEvents } from "../store/reduce";

describe("WorkbenchContextPanel", () => {
  it("opens on current chat context without promoting low-signal token counts", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        overview={overview({
          headline: "Fix failing checkout tests",
          detail: "Tests failed after the payment route changed.",
          stateLabel: "Review needed",
          tone: "warning",
          metrics: [
            { label: "Next step", value: "rerun checkout spec", tone: "warning" },
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
    expect(panel).toHaveTextContent("Conversation context");
    expect(panel).not.toHaveTextContent("Review needed");
    expect(panel).not.toHaveTextContent("Fix failing checkout tests");
    expect(screen.getByTestId("workbench-context-actions-list")).toHaveTextContent("Artifact");
    expect(screen.getByTestId("workbench-context-actions-list")).toHaveTextContent("1 file");
    expect(screen.getByTestId("workbench-context-actions-list")).not.toHaveTextContent("Next step");
    expect(screen.getByTestId("workbench-context-actions-list")).not.toHaveTextContent("rerun checkout spec");
    expect(screen.getByTestId("workbench-context-actions-list")).not.toHaveTextContent("Tokens 12k");
    expect(screen.getByTestId("workbench-usage-card")).toHaveTextContent("Token usage");
    expect(screen.getByTestId("workbench-usage-card")).toHaveTextContent("Waiting for usage");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Workspace");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("affent");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("/work/affent");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Changes");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("2 changed files");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Files");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("3 file references");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("Run");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("1 failed command");

    expect(screen.queryByRole("button", { name: "Copy context" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Use context as draft" })).toBeNull();

    await user.click(screen.getByRole("button", { name: "Open Run" }));
    expect(onSelectSection).toHaveBeenCalledWith("run");
  });

  it("shows session, turn, and delegated token evidence in Workbench context", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        overview={overview({
          headline: "Inspect runtime evidence",
          detail: "Context has loaded.",
          metrics: [{ label: "Context", value: "96/120 · 80%", tone: "warning" }],
        })}
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
          totalTokens: 1540,
          trend: [
            { label: "Turn 1", value: 1540, valueLabel: "0.0015M tokens", detail: "t1" },
          ],
          items: [
            { label: "Session tokens", value: "0.0015M tokens (0.0012M in / 0.0003M out)", detail: "1 turn from loaded trace" },
            { label: "Latest turn tokens", value: "0.0015M tokens (0.0012M in / 0.0003M out)", detail: "t1" },
            { label: "Subagent tokens", value: "0.0004M tokens (0.0003M in / 0.0001M out)", detail: "Find WebUI requirements · merged ~0.0002M tokens" },
          ],
        }}
        contextSummary={{ message_count: 96, compact_trigger: 120, compact_percent: 80, messages_until_compact: 24 }}
      />,
    );

    const usageCard = screen.getByTestId("workbench-usage-card");
    const health = screen.getByTestId("workbench-context-health");
    expect(health).toHaveTextContent("Current context");
    expect(health).toHaveTextContent("Context is getting tight");
    expect(health).toHaveTextContent("80%");
    expect(health).toHaveTextContent("96 of 120 context messages are loaded.");
    expect(health).toHaveTextContent("24 messages before compaction");
    expect(usageCard).toHaveTextContent("Token usage");
    expect(screen.queryByTestId("workbench-context-budget")).toBeNull();
    expect(usageCard).toHaveTextContent("0.0015M tokens");
    expect(usageCard).toHaveTextContent("Session tokens");
    expect(usageCard).toHaveTextContent("0.0015M tokens (0.0012M in / 0.0003M out)");
    expect(usageCard).toHaveTextContent("Latest turn tokens");
    expect(usageCard).toHaveTextContent("Subagent tokens");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("/home/claudeuser/work/affent");

    await user.click(screen.getByRole("button", { name: "Open Workspace" }));
    expect(onSelectSection).toHaveBeenCalledWith("workspace");
  });

  it("links tool issue cards to concrete run evidence and suppresses generic next-step templates", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        overview={overview({
          headline: "Inspect runtime issue",
          detail: "A tool call failed.",
          metrics: [
            { label: "Tool issue", value: "1", tone: "warning" },
            { label: "Next step", value: "continue from the current plan state, execute the next concrete step, or answer the user", tone: "warning" },
          ],
        })}
        run={{
          summary: "1 failed command",
          detail: "1 failed",
          tone: "error",
          commands: [
            { command: "npm test -- checkout.spec.ts", status: "failed", turnNumber: 2, exitCode: 1, detail: "checkout assertion failed", next: "update payment route then rerun" },
          ],
        }}
      />,
    );

    const statusCards = screen.getByTestId("workbench-context-actions-list");
    expect(statusCards).toHaveTextContent("Tool issue");
    expect(statusCards).toHaveTextContent("checkout assertion failed");
    expect(statusCards).toHaveTextContent("Next: update payment route then rerun");
    expect(statusCards).not.toHaveTextContent("continue from the current plan state");

    await user.click(within(statusCards).getByRole("button", { name: /Tool issue/ }));
    expect(onSelectSection).toHaveBeenCalledWith("run");
  });

  it("keeps low-value work and tool-context metrics out of Context actions", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({
          headline: "Inspect runtime metrics",
          detail: "Trace loaded.",
          metrics: [
            { label: "Work", value: "2 actions · 2 sources" },
            { label: "Tool context", value: "8 trims · 37 KiB omitted", tone: "warning" },
            { label: "Session tokens", value: "0.87M" },
          ],
        })}
        contextSummary={{ message_count: 99, compact_trigger: 240, compact_percent: 41, messages_until_compact: 141 }}
      />,
    );

    expect(screen.queryByTestId("workbench-context-actions-list")).toBeNull();
    expect(screen.getByTestId("workbench-context-health")).toHaveTextContent("Context has room");
    expect(screen.getByTestId("workbench-context-health")).toHaveTextContent("41%");
    expect(screen.getByTestId("workbench-context-panel")).not.toHaveTextContent("8 trims");
    expect(screen.getByTestId("workbench-context-panel")).not.toHaveTextContent("2 actions");
  });

  it("shows concrete non-shell tool issues instead of a generic trace instruction", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "inspect source" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "web_fetch", args: { url: "https://example.invalid" } } },
      { id: 4, type: "tool.result", data: { turn_id: "t1", call_id: "c1", exit_code: 1, failure_kind: "network_error", result_summary: "provider returned 503\nNext: retry later\nFailure: kind=network_error" } },
    ]);

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        session={session}
        overview={overview({
          headline: "Inspect source",
          detail: "A web fetch failed.",
          metrics: [{ label: "Tool issue", value: "1", tone: "warning" }],
        })}
      />,
    );

    const statusCards = screen.getByTestId("workbench-context-actions-list");
    expect(statusCards).toHaveTextContent("web_fetch");
    expect(statusCards).toHaveTextContent("network");
    expect(statusCards).toHaveTextContent("provider returned 503");
    expect(statusCards).not.toHaveTextContent("Open trace to inspect");
  });

  it("shows the active request mode as trace-backed context", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "market monitor", display_text: "Set up loop: market monitor", mode: "loop_setup" } },
    ]);

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        session={session}
        onSelectSection={onSelectSection}
        overview={overview({ headline: "Set up market monitor", detail: "Loop setup is running." })}
      />,
    );

    const statusCards = screen.getByTestId("workbench-context-actions-list");
    expect(statusCards).toHaveTextContent("Request mode");
    expect(statusCards).toHaveTextContent("Loop setup");
    expect(statusCards).toHaveTextContent("latest request · t1");

    await user.click(within(statusCards).getByRole("button", { name: /Request mode/ }));
    expect(onSelectSection).toHaveBeenCalledWith("trace");
  });

  it("keeps automation out of the context evidence cards", () => {
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

    expect(screen.queryByTestId("workbench-context-automation")).toBeNull();
    expect(screen.queryByText("AUTOMATION")).toBeNull();
    expect(onSelectSection).not.toHaveBeenCalled();
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

  it("keeps completed chat state labels out of the collapsed summary", () => {
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
    expect(summary).toHaveTextContent("Conversation context");
    expect(summary).not.toHaveTextContent("Result ready");
    expect(summary).not.toHaveTextContent("Checkout route inspected");
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
    expect(within(panel).getByText("No chat selected")).toBeVisible();
    expect(panel).toHaveTextContent("Start a task or open a saved chat before inspecting session evidence.");
    expect(panel).not.toHaveTextContent("run evidence, changes, memory, and automation");
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
