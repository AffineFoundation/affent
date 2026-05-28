import type { UseAsDraft } from "../view/draftSource";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import { workspaceDraft, workspaceEvidenceText } from "../view/sessionWorkspace";
import { CopyButton } from "./CopyButton";

export function SessionWorkspacePanel({
  workspace,
  defaultOpen = false,
  onUseAsDraft,
}: {
  workspace: SessionWorkspaceView;
  defaultOpen?: boolean;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <details className="session-skills-panel session-workspace-panel" data-testid="session-workspace-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Workspace</span>
        <strong>{workspace.summary}</strong>
        <span>{workspace.detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="session-workspace-card" data-tone={workspace.tone} data-testid="session-workspace-card">
          <div className="session-workspace-main">
            <div className="session-workspace-hero" data-tone={workspace.tone ?? "ok"}>
              <span>{workspace.issue ? "Check cwd" : "Boundary verified"}</span>
              <strong title={workspace.path ?? workspace.label}>{workspace.label ?? displayPath(workspace.path) ?? "Workspace recorded"}</strong>
              <small>{workspace.issue ?? "Commands and file actions are inside the session workspace."}</small>
            </div>
            <div className="session-workspace-fields" aria-label="Workspace fields">
              {workspace.path ? <WorkspaceField label="Workspace" value={displayPath(workspace.path)} title={workspace.path} mono /> : null}
              {workspace.lastAgentCwd ? <WorkspaceField label="Last cwd" value={displayPath(workspace.lastAgentCwd)} title={workspace.lastAgentCwd} mono tone={workspace.issue ? "warning" : undefined} /> : null}
              {workspace.latestCommandCwd && workspace.latestCommandCwd !== workspace.lastAgentCwd ? (
                <WorkspaceField label="Command cwd" value={displayPath(workspace.latestCommandCwd)} title={workspace.latestCommandCwd} mono />
              ) : null}
              {workspace.branch ? <WorkspaceField label="Branch" value={workspace.branch} /> : null}
              {workspace.dirtyState ? <WorkspaceField label="State" value={workspace.dirtyState} /> : null}
            </div>
          </div>
          <span className="session-evidence-actions">
            {workspace.path ? <CopyButton label="Copy path" value={workspace.path} className="ghost-action" /> : null}
            {workspace.lastAgentCwd ? <CopyButton label="Copy cwd" value={workspace.lastAgentCwd} className="ghost-action" /> : null}
            <CopyButton label="Copy workspace evidence" value={workspaceEvidenceText(workspace)} className="ghost-action" />
            {onUseAsDraft ? (
              <button type="button" className="ghost-action" onClick={() => onUseAsDraft(workspaceDraft(workspace), "workspace")}>
                {workspace.issue ? "Resolve as draft" : "Use workspace as draft"}
              </button>
            ) : null}
          </span>
        </div>
      </div>
    </details>
  );
}

function WorkspaceField({
  label,
  value,
  title,
  mono = false,
  tone,
}: {
  label: string;
  value?: string;
  title?: string;
  mono?: boolean;
  tone?: "warning";
}) {
  if (!value) return null;
  return (
    <div className="session-workspace-field" data-tone={tone}>
      <span>{label}</span>
      {mono ? <code title={title ?? value}>{value}</code> : <strong title={title ?? value}>{value}</strong>}
    </div>
  );
}

function displayPath(path: string | undefined): string | undefined {
  if (!path) return undefined;
  const normalized = path.replace(/\\/g, "/");
  const parts = normalized.split("/").filter(Boolean);
  if (normalized.length <= 48) return path;
  if (parts.length >= 2) return `.../${parts.slice(-2).join("/")}`;
  return `...${normalized.slice(-45)}`;
}
