import type { SessionSummary } from "../api/sessions";
import type { SessionRunView } from "./sessionRun";

export interface SessionWorkspaceView {
  hasData: boolean;
  summary: string;
  shortStatus: string;
  detail: string;
  tone?: "warning" | "error";
  label?: string;
  path?: string;
  branch?: string;
  dirtyState?: string;
  lastAgentCwd?: string;
  latestCommandCwd?: string;
  issue?: string;
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
    };
  }

  const summary = issue ? "Workspace mismatch" : label ?? "Workspace recorded";
  return {
    hasData: true,
    summary,
    shortStatus: workspaceShortStatus({ summary, label, path, branch, dirtyState }),
    detail: workspaceDetail({ path, branch, dirtyState, lastAgentCwd }),
    tone: issue ? "warning" : undefined,
    label,
    path,
    branch,
    dirtyState,
    lastAgentCwd,
    latestCommandCwd,
    issue,
  };
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

export function workspaceDraft(workspace: SessionWorkspaceView): string {
  const lead = workspace.issue
    ? "Verify this workspace mismatch before making more file changes or running commands:"
    : "Use this workspace boundary for the next step:";
  return [
    lead,
    "",
    workspaceEvidenceText(workspace),
  ].join("\n");
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
