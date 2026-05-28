import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionTrace, sessionTraceDraft, sessionTraceEvidenceText } from "./sessionTrace";

describe("buildSessionTrace", () => {
  it("summarizes current session trace evidence without raw event parsing in components", () => {
    const session = reduceRawEvents([
      { id: 0, type: "trace.meta", data: { schema_version: 1 } },
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Inspect WebUI trace" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "README.md" } } },
      { id: 4, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "README.md", result: "README.md" } },
      { id: 5, type: "future.trace", data: { turn_id: "t1", value: true } },
    ]);

    const trace = buildSessionTrace(session);

    expect(trace).toMatchObject({
      summary: "6 trace entries",
      detail: "5 grouped records · schema v1 · 1 unclassified",
      eventCount: 6,
      recordCount: 5,
      metadataCount: 1,
      unknownCount: 1,
      schemaVersion: 1,
      latest: {
        label: "future.trace",
      },
    });
    expect(sessionTraceEvidenceText(trace)).toContain("Unclassified events: 1");
    expect(sessionTraceDraft(trace)).toContain("Inspect this session trace");
  });

  it("builds concise trace-backed tool issue summaries without raw next-step noise", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "Run tests" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "shell", tool: "shell", args: { command: "npm test" } } },
      {
        id: 4,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "shell",
          exit_code: 1,
          failure_kind: "invalid_args",
          result_summary: "failed\nNext: rerun npm test after fixing checkout\nFailure: kind=invalid_args",
          result: "failed\nNext: rerun npm test after fixing checkout\nFailure: kind=invalid_args",
          result_artifact_path: ".affent/artifacts/tool-results/000001-shell.txt",
          duration_ms: 340,
        },
      },
    ]);

    const trace = buildSessionTrace(session);

    expect(trace.toolIssueCount).toBe(1);
    expect(trace.toolIssues).toEqual([
      {
        id: "shell",
        query: "call:shell",
        requestQuery: "request:1",
        title: "Request 1 · shell",
        tool: "shell",
        detail: "invalid_args · failed",
        badges: ["exit 1", "invalid_args"],
        turnNumber: 1,
        turnId: "t1",
        exitCode: 1,
        durationMs: 340,
        artifactPath: ".affent/artifacts/tool-results/000001-shell.txt",
        next: "rerun npm test after fixing checkout",
        occurrences: 1,
      },
    ]);
    expect(sessionTraceEvidenceText(trace)).toContain("Tool issue: Request 1 · shell · invalid_args · failed");
    expect(sessionTraceEvidenceText(trace)).not.toContain("Next: rerun npm test after fixing checkout");
  });

  it("compacts repeated tool issues while preserving total issue count", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "loop1", tool: "loop_protocol", args: {} } },
      { id: 3, type: "tool.result", data: { turn_id: "t1", call_id: "loop1", exit_code: 1, failure_kind: "loop_protocol_activation_status", result_summary: "Error: complete_activation requires LOOP.md metadata status: draft or running" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "loop2", tool: "loop_protocol", args: {} } },
      { id: 5, type: "tool.result", data: { turn_id: "t1", call_id: "loop2", exit_code: 1, failure_kind: "loop_protocol_activation_status", result_summary: "Error: complete_activation requires LOOP.md metadata status: draft or running" } },
    ]);

    const trace = buildSessionTrace(session);

    expect(trace.toolIssueCount).toBe(2);
    expect(trace.toolIssues).toHaveLength(1);
    expect(trace.toolIssues[0]).toMatchObject({
      id: "loop1",
      tool: "loop_protocol",
      occurrences: 2,
      badges: ["exit 1", "loop_protocol_activation_status", "2x"],
    });
    expect(sessionTraceEvidenceText(trace)).toContain("2 occurrences");
  });

  it("returns an empty state for sessions without trace", () => {
    const trace = buildSessionTrace(reduceRawEvents([]));

    expect(trace).toMatchObject({
      summary: "No trace entries",
      detail: "No persisted trace loaded for this chat.",
      eventCount: 0,
      recordCount: 0,
    });
  });
});
