import type { SessionWorkspaceView } from "../view/sessionWorkspace";

export function WorkspaceStatusPill({
  workspace,
  onOpen,
}: {
  workspace: SessionWorkspaceView;
  onOpen: () => void;
}) {
  if (!workspace.hasData) return null;
  return (
    <button
      type="button"
      className="workspace-status-pill"
      data-tone={workspace.tone}
      data-testid="workspace-status-pill"
      title={workspace.detail}
      onClick={onOpen}
    >
      <span className="workspace-status-dot" aria-hidden="true" />
      <span>{workspace.shortStatus}</span>
    </button>
  );
}
