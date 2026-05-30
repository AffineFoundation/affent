import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionTrace } from "../view/sessionTrace";
import { SessionTracePanel } from "./SessionTracePanel";

describe("SessionTracePanel", () => {
  it("renders a filterable event list and useful selected failure detail", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const session = reduceRawEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Inspect trace" } },
      {
        id: 3,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "web", tool: "web_fetch", args: { url: "https://example.test/source" } },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "web",
          exit_code: 0,
          result_summary: "SourceAccess: fetched_url=https://example.test/source",
          result: "SourceAccess: fetched_url=https://example.test/source",
        },
      },
      {
        id: 5,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "shell", tool: "shell", args: { command: "npm test" } },
      },
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

    render(
      <SessionTracePanel
        trace={buildSessionTrace(session)}
        events={session.events}
        defaultOpen
        onOpenArtifact={onOpenArtifact}
      />,
    );

    expect(screen.getByTestId("session-trace-panel")).toHaveTextContent("1 failed tool call");
    expect(screen.getByLabelText("Trace status")).toHaveTextContent("Failures1");
    expect(screen.getByLabelText("Search events")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Failures 1" })).toBeInTheDocument();

    const list = screen.getByTestId("session-trace-event-list");
    expect(list).toHaveTextContent("User message");
    expect(list).toHaveTextContent("Source action");
    expect(list).toHaveTextContent("Action failed");
    expect(list).toHaveTextContent("npm test");
    expect(within(list).getAllByRole("option")).toHaveLength(5);
    expect(list).toHaveTextContent("#3-#4");
    expect(list).toHaveTextContent("#5-#6");

    const detail = screen.getByTestId("session-trace-event-detail");
    expect(detail).toHaveTextContent("Action failed");
    expect(detail).toHaveTextContent("Failure cause");
    expect(detail).toHaveTextContent("Invalid request");
    expect(detail).toHaveTextContent("rerun npm test after fixing checkout");
    expect(detail).toHaveTextContent("Command");
    expect(detail).toHaveTextContent("npm test");
    expect(detail).toHaveTextContent("000001-shell.txt");
    expect(detail).toHaveTextContent("Raw event JSON");

    await user.type(screen.getByLabelText("Search events"), "tool:shell status:failed");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("1");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: tool:shell status:failed");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("Action started");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(screen.getByRole("button", { name: "Sources 2" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Sources");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("Source action");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("npm test");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(screen.getByTestId("session-trace-event-list")).getByRole("option", { name: /Action failed/ }));
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Whole request" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: request:1");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("Inspect trace");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("https://example.test/source");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(screen.getByTestId("session-trace-event-list")).getByRole("option", { name: /Action failed/ }));
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Only this call" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: call:shell");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("Source evidence");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(screen.getByTestId("session-trace-event-list")).getByRole("option", { name: /Action failed/ }));
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Open artifact" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-shell.txt");
  });

  it("keeps empty trace states explicit", () => {
    const session = reduceRawEvents([]);

    render(
      <SessionTracePanel
        trace={buildSessionTrace(session)}
        events={session.events}
        defaultOpen
      />,
    );

    expect(within(screen.getByTestId("session-trace-panel")).getByTestId("session-trace-empty")).toHaveTextContent("No persisted trace");
  });

  it("shows source evidence in the event list when there are no failures", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Verify current market data" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "network", tool: "browser_network_read", args: { ref: "n1" } } },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "network",
          exit_code: 0,
          result_summary:
            "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch",
          result:
            'SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; requested_url=https://taostats.io/subnets/120; ref=n1; status=200; content_type=application/json; source_method=network_xhr_fetch\n{"price":"0.06342 T"}',
        },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByTestId("session-trace-panel")).toHaveTextContent("No failed tool calls");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("Source action");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("taostats.io/api/subnets/120");
    expect(within(screen.getByTestId("session-trace-event-list")).getAllByRole("option")).toHaveLength(3);
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("#3-#4");

    await user.click(screen.getByRole("button", { name: "Sources 2" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Sources");
    expect(screen.getByTestId("session-trace-event-detail")).toHaveTextContent("Source");
    expect(screen.getByTestId("session-trace-event-detail")).toHaveTextContent("http 200");
  });

  it("keeps the full event list while selecting different failed tool results", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "shell", tool: "shell", args: { command: "npm test" } } },
      { id: 3, type: "tool.result", data: { turn_id: "t1", call_id: "shell", exit_code: 1, result_summary: "npm test failed", result: "npm test failed" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "browser", tool: "browser_click", args: { ref: "123" } } },
      { id: 5, type: "tool.result", data: { turn_id: "t1", call_id: "browser", exit_code: 1, failure_kind: "timeout", result_summary: "browser click timed out", result: "browser click timed out" } },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("npm test");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("browser click timed out");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("npm test failed");

    await user.click(within(screen.getByTestId("session-trace-event-list")).getByRole("option", { name: /npm test failed/ }));
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("shell");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("browser click timed out");
    await user.click(within(screen.getByTestId("session-trace-event-list")).getByRole("option", { name: /browser click timed out/ }));
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Timeout");
  });

  it("filters repair and truncation diagnostics as first-class trace evidence", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "README.md" }, args_repaired: true, repair_notes: ["renamed readFile -> read_file"] },
      },
      {
        id: 3,
        type: "tool.result",
        data: { turn_id: "t1", call_id: "read", exit_code: 0, result_summary: "line 1\nline 2", result: "line 1\nline 2", result_truncated: true, result_omitted_bytes: 2048 },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    await user.click(screen.getByRole("button", { name: "Repairs 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Repairs");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("repaired");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("line 1");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(screen.getByLabelText("Trace search shortcuts")).getByRole("button", { name: "truncated output" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Truncated");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("truncated");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("Request started");
  });

  it("shows admitted and skipped tool request totals from trace stats", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: { turn_id: "t1", call_id: "skipped-plan", tool: "plan", args: { action: "update" }, skipped: true, skip_failure_kind: "loop_guard_no_budget" },
      },
      {
        id: 3,
        type: "turn.end",
        data: { turn_id: "t1", reason: "max_turns", tool_stats: { tool_requests: 4, tool_requests_admitted: 3, tool_requests_skipped: 1, tool_errors: 1 } },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Actions");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("4 · 3 admitted · 1 skipped");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("not dispatched");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("Context budget");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("loop_guard_no_budget");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("3 admitted / 1 skipped");
  });

  it("keeps the issue detail aligned with structured filters and query aliases", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "git", tool: "shell", args: { command: "git ls-remote git@github.com:team/private.git HEAD" } } },
      { id: 3, type: "tool.result", data: { turn_id: "t1", call_id: "git", exit_code: 128, failure_kind: "command_failed", result_summary: "git command failed\nFailure: kind=command_failed\nNext: inspect configured repository credentials before retrying", result: "git command failed\nFailure: kind=command_failed\nNext: inspect configured repository credentials before retrying" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "missing.md" } } },
      { id: 5, type: "tool.result", data: { turn_id: "t1", call_id: "read", exit_code: 0, failure_kind: "not_found", result_summary: "missing.md not found\nFailure: kind=not_found\nNext: list files before retrying", result: "missing.md not found\nFailure: kind=not_found\nNext: list files before retrying" } },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByLabelText("Trace status")).toHaveTextContent("Failures2");
    await user.click(within(screen.getByLabelText("Trace search shortcuts")).getByRole("button", { name: "exit:128" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: exit:128");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("shell");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("read_file");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.type(screen.getByLabelText("Search events"), "tool:read_file not_found");
    expect(screen.getByTestId("session-trace-event-list")).toHaveTextContent("read_file");
    expect(screen.getByTestId("session-trace-event-list")).not.toHaveTextContent("shell");
    expect(screen.getByTestId("session-trace-event-detail")).toHaveTextContent("list files before retrying");
  });

  it("adds issue-derived shortcuts for Git and SSH failures", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "git", tool: "shell", args: { command: "git ls-remote git@github.com:team/private.git HEAD" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "git",
          exit_code: 128,
          failure_kind: "command_failed",
          result_summary: 'STDERR: Load key "[REDACTED:account-secret]": invalid format\ngit@github.com: Permission denied (publickey).\n[exit 128]',
          result: 'STDERR: Load key "[REDACTED:account-secret]": invalid format\ngit@github.com: Permission denied (publickey).\n[exit 128]',
        },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    const shortcuts = screen.getByLabelText("Trace search shortcuts");
    expect(shortcuts).toHaveTextContent("permission denied");
    expect(shortcuts).toHaveTextContent("invalid key");
    expect(shortcuts).toHaveTextContent("github");

    await user.click(within(shortcuts).getByRole("button", { name: "permission denied" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Commands");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: permission denied");
    expect(screen.getByTestId("session-trace-event-detail")).toHaveTextContent("Permission denied");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(shortcuts).getByRole("button", { name: "invalid key" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: invalid format");
    expect(screen.getByTestId("session-trace-event-detail")).toHaveTextContent("invalid format");
  });
});
