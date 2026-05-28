import type { UseAsDraft } from "../view/draftSource";
import type { RunCommandExecutionRequest } from "../view/sessionRun";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import { workspaceDraft, workspaceEvidenceText, workspaceVerifyDraft, workspaceVerifyRequest } from "../view/sessionWorkspace";
import { CopyButton } from "./CopyButton";

type WorkspaceVerifyAction = (request: RunCommandExecutionRequest) => Promise<void> | void;

export function SessionWorkspacePanel({
  workspace,
  defaultOpen = false,
  onVerifyWorkspace,
  onUseAsDraft,
}: {
  workspace: SessionWorkspaceView;
  defaultOpen?: boolean;
  onVerifyWorkspace?: WorkspaceVerifyAction;
  onUseAsDraft?: UseAsDraft;
}) {
  const canVerify = workspace.hasData && (onVerifyWorkspace || onUseAsDraft);
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
              <span>{verificationLabel(workspace.verification)}</span>
              <strong title={workspace.path ?? workspace.lastAgentCwd ?? workspace.label}>{workspace.label ?? workspaceNameFromPath(workspace.path ?? workspace.lastAgentCwd) ?? "Workspace evidence"}</strong>
              <small>{workspace.issue ?? verificationDetail(workspace.verification)}</small>
            </div>
            <div className="session-workspace-boundary" data-testid="session-workspace-boundary">
              <BoundaryField
                label="Session workspace"
                value={workspace.path}
                fallback={workspace.path ? undefined : "Not recorded"}
                tone={workspace.verification === "mismatch" ? "warning" : undefined}
              />
              <BoundaryField
                label="Latest command cwd"
                value={workspace.lastAgentCwd}
                fallback={workspace.lastAgentCwd ? undefined : "No shell cwd recorded"}
                tone={workspace.issue ? "warning" : undefined}
              />
            </div>
            <div className="session-workspace-fields" aria-label="Workspace fields">
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
            {canVerify ? (
              <button
                type="button"
                className={onVerifyWorkspace ? "ghost-action primary-run-action" : "ghost-action"}
                onClick={() => {
                  if (onVerifyWorkspace) {
                    void onVerifyWorkspace(workspaceVerifyRequest(workspace));
                    return;
                  }
                  onUseAsDraft?.(workspaceVerifyDraft(workspace), "run_command");
                }}
              >
                {onVerifyWorkspace ? "Verify workspace" : "Draft verification"}
              </button>
            ) : null}
            {onUseAsDraft ? (
              <button type="button" className="ghost-action" onClick={() => onUseAsDraft(workspaceDraft(workspace), "workspace")}>
                {workspaceActionLabel(workspace)}
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

function BoundaryField({
  label,
  value,
  fallback,
  tone,
}: {
  label: string;
  value?: string;
  fallback?: string;
  tone?: "warning";
}) {
  return (
    <div className="session-workspace-boundary-field" data-tone={tone}>
      <span>{label}</span>
      {value ? <code title={value}>{value}</code> : <strong>{fallback ?? "Not recorded"}</strong>}
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

function workspaceNameFromPath(path: string | undefined): string | undefined {
  if (!path) return undefined;
  const normalized = path.replace(/\\/g, "/");
  const parts = normalized.split("/").filter(Boolean);
  return parts.at(-1) ?? path;
}

function verificationLabel(verification: SessionWorkspaceView["verification"]): string {
  if (verification === "mismatch") return "Check cwd";
  if (verification === "missing_binding") return "Binding missing";
  if (verification === "bound") return "Workspace bound";
  if (verification === "verified") return "Boundary verified";
  return "Evidence missing";
}

function verificationDetail(verification: SessionWorkspaceView["verification"]): string {
  if (verification === "missing_binding") return "This history has command cwd evidence, but no active session workspace path.";
  if (verification === "bound") return "Session workspace is recorded; no shell cwd has been observed yet.";
  if (verification === "verified") return "Latest command cwd is inside the session workspace.";
  return "No workspace binding or command cwd has been recorded.";
}

function workspaceActionLabel(workspace: SessionWorkspaceView): string {
  if (workspace.verification === "mismatch") return "Ask to verify";
  if (workspace.verification === "missing_binding") return "Use cwd in chat";
  return "Use workspace in chat";
}
