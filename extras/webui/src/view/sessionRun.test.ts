import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionRun, manualRunDraft, runCommandDraft, runCommandEvidenceText, runCommandMeta } from "./sessionRun";

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
    expect(run.latestCommandCwd).toBe("extras/webui");
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
    expect(runCommandMeta(run.commands[0])).toBe("failed · exit 1 · 1.48s · turn 1");
    expect(runCommandEvidenceText(run.commands[0])).toBe(
      [
        "Run evidence for npm test -- checkout.spec.ts",
        "Status: failed",
        "Exit: 1",
        "Duration: 1.48s",
        "Turn: 1",
        "Working directory: extras/webui",
        "Output: checkout spec failed",
        "Next: update payment route then rerun",
        "Output artifact: .affent/artifacts/tool-results/test.txt",
      ].join("\n"),
    );
    expect(runCommandDraft(run.commands[0])).toContain("Run evidence for npm test -- checkout.spec.ts");
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

  it("builds a structured manual command draft", () => {
    expect(manualRunDraft(" npm run build ", " extras/webui ")).toBe(
      [
        "Run this command in the session workspace, then report the exit code, working directory, and relevant output:",
        "npm run build",
        "Working directory: extras/webui",
      ].join("\n"),
    );
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

  it("prioritizes failed and running commands before passed history", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "fail", tool: "shell", args: { command: "npm test" } } },
      { id: 3, type: "tool.result", data: { call_id: "fail", exit_code: 1, result_summary: "tests failed", result: "tests failed" } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      { id: 6, type: "tool.request", data: { turn_id: "t2", call_id: "pass", tool: "shell", args: { command: "npm run lint" } } },
      { id: 7, type: "tool.result", data: { call_id: "pass", exit_code: 0, result_summary: "ok", result: "ok" } },
      { id: 8, type: "tool.request", data: { turn_id: "t2", call_id: "running", tool: "shell", args: { command: "npm run build" } } },
    ]);

    expect(buildSessionRun(session).commands.map((command) => command.command)).toEqual([
      "npm test",
      "npm run build",
      "npm run lint",
    ]);
  });
});
