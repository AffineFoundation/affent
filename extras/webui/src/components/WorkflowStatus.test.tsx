import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { deriveWorkflowStatus } from "../store/workflowStatus";
import { buildSessionOverview } from "../view/sessionOverview";
import { WorkflowStatus } from "./WorkflowStatus";

describe("WorkflowStatus", () => {
  it("summarizes the latest completed chat with visible run metrics", async () => {
    const session = reduceRawEvents(completedTurn);
    const user = userEvent.setup();
    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    expect(screen.getByTestId("workflow-status")).toHaveTextContent("list the files");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("Result ready");
    const details = screen.getByTestId("workflow-details");
    expect(screen.getByTestId("workflow-status")).not.toHaveAttribute("open");
    expect(details).not.toBeVisible();
    const summary = screen.getByTestId("workflow-status").querySelector("summary");
    expect(summary).toBeTruthy();
    await user.click(summary!);
    expect(screen.getByTestId("workflow-status")).toHaveAttribute("open");
    expect(details).toBeVisible();
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("README.md main.go");
    expect(metric(details, "Work 1 action")).toBeVisible();
    expect(within(details).getByText("+1 more")).toBeInTheDocument();
    expect(within(details).getByLabelText("Work metrics: 1 more metric")).toBeInTheDocument();
    expect(within(details).getByText((_, element) => element?.textContent?.includes("Work 1 action") ?? false, { selector: ".run-detail-line" })).toBeInTheDocument();
    await user.click(within(details).getByLabelText("Work metrics: 1 more metric"));
    expect(metric(details, "Tokens 138")).toBeVisible();
    expect(screen.queryByText("Metrics")).toBeNull();
    expect(screen.queryByText("Run details")).toBeNull();
  });

  it("summarizes active work without exposing implementation labels", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "inspect" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "shell",
          args: { command: "ls" },
          args_truncated: false,
          args_bytes: 16,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
    ]);
    const workflow = deriveWorkflowStatus(session);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow,
      hasSelectedSession: true,
    })} />);

    expect(screen.getByTestId("workflow-status")).toHaveTextContent("Working");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("List current directory");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("inspect");
    expect(screen.getByTestId("workflow-status")).not.toHaveTextContent("Using shell");
    expect(screen.getByTestId("workflow-status")).not.toHaveTextContent("shell");
  });

  it("marks a completed chat with tool failures as a warning", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 1,
          duration_ms: 42,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "message.done", data: { turn_id: "t1", text: "I still found enough to answer." } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    expect(screen.getByTestId("workflow-status")).toHaveAttribute("data-tone", "warning");
    expect(screen.getByTestId("workflow-status").querySelector(".pulse-dot")).toHaveAttribute("data-status", "warning");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("Result ready");
    expect(within(screen.getByTestId("workflow-details")).getByText((_, element) => element?.textContent?.includes("Work 1 action") ?? false, { selector: ".run-detail-line" }))
      .toHaveAttribute("data-tone", "warning");
  });

  it("shows artifact output in the workflow summary when the latest chat has files", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize the repo" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 42,
          result_summary: "saved output",
          result: "saved output",
          result_truncated: true,
          result_bytes: 8192,
          result_omitted_bytes: 1048576,
          result_cap_bytes: 262144,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    expect(screen.getByTestId("workflow-status")).toHaveTextContent("1 file (8 KiB, 1 MiB omitted)");
  });

  it("pins context pressure and compaction health in the collapsed workflow summary", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "continue long run" } },
      {
        id: 3,
        type: "context.compacted",
        data: {
          turn_id: "t1",
          before_messages: 90,
          after_messages: 18,
          removed_messages: 72,
          reactive: true,
          reason: "context_overflow",
          summary_present: true,
          summary_bytes: 4096,
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      contextSummary: {
        message_count: 230,
        compact_trigger: 240,
        compact_percent: 96,
        messages_until_compact: 10,
      },
    })} />);

    const summary = screen.getByTestId("workflow-status").querySelector("summary") as HTMLElement;
    expect(summary).toHaveTextContent("230/240 · 96%");
    expect(summary).toHaveTextContent("1 · reactive · -72 msgs · 4 KiB summary");
    expect(within(summary).getByText("230/240 · 96%")).toHaveAttribute("data-tone", "error");
    expect(within(summary).getByText("1 · reactive · -72 msgs · 4 KiB summary")).toHaveAttribute("data-tone", "warning");
  });

  it("pins memory update summaries in the collapsed workflow summary", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "remember market policy" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "memory",
          args: {
            action: "add",
            target: "memory",
            topic: "markets",
            content: "Alpha Coast market reports use marker MEM-STOCK-73 and source-led confidence.",
          },
          args_truncated: false,
          args_bytes: 64,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          result_summary: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\"}",
          result: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\"}",
          result_truncated: false,
          result_bytes: 48,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    const summary = screen.getByTestId("workflow-status").querySelector("summary") as HTMLElement;
    expect(summary).toHaveTextContent("1 update · memory:markets: Alpha Coast market reports use marker MEM-STOCK...");
    expect(within(summary).getByText("1 update · memory:markets: Alpha Coast market reports use marker MEM-STOCK...")).toHaveAttribute("data-tone", "success");
  });

  it("pins session recall summaries in the collapsed workflow summary", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "resume alpha coast analysis" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "completed",
          tool_stats: {
            session_search_calls: 1,
            session_search_results: 2,
            session_search_context_hits: 1,
            session_search_matched_terms: 3,
          },
        },
      },
    ]);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    const summary = screen.getByTestId("workflow-status").querySelector("summary") as HTMLElement;
    expect(summary).toHaveTextContent("2 hits · 1 context · 3 terms");
    expect(within(summary).getByText("2 hits · 1 context · 3 terms")).toHaveAttribute("data-tone", "success");
    expect(screen.getByTestId("workflow-details")).toHaveTextContent("Recall 2 hits · 1 context · 3 terms");
  });

  it("pins loop pressure in the collapsed workflow summary", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "keep investigating" } },
      {
        id: 3,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "max_turns",
          tool_stats: {
            loop_guard_interventions: 2,
            forced_no_tools: 1,
          },
        },
      },
    ]);

    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    const summary = screen.getByTestId("workflow-status").querySelector("summary") as HTMLElement;
    expect(summary).toHaveTextContent("1 max-turn · 2 guards · 1 no-tools");
    expect(within(summary).getByText("1 max-turn · 2 guards · 1 no-tools")).toHaveAttribute("data-tone", "warning");
    expect(screen.getByTestId("workflow-details")).toHaveTextContent("Loop 1 max-turn · 2 guards · 1 no-tools");
  });
});

function metric(root: HTMLElement, text: string): HTMLElement {
  return within(root).getAllByText((_, element) => element?.textContent?.includes(text) ?? false, { selector: "span" })[0];
}
