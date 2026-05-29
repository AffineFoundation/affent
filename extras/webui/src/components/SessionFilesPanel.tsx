import { useMemo, useRef, useState, type CSSProperties } from "react";
import type { UseAsDraft } from "../view/draftSource";
import {
  fileContentText,
  fileEvidenceDraft,
  fileLines,
  fileRangeDraft,
  fileRangeText,
  type SessionFileEvidence,
  type SessionFilesView,
} from "../view/sessionFiles";
import { parentWorkspacePath, workspaceFileDraft, type WorkspaceFileBrowserState, type WorkspaceFileEntryView, type WorkspaceFileView } from "../view/workspaceFile";
import { CopyButton } from "./CopyButton";
import { HighlightText } from "./HighlightText";

type FileFilter = "all" | "changed" | "snapshots" | "issues" | "listed";

interface FileTreeNode {
  name: string;
  path: string;
  children: FileTreeNode[];
  item?: SessionFileEvidence;
}

export function SessionFilesPanel({
  files,
  workspaceBrowser,
  defaultOpen = false,
  onOpenWorkspacePath,
  onOpenWorkspacePanel,
  onOpenArtifact,
  onUseAsDraft,
}: {
  files: SessionFilesView;
  workspaceBrowser?: WorkspaceFileBrowserState;
  defaultOpen?: boolean;
  onOpenWorkspacePath?: (path: string) => void;
  onOpenWorkspacePanel?: () => void;
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
  const filteredItems = filter === "all" ? files.items : files.items.filter((item) => fileMatchesFilter(item, filter));
  const visibleItems = trimmedQuery ? filteredItems.filter((item) => fileMatchesQuery(item, trimmedQuery)) : filteredItems;
  const treeNodes = useMemo(() => buildFileTree(visibleItems), [visibleItems]);
  const snapshotItems = visibleItems.filter((item) => item.contentPreview);
  const selectedItem = snapshotItems.find((item) => item.path === selectedPath) ?? snapshotItems[0];
  const snapshotLines = selectedItem ? fileLines(selectedItem) : [];
  const activeRange = selectedItem && selectedRange?.path === selectedItem.path ? selectedRange : undefined;
  const workspaceReady = workspaceBrowser?.state === "ready" ? workspaceBrowser.file : undefined;
  const workspaceCurrentPath = workspaceBrowser?.state === "ready"
    ? workspaceBrowser.file.path
    : workspaceBrowser?.state === "loading" || workspaceBrowser?.state === "error"
      ? workspaceBrowser.path ?? "."
      : ".";
  const workspaceParent = workspaceReady ? parentWorkspacePath(workspaceReady.path) : undefined;
  const previewCodeRef = useRef<HTMLDivElement | null>(null);

  function openEvidenceItem(item: SessionFileEvidence) {
    if (item.contentPreview) {
      setSelectedPath(item.path);
      return;
    }
    if (onOpenWorkspacePath) {
      onOpenWorkspacePath(item.path);
      return;
    }
    onOpenWorkspacePanel?.();
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

  function openTypedWorkspacePath() {
    const path = workspaceQuery.trim() || workspaceCurrentPath || ".";
    if (onOpenWorkspacePath) {
      onOpenWorkspacePath(path);
      return;
    }
    onOpenWorkspacePanel?.();
  }

  return (
    <details className="session-skills-panel session-files-panel" data-testid="session-files-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Files</span>
        <strong>{files.summary}</strong>
        <span>{files.detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="session-files-ide" data-testid="session-files-ide">
          <aside className="session-files-explorer" aria-label="File explorer">
            <div className="session-files-explorer-head">
              <div>
                <span>Explorer</span>
                <strong>{workspaceBrowserTitle(workspaceBrowser ?? { state: "idle" })}</strong>
                <small>{workspaceBrowserDetail(workspaceBrowser ?? { state: "idle" })}</small>
              </div>
              {onOpenWorkspacePath || onOpenWorkspacePanel ? (
                <button
                  type="button"
                  className="ghost-action"
                  disabled={workspaceBrowser?.state === "loading"}
                  onClick={() => (onOpenWorkspacePath ? onOpenWorkspacePath(".") : onOpenWorkspacePanel?.())}
                >
                  Root
                </button>
              ) : null}
            </div>
            <div className="session-workspace-browser-path">
              <input
                aria-label="Workspace path"
                value={workspaceQuery}
                onChange={(event) => setWorkspaceQuery(event.target.value)}
                onKeyDown={(event) => {
                  if (event.key === "Enter") openTypedWorkspacePath();
                }}
                placeholder={workspaceCurrentPath === "." ? "src/main.go" : workspaceCurrentPath}
              />
              <button
                type="button"
                className="ghost-action"
                disabled={workspaceBrowser?.state === "loading" || (!onOpenWorkspacePath && !onOpenWorkspacePanel)}
                onClick={openTypedWorkspacePath}
              >
                Open
              </button>
            </div>
            {workspaceBrowser?.state === "loading" ? <div className="session-skills-empty">Loading {workspaceBrowser.path}...</div> : null}
            {workspaceBrowser?.state === "error" ? <div className="session-skills-empty">Could not open {workspaceBrowser.path ?? "workspace"}: {workspaceBrowser.error}</div> : null}
            {workspaceReady?.kind === "directory" ? (
              <WorkspaceDirectory
                file={workspaceReady}
                parent={workspaceParent}
                onOpenPath={onOpenWorkspacePath}
                onUseAsDraft={onUseAsDraft}
              />
            ) : null}
            <div className="session-files-explorer-tools">
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
            <div className="session-files-filterbar" role="group" aria-label="File filters">
              <FileFilterButton label="All" value={stats.total} active={filter === "all"} onClick={() => setFilter("all")} />
              <FileFilterButton label="Changed" value={stats.changed} active={filter === "changed"} onClick={() => setFilter("changed")} />
              <FileFilterButton label="Snapshot" value={stats.snapshots} active={filter === "snapshots"} onClick={() => setFilter("snapshots")} />
              <FileFilterButton label="Issues" value={stats.failed + stats.running} active={filter === "issues"} onClick={() => setFilter("issues")} />
              <FileFilterButton label="Dirs" value={stats.listed} active={filter === "listed"} onClick={() => setFilter("listed")} />
            </div>
            <FileEvidenceTree
              nodes={treeNodes}
              selectedPath={selectedItem?.path}
              onOpenDirectory={(path) => onOpenWorkspacePath?.(path)}
              onOpenItem={openEvidenceItem}
              onOpenWorkspacePath={onOpenWorkspacePath}
              onOpenArtifact={onOpenArtifact}
              onUseAsDraft={onUseAsDraft}
            />
            {visibleItems.length === 0 ? (
              files.items.length > 0 ? (
                <div className="session-skills-empty">No {filter === "all" ? "file evidence" : filter} result matching "{trimmedQuery}".</div>
              ) : (
                <div className="session-skills-empty">No read, list, write, or edit actions in this chat.</div>
              )
            ) : null}
          </aside>
          <section className="session-files-editor" aria-label="File editor preview">
            {workspaceReady?.kind === "file" ? (
              <WorkspaceFilePreview
                file={workspaceReady}
                parent={workspaceParent}
                onOpenPath={onOpenWorkspacePath}
                onUseAsDraft={onUseAsDraft}
              />
            ) : selectedItem ? (
              <div className="session-file-preview" data-testid="session-file-preview">
                <div className="session-file-preview-head">
                  <div>
                    <span>Snapshot</span>
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
            ) : (
              <div className="session-files-editor-empty" data-testid="session-files-editor-empty">
                <strong>No file open</strong>
                <span>Open a workspace path or select a loaded snapshot from Explorer.</span>
                {onOpenWorkspacePath || onOpenWorkspacePanel ? (
                  <button type="button" className="ghost-action primary-run-action" onClick={() => (onOpenWorkspacePath ? onOpenWorkspacePath(".") : onOpenWorkspacePanel?.())}>
                    Open root
                  </button>
                ) : null}
              </div>
            )}
          </section>
        </div>
      </div>
    </details>
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
  const entries = [...file.entries].sort(compareWorkspaceEntries);
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
      {entries.length > 0 ? (
        <ol className="session-workspace-browser-list" data-testid="session-workspace-browser-list" aria-label="Workspace directory tree">
          {entries.map((entry) => (
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

function FileEvidenceTree({
  nodes,
  selectedPath,
  onOpenDirectory,
  onOpenItem,
  onOpenWorkspacePath,
  onOpenArtifact,
  onUseAsDraft,
}: {
  nodes: FileTreeNode[];
  selectedPath?: string;
  onOpenDirectory: (path: string) => void;
  onOpenItem: (item: SessionFileEvidence) => void;
  onOpenWorkspacePath?: (path: string) => void;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  if (nodes.length === 0) return null;
  return (
    <ol className="session-files-tree" data-testid="session-files-list" aria-label="Agent file tree">
      {nodes.map((node) => (
        <FileTreeBranch
          key={node.path}
          node={node}
          depth={0}
          selectedPath={selectedPath}
          onOpenDirectory={onOpenDirectory}
          onOpenItem={onOpenItem}
          onOpenWorkspacePath={onOpenWorkspacePath}
          onOpenArtifact={onOpenArtifact}
          onUseAsDraft={onUseAsDraft}
        />
      ))}
    </ol>
  );
}

function FileTreeBranch({
  node,
  depth,
  selectedPath,
  onOpenDirectory,
  onOpenItem,
  onOpenWorkspacePath,
  onOpenArtifact,
  onUseAsDraft,
}: {
  node: FileTreeNode;
  depth: number;
  selectedPath?: string;
  onOpenDirectory: (path: string) => void;
  onOpenItem: (item: SessionFileEvidence) => void;
  onOpenWorkspacePath?: (path: string) => void;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const isDirectory = node.children.length > 0;
  const item = node.item;
  return (
    <li className="session-files-tree-node" data-kind={isDirectory ? "directory" : "file"} data-status={item?.status} data-selected={selectedPath === item?.path ? "true" : "false"}>
      <button
        type="button"
        className="session-files-tree-row"
        style={{ "--depth": depth } as CSSProperties}
        onClick={() => (item ? onOpenItem(item) : onOpenDirectory(node.path))}
      >
        <span className="session-files-tree-icon" aria-hidden="true">
          {isDirectory ? "▸" : item?.actions.includes("changed") ? "M" : item?.contentPreview ? "R" : "F"}
        </span>
        <strong title={node.path}>{node.name}</strong>
        {item ? <small>{compactFileMeta(item)}</small> : <small>{node.children.length}</small>}
      </button>
      {item ? (
        <span className="session-files-actions" style={{ "--depth": depth } as CSSProperties}>
          {onOpenWorkspacePath ? (
            <button type="button" className="ghost-action" onClick={() => onOpenWorkspacePath(item.path)}>
              Current
            </button>
          ) : null}
          {item.artifactPath && onOpenArtifact ? (
            <button type="button" className="ghost-action" onClick={() => onOpenArtifact(item.artifactPath ?? "")}>
              Evidence
            </button>
          ) : null}
          {onUseAsDraft ? (
            <button type="button" className="ghost-action" onClick={() => onUseAsDraft(fileEvidenceDraft(item), "file_evidence")}>
              {fileDraftActionLabel(item)}
            </button>
          ) : null}
          <CopyButton label="Copy path" value={item.path} className="ghost-action" />
        </span>
      ) : null}
      {node.children.length > 0 ? (
        <ol>
          {node.children.map((child) => (
            <FileTreeBranch
              key={child.path}
              node={child}
              depth={depth + 1}
              selectedPath={selectedPath}
              onOpenDirectory={onOpenDirectory}
              onOpenItem={onOpenItem}
              onOpenWorkspacePath={onOpenWorkspacePath}
              onOpenArtifact={onOpenArtifact}
              onUseAsDraft={onUseAsDraft}
            />
          ))}
        </ol>
      ) : null}
    </li>
  );
}

function buildFileTree(items: readonly SessionFileEvidence[]): FileTreeNode[] {
  const root: FileTreeNode[] = [];
  for (const item of items) {
    const parts = normalizedPathParts(item.path);
    let siblings = root;
    let currentPath = "";
    parts.forEach((part, index) => {
      currentPath = currentPath ? `${currentPath}/${part}` : part;
      let node = siblings.find((candidate) => candidate.name === part);
      if (!node) {
        node = { name: part, path: currentPath, children: [] };
        siblings.push(node);
      }
      if (index === parts.length - 1) node.item = item;
      siblings = node.children;
    });
  }
  return sortTree(root);
}

function sortTree(nodes: FileTreeNode[]): FileTreeNode[] {
  nodes.sort((a, b) => {
    const aDir = a.children.length > 0 && !a.item;
    const bDir = b.children.length > 0 && !b.item;
    if (aDir !== bDir) return aDir ? -1 : 1;
    return a.name.localeCompare(b.name);
  });
  nodes.forEach((node) => sortTree(node.children));
  return nodes;
}

function normalizedPathParts(path: string): string[] {
  const clean = path.replace(/\\/g, "/").replace(/^\/+/, "").trim();
  if (!clean || clean === ".") return ["."];
  return clean.split("/").filter(Boolean);
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
  if (item.status === "failed") return "Check path";
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

function compactFileMeta(item: SessionFileEvidence): string {
  const parts = [
    actionLabel(item.actions),
    item.contentPreview ? "snapshot" : undefined,
    item.contentStale ? "stale" : undefined,
    statusLabel(item.status),
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

function compareWorkspaceEntries(a: WorkspaceFileEntryView, b: WorkspaceFileEntryView): number {
  if (a.kind !== b.kind) return a.kind === "directory" ? -1 : 1;
  return a.name.localeCompare(b.name);
}
