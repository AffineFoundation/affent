import { describe, expect, it } from "vitest";
import type { SessionSummary } from "../api/sessions";
import type { SessionRunView } from "./sessionRun";
import { buildSessionWorkspace } from "./sessionWorkspace";

describe("buildSessionWorkspace", () => {
  it("summarizes a recorded workspace binding with the latest command cwd", () => {
    const workspace = buildSessionWorkspace(
      session({
        workspace_path: "/repo/affent",
        workspace_label: "affent",
        default_branch: "main",
        dirty_state: "dirty",
      }),
      run({ cwd: "extras/webui" }),
    );

    expect(workspace).toMatchObject({
      hasData: true,
      summary: "affent",
      detail: "/repo/affent · branch main · dirty · cwd extras/webui",
      path: "/repo/affent",
      lastAgentCwd: "extras/webui",
    });
  });

  it("flags absolute command cwd outside the session workspace", () => {
    expect(buildSessionWorkspace(
      session({ workspace_path: "/repo/affent", workspace_label: "affent", last_agent_cwd: "/tmp" }),
      run(),
    )).toMatchObject({
      hasData: true,
      summary: "Workspace mismatch",
      tone: "warning",
      issue: "Latest command cwd is outside the session workspace.",
    });
  });

  it("stays absent when no workspace evidence exists", () => {
    expect(buildSessionWorkspace(session({}), run())).toMatchObject({
      hasData: false,
      summary: "No workspace evidence",
    });
  });
});

function session(overrides: Partial<SessionSummary>): SessionSummary {
  return {
    id: "s1",
    active: false,
    durable: true,
    has_conversation: true,
    has_events: true,
    has_artifacts: false,
    has_memory: false,
    has_runtime_skills: false,
    ...overrides,
  };
}

function run(opts: { cwd?: string } = {}): SessionRunView {
  return {
    summary: "Run",
    detail: "Run",
    commands: opts.cwd ? [{ command: "npm test", cwd: opts.cwd, status: "passed", turnNumber: 1 }] : [],
  };
}
