import { useEffect, useMemo, useRef, useState, type CSSProperties } from "react";
import type { UseAsDraft } from "../view/draftSource";
import {
  fileContentText,
  fileEvidenceDraft,
  fileLines,
  fileRangeDraft,
  fileRangeText,
  filesReviewQueue,
  type SessionFilesReviewItem,
  type SessionFileEvidence,
  type SessionFilesView,
} from "../view/sessionFiles";
import {
  parentWorkspacePath,
  workspaceFileDraft,
  workspaceFileRangeDraft,
  workspaceFileRangeText,
  type WorkspaceFileBrowserState,
  type WorkspaceFileEntryView,
  type WorkspaceFileView,
} from "../view/workspaceFile";
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
  const [selectedEvidencePath, setSelectedEvidencePath] = useState<string | undefined>();
  const [selectedRange, setSelectedRange] = useState<{ path: string; start: number; end: number } | undefined>();
  const [collapsedPaths, setCollapsedPaths] = useState<ReadonlySet<string>>(() => new Set());
  const [filter, setFilter] = useState<FileFilter>("all");
  const [wrapLines, setWrapLines] = useState(true);
  const trimmedQuery = query.trim();
  const stats = fileStats(files);
  const filteredItems = filter === "all" ? files.items : files.items.filter((item) => fileMatchesFilter(item, filter));
  const visibleItems = trimmedQuery ? filteredItems.filter((item) => fileMatchesQuery(item, trimmedQuery)) : filteredItems;
  const treeNodes = useMemo(() => buildFileTree(visibleItems), [visibleItems]);
  const primaryFileAttention = fileAttentionItem(visibleItems);
  const snapshotItems = visibleItems.filter((item) => item.contentPreview);
  const selectedEvidenceCandidate = visibleItems.find((item) => item.path === selectedEvidencePath);
  const selectedItem = selectedEvidenceCandidate?.contentPreview
    ? selectedEvidenceCandidate
    : snapshotItems.find((item) => item.path === selectedPath) ?? (selectedEvidenceCandidate ? undefined : snapshotItems[0]);
  const selectedEvidence = selectedEvidenceCandidate
    ?? selectedItem
    ?? preferredFileEvidence(visibleItems);
  const snapshotLines = selectedItem ? fileLines(selectedItem) : [];
  const activeRange = selectedItem && selectedRange?.path === selectedItem.path ? selectedRange : undefined;
  const workspaceReady = workspaceBrowser?.state === "ready" ? workspaceBrowser.file : undefined;
  const workspaceCurrentPath = workspaceBrowser?.state === "ready"
    ? workspaceBrowser.file.path
    : workspaceBrowser?.state === "loading" || workspaceBrowser?.state === "error"
      ? workspaceBrowser.path ?? "."
      : ".";
  const workspaceParent = workspaceReady ? parentWorkspacePath(workspaceReady.path) : undefined;
  const canOpenWorkspacePath = Boolean(onOpenWorkspacePath);
  const previewCodeRef = useRef<HTMLDivElement | null>(null);
  const autoOpenedWorkspaceRef = useRef<string | undefined>(undefined);
  const editorTitle = workspaceReady
    ? workspaceReady.path
    : selectedItem?.path ?? selectedEvidence?.path ?? "No file selected";
  const editorDetail = workspaceReady
    ? `${workspaceReady.kind === "directory" ? "Workspace directory" : "Workspace file"} · ${workspaceReady.detail}`
    : selectedItem
      ? snapshotEditorDetail(selectedItem, snapshotLines.length)
      : selectedEvidence
        ? `${fileEvidenceKindLabel(selectedEvidence)} · ${compactFileMeta(selectedEvidence)}`
        : "Open a workspace path or select recorded file evidence.";

  useEffect(() => {
    if (!defaultOpen || !onOpenWorkspacePath) return;
    if (workspaceBrowser?.state !== "idle" || !workspaceBrowser.workspacePath) return;
    if (autoOpenedWorkspaceRef.current === workspaceBrowser.workspacePath) return;
    autoOpenedWorkspaceRef.current = workspaceBrowser.workspacePath;
    onOpenWorkspacePath(".");
  }, [defaultOpen, onOpenWorkspacePath, workspaceBrowser]);

  function openEvidenceItem(item: SessionFileEvidence) {
    setSelectedEvidencePath(item.path);
    if (item.contentPreview) {
      setSelectedPath(item.path);
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

  function openTypedWorkspacePath() {
    const path = workspaceQuery.trim() || workspaceCurrentPath || ".";
    if (onOpenWorkspacePath) {
      onOpenWorkspacePath(path);
      return;
    }
    onOpenWorkspacePanel?.();
  }

  function toggleTreePath(path: string) {
    setCollapsedPaths((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
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
        {primaryFileAttention ? (
          <FileAttentionStrip
            attention={primaryFileAttention}
            onOpenWorkspacePath={onOpenWorkspacePath}
            onOpenWorkspacePanel={onOpenWorkspacePanel}
            onUseAsDraft={onUseAsDraft}
            onViewEvidence={openEvidenceItem}
          />
        ) : null}
        <div className="session-files-ide" data-testid="session-files-ide">
          <aside className="session-files-explorer" aria-label="File explorer">
            <div className="session-files-explorer-head">
              <div>
                <span>Explorer</span>
                <strong>{canOpenWorkspacePath ? workspaceBrowserTitle(workspaceBrowser ?? { state: "idle" }) : "Agent file evidence"}</strong>
                <small>{canOpenWorkspacePath ? workspaceBrowserDetail(workspaceBrowser ?? { state: "idle" }) : "Workspace not bound; showing recorded file actions."}</small>
              </div>
              {canOpenWorkspacePath ? (
                <button
                  type="button"
                  className="ghost-action"
                  disabled={workspaceBrowser?.state === "loading"}
                  onClick={() => onOpenWorkspacePath?.(".")}
                >
                  Root
                </button>
              ) : null}
            </div>
            {canOpenWorkspacePath ? (
              <>
                <WorkspaceBreadcrumbs path={workspaceCurrentPath} onOpenPath={onOpenWorkspacePath} />
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
                    disabled={workspaceBrowser?.state === "loading"}
                    onClick={openTypedWorkspacePath}
                  >
                    Open
                  </button>
                </div>
              </>
            ) : (
              <div className="session-files-explorer-status" data-state="unbound">
                <span>Workspace unavailable</span>
                <strong>Recorded evidence only</strong>
                {onOpenWorkspacePanel ? <button type="button" className="ghost-action" onClick={onOpenWorkspacePanel}>Open Workspace</button> : null}
              </div>
            )}
            {workspaceBrowser?.state === "loading" ? <div className="session-skills-empty">Loading {workspaceBrowser.path}...</div> : null}
            {workspaceBrowser?.state === "error" ? <div className="session-skills-empty">{workspaceBrowserErrorMessage(workspaceBrowser)}</div> : null}
            {workspaceReady?.kind === "directory" ? (
              <WorkspaceDirectory
                file={workspaceReady}
                parent={workspaceParent}
                query={trimmedQuery}
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
              selectedPath={selectedEvidence?.path}
              collapsedPaths={collapsedPaths}
              onToggleDirectory={toggleTreePath}
              onOpenDirectory={(path) => onOpenWorkspacePath?.(path)}
              onOpenItem={openEvidenceItem}
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
            <div className="session-files-editor-chrome" data-testid="session-files-editor-chrome">
              <div>
                <span>{workspaceReady?.kind === "file" ? "Workspace" : selectedItem ? "Snapshot" : selectedEvidence ? "Evidence" : "Editor"}</span>
                <strong title={editorTitle}>{editorTitle}</strong>
              </div>
              <small>{editorDetail}</small>
            </div>
            {workspaceReady?.kind === "directory" ? (
              <WorkspaceDirectoryPreview
                file={workspaceReady}
                parent={workspaceParent}
                query={trimmedQuery}
                onOpenPath={onOpenWorkspacePath}
                onUseAsDraft={onUseAsDraft}
              />
            ) : workspaceReady?.kind === "file" ? (
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
                  <span className="session-file-preview-actions">
                    {onOpenWorkspacePath ? (
                      <button type="button" className="ghost-action" onClick={() => onOpenWorkspacePath(selectedItem.path)}>
                        Open current
                      </button>
                    ) : null}
                    {selectedItem.artifactPath && onOpenArtifact ? (
                      <button type="button" className="ghost-action" onClick={() => onOpenArtifact(selectedItem.artifactPath ?? "")}>
                        Evidence
                      </button>
                    ) : null}
                    {onUseAsDraft ? (
                      <button type="button" className="ghost-action" onClick={() => onUseAsDraft(fileEvidenceDraft(selectedItem), "file_evidence")}>
                        {fileDraftActionLabel(selectedItem)}
                      </button>
                    ) : null}
                  </span>
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
                <FileEditorStatus
                  source={selectedItem.contentStale ? "snapshot stale" : "snapshot"}
                  path={selectedItem.path}
                  lines={snapshotLines.length}
                  detail={compactFileMeta(selectedItem)}
                />
              </div>
            ) : selectedEvidence ? (
              <FileEvidenceInspector
                item={selectedEvidence}
                onOpenWorkspacePath={onOpenWorkspacePath}
                onOpenWorkspacePanel={onOpenWorkspacePanel}
                onOpenArtifact={onOpenArtifact}
                onUseAsDraft={onUseAsDraft}
              />
            ) : (
              <div className="session-files-editor-empty" data-testid="session-files-editor-empty">
                <strong>No file open</strong>
                <span>Select a file reference or open a workspace path.</span>
                {canOpenWorkspacePath || onOpenWorkspacePanel ? (
                  <button type="button" className="ghost-action primary-run-action" onClick={() => (canOpenWorkspacePath ? onOpenWorkspacePath?.(".") : onOpenWorkspacePanel?.())}>
                    {canOpenWorkspacePath ? "Open root" : "Open Workspace"}
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

function FileAttentionStrip({
  attention,
  onOpenWorkspacePath,
  onOpenWorkspacePanel,
  onUseAsDraft,
  onViewEvidence,
}: {
  attention: SessionFilesReviewItem;
  onOpenWorkspacePath?: (path: string) => void;
  onOpenWorkspacePanel?: () => void;
  onUseAsDraft?: UseAsDraft;
  onViewEvidence: (item: SessionFileEvidence) => void;
}) {
  const primary = fileAttentionPrimaryAction(attention, { onOpenWorkspacePath, onOpenWorkspacePanel, onViewEvidence });
  return (
    <section className="session-files-attention" data-testid="session-files-attention" data-tone={attention.tone ?? "neutral"} aria-label="File attention">
      <div className="session-files-attention-copy">
        <span>{attention.label}</span>
        <strong title={attention.title}>{attention.title}</strong>
        <small>{attention.detail}</small>
      </div>
      <div className="session-files-attention-actions">
        {primary ? (
          <button type="button" className="ghost-action primary-run-action" onClick={primary.onClick}>
            {primary.label}
          </button>
        ) : null}
        {onUseAsDraft && attention.action !== "wait" ? (
          <button type="button" className="ghost-action" onClick={() => onUseAsDraft(fileEvidenceDraft(attention.item), "file_evidence")}>
            Ask Affent
          </button>
        ) : null}
      </div>
    </section>
  );
}

function fileAttentionPrimaryAction(
  attention: SessionFilesReviewItem,
  handlers: {
    onOpenWorkspacePath?: (path: string) => void;
    onOpenWorkspacePanel?: () => void;
    onViewEvidence: (item: SessionFileEvidence) => void;
  },
): { label: string; onClick: () => void } | undefined {
  if (attention.action === "view_snapshot") {
    return { label: "View snapshot", onClick: () => handlers.onViewEvidence(attention.item) };
  }
  if (attention.action === "open_current" || attention.action === "recover_path") {
    if (handlers.onOpenWorkspacePath) return { label: "Open current", onClick: () => handlers.onOpenWorkspacePath?.(attention.item.path) };
    if (handlers.onOpenWorkspacePanel) return { label: "Open workspace", onClick: () => handlers.onOpenWorkspacePanel?.() };
  }
  return undefined;
}

function WorkspaceBreadcrumbs({ path, onOpenPath }: { path: string; onOpenPath?: (path: string) => void }) {
  const crumbs = workspaceCrumbs(path);
  return (
    <nav className="session-workspace-breadcrumbs" aria-label="Workspace path breadcrumbs">
      {crumbs.map((crumb, index) => (
        <button key={`${crumb.path}:${index}`} type="button" onClick={() => onOpenPath?.(crumb.path)} disabled={!onOpenPath}>
          {crumb.label}
        </button>
      ))}
    </nav>
  );
}

function workspaceCrumbs(path: string): Array<{ label: string; path: string }> {
  const clean = path.replace(/\\/g, "/").replace(/^\/+/, "").trim();
  if (!clean || clean === ".") return [{ label: ".", path: "." }];
  const parts = clean.split("/").filter(Boolean);
  const crumbs = [{ label: ".", path: "." }];
  let current = "";
  for (const part of parts) {
    current = current ? `${current}/${part}` : part;
    crumbs.push({ label: part, path: current });
  }
  return crumbs;
}

function WorkspaceDirectory({
  file,
  parent,
  query,
  onOpenPath,
  onUseAsDraft,
}: {
  file: WorkspaceFileView;
  parent?: string;
  query?: string;
  onOpenPath?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const entries = workspaceDirectoryEntries(file, query);
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
        <div className="session-skills-empty">{query ? `No entries matching "${query}".` : "Directory is empty."}</div>
      )}
      {file.hasMore ? <small className="session-workspace-browser-more">More entries are available; open a narrower path.</small> : null}
    </div>
  );
}

function WorkspaceDirectoryPreview({
  file,
  parent,
  query,
  onOpenPath,
  onUseAsDraft,
}: {
  file: WorkspaceFileView;
  parent?: string;
  query?: string;
  onOpenPath?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const entries = workspaceDirectoryEntries(file, query);
  return (
    <div className="session-file-preview session-workspace-directory-preview" data-testid="session-workspace-directory-preview">
      <div className="session-file-preview-head">
        <div>
          <span>Workspace directory</span>
          <strong title={file.path}>{file.path === "." ? "Workspace root" : file.path}</strong>
        </div>
        <small>{file.detail}</small>
      </div>
      <div className="session-file-preview-toolbar">
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
      </div>
      {entries.length > 0 ? (
        <ol className="session-workspace-directory-table" data-testid="session-workspace-directory-table" aria-label="Workspace directory entries">
          {entries.map((entry) => (
            <WorkspaceDirectoryPreviewEntry key={entry.path} entry={entry} onOpenPath={onOpenPath} />
          ))}
        </ol>
      ) : (
        <div className="session-files-editor-empty compact">
          <strong>{query ? "No matches" : "Empty directory"}</strong>
          <span>{query ? `No entries in this directory match "${query}".` : file.path === "." ? "Workspace root has no visible entries." : `${file.path} has no visible entries.`}</span>
        </div>
      )}
      <FileEditorStatus
        source="directory"
        path={file.path}
        lines={entries.length}
        countLabel={entries.length === 1 ? "entry" : "entries"}
        detail={query ? `${file.entries.length} total` : file.hasMore ? "more entries available" : file.detail}
      />
      {file.hasMore ? <small className="session-workspace-browser-more">More entries are available; open a narrower path.</small> : null}
    </div>
  );
}

function workspaceDirectoryEntries(file: WorkspaceFileView, query?: string): WorkspaceFileEntryView[] {
  const entries = [...file.entries].sort(compareWorkspaceEntries);
  const cleanQuery = query?.trim();
  if (!cleanQuery) return entries;
  return entries.filter((entry) => workspaceEntryMatchesQuery(entry, cleanQuery));
}

function workspaceEntryMatchesQuery(entry: WorkspaceFileEntryView, query: string): boolean {
  const haystack = [entry.name, entry.path, entry.kind, entry.size].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
}

function WorkspaceDirectoryPreviewEntry({ entry, onOpenPath }: { entry: WorkspaceFileEntryView; onOpenPath?: (path: string) => void }) {
  return (
    <li className="session-workspace-directory-row" data-kind={entry.kind}>
      <button type="button" onClick={() => onOpenPath?.(entry.path)} disabled={!onOpenPath}>
        <span className="session-files-tree-icon" aria-hidden="true">
          <span data-icon={entry.kind === "directory" ? "directory" : "file"} />
        </span>
        <strong title={entry.path}>{entry.name}</strong>
        <small>{entry.kind === "directory" ? "Directory" : entry.size ?? "File"}</small>
      </button>
    </li>
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
  const [query, setQuery] = useState("");
  const [lineJump, setLineJump] = useState("");
  const [selectedRange, setSelectedRange] = useState<{ start: number; end: number } | undefined>();
  const [wrapLines, setWrapLines] = useState(true);
  const codeRef = useRef<HTMLDivElement | null>(null);
  const activeRange = selectedRange;

  function selectLine(lineNumber: number, scroll = false) {
    setSelectedRange((current) => {
      if (!current || current.start !== current.end) return { start: lineNumber, end: lineNumber };
      return {
        start: Math.min(current.start, lineNumber),
        end: Math.max(current.end, lineNumber),
      };
    });
    if (scroll) {
      window.requestAnimationFrame(() => {
        const target = codeRef.current?.querySelector<HTMLElement>(`[data-line-number="${lineNumber}"]`);
        target?.scrollIntoView?.({ block: "center" });
      });
    }
  }

  function jumpToLine() {
    const lineNumber = Number.parseInt(lineJump, 10);
    if (!Number.isFinite(lineNumber) || file.lines.length === 0) return;
    selectLine(Math.max(1, Math.min(file.lines.length, lineNumber)), true);
  }

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
        <label className="session-skills-search">
          <span>Search file</span>
          <input
            aria-label="Search workspace file"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="text in current file"
          />
        </label>
        <span className="session-file-line-jump">
          <input
            aria-label="Go to workspace line"
            inputMode="numeric"
            value={lineJump}
            onChange={(event) => setLineJump(event.target.value)}
            onKeyDown={(event) => {
              if (event.key === "Enter") jumpToLine();
            }}
            placeholder="line"
          />
          <button type="button" className="ghost-action" onClick={jumpToLine}>
            Go
          </button>
        </span>
        {parent ? (
          <button type="button" className="ghost-action" onClick={() => onOpenPath?.(parent)}>
            Up
          </button>
        ) : null}
        <CopyButton label="Copy file" value={file.text ?? ""} className="ghost-action" />
        <CopyButton label="Copy path" value={file.path} className="ghost-action" />
        <button type="button" className="ghost-action" aria-pressed={wrapLines} onClick={() => setWrapLines((value) => !value)}>
          Wrap
        </button>
        {onUseAsDraft ? (
          <button type="button" className="ghost-action" onClick={() => onUseAsDraft(workspaceFileDraft(file), "file_snapshot")}>
            Ask about file
          </button>
        ) : null}
      </div>
      {activeRange ? (
        <div className="session-file-range-actions" data-testid="session-workspace-file-range-actions">
          <span>
            Lines {activeRange.start}-{activeRange.end}
          </span>
          <CopyButton label="Copy range" value={workspaceFileRangeText(file, activeRange.start, activeRange.end)} className="ghost-action" />
          {onUseAsDraft ? (
            <>
              <button
                type="button"
                className="ghost-action"
                onClick={() => onUseAsDraft(workspaceFileRangeDraft(file, activeRange.start, activeRange.end, "ask"), "file_range")}
              >
                Ask about range
              </button>
              <button
                type="button"
                className="ghost-action"
                onClick={() => onUseAsDraft(workspaceFileRangeDraft(file, activeRange.start, activeRange.end, "edit"), "file_range")}
              >
                Edit range
              </button>
            </>
          ) : null}
        </div>
      ) : null}
      <div className="code session-file-preview-code" data-wrap={wrapLines ? "true" : "false"} role="list" aria-label="Workspace file content" ref={codeRef}>
        {file.lines.map((line, index) => {
          const lineNumber = index + 1;
          const selected = activeRange ? lineNumber >= activeRange.start && lineNumber <= activeRange.end : false;
          return (
            <button
              key={index}
              type="button"
              className="session-file-code-line"
              data-line-number={lineNumber}
              data-selected={selected ? "true" : "false"}
              onClick={() => selectLine(lineNumber)}
            >
              <span className="session-file-code-line-number">{lineNumber}</span>
              <span className="session-file-code-line-text">
                <HighlightText text={line || " "} query={query} />
              </span>
            </button>
          );
        })}
      </div>
      <FileEditorStatus
        source="workspace"
        path={file.path}
        lines={file.lines.length}
        detail={file.hasMore ? "truncated" : "loaded"}
      />
      {file.hasMore ? <small className="session-workspace-browser-more">Preview truncated; open through the agent before making broad edits.</small> : null}
    </div>
  );
}

function FileEvidenceTree({
  nodes,
  selectedPath,
  collapsedPaths,
  onToggleDirectory,
  onOpenDirectory,
  onOpenItem,
}: {
  nodes: FileTreeNode[];
  selectedPath?: string;
  collapsedPaths: ReadonlySet<string>;
  onToggleDirectory: (path: string) => void;
  onOpenDirectory: (path: string) => void;
  onOpenItem: (item: SessionFileEvidence) => void;
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
          collapsedPaths={collapsedPaths}
          onToggleDirectory={onToggleDirectory}
          onOpenDirectory={onOpenDirectory}
          onOpenItem={onOpenItem}
        />
      ))}
    </ol>
  );
}

function FileTreeBranch({
  node,
  depth,
  selectedPath,
  collapsedPaths,
  onToggleDirectory,
  onOpenDirectory,
  onOpenItem,
}: {
  node: FileTreeNode;
  depth: number;
  selectedPath?: string;
  collapsedPaths: ReadonlySet<string>;
  onToggleDirectory: (path: string) => void;
  onOpenDirectory: (path: string) => void;
  onOpenItem: (item: SessionFileEvidence) => void;
}) {
  const isDirectory = node.children.length > 0 || isDirectoryEvidence(node.item);
  const item = node.item;
  const hasChildren = node.children.length > 0;
  const expanded = !collapsedPaths.has(node.path);
  return (
    <li className="session-files-tree-node" data-kind={isDirectory ? "directory" : "file"} data-status={item?.status} data-selected={selectedPath === item?.path ? "true" : "false"}>
      <button
        type="button"
        className="session-files-tree-row"
        style={{ "--depth": depth } as CSSProperties}
        aria-expanded={hasChildren ? expanded : undefined}
        onClick={(event) => {
          const target = event.target;
          const clickedChevron = target instanceof HTMLElement && Boolean(target.closest(".session-files-tree-chevron"));
          if (hasChildren && (!item || clickedChevron)) {
            onToggleDirectory(node.path);
            return;
          }
          if (item) {
            onOpenItem(item);
            return;
          }
          onOpenDirectory(node.path);
        }}
      >
        <span className="session-files-tree-chevron" data-visible={hasChildren ? "true" : "false"} aria-hidden="true" />
        <span className="session-files-tree-icon" aria-hidden="true">
          <span data-icon={fileTreeIcon(item, isDirectory)} />
        </span>
        <strong title={node.path}>{displayTreeName(node)}</strong>
        {item ? <small>{compactFileMeta(item)}</small> : <small>{node.children.length}</small>}
      </button>
      {hasChildren && expanded ? (
        <ol>
          {node.children.map((child) => (
            <FileTreeBranch
              key={child.path}
              node={child}
              depth={depth + 1}
              selectedPath={selectedPath}
              collapsedPaths={collapsedPaths}
              onToggleDirectory={onToggleDirectory}
              onOpenDirectory={onOpenDirectory}
              onOpenItem={onOpenItem}
            />
          ))}
        </ol>
      ) : null}
    </li>
  );
}

function FileEvidenceInspector({
  item,
  onOpenWorkspacePath,
  onOpenWorkspacePanel,
  onOpenArtifact,
  onUseAsDraft,
}: {
  item: SessionFileEvidence;
  onOpenWorkspacePath?: (path: string) => void;
  onOpenWorkspacePanel?: () => void;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const facts = [
    item.detail ? { label: "Latest result", value: item.detail } : undefined,
    item.next ? { label: "Next check", value: item.next } : undefined,
    { label: "Turn", value: `turn ${item.turnNumber} · ${item.actionCount} ${item.actionCount === 1 ? "action" : "actions"}` },
    item.artifactPath ? { label: "Evidence", value: item.artifactPath } : undefined,
  ].filter(Boolean) as Array<{ label: string; value: string }>;
  return (
    <div className="session-file-inspector" data-testid="session-file-inspector" data-status={item.status}>
      <div className="session-file-inspector-head">
        <div>
          <span>{item.status === "failed" ? "Path issue" : item.actions.includes("changed") ? "Changed file" : item.contentPreview ? "Loaded snapshot" : "File evidence"}</span>
          <strong title={item.path}>{item.path === "." ? "workspace root" : item.path}</strong>
          <small>{compactFileMeta(item)}</small>
        </div>
        <b>{statusLabel(item.status)}</b>
      </div>
      {facts.length > 0 ? (
        <dl className="session-file-inspector-facts">
          {facts.map((fact) => (
            <div key={fact.label}>
              <dt>{fact.label}</dt>
              <dd title={fact.value}>{fact.value}</dd>
            </div>
          ))}
        </dl>
      ) : null}
      <span className="session-file-inspector-actions">
        {onOpenWorkspacePath ? (
          <button type="button" className="ghost-action" onClick={() => onOpenWorkspacePath(item.path)}>
            Open current
          </button>
        ) : onOpenWorkspacePanel ? (
          <button type="button" className="ghost-action" onClick={onOpenWorkspacePanel}>
            Open Workspace
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
      <FileEditorStatus source="evidence" path={item.path} lines={item.contentPreview ? fileLines(item).length : undefined} detail={compactFileMeta(item)} />
    </div>
  );
}

function FileEditorStatus({
  source,
  path,
  lines,
  countLabel,
  detail,
}: {
  source: string;
  path: string;
  lines?: number;
  countLabel?: string;
  detail?: string;
}) {
  return (
    <div className="session-files-editor-status" data-testid="session-files-editor-status">
      <span>{source}</span>
      <strong title={path}>{path === "." ? "workspace root" : path}</strong>
      {Number.isFinite(lines) ? <span>{lines} {countLabel ?? (lines === 1 ? "line" : "lines")}</span> : null}
      {detail ? <span>{detail}</span> : null}
    </div>
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
  if (browser.state === "error") return workspaceErrorTitle(browser.error, browser.path);
  return browser.file.title;
}

function workspaceBrowserDetail(browser: WorkspaceFileBrowserState): string {
  if (browser.state === "idle") return browser.workspacePath ? "Open root or enter a workspace-relative path." : "Workspace binding required.";
  if (browser.state === "loading") return "Reading from the session workspace.";
  if (browser.state === "error") return workspaceErrorDetail(browser.error);
  return browser.file.detail;
}

function workspaceBrowserErrorMessage(browser: Extract<WorkspaceFileBrowserState, { state: "error" }>): string {
  return `${workspaceErrorTitle(browser.error, browser.path)}: ${workspaceErrorDetail(browser.error)}`;
}

function workspaceErrorTitle(error: string, path?: string): string {
  if (isMissingWorkspaceError(error)) return "Workspace missing";
  return path ? `Could not open ${path}` : "Workspace unavailable";
}

function workspaceErrorDetail(error: string): string {
  if (isMissingWorkspaceError(error)) return "The saved workspace path no longer exists in this container.";
  return compactError(error);
}

function isMissingWorkspaceError(error: string): boolean {
  return /workspace_unavailable|no such file or directory|lstat .*workspace/i.test(error);
}

function compactError(error: string): string {
  const clean = error.replace(/\s+/g, " ").trim();
  if (clean.length <= 180) return clean;
  return `${clean.slice(0, 177)}...`;
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

function fileEvidenceKindLabel(item: SessionFileEvidence): string {
  if (item.status === "failed") return "Path issue";
  if (item.actions.includes("changed")) return "Changed file";
  if (item.contentPreview) return "Loaded snapshot";
  if (item.actions.includes("listed")) return "Directory listing";
  return "File evidence";
}

function snapshotEditorDetail(item: SessionFileEvidence, lines: number): string {
  const parts = [
    item.contentStale ? "stale snapshot" : item.contentTruncated ? "partial snapshot" : "loaded snapshot",
    `${lines} ${lines === 1 ? "line" : "lines"}`,
    compactFileMeta(item),
  ].filter(Boolean);
  return parts.join(" · ");
}

function isDirectoryEvidence(item: SessionFileEvidence | undefined): boolean {
  return Boolean(item?.actions.includes("listed") && !item.contentPreview && !item.actions.includes("changed"));
}

function fileTreeIcon(item: SessionFileEvidence | undefined, isDirectory: boolean): "directory" | "changed" | "read" | "file" {
  if (isDirectory) return "directory";
  if (item?.actions.includes("changed")) return "changed";
  if (item?.contentPreview) return "read";
  return "file";
}

function displayTreeName(node: FileTreeNode): string {
  if (node.path === "." || node.name === ".") return "workspace root";
  return node.name;
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

function preferredFileEvidence(items: readonly SessionFileEvidence[]): SessionFileEvidence | undefined {
  return items.find((item) => item.contentPreview)
    ?? items.find((item) => item.actions.includes("changed"))
    ?? items.find((item) => item.status === "failed")
    ?? items.find((item) => item.path === ".")
    ?? items[0];
}

function fileAttentionItem(items: readonly SessionFileEvidence[]): SessionFilesReviewItem | undefined {
  return filesReviewQueue(items).find((item) =>
    item.item.status !== "available"
    || item.item.actions.includes("changed")
    || item.action === "open_current"
  );
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
