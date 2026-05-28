import { useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { changedFileDiffText, changedFileDraft, type SessionChangedFile, type SessionChangesView } from "../view/sessionChanges";
import { CopyButton } from "./CopyButton";

type ChangeFilter = "all" | "changed" | "issues" | "diff";

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
  const [filter, setFilter] = useState<ChangeFilter>("all");
  const trimmedQuery = query.trim();
  const stats = changeStats(changes.files);
  const filteredFiles = filter === "all" ? changes.files : changes.files.filter((file) => changeMatchesFilter(file, filter));
  const visibleFiles = trimmedQuery ? filteredFiles.filter((file) => changeMatchesQuery(file, trimmedQuery)) : filteredFiles;
  const focusFile = visibleFiles.find((file) => file.status === "failed")
    ?? visibleFiles.find((file) => file.status === "running")
    ?? visibleFiles.find((file) => file.diffPreview && file.diffPreview.length > 0)
    ?? visibleFiles[0];
  const showChangeList = visibleFiles.length > 1 || !!trimmedQuery || filter !== "all";
  return (
    <details className="session-skills-panel session-changes-panel" data-testid="session-changes-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Changes</span>
        <strong>{changes.summary}</strong>
        <span>{changes.detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="session-changes-overview" aria-label="Changes summary">
          <div className="session-changes-overview-main">
            <span>Review</span>
            <strong>{changes.summary}</strong>
            <small>{changes.detail || "No write or edit actions recorded."}</small>
          </div>
          <div className="session-changes-filterbar" role="group" aria-label="Change filters">
            {changeFilterItems(stats).map((item) => (
              <ChangeFilterButton
                key={item.filter}
                label={item.label}
                value={item.value}
                active={filter === item.filter}
                onClick={() => setFilter(item.filter)}
              />
            ))}
          </div>
        </div>
        {focusFile ? (
          <section className="session-changes-focus" data-status={focusFile.status} data-testid="session-changes-focus" aria-label="Change focus">
            <div className="session-changes-focus-main">
              <span>{changeFocusLabel(focusFile)}</span>
              <strong title={focusFile.path}>{displayPath(focusFile.path)}</strong>
              <small>{changeMeta(focusFile)}</small>
              {focusFile.detail ? <p>{focusFile.detail}</p> : null}
              <small className="session-changes-evidence-state" data-state={changeEvidenceState(focusFile).state}>
                {changeEvidenceState(focusFile).label}
              </small>
              {focusFile.artifactPath ? <small title={focusFile.artifactPath}>Evidence: {artifactLabel(focusFile.artifactPath)}</small> : null}
            </div>
            <span className="session-evidence-actions">
              <CopyButton label="Copy path" value={focusFile.path} className="ghost-action" />
              {focusFile.diffPreview && focusFile.diffPreview.length > 0 ? (
                <CopyButton label="Copy diff" value={changedFileDiffText(focusFile)} className="ghost-action" />
              ) : null}
              {focusFile.artifactPath && onOpenArtifact ? (
                <button type="button" className="ghost-action" onClick={() => onOpenArtifact(focusFile.artifactPath ?? "")}>
                  Open evidence
                </button>
              ) : null}
              {onUseAsDraft ? (
                <button type="button" className="ghost-action primary-run-action" onClick={() => onUseAsDraft(changedFileDraft(focusFile), "changed_file")}>
                  {changeDraftActionLabel(focusFile)}
                </button>
              ) : null}
            </span>
          </section>
        ) : null}
        {focusFile && !showChangeList && focusFile.diffPreview && focusFile.diffPreview.length > 0 ? (
          <ChangeDiff file={focusFile} />
        ) : null}
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
        {showChangeList && visibleFiles.length > 0 ? (
          <ol className="session-changes-list" data-testid="session-changes-list">
            {visibleFiles.map((file) => (
              <li key={file.path} className="session-changes-item" data-status={file.status}>
                <div className="session-changes-main">
                  <strong title={file.path}>{displayPath(file.path)}</strong>
                  <span>{changeMeta(file)}</span>
                  {file.detail ? <small>{file.detail}</small> : null}
                  <small className="session-changes-evidence-state" data-state={changeEvidenceState(file).state}>
                    {changeEvidenceState(file).label}
                  </small>
                  {file.artifactPath ? <small title={file.artifactPath}>Evidence: {artifactLabel(file.artifactPath)}</small> : null}
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
                      {changeDraftActionLabel(file)}
                    </button>
                  ) : null}
                </span>
                {file.diffPreview && file.diffPreview.length > 0 ? <ChangeDiff file={file} /> : null}
              </li>
            ))}
          </ol>
        ) : changes.files.length > 0 && visibleFiles.length === 0 ? (
          <div className="session-skills-empty">No {filter === "all" ? "changed files" : filter} matching "{trimmedQuery}".</div>
        ) : changes.files.length === 0 ? (
          <div className="session-skills-empty">No write or edit actions in this chat.</div>
        ) : null}
      </div>
    </details>
  );
}

function ChangeDiff({ file }: { file: SessionChangedFile }) {
  return (
    <pre className="session-change-diff" data-testid="session-change-diff" aria-label={`Diff preview for ${file.path}`}>
      {file.diffPreview?.map((line, index) => (
        <span key={`${index}:${line.text}`} data-kind={line.kind}>{line.text}</span>
      ))}
      {file.diffTruncated ? <span data-kind="meta">Diff preview truncated</span> : null}
    </pre>
  );
}

function ChangeFilterButton({
  label,
  value,
  active,
  onClick,
}: {
  label: string;
  value: number;
  active: boolean;
  onClick: () => void;
}) {
  return (
    <button type="button" className="session-changes-filter" data-active={active ? "true" : "false"} onClick={onClick}>
      <span>{label}</span>
      <strong>{value}</strong>
    </button>
  );
}

function changeStats(files: readonly SessionChangedFile[]) {
  return {
    total: files.length,
    changed: files.filter((file) => file.status === "changed").length,
    issues: files.filter((file) => file.status === "failed" || file.status === "running").length,
    diff: files.filter((file) => file.diffPreview && file.diffPreview.length > 0).length,
  };
}

function changeFilterItems(stats: ReturnType<typeof changeStats>): Array<{ filter: ChangeFilter; label: string; value: number }> {
  return [
    { filter: "all", label: "All", value: stats.total },
    stats.changed > 0 ? { filter: "changed", label: "Changed", value: stats.changed } : undefined,
    stats.issues > 0 ? { filter: "issues", label: "Issues", value: stats.issues } : undefined,
    stats.diff > 0 ? { filter: "diff", label: "Diff", value: stats.diff } : undefined,
  ].filter((item): item is { filter: ChangeFilter; label: string; value: number } => Boolean(item));
}

function changeMatchesFilter(file: SessionChangedFile, filter: ChangeFilter): boolean {
  if (filter === "changed") return file.status === "changed";
  if (filter === "issues") return file.status === "failed" || file.status === "running";
  if (filter === "diff") return !!file.diffPreview && file.diffPreview.length > 0;
  return true;
}

function changeEvidenceState(file: SessionChangedFile): { state: "diff" | "artifact" | "missing"; label: string } {
  if (file.diffPreview && file.diffPreview.length > 0) return { state: "diff", label: "Diff preview captured" };
  if (file.artifactPath) return { state: "artifact", label: "Evidence artifact captured" };
  return { state: "missing", label: "No diff preview captured" };
}

function changeDraftActionLabel(file: SessionChangedFile): string {
  if (file.diffPreview && file.diffPreview.length > 0) return "Revise diff";
  return "Review file";
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

function changeFocusLabel(file: SessionChangedFile): string {
  if (file.status === "failed") return "Fix needed";
  if (file.status === "running") return "Changing now";
  if (file.diffPreview && file.diffPreview.length > 0) return "Diff ready";
  return "Changed file";
}

function displayPath(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const parts = normalized.split("/").filter(Boolean);
  if (path.length > 64 && parts.length >= 2) return `.../${parts.slice(-2).join("/")}`;
  if (parts.length <= 3) return path;
  return parts.slice(-3).join("/");
}

function artifactLabel(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  return normalized.split("/").filter(Boolean).at(-1) ?? path;
}
