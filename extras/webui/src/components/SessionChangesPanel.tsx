import { useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { changedFileDiffText, changedFileDraft, type SessionChangedFile, type SessionChangesView } from "../view/sessionChanges";
import { CopyButton } from "./CopyButton";

export function SessionChangesPanel({
  changes,
  defaultOpen = false,
  onOpenArtifact,
  onUseAsDraft,
}: {
  changes: SessionChangesView;
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const trimmedQuery = query.trim();
  const visibleFiles = trimmedQuery ? changes.files.filter((file) => changeMatchesQuery(file, trimmedQuery)) : changes.files;
  return (
    <details className="session-skills-panel session-changes-panel" data-testid="session-changes-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Changes</span>
        <strong>{changes.summary}</strong>
        <span>{changes.detail}</span>
      </summary>
      <div className="session-skills-body">
        {changes.files.length > 1 ? (
          <div className="session-skills-controls">
            <label className="session-skills-search">
              <span>Search changes</span>
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="path, diff, or summary" />
            </label>
            {trimmedQuery ? (
              <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                Clear
              </button>
            ) : null}
          </div>
        ) : null}
        {visibleFiles.length > 0 ? (
          <ol className="session-changes-list" data-testid="session-changes-list">
            {visibleFiles.map((file) => (
              <li key={file.path} className="session-changes-item" data-status={file.status}>
                <div className="session-changes-main">
                  <strong title={file.path}>{file.path}</strong>
                  <span>{changeMeta(file)}</span>
                  {file.detail ? <small>{file.detail}</small> : null}
                  {file.artifactPath ? <small>Evidence artifact: {file.artifactPath}</small> : null}
                </div>
                <span className="session-evidence-actions">
                  <CopyButton label="Copy path" value={file.path} className="ghost-action" />
                  {file.diffPreview && file.diffPreview.length > 0 ? (
                    <CopyButton label="Copy diff" value={changedFileDiffText(file)} className="ghost-action" />
                  ) : null}
                  {file.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(file.artifactPath ?? "")}>
                      Open evidence
                    </button>
                  ) : null}
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(changedFileDraft(file), "changed_file")}>
                      Adjust
                    </button>
                  ) : null}
                </span>
                {file.diffPreview && file.diffPreview.length > 0 ? (
                  <pre className="session-change-diff" data-testid="session-change-diff" aria-label={`Diff preview for ${file.path}`}>
                    {file.diffPreview.map((line, index) => (
                      <span key={`${index}:${line.text}`} data-kind={line.kind}>{line.text}</span>
                    ))}
                    {file.diffTruncated ? <span data-kind="meta">Diff preview truncated</span> : null}
                  </pre>
                ) : null}
              </li>
            ))}
          </ol>
        ) : changes.files.length > 0 ? (
          <div className="session-skills-empty">No changed files matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-skills-empty">No write or edit actions in this chat.</div>
        )}
      </div>
    </details>
  );
}

function changeMatchesQuery(file: SessionChangedFile, query: string): boolean {
  const haystack = [
    file.path,
    file.operation,
    file.status,
    file.detail,
    file.artifactPath,
    ...(file.diffPreview?.map((line) => line.text) ?? []),
  ].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
}

function changeMeta(file: SessionChangedFile): string {
  const parts = [
    file.operation === "write" ? "Write" : "Edit",
    statusLabel(file.status),
    file.additions != null || file.deletions != null ? `+${file.additions ?? 0} -${file.deletions ?? 0}` : undefined,
    `turn ${file.turnNumber}`,
    file.actionCount > 1 ? `${file.actionCount} actions` : undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function statusLabel(status: SessionChangedFile["status"]): string {
  if (status === "running") return "pending";
  if (status === "failed") return "failed";
  return "changed";
}
