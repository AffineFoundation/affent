import { useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import {
  fileContentText,
  fileLines,
  fileRangeDraft,
  type SessionFileEvidence,
  type SessionFilesView,
} from "../view/sessionFiles";
import { CopyButton } from "./CopyButton";
import { HighlightText } from "./HighlightText";

type FileFilter = "all" | "changed" | "snapshots" | "issues" | "listed";

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
  const [previewQuery, setPreviewQuery] = useState("");
  const [selectedPath, setSelectedPath] = useState<string | undefined>();
  const [selectedRange, setSelectedRange] = useState<{ path: string; start: number; end: number } | undefined>();
  const [filter, setFilter] = useState<FileFilter>("all");
  const trimmedQuery = query.trim();
  const stats = fileStats(files);
  const filteredItems = filter === "all" ? files.items : files.items.filter((item) => fileMatchesFilter(item, filter));
  const visibleItems = trimmedQuery ? filteredItems.filter((item) => fileMatchesQuery(item, trimmedQuery)) : filteredItems;
  const snapshotItems = visibleItems.filter((item) => item.contentPreview);
  const selectedItem = snapshotItems.find((item) => item.path === selectedPath) ?? snapshotItems[0];
  const snapshotLines = selectedItem ? fileLines(selectedItem) : [];
  const activeRange = selectedItem && selectedRange?.path === selectedItem.path ? selectedRange : undefined;
  const focus = filesFocus(files.items);
  function selectPreviewLine(lineNumber: number) {
    if (!selectedItem) return;
    setSelectedRange((current) => {
      if (!current || current.path !== selectedItem.path || current.start !== current.end) {
        return { path: selectedItem.path, start: lineNumber, end: lineNumber };
      }
      return {
        path: selectedItem.path,
        start: Math.min(current.start, lineNumber),
        end: Math.max(current.end, lineNumber),
      };
    });
  }
  return (
    <details className="session-skills-panel session-files-panel" data-testid="session-files-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Files</span>
        <strong>{files.summary}</strong>
        <span>{files.detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="session-files-overview" aria-label="File work summary">
          <div className="session-files-overview-main">
            <span>Evidence</span>
            <strong>{files.summary}</strong>
            <small>{files.detail || "No file actions recorded."}</small>
          </div>
          <div className="session-files-filterbar" role="group" aria-label="File filters">
            <FileFilterButton label="All" value={stats.total} active={filter === "all"} onClick={() => setFilter("all")} />
            <FileFilterButton label="Changed" value={stats.changed} active={filter === "changed"} onClick={() => setFilter("changed")} />
            <FileFilterButton label="Snapshot" value={stats.snapshots} active={filter === "snapshots"} onClick={() => setFilter("snapshots")} />
            <FileFilterButton label="Issues" value={stats.failed + stats.running} active={filter === "issues"} onClick={() => setFilter("issues")} />
            <FileFilterButton label="Dirs" value={stats.listed} active={filter === "listed"} onClick={() => setFilter("listed")} />
          </div>
        </div>
        {focus ? (
          <div className="session-files-focus" data-tone={focus.tone}>
            <div className="session-files-focus-main">
              <span>{focus.label}</span>
              <strong title={focus.item.path}>{displayPath(focus.item.path)}</strong>
              <small>{focus.detail}</small>
            </div>
            {focus.item.contentPreview || (focus.item.artifactPath && onOpenArtifact) ? (
              <span className="session-files-focus-actions">
                {focus.item.contentPreview ? (
                  <button type="button" className="ghost-action" onClick={() => setSelectedPath(focus.item.path)}>
                    View snapshot
                  </button>
                ) : null}
                {focus.item.artifactPath && onOpenArtifact ? (
                  <button type="button" className="ghost-action" onClick={() => onOpenArtifact(focus.item.artifactPath ?? "")}>
                    Open evidence
                  </button>
                ) : null}
              </span>
            ) : null}
          </div>
        ) : null}
        {selectedItem ? (
          <div className="session-file-preview" data-testid="session-file-preview">
            <div className="session-file-preview-head">
              <div>
                <span>File snapshot</span>
                <strong title={selectedItem.path}>{selectedItem.path}</strong>
              </div>
              <small>{selectedItem.contentTruncated ? "partial read_file output" : "read_file output"}</small>
            </div>
            <div className="session-file-preview-toolbar">
              <label className="session-skills-search">
                <span>Search snapshot</span>
                <input
                  aria-label="Search file snapshot"
                  value={previewQuery}
                  onChange={(event) => setPreviewQuery(event.target.value)}
                  placeholder="text in loaded file"
                />
              </label>
              <CopyButton label="Copy snapshot" value={fileContentText(selectedItem)} className="ghost-action" />
            </div>
            {activeRange && onUseAsDraft ? (
              <div className="session-file-range-actions" data-testid="session-file-range-actions">
                <span>
                  Lines {activeRange.start}-{activeRange.end}
                </span>
                <button
                  type="button"
                  className="ghost-action"
                  onClick={() => onUseAsDraft(fileRangeDraft(selectedItem, activeRange.start, activeRange.end, "ask"), "file_range")}
                >
                  Ask about range
                </button>
                <button
                  type="button"
                  className="ghost-action"
                  onClick={() => onUseAsDraft(fileRangeDraft(selectedItem, activeRange.start, activeRange.end, "edit"), "file_range")}
                >
                  Edit range
                </button>
              </div>
            ) : null}
            <div className="code session-file-preview-code" data-testid="session-file-preview-content" role="list" aria-label="Loaded file snapshot">
              {snapshotLines.map((line, index) => {
                const lineNumber = index + 1;
                const selected = activeRange ? lineNumber >= activeRange.start && lineNumber <= activeRange.end : false;
                return (
                  <button
                    key={lineNumber}
                    type="button"
                    className="session-file-code-line"
                    data-selected={selected ? "true" : "false"}
                    onClick={() => selectPreviewLine(lineNumber)}
                  >
                    <span className="session-file-code-line-number">{lineNumber}</span>
                    <span className="session-file-code-line-text">
                      <HighlightText text={line || " "} query={previewQuery} />
                    </span>
                  </button>
                );
              })}
            </div>
          </div>
        ) : files.items.some((item) => item.contentPreview) && visibleItems.length > 0 ? (
          <div className="session-skills-empty">No loaded file snapshot in the visible results.</div>
        ) : null}
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
                  <strong title={item.path}>{displayPath(item.path)}</strong>
                  <span>{fileMeta(item)}</span>
                  {displayPath(item.path) !== item.path ? <small title={item.path}>{item.path}</small> : null}
                  {item.detail ? <small>{item.detail}</small> : null}
                  {item.next ? <small>Next: {item.next}</small> : null}
                  {item.artifactPath ? <small>Evidence: {artifactLabel(item.artifactPath)}</small> : null}
                  {item.contentPreview ? (
                    <small>{item.contentTruncated ? "Partial read_file snapshot available" : "read_file snapshot available"}</small>
                  ) : null}
                </div>
                <span className="session-files-actions">
                  {item.contentPreview ? (
                    <button
                      type="button"
                      className="ghost-action"
                      aria-pressed={selectedItem?.path === item.path}
                      onClick={() => setSelectedPath(item.path)}
                    >
                      View snapshot
                    </button>
                  ) : null}
                  {item.artifactPath && onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(item.artifactPath ?? "")}>
                      Open evidence
                    </button>
                  ) : null}
                  <CopyButton label="Copy path" value={item.path} className="ghost-action" />
                </span>
              </li>
            ))}
          </ol>
        ) : files.items.length > 0 ? (
          <div className="session-skills-empty">No {filter === "all" ? "file evidence" : filter} result matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-skills-empty">No read, list, write, or edit actions in this chat.</div>
        )}
      </div>
    </details>
  );
}

function FileFilterButton({
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
  if (value === 0 && !active) return null;
  return (
    <button type="button" className="session-files-filter" data-active={active ? "true" : "false"} onClick={onClick}>
      <span>{label}</span>
      <strong>{value}</strong>
    </button>
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

function fileMatchesFilter(item: SessionFileEvidence, filter: FileFilter): boolean {
  if (filter === "changed") return item.actions.includes("changed");
  if (filter === "snapshots") return Boolean(item.contentPreview);
  if (filter === "issues") return item.status === "failed" || item.status === "running";
  if (filter === "listed") return item.actions.includes("listed");
  return true;
}

function fileStats(files: SessionFilesView) {
  return files.stats ?? {
    total: files.items.length,
    available: files.items.filter((item) => item.status === "available").length,
    failed: files.items.filter((item) => item.status === "failed").length,
    running: files.items.filter((item) => item.status === "running").length,
    read: files.items.filter((item) => item.actions.includes("read")).length,
    listed: files.items.filter((item) => item.actions.includes("listed")).length,
    changed: files.items.filter((item) => item.actions.includes("changed")).length,
    snapshots: files.items.filter((item) => item.contentPreview).length,
  };
}

function filesFocus(items: readonly SessionFileEvidence[]):
  | { label: string; detail: string; tone: "error" | "warning" | "changed" | "snapshot"; item: SessionFileEvidence }
  | undefined {
  const failed = items.find((item) => item.status === "failed");
  if (failed) {
    return {
      label: "Path issue",
      detail: failed.next ? `Suggested recovery: ${failed.next}` : failed.detail ?? "A file action failed and needs path recovery.",
      tone: "error",
      item: failed,
    };
  }
  const running = items.find((item) => item.status === "running");
  if (running) return { label: "Pending file action", detail: running.detail ?? "A file action is still running.", tone: "warning", item: running };
  const changed = items.find((item) => item.actions.includes("changed"));
  if (changed) return { label: "Changed file", detail: changed.detail ?? "Agent wrote or edited this file.", tone: "changed", item: changed };
  const snapshot = items.find((item) => item.contentPreview);
  if (snapshot) return { label: "Loaded snapshot", detail: "read_file text is available for review.", tone: "snapshot", item: snapshot };
  return undefined;
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

function displayPath(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const parts = normalized.split("/").filter(Boolean);
  if (path.length <= 48) return path;
  if (parts.length >= 2) {
    const file = parts.at(-1) ?? path;
    const parent = shortenPathSegment(parts.at(-2) ?? "");
    return parent ? `.../${parent}/${file}` : `.../${file}`;
  }
  if (parts.length <= 3) return path;
  return parts.slice(-3).join("/");
}

function shortenPathSegment(segment: string): string {
  if (segment.length <= 22) return segment;
  if (segment.startsWith("sess_")) return `${segment.slice(0, 13)}...${segment.slice(-6)}`;
  return `${segment.slice(0, 10)}...${segment.slice(-6)}`;
}

function artifactLabel(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  return normalized.split("/").filter(Boolean).at(-1) ?? path;
}
