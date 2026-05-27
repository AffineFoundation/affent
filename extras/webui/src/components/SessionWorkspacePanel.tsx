import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import { CopyButton } from "./CopyButton";

export function SessionWorkspacePanel({
  workspace,
  defaultOpen = false,
}: {
  workspace: SessionWorkspaceView;
  defaultOpen?: boolean;
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
            {workspace.issue ? <strong className="session-workspace-issue">{workspace.issue}</strong> : null}
            {workspace.label ? <span>Label: {workspace.label}</span> : null}
            {workspace.path ? <span title={workspace.path}>Path: {workspace.path}</span> : null}
            {workspace.lastAgentCwd ? <span title={workspace.lastAgentCwd}>Last agent cwd: {workspace.lastAgentCwd}</span> : null}
            {workspace.branch ? <span>Branch: {workspace.branch}</span> : null}
            {workspace.dirtyState ? <span>State: {workspace.dirtyState}</span> : null}
          </div>
          <span className="session-evidence-actions">
            {workspace.path ? <CopyButton label="Copy path" value={workspace.path} className="ghost-action" /> : null}
            {workspace.lastAgentCwd ? <CopyButton label="Copy cwd" value={workspace.lastAgentCwd} className="ghost-action" /> : null}
          </span>
        </div>
      </div>
    </details>
  );
}
