import { describe, expect, it } from "vitest";
import type { SessionSummary } from "../api/sessions";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionRun } from "./sessionRun";
import type { SessionRunView } from "./sessionRun";
import { buildSessionWorkspace, workspaceCwdBrowserPath, workspaceDraft, workspaceEvidenceText, workspaceReviewFacts, workspaceVerifyDraft, workspaceVerifyRequest } from "./sessionWorkspace";

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
      shortStatus: "affent · main · dirty",
      detail: "/repo/affent · branch main · dirty · cwd extras/webui",
      verification: "verified",
      path: "/repo/affent",
      latestCommandCwd: "extras/webui",
    });
    expect(workspaceReviewFacts(workspace)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Binding", value: "Recorded", tone: "ok" }),
      expect.objectContaining({ label: "Agent cwd", value: "Inside", detail: "latest command", tone: "ok" }),
    ]));
    expect(workspaceCwdBrowserPath(workspace)).toBe("extras/webui");
  });

  it("flags absolute command cwd outside the session workspace", () => {
    const workspace = buildSessionWorkspace(
      session({ workspace_path: "/repo/affent", workspace_label: "affent", last_agent_cwd: "/tmp" }),
      run(),
    );

    expect(workspace).toMatchObject({
      hasData: true,
      summary: "Workspace mismatch",
      shortStatus: "Workspace mismatch",
      verification: "mismatch",
      tone: "warning",
      issue: "Recorded agent cwd is outside the session workspace.",
    });
    expect(workspaceEvidenceText(workspace)).toBe([
      "Workspace evidence",
      "Status: Workspace mismatch",
      "Issue: Recorded agent cwd is outside the session workspace.",
      "Label: affent",
      "Workspace path: /repo/affent",
      "Last agent cwd: /tmp",
    ].join("\n"));
    expect(workspaceDraft(workspace)).toContain("Verify this workspace mismatch before making more file changes or running commands");
    expect(workspaceReviewFacts(workspace)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Agent cwd", value: "Outside", tone: "danger" }),
    ]));
    expect(workspaceVerifyRequest(workspace)).toEqual({
      command: "pwd; git status --short --branch 2>/dev/null || true",
      cwd: "/repo/affent",
    });
    expect(workspaceVerifyDraft(workspace)).toContain("Working directory: /repo/affent");
    expect(workspaceCwdBrowserPath(workspace)).toBeUndefined();
  });

  it("marks historical command cwd evidence as missing an active workspace binding", () => {
    const workspace = buildSessionWorkspace(
      session({ last_agent_cwd: "/workspace/sessions/sess_123" }),
      run(),
    );

    expect(workspace).toMatchObject({
      hasData: true,
      summary: "Workspace binding missing",
      shortStatus: "Workspace binding missing",
      detail: "cwd /workspace/sessions/sess_123",
      verification: "missing_binding",
      tone: "warning",
      lastAgentCwd: "/workspace/sessions/sess_123",
    });
    expect(workspaceEvidenceText(workspace)).toContain("Last agent cwd: /workspace/sessions/sess_123");
    expect(workspaceReviewFacts(workspace)).toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Binding", value: "Missing", tone: "attention" }),
      expect.objectContaining({ label: "Agent cwd", value: "Recorded", detail: "historical cwd", tone: "ok" }),
    ]));
    expect(workspaceDraft(workspace)).toContain("Use this historical command cwd as workspace evidence");
    expect(workspaceCwdBrowserPath(workspace)).toBeUndefined();
  });

  it("drafts a workspace location request when no boundary evidence exists", () => {
    const workspace = buildSessionWorkspace(undefined, run());

    expect(workspace).toMatchObject({
      hasData: false,
      verification: "unknown",
      summary: "No workspace evidence",
    });
    expect(workspaceDraft(workspace)).toContain("Identify the current workspace before making file changes or running project commands");
  });

  it("uses the chronologically latest command cwd instead of the prioritized Run list", () => {
    const run = buildSessionRun(reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "old-failed", tool: "shell", args: { command: "npm test", cwd: "/tmp" } } },
      { id: 3, type: "tool.result", data: { call_id: "old-failed", exit_code: 1, result_summary: "failed" } },
      { id: 4, type: "turn.start", data: { turn_id: "t2" } },
      { id: 5, type: "tool.request", data: { turn_id: "t2", call_id: "latest-passed", tool: "shell", args: { command: "npm run build", cwd: "/repo/affent/extras/webui" } } },
      { id: 6, type: "tool.result", data: { call_id: "latest-passed", exit_code: 0, result_summary: "built" } },
    ]));

    expect(run.commands[0]).toMatchObject({ command: "npm test", cwd: "/tmp", status: "failed" });
    expect(run.latestCommandCwd).toBe("/repo/affent/extras/webui");
    expect(buildSessionWorkspace(
      session({ workspace_path: "/repo/affent", workspace_label: "affent" }),
      run,
    )).toMatchObject({
      summary: "affent",
      latestCommandCwd: "/repo/affent/extras/webui",
      issue: undefined,
    });
    expect(workspaceCwdBrowserPath(buildSessionWorkspace(
      session({ workspace_path: "/repo/affent", workspace_label: "affent" }),
      run,
    ))).toBe("extras/webui");
  });

  it("keeps recorded agent cwd while validating against the latest command cwd", () => {
    const workspace = buildSessionWorkspace(
      session({ workspace_path: "/repo/affent", workspace_label: "affent", last_agent_cwd: "/repo/affent" }),
      run({ cwd: "/tmp/outside" }),
    );

    expect(workspace).toMatchObject({
      summary: "Workspace mismatch",
      verification: "mismatch",
      lastAgentCwd: "/repo/affent",
      latestCommandCwd: "/tmp/outside",
      issue: "Latest command cwd is outside the session workspace.",
    });
    expect(workspaceEvidenceText(workspace)).toBe([
      "Workspace evidence",
      "Status: Workspace mismatch",
      "Issue: Latest command cwd is outside the session workspace.",
      "Label: affent",
      "Workspace path: /repo/affent",
      "Last agent cwd: /repo/affent",
      "Latest command cwd: /tmp/outside",
    ].join("\n"));
    expect(workspaceCwdBrowserPath(workspace)).toBeUndefined();
  });

  it("stays absent when no workspace evidence exists", () => {
    expect(buildSessionWorkspace(session({}), run())).toMatchObject({
      hasData: false,
      summary: "No workspace evidence",
      shortStatus: "No workspace evidence",
      verification: "unknown",
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
