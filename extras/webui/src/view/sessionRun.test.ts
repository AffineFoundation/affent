import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionRun } from "./sessionRun";

describe("buildSessionRun", () => {
  it("summarizes shell commands with failure recovery and artifacts", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "test", tool: "shell", args: { command: "npm test -- checkout.spec.ts", cwd: "extras/webui" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "test",
          exit_code: 1,
          duration_ms: 1480,
          result_summary: "checkout spec failed\nNext: update payment route then rerun\nFailure: kind=test_failed",
          result: "checkout spec failed\nNext: update payment route then rerun\nFailure: kind=test_failed",
          result_artifact_path: ".affent/artifacts/tool-results/test.txt",
        },
      },
    ]);

    const run = buildSessionRun(session);

    expect(run).toMatchObject({ summary: "1 failed command", detail: "1 failed", tone: "error" });
    expect(run.commands[0]).toMatchObject({
      command: "npm test -- checkout.spec.ts",
      cwd: "extras/webui",
      status: "failed",
      exitCode: 1,
      durationMs: 1480,
      detail: "checkout spec failed",
      next: "update payment route then rerun",
      artifactPath: ".affent/artifacts/tool-results/test.txt",
    });
  });

  it("keeps non-shell actions out of Run", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "read", tool: "read_file", args: { path: "README.md" } } },
      { id: 3, type: "tool.result", data: { call_id: "read", exit_code: 0, result_summary: "read", result: "read" } },
    ]);

    expect(buildSessionRun(session)).toMatchObject({
      commands: [],
      summary: "No commands",
      detail: "No shell commands in this chat.",
    });
  });

  it("tracks running commands before a result arrives", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "build", tool: "shell", args: { command: "npm run build" } } },
    ]);

    expect(buildSessionRun(session)).toMatchObject({
      summary: "1 running command",
      detail: "1 running",
      tone: "warning",
      commands: [expect.objectContaining({ command: "npm run build", status: "running" })],
    });
  });

  it("orders newer shell commands first within the same turn", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "first", tool: "shell", args: { command: "npm test" } } },
      { id: 3, type: "tool.result", data: { call_id: "first", exit_code: 0, result_summary: "ok", result: "ok" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "second", tool: "shell", args: { command: "npm run build" } } },
      { id: 5, type: "tool.result", data: { call_id: "second", exit_code: 0, result_summary: "built", result: "built" } },
    ]);

    expect(buildSessionRun(session).commands.map((command) => command.command)).toEqual(["npm run build", "npm test"]);
  });
});
