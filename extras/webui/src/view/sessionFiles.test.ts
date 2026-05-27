import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionFiles } from "./sessionFiles";

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

    expect(files).toMatchObject({ summary: "2 file references", detail: "1 read · 1 listed · 1 changed" });
    expect(files.items).toEqual([
      expect.objectContaining({
        path: "src/payments.ts",
        actions: ["read", "changed"],
        status: "available",
        actionCount: 2,
        turnNumber: 1,
        detail: "Updated payment route",
        artifactPath: ".affent/artifacts/tool-results/read.txt",
      }),
      expect.objectContaining({ path: "src", actions: ["listed"], status: "available" }),
    ]);
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
});
