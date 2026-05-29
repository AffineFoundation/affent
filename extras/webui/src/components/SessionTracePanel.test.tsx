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
    expect(screen.getByLabelText("Trace search shortcuts")).toHaveTextContent("exit:1");
    expect(screen.queryByTestId("session-trace-focus")).toBeNull();
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("2");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Tool issues");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Span");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("#5-#6");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Request 1");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Failures");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Tools");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("Request 1 · shell");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("1 issue across 1 tool");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("invalid_args");
    expect(screen.getByTestId("session-trace-issues")).not.toHaveTextContent("Next: rerun npm test after fixing checkout");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Request 1 · shell");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("rerun npm test after fixing checkout");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("7");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("trace entries loaded");
    expect(screen.getByTestId("session-trace-latest")).toHaveTextContent("Action failed");

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
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Tool issues");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");
    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));

    await user.click(within(screen.getByTestId("session-trace-issues")).getByRole("button", { name: /Request 1/ }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Tool issues");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("2");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Issue detail");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Tool");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("shell");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("Exit");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("1");
    expect(screen.getByTestId("session-trace-issue-focus")).toHaveTextContent("rerun npm test after fixing checkout");
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Show request" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: request:1");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Inspect trace");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Request 1");
    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(screen.getByTestId("session-trace-issues")).getByRole("button", { name: /Request 1/ }));
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Open artifact" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-shell.txt");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("SourceAccess");
    await user.click(within(screen.getByTestId("session-trace-issue-focus")).getByRole("button", { name: "Show request" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: request:1");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Inspect trace");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("https://example.test/source");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("npm test");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    await user.click(screen.getByRole("button", { name: "Tool issues 1" }));

    await user.click(within(screen.getByTestId("session-trace-issues")).getByRole("button", { name: "shell 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Tool issues");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("2");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.click(screen.getByRole("button", { name: "Commands 2" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Commands");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Started action");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Inspect trace");
    await user.click(screen.getByRole("button", { name: "Commands 2" }));

    await user.click(screen.getByRole("button", { name: "Sources 2" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Sources");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("verified source");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("https://example.test/source");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("npm test");
    await user.click(screen.getByRole("button", { name: "Sources 2" }));

    await user.click(screen.getByRole("button", { name: "Artifacts 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Artifacts");
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

    await user.click(within(screen.getByLabelText("Trace search shortcuts")).getByRole("button", { name: "exit:1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Tool issues");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("1");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Action failed");
    await user.click(screen.getByRole("button", { name: "Reset" }));

    await user.type(screen.getByLabelText("Search trace"), "missing event");
    expect(screen.queryByTestId("event-trace")).toBeNull();
    expect(screen.getByTestId("session-trace-panel")).toHaveTextContent('No trace entries matching "missing event".');
  });

  it("keeps empty trace states explicit", () => {
    const session = reduceRawEvents([]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(within(screen.getByTestId("session-trace-panel")).getByTestId("session-trace-empty")).toHaveTextContent("No persisted trace");
  });

  it("filters repair and truncation diagnostics as first-class trace evidence", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "read",
          tool: "read_file",
          args: { path: "README.md" },
          args_repaired: true,
          repair_notes: ["renamed readFile -> read_file"],
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "read",
          exit_code: 0,
          result_summary: "line 1\nline 2",
          result: "line 1\nline 2",
          result_truncated: true,
          result_omitted_bytes: 2048,
        },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByRole("button", { name: "Repairs 1" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Truncated 1" })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Repairs 1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Repairs");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Repairs");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("1");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("repaired");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("line 1");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(screen.getByLabelText("Trace search shortcuts")).getByRole("button", { name: "truncated" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Truncated");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Truncated");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("truncated");
    expect(screen.getByTestId("event-trace")).not.toHaveTextContent("Started request");
  });

  it("shows admitted and skipped tool request totals from trace stats", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "skipped-plan",
          tool: "plan",
          args: { action: "update" },
          skipped: true,
          skip_failure_kind: "loop_guard_no_budget",
        },
      },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "max_turns",
          tool_stats: {
            tool_requests: 4,
            tool_requests_admitted: 3,
            tool_requests_skipped: 1,
            tool_errors: 1,
          },
        },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("Actions");
    expect(screen.getByTestId("session-trace-selection")).toHaveTextContent("4 · 3 admitted · 1 skipped");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("not dispatched");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("loop_guard_no_budget");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("3 admitted / 1 skipped");
  });

  it("keeps the issue navigator aligned with structured trace filters and query aliases", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "git",
          tool: "shell",
          args: { command: "git ls-remote git@github.com:team/private.git HEAD" },
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "git",
          exit_code: 128,
          failure_kind: "command_failed",
          result_summary: "git command failed\nFailure: kind=command_failed\nNext: inspect configured repository credentials before retrying",
          result: "git command failed\nFailure: kind=command_failed\nNext: inspect configured repository credentials before retrying",
        },
      },
      {
        id: 4,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "read",
          tool: "read_file",
          args: { path: "missing.md" },
        },
      },
      {
        id: 5,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "read",
          exit_code: 0,
          failure_kind: "not_found",
          result_summary: "missing.md not found\nFailure: kind=not_found\nNext: list files before retrying",
          result: "missing.md not found\nFailure: kind=not_found\nNext: list files before retrying",
        },
      },
    ]);

    render(<SessionTracePanel trace={buildSessionTrace(session)} events={session.events} defaultOpen />);

    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("2 issues across 2 tools");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("Request 1 · shell");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("Request 1 · read_file");

    await user.click(within(screen.getByLabelText("Trace search shortcuts")).getByRole("button", { name: "exit:1" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: exit:1");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("1 issue across 1 tool");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("Request 1 · shell");
    expect(screen.getByTestId("session-trace-issues")).not.toHaveTextContent("read_file");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.type(screen.getByLabelText("Search trace"), "tool:read_file not_found");
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: tool:read_file not_found");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("1 issue across 1 tool");
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("Request 1 · read_file");
    expect(screen.getByTestId("session-trace-issues")).not.toHaveTextContent("shell");
  });

  it("adds issue-derived shortcuts for Git and SSH failures", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "git",
          tool: "shell",
          args: { command: "git ls-remote git@github.com:team/private.git HEAD" },
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "git",
          exit_code: 128,
          failure_kind: "command_failed",
          result_summary: "STDERR: Load key \"[REDACTED:account-secret]\": invalid format\ngit@github.com: Permission denied (publickey).\n[exit 128]",
          result: "STDERR: Load key \"[REDACTED:account-secret]\": invalid format\ngit@github.com: Permission denied (publickey).\n[exit 128]",
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
    expect(screen.getByTestId("session-trace-issues")).toHaveTextContent("1 issue across 1 tool");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("Permission denied");

    await user.click(screen.getByRole("button", { name: "Reset" }));
    await user.click(within(shortcuts).getByRole("button", { name: "invalid key" }));
    expect(screen.getByTestId("session-trace-resultbar")).toHaveTextContent("Search: invalid format");
    expect(screen.getByTestId("event-trace")).toHaveTextContent("invalid format");
  });
});
