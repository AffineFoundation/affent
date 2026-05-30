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
          verification: "bound",
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
    expect(panel).toHaveTextContent("Task");
    expect(panel).toHaveTextContent("Current task");
    expect(panel).not.toHaveTextContent("Review needed");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Task");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Fix failing checkout tests");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Verification");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Unresolved failure");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Open first");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Run");
    expect(screen.queryByTestId("workbench-context-snapshot")).toBeNull();
    expect(screen.queryByTestId("workbench-usage-card")).toBeNull();
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

    await user.click(screen.getByRole("button", { name: "Open Verification" }));
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
          verification: "bound",
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
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("Context");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("80% used");
    expect(screen.getByTestId("workbench-context-brief")).toHaveTextContent("0.0015M tokens");
    expect(usageCard).toHaveTextContent("Token usage");
    expect(screen.queryByTestId("workbench-context-budget")).toBeNull();
    expect(usageCard).toHaveTextContent("0.0015M tokens");
    expect(usageCard).toHaveTextContent("Session tokens");
    expect(usageCard).toHaveTextContent("0.0015M tokens (0.0012M in / 0.0003M out)");
    expect(usageCard).toHaveTextContent("Latest turn tokens");
    expect(usageCard).toHaveTextContent("Subagent tokens");
    expect(screen.getByTestId("workbench-context-evidence")).toHaveTextContent("/home/claudeuser/work/affent");

    await user.click(within(screen.getByTestId("workbench-context-brief")).getByRole("button", { name: "Open Workspace" }));
    expect(onSelectSection).toHaveBeenCalledWith("files");
  });

  it("shows derived task state without treating recovered failures as current errors", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        overview={overview({ headline: "Fix clamp behavior", detail: "The task finished after rerunning tests." })}
        taskState={{
          objective: "Fix clamp behavior, verify it, and push the code",
          status: "completed",
          verification_state: "last_shell_passed",
          changed_files: [{ path: "app/mathutil/clamp.go", action: "edit" }],
          attempted_actions: [
            { tool: "shell", summary: "git push origin main" },
            { tool: "shell", summary: "go test ./..." },
          ],
          failed_actions: [{ tool: "shell", summary: "FAIL ./...", kinds: ["test_failed"], next: "Inspect clamp bounds then rerun go test" }],
          evidence: [
            { source: "runtime_workspace", summary: "Workspace tools resolve relative paths from the session workspace root." },
            { source: "shell", summary: "go test ./..." },
            { source: "git_push", summary: "git push origin main" },
          ],
        }}
      />,
    );

    const brief = screen.getByTestId("workbench-context-brief");
    expect(brief).toHaveTextContent("Task state");
    expect(brief).toHaveTextContent("Completed");
    expect(brief).not.toHaveTextContent("Failed");

    const taskState = screen.getByTestId("workbench-task-state");
    expect(taskState).toHaveAttribute("data-tone", "ready");
    expect(taskState).toHaveTextContent("Fix clamp behavior, verify it, and push the code");
    expect(taskState).toHaveTextContent("Last shell check passed");
    expect(taskState).toHaveTextContent("Recent failed actions");
    expect(taskState).toHaveTextContent("shell");
    expect(taskState).toHaveTextContent("test failed");
    expect(taskState).toHaveTextContent("Next: Inspect clamp bounds then rerun go test");
    expect(taskState).toHaveTextContent("Recent actions");
    expect(taskState).toHaveTextContent("git push origin main");
    expect(taskState).toHaveTextContent("Evidence");
    expect(taskState).toHaveTextContent("runtime workspace");
    expect(taskState).toHaveTextContent("git push");
    expect(taskState).toHaveTextContent("go test ./...");

    const sourceLinks = screen.getByTestId("workbench-context-evidence");
    expect(sourceLinks).toHaveTextContent("Changed files");
    expect(sourceLinks).toHaveTextContent("app/mathutil/clamp.go");
    expect(sourceLinks).toHaveTextContent("Execution record");
    expect(sourceLinks).toHaveTextContent("2 actions");
    expect(sourceLinks).toHaveTextContent("1 failure");
    expect(sourceLinks).toHaveTextContent("test failed");

    await user.click(within(taskState).getByRole("button", { name: "Open trace" }));
    expect(onSelectSection).toHaveBeenCalledWith("trace");
    await user.click(within(taskState).getByRole("button", { name: "Open changes" }));
    expect(onSelectSection).toHaveBeenCalledWith("changes");
    await user.click(within(sourceLinks).getByRole("button", { name: "Open Changed files" }));
    expect(onSelectSection).toHaveBeenCalledWith("changes");
    await user.click(within(sourceLinks).getByRole("button", { name: "Open Execution record" }));
    expect(onSelectSection).toHaveBeenCalledWith("trace");
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

    const brief = screen.getByTestId("workbench-context-brief");
    expect(brief).toHaveTextContent("Verification");
    expect(brief).toHaveTextContent("update payment route then rerun");
    expect(brief).not.toHaveTextContent("continue from the current plan state");
    expect(screen.queryByTestId("workbench-context-snapshot")).toBeNull();

    await user.click(within(brief).getByRole("button", { name: "Open Run" }));
    expect(onSelectSection).toHaveBeenCalledWith("run");
  });

  it("uses task state as concrete Context evidence instead of generic next-step text", async () => {
    const user = userEvent.setup();
    const onSelectSection = vi.fn();

    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        onSelectSection={onSelectSection}
        overview={overview({
          headline: "Harden inline message editing",
          detail: "Workbench should expose the real task state.",
        })}
        taskState={{
          objective: "Harden inline message editing",
          status: "running",
          request_mode: "execute_plan",
          request_source: "schedule",
          schedule_id: "sched_context",
          schedule_kind: "checkin",
          current_step: "Wire task_state into Workbench Context",
          next_step: "Run WorkbenchContextPanel tests and screenshot the Context tab",
          changed_files: [{ path: "extras/webui/src/components/WorkbenchContextPanel.tsx", action: "edit" }],
          attempted_actions: [{ tool: "shell", summary: "npm --prefix extras/webui test -- WorkbenchContextPanel.test.tsx" }],
          failed_actions: [{
            tool: "shell",
            summary: "npm test -- WorkbenchContextPanel.test.tsx failed",
            kinds: ["command_failed"],
            next: "Run the focused test after checking the Context panel state",
          }],
          evidence: [
            { source: "runtime_workspace", summary: "Workspace tools resolve relative paths from the session workspace root." },
            { source: "shell", summary: "npm --prefix extras/webui test -- WorkbenchContextPanel.test.tsx" },
          ],
          verification_state: "failed",
        }}
      />,
    );

    const brief = screen.getByTestId("workbench-context-brief");
    expect(brief).toHaveTextContent("Task state");
    expect(brief).toHaveTextContent("Running");
    expect(brief).toHaveTextContent("Wire task_state into Workbench Context");

    const taskState = screen.getByTestId("workbench-task-state");
    expect(taskState).toHaveTextContent("Harden inline message editing");
    expect(taskState).toHaveTextContent("Request mode");
    expect(taskState).toHaveTextContent("Execute plan");
    expect(taskState).toHaveTextContent("Request source");
    expect(taskState).toHaveTextContent("Scheduled check-in");
    expect(taskState).not.toHaveTextContent("sched_context");
    expect(taskState).toHaveTextContent("Current step");
    expect(taskState).toHaveTextContent("Wire task_state into Workbench Context");
    expect(taskState).toHaveTextContent("Next step");
    expect(taskState).toHaveTextContent("Run WorkbenchContextPanel tests and screenshot the Context tab");
    expect(taskState).toHaveTextContent("Recent failed actions");
    expect(taskState).toHaveTextContent("shell · command failed");
    expect(taskState).toHaveTextContent("Recent actions");
    expect(taskState).toHaveTextContent("npm --prefix extras/webui test -- WorkbenchContextPanel.test.tsx");
    expect(taskState).toHaveTextContent("Evidence");
    expect(taskState).toHaveTextContent("runtime workspace");
    expect(taskState).toHaveTextContent("Changed files");
    expect(taskState).not.toHaveTextContent("continue from the current plan state");

    await user.click(within(taskState).getByRole("button", { name: "Open trace" }));
    expect(onSelectSection).toHaveBeenCalledWith("trace");
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

    expect(screen.queryByTestId("workbench-context-snapshot")).toBeNull();
    expect(screen.queryByTestId("workbench-context-health")).toBeNull();
    expect(screen.getByTestId("workbench-context-panel")).not.toHaveTextContent("41% used");
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

    const statusCards = screen.getByTestId("workbench-context-snapshot");
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

    const brief = screen.getByTestId("workbench-context-brief");
    expect(brief).toHaveTextContent("Request");
    expect(brief).toHaveTextContent("Loop setup");
    expect(brief).toHaveTextContent("latest request · t1");
    expect(screen.queryByTestId("workbench-context-snapshot")).toBeNull();

    await user.click(within(brief).getByRole("button", { name: "Open Request" }));
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

    const summary = within(screen.getByTestId("workbench-context-panel")).getByText("Current task").closest("summary");
    expect(summary).toHaveTextContent("Current task");
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
    expect(within(panel).getByText("No task selected")).toBeVisible();
    expect(panel).toHaveTextContent("Start or open a chat to see the objective, next step, and source tabs.");
    expect(panel).not.toHaveTextContent("run evidence, changes, memory, and automation");
    expect(screen.queryByRole("button", { name: "Copy context" })).toBeNull();
  });

  it("shows byte pressure when large tool arguments dominate context", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({ headline: "Long run", detail: "Generating a project." })}
        contextSummary={{
          message_count: 130,
          compact_trigger: 240,
          compact_percent: 125,
          messages_until_compact: 110,
          context_bytes: 655360,
          compact_trigger_bytes: 524288,
          byte_compact_percent: 125,
          bytes_until_compact: 0,
          message_compact_percent: 54,
        }}
      />,
    );

    const health = screen.getByTestId("workbench-context-health");
    expect(health).toHaveTextContent("Compaction likely");
    expect(health).toHaveTextContent("100%");
    expect(health).toHaveTextContent("640 KiB of 512 KiB context bytes are loaded.");
    expect(health).toHaveTextContent("Compaction byte threshold reached");
  });

  it("shows request input pressure when tool schemas dominate the next call", () => {
    render(
      <WorkbenchContextPanel
        defaultOpen
        hasSelectedSession
        overview={overview({ headline: "Long run", detail: "Tool surface is large." })}
        contextSummary={{
          message_count: 32,
          compact_trigger: 240,
          compact_percent: 92,
          messages_until_compact: 208,
          context_bytes: 32768,
          conversation_bytes: 32768,
          tool_schema_bytes: 143232,
          compact_trigger_bytes: 196608,
          byte_compact_percent: 17,
          bytes_until_compact: 163840,
          message_compact_percent: 13,
          estimated_request_input_tokens: 44000,
          estimated_conversation_tokens: 8192,
          estimated_tool_schema_tokens: 35808,
          compact_trigger_input_tokens: 48000,
          model_context_window_source: "registry",
          request_input_compact_percent: 92,
          request_input_tokens_until_compact: 4000,
        }}
      />,
    );

    const health = screen.getByTestId("workbench-context-health");
    expect(health).toHaveTextContent("Context is getting tight");
    expect(health).toHaveTextContent("44,000 estimated input tokens of 48,000 before the next request.");
    expect(health).toHaveTextContent("4,000 estimated input tokens before compaction");
    expect(health).toHaveTextContent("window source: registry");
    const usageCard = screen.getByTestId("workbench-usage-card");
    expect(usageCard).toHaveTextContent("Conversation");
    expect(usageCard).toHaveTextContent("8,192 estimated tokens");
    expect(usageCard).toHaveTextContent("32 KiB");
    expect(usageCard).toHaveTextContent("Tool schema");
    expect(usageCard).toHaveTextContent("35,808 estimated tokens");
    expect(usageCard).toHaveTextContent("140 KiB");
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
