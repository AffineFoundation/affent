import type { UseAsDraft } from "../view/draftSource";
import type { SessionFileEvidence, SessionFilesView } from "../view/sessionFiles";

export function SessionFilesPanel({
  files,
  defaultOpen = false,
  onOpenArtifact,
  onUseAsDraft,
}: {
  files: SessionFilesView;
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <details className="session-skills-panel session-files-panel" data-testid="session-files-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Files</span>
        <strong>{files.summary}</strong>
        <span>{files.detail}</span>
      </summary>
      <div className="session-skills-body">
        {files.items.length > 0 ? (
          <ol className="session-files-list" data-testid="session-files-list">
            {files.items.map((item) => (
              <li key={item.path} className="session-files-item" data-status={item.status}>
                <div className="session-files-main">
                  <strong title={item.path}>{item.path}</strong>
                  <span>{fileMeta(item)}</span>
                  {item.detail ? <small>{item.detail}</small> : null}
                  {item.next ? <small>Next: {item.next}</small> : null}
                  {item.artifactPath ? <small>Evidence artifact: {item.artifactPath}</small> : null}
                </div>
                <span className="session-files-actions">
                  {item.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(item.artifactPath ?? "")}>
                      Open preview
                    </button>
                  ) : null}
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(fileDraft(item), "file_evidence")}>
                      Use
                    </button>
                  ) : null}
                </span>
              </li>
            ))}
          </ol>
        ) : (
          <div className="session-skills-empty">No read, list, write, or edit actions in this chat.</div>
        )}
      </div>
    </details>
  );
}

function fileMeta(item: SessionFileEvidence): string {
  const parts = [
    actionLabel(item.actions),
    statusLabel(item.status),
    `turn ${item.turnNumber}`,
    item.actionCount > 1 ? `${item.actionCount} actions` : undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function actionLabel(actions: SessionFileEvidence["actions"]): string {
  const labels = actions.map((action) => {
    if (action === "read") return "Read";
    if (action === "listed") return "Listed";
    return "Changed";
  });
  return labels.join(" + ");
}

function statusLabel(status: SessionFileEvidence["status"]): string {
  if (status === "running") return "pending";
  if (status === "failed") return "failed";
  return "available";
}

function fileDraft(item: SessionFileEvidence): string {
  if (item.actions.includes("changed")) return `Review this file in the next step: ${item.path}`;
  if (item.actions.includes("listed")) return `Use this listed directory in the next step: ${item.path}`;
  return `Use this file path in the next step: ${item.path}`;
}
