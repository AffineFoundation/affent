import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { deriveWorkflowStatus } from "../store/workflowStatus";
import { buildSessionOverview } from "../view/sessionOverview";
import { WorkflowStatus } from "./WorkflowStatus";

describe("WorkflowStatus", () => {
  it("summarizes the latest completed chat without exposing a separate mode", () => {
    const session = reduceRawEvents(completedTurn);
    render(<WorkflowStatus overview={buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    })} />);

    expect(screen.getByTestId("workflow-status")).toHaveTextContent("list the files");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("Result ready");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("README.md main.go");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("1 action");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("138 tokens");
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
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("ls");
    expect(screen.getByTestId("workflow-status")).toHaveTextContent("inspect");
    expect(screen.getByTestId("workflow-status")).not.toHaveTextContent("Using shell");
  });
});
