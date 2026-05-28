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
