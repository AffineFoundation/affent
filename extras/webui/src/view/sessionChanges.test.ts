import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionChanges, changedFileDiffText, changedFileDraft } from "./sessionChanges";

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

  it("extracts unified diff evidence without making prose look like a diff", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "edit", tool: "edit_file", args: { path: "src/payments.ts" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "edit",
          exit_code: 0,
          result_summary: "Updated payment route",
          result: [
            "Updated payment route",
            "diff --git a/src/payments.ts b/src/payments.ts",
            "index 1111111..2222222 100644",
            "--- a/src/payments.ts",
            "+++ b/src/payments.ts",
            "@@ -1,3 +1,4 @@",
            " export function pay() {",
            "-  return false;",
            "+  const enabled = true;",
            "+  return enabled;",
            " }",
          ].join("\n"),
        },
      },
    ]);

    const [file] = buildSessionChanges(session).files;

    expect(file).toMatchObject({
      path: "src/payments.ts",
      detail: "Updated payment route",
      additions: 2,
      deletions: 1,
      diffTruncated: false,
    });
    expect(file.diffPreview?.map((line) => [line.kind, line.text])).toEqual([
      ["meta", "diff --git a/src/payments.ts b/src/payments.ts"],
      ["meta", "index 1111111..2222222 100644"],
      ["meta", "--- a/src/payments.ts"],
      ["meta", "+++ b/src/payments.ts"],
      ["hunk", "@@ -1,3 +1,4 @@"],
      ["context", " export function pay() {"],
      ["remove", "-  return false;"],
      ["add", "+  const enabled = true;"],
      ["add", "+  return enabled;"],
      ["context", " }"],
    ]);
  });

  it("keeps the latest non-empty diff evidence when a file changes again", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "edit-1", tool: "edit_file", args: { path: "src/app.ts" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "edit-1",
          exit_code: 0,
          result_summary: "Updated app shell",
          result: [
            "Updated app shell",
            "diff --git a/src/app.ts b/src/app.ts",
            "index 1111111..2222222 100644",
            "--- a/src/app.ts",
            "+++ b/src/app.ts",
            "@@ -1,2 +1,3 @@",
            " export const app = true;",
            "-export const ready = false;",
            "+export const ready = true;",
            "+export const active = true;",
          ].join("\n"),
        },
      },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "edit-2", tool: "edit_file", args: { path: "src/app.ts" } } },
      { id: 5, type: "tool.result", data: { call_id: "edit-2", exit_code: 1, failure_kind: "not_found", result_summary: "file moved", result: "file moved" } },
    ]);

    const [file] = buildSessionChanges(session).files;
    expect(file).toMatchObject({
      path: "src/app.ts",
      status: "failed",
      actionCount: 2,
      additions: 2,
      deletions: 1,
    });
    expect(file.diffPreview?.[0]).toMatchObject({ kind: "meta", text: "diff --git a/src/app.ts b/src/app.ts" });
  });

  it("shows diff-backed completed changes before artifact-only changes", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "edit", tool: "edit_file", args: { path: "src/checkout.ts" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "edit",
          exit_code: 0,
          result_summary: "Updated checkout validation",
          result: [
            "Updated checkout validation",
            "diff --git a/src/checkout.ts b/src/checkout.ts",
            "--- a/src/checkout.ts",
            "+++ b/src/checkout.ts",
            "@@ -1 +1 @@",
            "-return false;",
            "+return true;",
          ].join("\n"),
        },
      },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "write", tool: "write_file", args: { path: "tests/checkout.spec.ts" } } },
      { id: 5, type: "tool.result", data: { call_id: "write", exit_code: 0, result_summary: "Wrote checkout spec", result: "Wrote checkout spec" } },
    ]);

    expect(buildSessionChanges(session).files.map((file) => file.path)).toEqual([
      "src/checkout.ts",
      "tests/checkout.spec.ts",
    ]);
  });

  it("builds copyable diff and adjustment draft text from view data", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "edit", tool: "edit_file", args: { path: "src/checkout.ts" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "edit",
          exit_code: 0,
          result_summary: "Updated checkout validation",
          result: [
            "Updated checkout validation",
            "diff --git a/src/checkout.ts b/src/checkout.ts",
            "--- a/src/checkout.ts",
            "+++ b/src/checkout.ts",
            "@@ -1 +1 @@",
            "-return false;",
            "+return true;",
          ].join("\n"),
        },
      },
    ]);

    const [file] = buildSessionChanges(session).files;

    expect(changedFileDiffText(file)).toContain("Diff for src/checkout.ts");
    expect(changedFileDiffText(file)).toContain("+return true;");
    expect(changedFileDraft(file)).toContain("Path: src/checkout.ts");
    expect(changedFileDraft(file)).toContain("+return true;");
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
