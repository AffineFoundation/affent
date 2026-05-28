import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionTrace } from "../view/sessionTrace";
import { SessionTracePanel } from "./SessionTracePanel";

describe("SessionTracePanel", () => {
  it("renders trace summary, filters, and normalized event rows", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const session = reduceRawEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Inspect trace" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "web", tool: "web_fetch", args: { url: "https://example.test/source" } } },
      { id: 4, type: "tool.result", data: { turn_id: "t1", call_id: "web", exit_code: 0, result_summary: "SourceAccess: fetched_url=https://example.test/source", result: "SourceAccess: fetched_url=https://example.test/source" } },
      { id: 5, type: "tool.request", data: { turn_id: "t1", call_id: "shell", tool: "shell", args: { command: "npm test" } } },
      {
        id: 6,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "shell",
          exit_code: 1,
          failure_kind: "invalid_args",
          result_summary: "failed\nNext: rerun npm test after fixing checkout\nFailure: kind=invalid_args",
          result: "failed\nNext: rerun npm test after fixing checkout\nFailure: kind=invalid_args",
          result_artifact_path: ".affent/artifacts/tool-results/000001-shell.txt",
        },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen onOpenArtifact={onOpenArtifact} />);

    expect(screen.getByTestId("session-trace-panel")).toHaveTextContent("7 trace entries");
    expect(screen.getByLabelText("Search trace")).toBeInTheDocument();
    expect(screen.getByLabelText("Trace search shortcuts")).toHaveTextContent("status:failed");
    expect(screen.queryByTestId("session-trace-focus")).toBeNull();
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("7");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("trace entries loaded");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("Request 1 · shell");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("1 issue across 1 tool");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("invalid_args");
    expect(screen.getByTestId("session-trace-issues")).not.toHaveTextContent("Next: rerun npm test after fixing checkout");
    expect(screen.getByTestId("session-trace-latest")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");

    expect(screen.queryByRole("button", { name: "Use trace as draft" })).toBeNull();

    await user.type(screen.getByLabelText("Search trace"), "npm test");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("2");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: npm test");
    expect(screen.queryByTestId("session-trace-latest")).toBeNull();
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");

    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-trace-latest")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");

    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Filter: Tool issues");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");
    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));

    await user.click(within(screen.getByTestId("session-trace-issues")).getByRole("button", { name: /Request 1/ }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Filter: Tool issues");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("2");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Selected issue");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Tool");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("shell");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Exit");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("1");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("rerun npm test after fixing checkout");
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Open artifact" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-shell.txt");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("SourceAccess");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));

    await user.click(within(screen.getByTestId("session-trace-issues")).getByRole("button", { name: "shell 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Filter: Tool issues");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("2");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.click(screen.getByRole("button", { name: "Commands 2" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Filter: Commands");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");
    await user.click(screen.getByRole("button", { name: "Commands 2" }));

    await user.click(screen.getByRole("button", { name: "Sources 2" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Filter: Sources");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("verified source");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("https://example.test/source");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("npm test");
    await user.click(screen.getByRole("button", { name: "Sources 2" }));

    await user.click(screen.getByRole("button", { name: "Artifacts 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Filter: Artifacts");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("artifact 000001-shell.txt");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("SourceAccess");
    await user.click(screen.getByRole("button", { name: "Artifacts 1" }));

    await user.type(screen.getByLabelText("Search trace"), "tool:shell status:failed");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("1");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Started action");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.click(within(screen.getByLabelText("Trace search shortcuts")).getByRole("button", { name: "status:failed" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("1");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Started action");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.type(screen.getByLabelText("Search trace"), "missing event");
    expect(screen.queryByTestId("event-trace")).toBeNull();
    expect(screen.getByTestId("session-trace-panel")).toHaveTextContent('No trace entries matching "missing event".');
  });

  it("keeps empty trace states explicit", () => {
    const session = reduceRawEvents([]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(within(screen.getByTestId("session-trace-panel")).getByTestId("session-trace-empty")).toHaveTextContent("No persisted trace");
  });
});
