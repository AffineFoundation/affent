import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionTrace } from "../view/sessionTrace";
import { SessionTracePanel } from "./SessionTracePanel";

describe("SessionTracePanel", () => {
  it("renders trace summary, evidence actions, and normalized event rows", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const session = reduceRawEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Inspect trace" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "shell", tool: "shell", args: { command: "npm test" } } },
      { id: 4, type: "tool.result", data: { call_id: "shell", exit_code: 1, result_summary: "failed", result: "failed" } },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen onUseAsDraft={onUseAsDraft} />);

    expect(screen.getByTestId("session-trace-panel")).toHaveTextContent("5 trace entries");
    expect(screen.getByLabelText("Search trace")).toBeInTheDocument();
    expect(screen.getByText(/Search supports plain text/)).toHaveTextContent("tool:");
    expect(screen.getByTestId("session-trace-metrics")).toHaveTextContent("Entries5");
    expect(screen.getByTestId("session-trace-metrics")).toHaveTextContent("Tool issues1");
    expect(screen.getByTestId("session-trace-latest")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");

    await user.click(screen.getByRole("button", { name: "Copy trace evidence" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Session trace evidence"));

    await user.click(screen.getByRole("button", { name: "Use trace as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Inspect this session trace"), "trace");

    await user.type(screen.getByLabelText("Search trace"), "npm test");
    expect(screen.getByTestId("session-trace-metrics")).toHaveTextContent("Matching1");
    expect(screen.queryByTestId("session-trace-latest")).toBeNull();
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Action failed");

    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-trace-latest")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");

    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));
    expect(screen.getByTestId("session-trace-metrics")).toHaveTextContent("FilterTool issues");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");
    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));

    await user.click(screen.getByRole("button", { name: "Commands 2" }));
    expect(screen.getByTestId("session-trace-metrics")).toHaveTextContent("FilterCommands");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");
    await user.click(screen.getByRole("button", { name: "Commands 2" }));

    await user.type(screen.getByLabelText("Search trace"), "tool:shell status:failed");
    expect(screen.getByTestId("session-trace-metrics")).toHaveTextContent("Matching1");
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
