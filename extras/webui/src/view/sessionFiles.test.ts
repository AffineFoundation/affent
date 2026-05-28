import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionFiles, fileContentDraft, fileContentText, fileEvidenceDraft, fileEvidenceText, fileLines, fileRangeDraft, filesEvidenceDraft, filesEvidenceText, filesReviewFacts, filesReviewFocus } from "./sessionFiles";

describe("buildSessionFiles", () => {
  it("summarizes read, list, and changed file evidence from reducer state", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "list", tool: "list_files", args: { path: "src" } } },
      { id: 3, type: "tool.result", data: { call_id: "list", exit_code: 0, result_summary: "src/payments.ts\nsrc/cart.ts", result: "src/payments.ts\nsrc/cart.ts" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "src/payments.ts" } } },
      { id: 5, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "existing route", result: "existing route", result_artifact_path: ".affent/artifacts/tool-results/read.txt" } },
      { id: 6, type: "tool.request", data: { turn_id: "t1", call_id: "edit", tool: "edit_file", args: { path: "src/payments.ts" } } },
      { id: 7, type: "tool.result", data: { call_id: "edit", exit_code: 0, result_summary: "Updated payment route", result: "Updated payment route" } },
    ]);

    const files = buildSessionFiles(session);

    expect(files).toMatchObject({
      summary: "2 file references",
      detail: "1 read · 1 listed · 1 changed",
      stats: {
        total: 2,
        available: 2,
        failed: 0,
        running: 0,
        read: 1,
        listed: 1,
        changed: 1,
        snapshots: 1,
      },
    });
    expect(files.items).toEqual([
      expect.objectContaining({
        path: "src/payments.ts",
        actions: ["read", "changed"],
        status: "available",
        actionCount: 2,
        turnNumber: 1,
        detail: "Updated payment route",
        artifactPath: ".affent/artifacts/tool-results/read.txt",
        contentPreview: "existing route",
        contentSource: "read_file",
        contentTruncated: false,
      }),
      expect.objectContaining({ path: "src", actions: ["listed"], status: "available" }),
    ]);
    expect(filesReviewFocus(files.items)).toMatchObject({
      label: "Changed file",
      title: "src/payments.ts",
      tone: "ok",
    });
    expect(filesReviewFacts(files.items)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Snapshots", value: "1/2", tone: "attention" }),
      expect.objectContaining({ label: "Issues", value: "0", detail: "none", tone: "neutral" }),
    ]));
  });

  it("keeps path recovery evidence when a file action fails", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { filename: "docs/missing.md" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "read",
          exit_code: 1,
          failure_kind: "not_found",
          result_summary: "file not found\nNext: run rg --files docs before retrying\nFailure: kind=not_found",
          result: "file not found\nNext: run rg --files docs before retrying\nFailure: kind=not_found",
        },
      },
    ]);

    const files = buildSessionFiles(session);

    expect(files).toMatchObject({ summary: "1 file issue", detail: "1 read", tone: "error" });
    expect(files.items[0]).toMatchObject({
      path: "docs/missing.md",
      status: "failed",
      detail: "file not found",
      next: "run rg --files docs before retrying",
    });
    expect(filesReviewFocus(files.items)).toMatchObject({
      label: "Path issue",
      title: "docs/missing.md",
      tone: "danger",
    });
    expect(filesReviewFacts(files.items)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Issues", value: "1", detail: "path failures", tone: "danger" }),
    ]));
  });

  it("merges workspace-absolute, workspace-relative, and relative evidence for the same file", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "write", tool: "write_file", args: { path: "/workspace/sessions/sess_123-456/game2048.py" } } },
      { id: 3, type: "tool.result", data: { call_id: "write", exit_code: 0, result_summary: "wrote file", result: "wrote file" } },
      { id: 4, type: "turn.start", data: { turn_id: "t2" } },
      { id: 5, type: "tool.request", data: { turn_id: "t2", call_id: "bad-read", tool: "read_file", args: { path: "workspace/sessions/sess_123-456/game2048.py" } } },
      {
        id: 6,
        type: "tool.result",
        data: {
          call_id: "bad-read",
          exit_code: 1,
          result_summary: "not found\nNext: list workspace root\nFailure: kind=not_found",
          result: "not found\nNext: list workspace root\nFailure: kind=not_found",
        },
      },
      { id: 7, type: "turn.start", data: { turn_id: "t3" } },
      { id: 8, type: "tool.request", data: { turn_id: "t3", call_id: "read", tool: "read_file", args: { path: "game2048.py" } } },
      { id: 9, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "loaded game", result: "print('2048')" } },
    ]);

    const files = buildSessionFiles(session);

    expect(files).toMatchObject({
      summary: "1 file reference",
      detail: "1 read · 1 changed",
      tone: undefined,
    });
    expect(files.items).toHaveLength(1);
    expect(files.items[0]).toMatchObject({
      path: "game2048.py",
      actions: ["read", "changed"],
      status: "available",
      actionCount: 3,
      detail: "loaded game",
      next: undefined,
      contentPreview: "print('2048')",
    });
  });

  it("stays empty when no file tools ran", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "shell", tool: "shell", args: { command: "npm test" } } },
    ]);

    expect(buildSessionFiles(session)).toMatchObject({
      items: [],
      summary: "No file evidence",
      detail: "No read, list, write, or edit actions in this chat.",
    });
  });

  it("builds reusable evidence and draft text from file view data", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "src/payments.ts" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "read",
          exit_code: 0,
          result_summary: "checkout route handler\nNext: rerun checkout tests",
          result: "checkout route handler\nNext: rerun checkout tests",
          result_artifact_path: ".affent/artifacts/tool-results/read.txt",
        },
      },
    ]);

    const [item] = buildSessionFiles(session).items;

    expect(fileEvidenceText(item)).toContain("File evidence for src/payments.ts");
    expect(fileEvidenceText(item)).toContain("Detail: checkout route handler");
    expect(fileEvidenceText(item)).toContain("Next: rerun checkout tests");
    expect(fileEvidenceText(item)).toContain("Loaded snapshot: read_file output");
    expect(fileEvidenceDraft(item)).toContain("Use this file evidence in the next step");
    expect(fileEvidenceDraft(item)).toContain("Evidence artifact: .affent/artifacts/tool-results/read.txt");
    expect(filesEvidenceText(buildSessionFiles(session))).toContain("Session file evidence");
    expect(filesEvidenceText(buildSessionFiles(session))).toContain("File evidence for src/payments.ts");
    expect(filesEvidenceDraft(buildSessionFiles(session))).toContain("decide what to inspect, fix, or review next");
    expect(fileContentText(item)).toContain("File snapshot for src/payments.ts");
    expect(fileContentText(item)).toContain("checkout route handler");
    expect(fileContentDraft(item)).toContain("Use this loaded file snapshot in the next step");
    expect(fileLines(item)).toEqual(["checkout route handler", "Next: rerun checkout tests"]);
    expect(fileRangeDraft(item, 2, 1, "ask")).toContain("Lines: 1-2");
    expect(fileRangeDraft(item, 2, 1, "ask")).toContain("Review this selected file range");
    expect(fileRangeDraft(item, 1, 1, "edit")).toContain("Edit this selected file range");
  });

  it("marks truncated read_file snapshots from reducer state", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "src/large.ts" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "read",
          exit_code: 0,
          result_summary: "partial file output",
          result: "export const partial = true;",
          result_truncated: true,
          result_bytes: 8192,
        },
      },
    ]);

    const [item] = buildSessionFiles(session).items;

    expect(item).toMatchObject({
      path: "src/large.ts",
      contentPreview: "export const partial = true;",
      contentTruncated: true,
      contentBytes: 8192,
    });
    expect(fileContentText(item)).toContain("Snapshot: partial output");
  });

  it("prioritizes failed, running, and changed files before passive reads", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "missing", tool: "read_file", args: { path: "docs/missing.md" } } },
      { id: 3, type: "tool.result", data: { call_id: "missing", exit_code: 1, result_summary: "missing", result: "missing" } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      { id: 6, type: "tool.request", data: { turn_id: "t2", call_id: "read", tool: "read_file", args: { path: "src/readme.ts" } } },
      { id: 7, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "read", result: "read" } },
      { id: 8, type: "tool.request", data: { turn_id: "t2", call_id: "edit", tool: "edit_file", args: { path: "src/payments.ts" } } },
      { id: 9, type: "tool.result", data: { call_id: "edit", exit_code: 0, result_summary: "changed", result: "changed" } },
      { id: 10, type: "tool.request", data: { turn_id: "t2", call_id: "running", tool: "read_file", args: { path: "src/current.ts" } } },
      { id: 11, type: "tool.request", data: { turn_id: "t2", call_id: "list", tool: "list_files", args: { path: "src" } } },
      { id: 12, type: "tool.result", data: { call_id: "list", exit_code: 0, result_summary: "listed", result: "listed" } },
    ]);

    expect(buildSessionFiles(session).items.map((item) => item.path)).toEqual([
      "docs/missing.md",
      "src/current.ts",
      "src/payments.ts",
      "src/readme.ts",
      "src",
    ]);
  });
});
