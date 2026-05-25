import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { deriveWorkflowStatus } from "../store/workflowStatus";
import { buildSessionOverview } from "../view/sessionOverview";
import { WorkflowStatus } from "./WorkflowStatus";

describe("WorkflowStatus", () => {
  it("summarizes the latest completed chat while folding technical metrics", async () => {
    const user = userEvent.setup();
    const session = reduceRawEvents(completedTurn);
    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    expect(screen.getByTestId("workflow-status")).toHaveTextContent("list the files");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("Result ready");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("README.md main.go");
    const details = screen.getByTestId("workflow-details");
    expect(details).not.toHaveAttribute("open");
    expect(metric(details, "1 action")).not.toBeVisible();

    await user.click(screen.getByText("Run details"));

    expect(details).toHaveAttribute("open");
    expect(metric(details, "1 action")).toBeVisible();
    expect(metric(details, "138 tokens")).toBeVisible();
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
});

function metric(root: HTMLElement, text: string): HTMLElement {
  return within(root).getByText((_, element) => element?.textContent === text);
}
