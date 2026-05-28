import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionChanges } from "./sessionChanges";

describe("buildSessionChanges", () => {
  it("summarizes write and edit tool calls from reducer state", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "fix checkout tests" } },
      { id: 3, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "src/payments.ts" } } },
      { id: 4, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "existing route", result: "existing route" } },
      { id: 5, type: "tool.request", data: { turn_id: "t1", call_id: "edit", tool: "edit_file", args: { path: "src/payments.ts" } } },
      { id: 6, type: "tool.result", data: { call_id: "edit", exit_code: 0, result_summary: "Updated payment route", result: "Updated payment route" } },
      { id: 7, type: "tool.request", data: { turn_id: "t1", call_id: "write", tool: "write_file", args: { path: "tests/payments.spec.ts" } } },
      { id: 8, type: "tool.result", data: { call_id: "write", exit_code: 0, result_summary: "Wrote checkout regression spec", result: "Wrote checkout regression spec", result_artifact_path: ".affent/artifacts/tool-results/write.txt" } },
      { id: 9, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    const changes = buildSessionChanges(session);

    expect(changes.summary).toBe("2 changed files");
    expect(changes.detail).toBe("2 changed");
    expect(changes.files).toEqual([
      expect.objectContaining({ path: "tests/payments.spec.ts", operation: "write", status: "changed", artifactPath: ".affent/artifacts/tool-results/write.txt" }),
      expect.objectContaining({ path: "src/payments.ts", operation: "edit", status: "changed", turnNumber: 1, detail: "Updated payment route" }),
    ]);
  });

  it("keeps the latest status for repeated changes to the same file", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "edit-1", tool: "edit_file", args: { path: "src/app.ts" } } },
      { id: 3, type: "tool.result", data: { call_id: "edit-1", exit_code: 0, result_summary: "first edit", result: "first edit" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "edit-2", tool: "edit_file", args: { path: "src/app.ts" } } },
      { id: 5, type: "tool.result", data: { call_id: "edit-2", exit_code: 1, failure_kind: "not_found", result_summary: "file moved", result: "file moved" } },
    ]);

    const changes = buildSessionChanges(session);

    expect(changes.summary).toBe("1 file issue");
    expect(changes.tone).toBe("error");
    expect(changes.files[0]).toMatchObject({ path: "src/app.ts", status: "failed", actionCount: 2, detail: "file moved" });
  });

  it("does not report read-only file evidence as a change", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "README.md" } } },
      { id: 3, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "read", result: "read" } },
    ]);

    expect(buildSessionChanges(session)).toMatchObject({
      files: [],
      summary: "No file changes",
      detail: "No write or edit actions in this chat.",
    });
  });

  it("prioritizes failed and running changes before completed edits", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "failed", tool: "edit_file", args: { path: "src/old.ts" } } },
      { id: 3, type: "tool.result", data: { call_id: "failed", exit_code: 1, result_summary: "file moved", result: "file moved" } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      { id: 6, type: "tool.request", data: { turn_id: "t2", call_id: "changed", tool: "write_file", args: { path: "src/new.ts" } } },
      { id: 7, type: "tool.result", data: { call_id: "changed", exit_code: 0, result_summary: "wrote file", result: "wrote file" } },
      { id: 8, type: "tool.request", data: { turn_id: "t2", call_id: "running", tool: "edit_file", args: { path: "src/current.ts" } } },
    ]);

    expect(buildSessionChanges(session).files.map((file) => file.path)).toEqual([
      "src/old.ts",
      "src/current.ts",
      "src/new.ts",
    ]);
  });
});
