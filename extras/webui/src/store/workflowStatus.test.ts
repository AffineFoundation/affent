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

  it("uses verified source evidence as the completed workflow detail", () => {
    const status = deriveWorkflowStatus(reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "verify dynamic web metrics" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "browser_network_read",
          args: { ref: "net:1" },
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 20,
          result_summary: "validated metrics",
          result_truncated: false,
        },
      },
      {
        id: 5,
        type: "turn.end",
        data: {
          turn_id: "t1",
          reason: "completed",
          tool_stats: {
            tool_requests: 1,
            source_access_verified: 2,
            source_access_network: 1,
            source_access_dynamic_partial: 1,
          },
        },
      },
    ]));

    expect(status.detail).toBe("1 network source verified after 1 partial dynamic source.");
  });
});
