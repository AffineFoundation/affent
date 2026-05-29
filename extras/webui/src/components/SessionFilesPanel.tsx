import { useRef, useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import {
  fileContentText,
  fileEvidenceDraft,
  fileLines,
  fileRangeDraft,
  fileRangeText,
  filesReviewFacts,
  filesReviewFocus,
  filesReviewQueue,
  type SessionFileEvidence,
  type SessionFilesReviewItem,
  type SessionFilesView,
} from "../view/sessionFiles";
import { parentWorkspacePath, workspaceFileDraft, type WorkspaceFileBrowserState, type WorkspaceFileEntryView, type WorkspaceFileView } from "../view/workspaceFile";
import { CopyButton } from "./CopyButton";
import { HighlightText } from "./HighlightText";

type FileFilter = "all" | "changed" | "snapshots" | "issues" | "listed";

export function SessionFilesPanel({
  files,
  workspaceBrowser,
  defaultOpen = false,
  onOpenWorkspacePath,
  onOpenArtifact,
  onUseAsDraft,
}: {
  files: SessionFilesView;
  workspaceBrowser?: WorkspaceFileBrowserState;
  defaultOpen?: boolean;
  onOpenWorkspacePath?: (path: string) => void;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [previewQuery, setPreviewQuery] = useState("");
  const [lineJump, setLineJump] = useState("");
  const [workspaceQuery, setWorkspaceQuery] = useState("");
  const [selectedPath, setSelectedPath] = useState<string | undefined>();
  const [selectedRange, setSelectedRange] = useState<{ path: string; start: number; end: number } | undefined>();
  const [filter, setFilter] = useState<FileFilter>("all");
  const [wrapLines, setWrapLines] = useState(true);
  const trimmedQuery = query.trim();
  const stats = fileStats(files);
  const review = filesReviewFocus(files.items);
  const reviewFacts = filesReviewFacts(files.items);
  const reviewQueue = filesReviewQueue(files.items);
  const filteredItems = filter === "all" ? files.items : files.items.filter((item) => fileMatchesFilter(item, filter));
  const visibleItems = trimmedQuery ? filteredItems.filter((item) => fileMatchesQuery(item, trimmedQuery)) : filteredItems;
  const snapshotItems = visibleItems.filter((item) => item.contentPreview);
  const selectedItem = snapshotItems.find((item) => item.path === selectedPath) ?? snapshotItems[0];
  const snapshotLines = selectedItem ? fileLines(selectedItem) : [];
  const activeRange = selectedItem && selectedRange?.path === selectedItem.path ? selectedRange : undefined;
  const previewCodeRef = useRef<HTMLDivElement | null>(null);
  function handleReviewQueueClick(entry: SessionFilesReviewItem) {
    if (entry.action === "view_snapshot" && entry.item.contentPreview) {
      setSelectedPath(entry.item.path);
      return;
    }
    if (entry.action === "open_current" || entry.action === "recover_path") {
      if (onOpenWorkspacePath) {
        onOpenWorkspacePath(entry.item.path);
        return;
      }
      if (entry.item.contentPreview) setSelectedPath(entry.item.path);
      return;
    }
    if (entry.action === "wait") {
      if (entry.item.contentPreview) setSelectedPath(entry.item.path);
      else onOpenWorkspacePath?.(entry.item.path);
    }
  }
  function selectPreviewLine(lineNumber: number, scroll = false) {
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
    if (scroll) {
      window.requestAnimationFrame(() => {
        const target = previewCodeRef.current?.querySelector<HTMLElement>(`[data-line-number="${lineNumber}"]`);
        target?.scrollIntoView?.({ block: "center" });
      });
    }
  }
  function jumpToPreviewLine() {
    if (!selectedItem || snapshotLines.length === 0) return;
    const lineNumber = Number.parseInt(lineJump, 10);
    if (!Number.isFinite(lineNumber)) return;
    selectPreviewLine(Math.max(1, Math.min(snapshotLines.length, lineNumber)), true);
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
          <div className="session-files-review" data-tone={review.tone ?? "neutral"} data-testid="session-files-review">
            <span>{review.label}</span>
            <strong title={review.title}>{displayPath(review.title)}</strong>
            <small>{review.detail}</small>
          </div>
          <div className="session-files-facts" aria-label="File review facts">
            {reviewFacts.map((fact) => (
              <span key={fact.label} data-tone={fact.tone ?? "neutral"}>
                <small>{fact.label}</small>
                <strong>{fact.value}</strong>
                <b>{fact.detail}</b>
              </span>
            ))}
          </div>
          {reviewQueue.length > 0 ? (
            <div className="session-files-review-queue" data-testid="session-files-review-queue" aria-label="File review queue">
              <span>Review queue</span>
              {reviewQueue.slice(0, 5).map((item) => (
                <button
                  key={item.id}
                  type="button"
                  data-tone={item.tone ?? "neutral"}
                  disabled={reviewQueueItemDisabled(item, Boolean(onOpenWorkspacePath))}
                  onClick={() => handleReviewQueueClick(item)}
                >
                  <small>{item.label}</small>
                  <strong title={item.title}>{displayPath(item.title)}</strong>
                  <b>{item.detail}</b>
                </button>
              ))}
            </div>
          ) : null}
          <div className="session-files-filterbar" role="group" aria-label="File filters">
            <FileFilterButton label="All" value={stats.total} active={filter === "all"} onClick={() => setFilter("all")} />
            <FileFilterButton label="Changed" value={stats.changed} active={filter === "changed"} onClick={() => setFilter("changed")} />
            <FileFilterButton label="Snapshot" value={stats.snapshots} active={filter === "snapshots"} onClick={() => setFilter("snapshots")} />
            <FileFilterButton label="Issues" value={stats.failed + stats.running} active={filter === "issues"} onClick={() => setFilter("issues")} />
            <FileFilterButton label="Dirs" value={stats.listed} active={filter === "listed"} onClick={() => setFilter("listed")} />
          </div>
          {onOpenWorkspacePath ? (
            <span className="session-files-overview-actions">
              <button type="button" className="ghost-action" onClick={() => onOpenWorkspacePath(".")}>
                Browse workspace
              </button>
            </span>
          ) : null}
        </div>
        {workspaceBrowser && workspaceBrowser.state !== "idle" ? (
          <WorkspaceBrowser
            browser={workspaceBrowser}
            query={workspaceQuery}
            onQueryChange={setWorkspaceQuery}
            onOpenPath={onOpenWorkspacePath}
            onUseAsDraft={onUseAsDraft}
          />
        ) : null}
        {selectedItem ? (
          <div className="session-file-preview" data-testid="session-file-preview">
            <div className="session-file-preview-head">
              <div>
                <span>File snapshot</span>
                <strong title={selectedItem.path}>{selectedItem.path}</strong>
              </div>
              <small>{selectedItem.contentStale ? "snapshot before latest change" : selectedItem.contentTruncated ? "partial read_file output" : "read_file output"}</small>
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
              <span className="session-file-line-jump">
                <input
                  aria-label="Go to line"
                  inputMode="numeric"
                  value={lineJump}
                  onChange={(event) => setLineJump(event.target.value)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter") jumpToPreviewLine();
                  }}
                  placeholder="line"
                />
                <button type="button" className="ghost-action" onClick={jumpToPreviewLine}>
                  Go
                </button>
              </span>
              <CopyButton label="Copy snapshot" value={fileContentText(selectedItem)} className="ghost-action" />
              <button type="button" className="ghost-action" aria-pressed={wrapLines} onClick={() => setWrapLines((value) => !value)}>
                Wrap
              </button>
            </div>
            {activeRange ? (
              <div className="session-file-range-actions" data-testid="session-file-range-actions">
                <span>
                  Lines {activeRange.start}-{activeRange.end}
                </span>
                <CopyButton label="Copy range" value={fileRangeText(selectedItem, activeRange.start, activeRange.end)} className="ghost-action" />
                {onUseAsDraft ? (
                  <>
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
                  </>
                ) : null}
              </div>
            ) : null}
            <div className="code session-file-preview-code" data-wrap={wrapLines ? "true" : "false"} data-testid="session-file-preview-content" role="list" aria-label="Loaded file snapshot" ref={previewCodeRef}>
              {snapshotLines.map((line, index) => {
                const lineNumber = index + 1;
                const selected = activeRange ? lineNumber >= activeRange.start && lineNumber <= activeRange.end : false;
                return (
                  <button
                    key={lineNumber}
                    type="button"
                    className="session-file-code-line"
                    data-line-number={lineNumber}
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
                    <small>{item.contentStale ? "Snapshot may predate latest change" : item.contentTruncated ? "Partial read_file snapshot available" : "read_file snapshot available"}</small>
                  ) : null}
                </div>
                <span className="session-files-actions">
                  {onOpenWorkspacePath ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenWorkspacePath(item.path)}>
                      Open current
                    </button>
                  ) : null}
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
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(fileEvidenceDraft(item), "file_evidence")}>
                      {fileDraftActionLabel(item)}
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

function reviewQueueItemDisabled(item: SessionFilesReviewItem, canOpenWorkspace: boolean): boolean {
  if (item.action === "view_snapshot") return !item.item.contentPreview;
  if (item.action === "open_current" || item.action === "recover_path") return !canOpenWorkspace && !item.item.contentPreview;
  if (item.action === "wait") return !canOpenWorkspace && !item.item.contentPreview;
  return false;
}

function WorkspaceBrowser({
  browser,
  query,
  onQueryChange,
  onOpenPath,
  onUseAsDraft,
}: {
  browser: WorkspaceFileBrowserState;
  query: string;
  onQueryChange: (value: string) => void;
  onOpenPath?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const ready = browser.state === "ready" ? browser.file : undefined;
  const currentPath = browser.state === "ready" ? browser.file.path : browser.state === "loading" || browser.state === "error" ? browser.path ?? "." : ".";
  const parent = ready ? parentWorkspacePath(ready.path) : undefined;

  function openTypedPath() {
    onOpenPath?.(query.trim() || currentPath || ".");
  }

  return (
    <section className="session-workspace-browser" data-testid="session-workspace-browser" aria-label="Workspace file browser">
      <div className="session-workspace-browser-head">
        <div>
          <span>Workspace browser</span>
          <strong>{workspaceBrowserTitle(browser)}</strong>
          <small>{workspaceBrowserDetail(browser)}</small>
        </div>
        <div className="session-workspace-browser-path">
          <input
            aria-label="Workspace path"
            value={query}
            onChange={(event) => onQueryChange(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") openTypedPath();
            }}
            placeholder={currentPath === "." ? "src/main.go" : currentPath}
          />
          <button type="button" className="ghost-action" disabled={!onOpenPath || browser.state === "loading"} onClick={openTypedPath}>
            Open
          </button>
        </div>
      </div>
      {browser.state === "loading" ? <div className="session-skills-empty">Loading {browser.path}...</div> : null}
      {browser.state === "error" ? <div className="session-skills-empty">Could not open {browser.path ?? "workspace"}: {browser.error}</div> : null}
      {ready?.kind === "directory" ? (
        <WorkspaceDirectory
          file={ready}
          parent={parent}
          onOpenPath={onOpenPath}
          onUseAsDraft={onUseAsDraft}
        />
      ) : null}
      {ready?.kind === "file" ? (
        <WorkspaceFilePreview
          file={ready}
          parent={parent}
          onOpenPath={onOpenPath}
          onUseAsDraft={onUseAsDraft}
        />
      ) : null}
    </section>
  );
}

function WorkspaceDirectory({
  file,
  parent,
  onOpenPath,
  onUseAsDraft,
}: {
  file: WorkspaceFileView;
  parent?: string;
  onOpenPath?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <div className="session-workspace-browser-body">
      <span className="session-evidence-actions">
        {parent ? (
          <button type="button" className="ghost-action" onClick={() => onOpenPath?.(parent)}>
            Up
          </button>
        ) : null}
        <CopyButton label="Copy path" value={file.path} className="ghost-action" />
        {onUseAsDraft ? (
          <button type="button" className="ghost-action" onClick={() => onUseAsDraft(workspaceFileDraft(file), "file_snapshot")}>
            Reference listing
          </button>
        ) : null}
      </span>
      {file.entries.length > 0 ? (
        <ol className="session-workspace-browser-list" data-testid="session-workspace-browser-list">
          {file.entries.map((entry) => (
            <WorkspaceEntry key={entry.path} entry={entry} onOpenPath={onOpenPath} />
          ))}
        </ol>
      ) : (
        <div className="session-skills-empty">Directory is empty.</div>
      )}
      {file.hasMore ? <small className="session-workspace-browser-more">More entries are available; open a narrower path.</small> : null}
    </div>
  );
}

function WorkspaceEntry({ entry, onOpenPath }: { entry: WorkspaceFileEntryView; onOpenPath?: (path: string) => void }) {
  return (
    <li className="session-workspace-browser-entry" data-kind={entry.kind}>
      <button type="button" onClick={() => onOpenPath?.(entry.path)} disabled={!onOpenPath}>
        <strong title={entry.path}>{entry.name}</strong>
        <span>{entry.kind === "directory" ? "Directory" : entry.size ?? "File"}</span>
      </button>
    </li>
  );
}

function WorkspaceFilePreview({
  file,
  parent,
  onOpenPath,
  onUseAsDraft,
}: {
  file: WorkspaceFileView;
  parent?: string;
  onOpenPath?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  return (
    <div className="session-file-preview session-workspace-file-preview" data-testid="session-workspace-file-preview">
      <div className="session-file-preview-head">
        <div>
          <span>Workspace file</span>
          <strong title={file.path}>{file.path}</strong>
        </div>
        <small>{file.detail}</small>
      </div>
      <div className="session-file-preview-toolbar">
        {parent ? (
          <button type="button" className="ghost-action" onClick={() => onOpenPath?.(parent)}>
            Up
          </button>
        ) : null}
        <CopyButton label="Copy file" value={file.text ?? ""} className="ghost-action" />
        <CopyButton label="Copy path" value={file.path} className="ghost-action" />
        {onUseAsDraft ? (
          <button type="button" className="ghost-action" onClick={() => onUseAsDraft(workspaceFileDraft(file), "file_snapshot")}>
            Reference file
          </button>
        ) : null}
      </div>
      <div className="code session-file-preview-code" role="list" aria-label="Workspace file content">
        {file.lines.map((line, index) => (
          <div key={index} className="session-file-code-line">
            <span className="session-file-code-line-number">{index + 1}</span>
            <span className="session-file-code-line-text">{line || " "}</span>
          </div>
        ))}
      </div>
      {file.hasMore ? <small className="session-workspace-browser-more">Preview truncated; open through the agent before making broad edits.</small> : null}
    </div>
  );
}

function workspaceBrowserTitle(browser: WorkspaceFileBrowserState): string {
  if (browser.state === "idle") return "Workspace file access";
  if (browser.state === "loading") return browser.path;
  if (browser.state === "error") return browser.path ?? "Workspace unavailable";
  return browser.file.title;
}

function workspaceBrowserDetail(browser: WorkspaceFileBrowserState): string {
  if (browser.state === "idle") return browser.workspacePath ? "Open root or enter a workspace-relative path." : "Workspace binding required.";
  if (browser.state === "loading") return "Reading from the session workspace.";
  if (browser.state === "error") return browser.error;
  return browser.file.detail;
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

function fileDraftActionLabel(item: SessionFileEvidence): string {
  if (item.status === "failed") return "Recover path";
  if (item.status === "running") return "Check status";
  if (item.actions.includes("changed")) return "Review file";
  if (item.actions.includes("listed")) return "Use listing";
  if (item.contentPreview) return "Use snapshot";
  return "Use evidence";
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
    staleSnapshots: files.items.filter((item) => item.contentStale).length,
  };
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
