import { useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { fileEvidenceDraft, fileEvidenceText, type SessionFileEvidence, type SessionFilesView } from "../view/sessionFiles";
import { CopyButton } from "./CopyButton";

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
  const [query, setQuery] = useState("");
  const trimmedQuery = query.trim();
  const visibleItems = trimmedQuery ? files.items.filter((item) => fileMatchesQuery(item, trimmedQuery)) : files.items;
  return (
    <details className="session-skills-panel session-files-panel" data-testid="session-files-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Files</span>
        <strong>{files.summary}</strong>
        <span>{files.detail}</span>
      </summary>
      <div className="session-skills-body">
        {files.items.length > 1 ? (
          <div className="session-skills-controls">
            <label className="session-skills-search">
              <span>Search files</span>
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="path, action, or note" />
            </label>
            {trimmedQuery ? (
              <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                Clear
              </button>
            ) : null}
          </div>
        ) : null}
        {visibleItems.length > 0 ? (
          <ol className="session-files-list" data-testid="session-files-list">
            {visibleItems.map((item) => (
              <li key={item.path} className="session-files-item" data-status={item.status}>
                <div className="session-files-main">
                  <strong title={item.path}>{item.path}</strong>
                  <span>{fileMeta(item)}</span>
                  {item.detail ? <small>{item.detail}</small> : null}
                  {item.next ? <small>Next: {item.next}</small> : null}
                  {item.artifactPath ? <small>Evidence artifact: {item.artifactPath}</small> : null}
                </div>
                <span className="session-files-actions">
                  <CopyButton label="Copy path" value={item.path} className="ghost-action" />
                  <CopyButton label="Copy evidence" value={fileEvidenceText(item)} className="ghost-action" />
                  {item.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(item.artifactPath ?? "")}>
                      Open evidence
                    </button>
                  ) : null}
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(fileEvidenceDraft(item), "file_evidence")}>
                      Use file as draft
                    </button>
                  ) : null}
                </span>
              </li>
            ))}
          </ol>
        ) : files.items.length > 0 ? (
          <div className="session-skills-empty">No file evidence matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-skills-empty">No read, list, write, or edit actions in this chat.</div>
        )}
      </div>
    </details>
  );
}

function fileMatchesQuery(item: SessionFileEvidence, query: string): boolean {
  const haystack = [
    item.path,
    item.actions.join(" "),
    item.status,
    item.detail,
    item.next,
    item.artifactPath,
  ].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
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
