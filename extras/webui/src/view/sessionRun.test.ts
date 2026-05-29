import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionRun, manualRunDraft, runCommandDraft, runCommandEvidenceText, runCommandKind, runCommandMeta, runFocusCommand, runReviewFacts, runReviewFocus } from "./sessionRun";

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
      kind: "test",
      cwd: "extras/webui",
      status: "failed",
      exitCode: 1,
      durationMs: 1480,
      detail: "checkout spec failed",
      next: "update payment route then rerun",
      artifactPath: ".affent/artifacts/tool-results/test.txt",
    });
    expect(runCommandMeta(run.commands[0])).toBe("test · failed · exit 1 · 1.48s · turn 1");
    expect(runFocusCommand(run.commands)).toMatchObject({
      label: "Recovery needed",
      tone: "error",
      command: expect.objectContaining({ command: "npm test -- checkout.spec.ts" }),
    });
    expect(runReviewFocus(run.commands)).toMatchObject({
      label: "Unresolved failure",
      tone: "danger",
      title: "npm test -- checkout.spec.ts",
    });
    expect(runReviewFacts(run.commands)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Failures", value: "1", detail: "unresolved", tone: "danger" }),
      expect.objectContaining({ label: "Verification", value: "0/1", detail: "test/build/lint/typecheck", tone: "attention" }),
      expect.objectContaining({ label: "Output", value: "1", detail: "artifact captured", tone: "ok" }),
    ]));
    expect(runCommandEvidenceText(run.commands[0])).toBe(
      [
        "Run evidence for npm test -- checkout.spec.ts",
        "Status: failed",
        "Kind: test",
        "Exit: 1",
        "Duration: 1.48s",
        "Turn: 1",
        "Working directory: extras/webui",
        "Output: checkout spec failed",
        "Next: update payment route then rerun",
        "Full output: captured",
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

  it("keeps internal budget notices out of command summaries", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "test", tool: "shell", args: { command: "python3 -m pytest" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          call_id: "test",
          exit_code: 1,
          result_summary: "(tool result context budget exhausted; final no-tool answer requested)",
          result: "(tool result context budget exhausted; final no-tool answer requested)",
        },
      },
    ]);

    expect(buildSessionRun(session).commands[0]).toMatchObject({
      command: "python3 -m pytest",
      status: "failed",
      detail: undefined,
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

  it("classifies common developer command kinds", () => {
    expect(runCommandKind("npm test -- checkout.spec.ts")).toBe("test");
    expect(runCommandKind("npm run typecheck")).toBe("typecheck");
    expect(runCommandKind("npm run lint")).toBe("lint");
    expect(runCommandKind("npm run build")).toBe("build");
    expect(runCommandKind("git push -u origin main")).toBe("git");
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

    const run = buildSessionRun(session);
    expect(run.commands.map((command) => command.command)).toEqual(["npm run build", "npm test"]);
    expect(runFocusCommand(run.commands)).toMatchObject({
      label: "Latest verification",
      tone: "success",
      command: expect.objectContaining({ command: "npm run build" }),
    });
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

  it("does not keep an older failed command as focus after a matching later command passes", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "fail", tool: "shell", args: { command: "npm test" } } },
      { id: 3, type: "tool.result", data: { call_id: "fail", exit_code: 1, result_summary: "tests failed", result: "tests failed" } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      { id: 6, type: "tool.request", data: { turn_id: "t2", call_id: "pass", tool: "shell", args: { command: "npm test" } } },
      { id: 7, type: "tool.result", data: { call_id: "pass", exit_code: 0, result_summary: "tests passed", result: "tests passed" } },
    ]);

    const run = buildSessionRun(session);
    expect(run).toMatchObject({
      summary: "1 recovered failure",
      detail: "1 failed · 1 passed",
      tone: undefined,
    });
    expect(runFocusCommand(run.commands)).toMatchObject({
      label: "Latest verification",
      tone: "success",
      command: expect.objectContaining({ command: "npm test" }),
    });
    expect(runReviewFocus(run.commands)).toMatchObject({
      label: "Recovered",
      tone: "ok",
      title: "1 earlier failure followed by a pass",
    });
    expect(runReviewFacts(run.commands)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Failures", value: "1", detail: "covered by later pass", tone: "ok" }),
      expect.objectContaining({ label: "Verification", value: "1/2", detail: "test/build/lint/typecheck", tone: "ok" }),
      expect.objectContaining({ label: "Latest", value: "passed", detail: "turn 2", tone: "ok" }),
    ]));
  });

  it("does not treat unrelated later success as recovery for a failed verification", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "fail", tool: "shell", args: { command: "npm test" } } },
      { id: 3, type: "tool.result", data: { call_id: "fail", exit_code: 1, result_summary: "tests failed", result: "tests failed" } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      { id: 6, type: "tool.request", data: { turn_id: "t2", call_id: "push", tool: "shell", args: { command: "git push -u origin main" } } },
      { id: 7, type: "tool.result", data: { call_id: "push", exit_code: 0, result_summary: "pushed", result: "pushed" } },
    ]);

    const run = buildSessionRun(session);

    expect(run).toMatchObject({ summary: "1 failed command", tone: "error" });
    expect(runFocusCommand(run.commands)).toMatchObject({
      label: "Recovery needed",
      command: expect.objectContaining({ command: "npm test" }),
    });
    expect(runReviewFocus(run.commands)).toMatchObject({
      label: "Unresolved failure",
      detail: "tests failed",
      tone: "danger",
    });
  });

  it("does not label non-verification shell success as verification", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "push", tool: "shell", args: { command: "git push -u origin main" } } },
      { id: 3, type: "tool.result", data: { call_id: "push", exit_code: 0, result_summary: "pushed", result: "pushed" } },
    ]);

    const run = buildSessionRun(session);

    expect(runFocusCommand(run.commands)).toMatchObject({
      label: "Latest command",
      command: expect.objectContaining({ command: "git push -u origin main", kind: "git" }),
    });
    expect(runReviewFocus(run.commands)).toMatchObject({
      label: "Passed",
      title: "git push -u origin main",
    });
    expect(runReviewFacts(run.commands)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Verification", value: "0/0", detail: "none recorded", tone: "neutral" }),
    ]));
  });

  it("keeps a newer failed command unresolved after an earlier pass", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "pass", tool: "shell", args: { command: "npm run build" } } },
      { id: 3, type: "tool.result", data: { call_id: "pass", exit_code: 0, result_summary: "built", result: "built" } },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      { id: 6, type: "tool.request", data: { turn_id: "t2", call_id: "fail", tool: "shell", args: { command: "npm test" } } },
      { id: 7, type: "tool.result", data: { call_id: "fail", exit_code: 1, result_summary: "tests failed", result: "tests failed" } },
    ]);

    const run = buildSessionRun(session);

    expect(runFocusCommand(run.commands)).toMatchObject({
      label: "Recovery needed",
      tone: "error",
      command: expect.objectContaining({ command: "npm test", status: "failed", turnNumber: 2 }),
    });
    expect(runReviewFocus(run.commands)).toMatchObject({
      label: "Unresolved failure",
      title: "npm test",
      detail: "tests failed",
      tone: "danger",
    });
    expect(runReviewFacts(run.commands)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Failures", value: "1", detail: "unresolved", tone: "danger" }),
      expect.objectContaining({ label: "Latest", value: "failed", detail: "turn 2", tone: "danger" }),
    ]));
  });
});
