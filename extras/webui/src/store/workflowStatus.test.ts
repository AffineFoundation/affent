import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "./reduce";
import { deriveWorkflowStatus } from "./workflowStatus";

describe("deriveWorkflowStatus", () => {
  it("reports a completed workflow with useful summary", () => {
    const status = deriveWorkflowStatus(reduceRawEvents(completedTurn));

    expect(status).toMatchObject({
      phase: "done",
      title: "Result ready",
      active: false,
      progress: 100,
    });
    expect(status.detail).toContain("action");
  });

  it("reports current background work without exposing tool labels in the title", () => {
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

    expect(deriveWorkflowStatus(session)).toMatchObject({
      phase: "tools",
      title: "Working",
      detail: "A background action is running.",
      active: true,
      currentTool: "shell",
    });
  });
});
