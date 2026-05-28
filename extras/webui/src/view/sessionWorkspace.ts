import type { SessionSummary } from "../api/sessions";
import type { RunCommandExecutionRequest, SessionRunView } from "./sessionRun";

export interface SessionWorkspaceView {
  hasData: boolean;
  summary: string;
  shortStatus: string;
  detail: string;
  verification: "verified" | "bound" | "missing_binding" | "mismatch" | "unknown";
  tone?: "warning" | "error";
  label?: string;
  path?: string;
  branch?: string;
  dirtyState?: string;
  lastAgentCwd?: string;
  latestCommandCwd?: string;
  issue?: string;
}

export interface SessionWorkspaceFact {
  label: string;
  value: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export function buildSessionWorkspace(
  session: SessionSummary | undefined,
  run: SessionRunView,
): SessionWorkspaceView {
  const path = clean(session?.workspace_path);
  const label = clean(session?.workspace_label) ?? workspaceLabel(path);
  const branch = clean(session?.default_branch);
  const dirtyState = clean(session?.dirty_state);
  const latestCommandCwd = clean(run.latestCommandCwd) ?? clean(run.commands.find((command) => command.cwd)?.cwd);
  const lastAgentCwd = latestCommandCwd ?? clean(session?.last_agent_cwd);
  const issue = workspaceIssue(path, lastAgentCwd);
  const hasData = !!(path || label || branch || dirtyState || lastAgentCwd || latestCommandCwd);

  if (!hasData) {
    return {
      hasData: false,
      summary: "No workspace evidence",
      shortStatus: "No workspace evidence",
      detail: "No workspace binding or command cwd recorded.",
      verification: "unknown",
    };
  }

  const verification = workspaceVerification(path, lastAgentCwd, issue);
  const summary = workspaceSummary(verification, label);
  return {
    hasData: true,
    summary,
    shortStatus: workspaceShortStatus({ summary, label, path, branch, dirtyState }),
    detail: workspaceDetail({ path, branch, dirtyState, lastAgentCwd }),
    verification,
    tone: issue || verification === "missing_binding" ? "warning" : undefined,
    label,
    path,
    branch,
    dirtyState,
    lastAgentCwd,
    latestCommandCwd,
    issue,
  };
}

export function workspaceReviewFacts(workspace: SessionWorkspaceView): SessionWorkspaceFact[] {
  return [
    {
      label: "Binding",
      value: workspace.path ? "Recorded" : "Missing",
      detail: workspace.path ? "session path" : "no session path",
      tone: workspace.path ? "ok" : workspace.hasData ? "attention" : "neutral",
    },
    {
      label: "Agent cwd",
      value: agentCwdValue(workspace),
      detail: agentCwdDetail(workspace),
      tone: workspace.verification === "mismatch" ? "danger" : workspace.lastAgentCwd ? "ok" : "neutral",
    },
    {
      label: "Branch",
      value: workspace.branch ?? "n/a",
      detail: workspace.branch ? "reported" : "not reported",
      tone: "neutral",
    },
    {
      label: "State",
      value: workspace.dirtyState ?? "n/a",
      detail: workspace.dirtyState ? "git status" : "not reported",
      tone: workspace.dirtyState && !/^clean$/i.test(workspace.dirtyState) ? "attention" : "neutral",
    },
  ];
}

export function workspaceEvidenceText(workspace: SessionWorkspaceView): string {
  const lines = [
    "Workspace evidence",
    `Status: ${workspace.summary}`,
    workspace.issue ? `Issue: ${workspace.issue}` : undefined,
    workspace.label ? `Label: ${workspace.label}` : undefined,
    workspace.path ? `Workspace path: ${workspace.path}` : undefined,
    workspace.lastAgentCwd ? `Last agent cwd: ${workspace.lastAgentCwd}` : undefined,
    workspace.latestCommandCwd && workspace.latestCommandCwd !== workspace.lastAgentCwd ? `Latest command cwd: ${workspace.latestCommandCwd}` : undefined,
    workspace.branch ? `Branch: ${workspace.branch}` : undefined,
    workspace.dirtyState ? `State: ${workspace.dirtyState}` : undefined,
  ];
  return lines.filter((line): line is string => Boolean(line)).join("\n");
}

function agentCwdValue(workspace: SessionWorkspaceView): string {
  if (workspace.verification === "mismatch") return "Outside";
  if (workspace.lastAgentCwd) return workspace.path ? "Inside" : "Recorded";
  return "Missing";
}

function agentCwdDetail(workspace: SessionWorkspaceView): string {
  if (workspace.verification === "mismatch") return "outside session";
  if (workspace.lastAgentCwd && workspace.path) return "inside session";
  if (workspace.lastAgentCwd) return "historical cwd";
  return "no shell cwd";
}

export function workspaceDraft(workspace: SessionWorkspaceView): string {
  const lead = workspace.verification === "mismatch"
    ? "Verify this workspace mismatch before making more file changes or running commands:"
    : workspace.verification === "missing_binding"
      ? "Use this historical command cwd as workspace evidence for the next step:"
    : "Use this workspace boundary for the next step:";
  return [
    lead,
    "",
    workspaceEvidenceText(workspace),
  ].join("\n");
}

export function workspaceVerifyRequest(workspace: SessionWorkspaceView): RunCommandExecutionRequest {
  return {
    command: "pwd; git status --short --branch 2>/dev/null || true",
    cwd: workspace.path ?? workspace.lastAgentCwd,
  };
}

export function workspaceVerifyDraft(workspace: SessionWorkspaceView): string {
  const request = workspaceVerifyRequest(workspace);
  return [
    "Verify the current workspace boundary, then report pwd, git branch/state, and whether commands are running in the expected directory:",
    request.command,
    request.cwd ? `Working directory: ${request.cwd}` : undefined,
    "",
    workspaceEvidenceText(workspace),
  ].filter((line): line is string => Boolean(line)).join("\n");
}

function workspaceShortStatus({
  summary,
  label,
  path,
  branch,
  dirtyState,
}: {
  summary: string;
  label?: string;
  path?: string;
  branch?: string;
  dirtyState?: string;
}): string {
  if (summary === "Workspace mismatch") return summary;
  return [
    label ?? (path ? compactPath(path) : summary),
    branch,
    dirtyState,
  ].filter(Boolean).join(" · ");
}

function workspaceDetail({
  path,
  branch,
  dirtyState,
  lastAgentCwd,
}: {
  path?: string;
  branch?: string;
  dirtyState?: string;
  lastAgentCwd?: string;
}): string {
  return [
    path ? compactPath(path) : undefined,
    branch ? `branch ${branch}` : undefined,
    dirtyState,
    lastAgentCwd ? `cwd ${compactPath(lastAgentCwd)}` : undefined,
  ].filter(Boolean).join(" · ");
}

function workspaceIssue(path?: string, cwd?: string): string | undefined {
  if (!path || !cwd || !isAbsolutePath(path) || !isAbsolutePath(cwd)) return undefined;
  if (cwd === path || cwd.startsWith(`${path.replace(/\/+$/, "")}/`)) return undefined;
  return "Latest command cwd is outside the session workspace.";
}

function workspaceVerification(
  path?: string,
  cwd?: string,
  issue?: string,
): SessionWorkspaceView["verification"] {
  if (issue === "Latest command cwd is outside the session workspace.") return "mismatch";
  if (!path && cwd) return "missing_binding";
  if (path && cwd) return "verified";
  if (path) return "bound";
  return "unknown";
}

function workspaceSummary(verification: SessionWorkspaceView["verification"], label?: string): string {
  if (verification === "mismatch") return "Workspace mismatch";
  if (verification === "missing_binding") return "Workspace binding missing";
  if (verification === "bound") return label ? `${label} bound` : "Workspace bound";
  return label ?? "Workspace recorded";
}

function workspaceLabel(path?: string): string | undefined {
  if (!path) return undefined;
  const parts = path.split(/[\\/]+/).filter(Boolean);
  return parts.at(-1) ?? path;
}

function compactPath(path: string): string {
  if (path.length <= 72) return path;
  return `...${path.slice(-69)}`;
}

function clean(value: unknown): string | undefined {
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function isAbsolutePath(path: string): boolean {
  return path.startsWith("/") || /^[A-Za-z]:[\\/]/.test(path);
}
