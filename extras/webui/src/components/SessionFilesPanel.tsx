import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState, type CSSProperties, type DragEvent as ReactDragEvent, type RefObject } from "react";
import type { OnMount } from "@monaco-editor/react";
import type { editor as MonacoEditor } from "monaco-editor";
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
import {
  parentWorkspacePath,
  type WorkspaceFileBrowserState,
  type WorkspaceFileEntryView,
  type WorkspaceFileView,
} from "../view/workspaceFile";
import { CopyButton } from "./CopyButton";
import { HighlightText } from "./HighlightText";

type FileFilter = "all" | "changed" | "snapshots" | "issues" | "listed";
type WorkspaceDraftState = { text: string; savedText: string };

const MonacoReactEditor = lazy(() => import("@monaco-editor/react"));

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
  onUploadWorkspaceFile,
  onOpenWorkspacePanel,
  onOpenArtifact,
  onUseAsDraft,
}: {
  files: SessionFilesView;
  workspaceBrowser?: WorkspaceFileBrowserState;
  defaultOpen?: boolean;
  onOpenWorkspacePath?: (path: string) => void;
  onUploadWorkspaceFile?: (path: string, text: string) => Promise<void> | void;
  onOpenWorkspacePanel?: () => void;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [previewQuery, setPreviewQuery] = useState("");
  const [lineJump, setLineJump] = useState("");
  const [selectedPath, setSelectedPath] = useState<string | undefined>();
  const [selectedEvidencePath, setSelectedEvidencePath] = useState<string | undefined>();
  const [selectedRange, setSelectedRange] = useState<{ path: string; start: number; end: number } | undefined>();
  const [collapsedPaths, setCollapsedPaths] = useState<ReadonlySet<string>>(() => new Set());
  const [filter, setFilter] = useState<FileFilter>("all");
  const [wrapLines, setWrapLines] = useState(true);
  const [uploadStatus, setUploadStatus] = useState<{ tone: "ready" | "error"; text: string } | undefined>();
  const [lastWorkspaceDirectory, setLastWorkspaceDirectory] = useState<WorkspaceFileView | undefined>();
  const [workspaceDirectoryCache, setWorkspaceDirectoryCache] = useState<ReadonlyMap<string, WorkspaceFileView>>(() => new Map());
  const [expandedWorkspacePaths, setExpandedWorkspacePaths] = useState<ReadonlySet<string>>(() => new Set(["."]));
  const [openWorkspacePaths, setOpenWorkspacePaths] = useState<readonly string[]>([]);
  const [workspaceDrafts, setWorkspaceDrafts] = useState<ReadonlyMap<string, WorkspaceDraftState>>(() => new Map());
  const [workspaceSaveErrors, setWorkspaceSaveErrors] = useState<ReadonlyMap<string, string>>(() => new Map());
  const [pendingDirtyClosePath, setPendingDirtyClosePath] = useState<string | undefined>();
  const [showWorkspaceHidden, setShowWorkspaceHidden] = useState(false);
  const [draggingUpload, setDraggingUpload] = useState(false);
  const [creatingWorkspaceFile, setCreatingWorkspaceFile] = useState(false);
  const trimmedQuery = query.trim();
  const stats = fileStats(files);
  const filteredItems = filter === "all" ? files.items : files.items.filter((item) => fileMatchesFilter(item, filter));
  const visibleItems = trimmedQuery ? filteredItems.filter((item) => fileMatchesQuery(item, trimmedQuery)) : filteredItems;
  const canOpenWorkspacePath = Boolean(onOpenWorkspacePath);
  const treeNodes = useMemo(() => buildFileTree(visibleItems), [visibleItems]);
  const snapshotItems = visibleItems.filter((item) => item.contentPreview);
  const selectedEvidenceCandidate = visibleItems.find((item) => item.path === selectedEvidencePath);
  const selectedItem = selectedEvidenceCandidate?.contentPreview
    ? selectedEvidenceCandidate
    : selectedPath
      ? snapshotItems.find((item) => item.path === selectedPath)
      : selectedEvidenceCandidate || canOpenWorkspacePath
        ? undefined
        : snapshotItems[0];
  const selectedEvidence = selectedEvidenceCandidate
    ?? selectedItem
    ?? (canOpenWorkspacePath ? undefined : preferredFileEvidence(visibleItems));
  const snapshotLines = selectedItem ? fileLines(selectedItem) : [];
  const activeRange = selectedItem && selectedRange?.path === selectedItem.path ? selectedRange : undefined;
  const workspaceReady = workspaceBrowser?.state === "ready" ? workspaceBrowser.file : undefined;
  const currentWorkspaceDirectory = workspaceReady?.kind === "directory" ? workspaceReady : undefined;
  const workspaceDirectory = currentWorkspaceDirectory ?? lastWorkspaceDirectory;
  const workspaceTreeDirectory = workspaceDirectoryCache.get(".") ?? currentWorkspaceDirectory ?? lastWorkspaceDirectory;
  const workspaceCurrentPath = workspaceBrowser?.state === "ready"
    ? workspaceBrowser.file.path
    : workspaceBrowser?.state === "loading" || workspaceBrowser?.state === "error"
      ? workspaceBrowser.path ?? "."
      : ".";
  const workspaceParent = workspaceReady ? parentWorkspacePath(workspaceReady.path) : undefined;
  const canUploadWorkspaceFile = Boolean(onUploadWorkspaceFile && canOpenWorkspacePath);
  const workspaceMissing = workspaceBrowser?.state === "error" && isMissingWorkspaceError(workspaceBrowser.error);
  const canManageWorkspaceFile = canUploadWorkspaceFile && !workspaceMissing && workspaceBrowser?.state !== "loading";
  const workspaceRootPath = canOpenWorkspacePath ? workspaceBrowser?.workspacePath ?? "Workspace not attached" : "Recorded file evidence";
  const dirtyWorkspacePaths = useMemo(() => {
    const next = new Set<string>();
    workspaceDrafts.forEach((draft, path) => {
      if (draft.text !== draft.savedText) next.add(path);
    });
    return next;
  }, [workspaceDrafts]);
  const activeWorkspaceDraft = workspaceReady?.kind === "file" ? workspaceDrafts.get(workspaceReady.path) : undefined;
  const activeWorkspaceSaveError = workspaceReady?.kind === "file" ? workspaceSaveErrors.get(workspaceReady.path) : undefined;
  const filesIdeRef = useRef<HTMLDivElement | null>(null);
  const previewCodeRef = useRef<HTMLDivElement | null>(null);
  const uploadInputRef = useRef<HTMLInputElement | null>(null);
  const autoOpenedWorkspaceRef = useRef<string | undefined>(undefined);
  const dragDepthRef = useRef(0);
  const editorTitle = workspaceReady
    ? workspaceAbsoluteDisplayPath(workspaceReady.path, workspaceRootPath)
    : workspaceMissing
      ? "No workspace"
    : selectedItem?.path ?? selectedEvidence?.path ?? "No file selected";
  const editorDetail = workspaceReady
    ? workspaceReady.kind === "directory" ? workspaceDirectorySummary(workspaceReady, showWorkspaceHidden) : workspaceReady.detail
    : workspaceMissing
      ? "Saved workspace path unavailable."
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

  useEffect(() => {
    if (workspaceBrowser?.state === "ready" && workspaceBrowser.revealDirectories?.length) {
      setWorkspaceDirectoryCache((current) => {
        const next = new Map(current);
        for (const directory of workspaceBrowser.revealDirectories ?? []) {
          if (directory.kind === "directory") next.set(directory.path, directory);
        }
        return next;
      });
    }
    if (workspaceReady?.kind === "directory") {
      setLastWorkspaceDirectory(workspaceReady);
      setWorkspaceDirectoryCache((current) => {
        const next = new Map(current);
        next.set(workspaceReady.path, workspaceReady);
        return next;
      });
      setExpandedWorkspacePaths((current) => {
        const revealPaths = workspaceAncestorDirectoryPaths(workspaceReady.path, true);
        if (revealPaths.every((path) => current.has(path))) return current;
        const next = new Set(current);
        for (const path of revealPaths) next.add(path);
        return next;
      });
    } else if (workspaceReady?.kind === "file") {
      setExpandedWorkspacePaths((current) => {
        const revealPaths = workspaceAncestorDirectoryPaths(workspaceReady.path, false);
        if (revealPaths.every((path) => current.has(path))) return current;
        const next = new Set(current);
        for (const path of revealPaths) next.add(path);
        return next;
      });
      setOpenWorkspacePaths((current) => current.includes(workspaceReady.path) ? current : [...current, workspaceReady.path]);
    }
  }, [workspaceBrowser, workspaceReady]);

  useEffect(() => {
    if (!workspaceReady) return;
    window.requestAnimationFrame(() => {
      revealWorkspaceTreePath(filesIdeRef.current, workspaceReady.path);
    });
  }, [workspaceReady, expandedWorkspacePaths, workspaceDirectoryCache, trimmedQuery, showWorkspaceHidden]);

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

  function toggleTreePath(path: string) {
    setCollapsedPaths((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }

  function toggleWorkspaceDirectory(path: string) {
    setExpandedWorkspacePaths((current) => {
      const next = new Set(current);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }

  function closeWorkspaceTab(path: string) {
    if (dirtyWorkspacePaths.has(path)) {
      setPendingDirtyClosePath(path);
      return;
    }
    discardWorkspaceTab(path);
  }

  function discardWorkspaceTab(path: string) {
    setOpenWorkspacePaths((current) => {
      const next = current.filter((item) => item !== path);
      if (workspaceReady?.kind === "file" && workspaceReady.path === path) {
        const fallback = next.at(-1) ?? workspaceDirectory?.path ?? ".";
        onOpenWorkspacePath?.(fallback);
      }
      return next;
    });
    setWorkspaceDrafts((current) => {
      if (!current.has(path)) return current;
      const next = new Map(current);
      next.delete(path);
      return next;
    });
    setWorkspaceSaveErrors((current) => {
      if (!current.has(path)) return current;
      const next = new Map(current);
      next.delete(path);
      return next;
    });
    setPendingDirtyClosePath((current) => current === path ? undefined : current);
  }

  const updateWorkspaceDraft = useCallback((path: string, draft: WorkspaceDraftState) => {
    setWorkspaceDrafts((current) => {
      const currentDraft = current.get(path);
      if (currentDraft?.text === draft.text && currentDraft.savedText === draft.savedText) return current;
      const next = new Map(current);
      next.set(path, draft);
      return next;
    });
    setWorkspaceSaveErrors((current) => {
      if (!current.has(path)) return current;
      const next = new Map(current);
      next.delete(path);
      return next;
    });
  }, []);

  const markWorkspaceDraftSaved = useCallback((path: string, text: string) => {
    setWorkspaceDrafts((current) => {
      const currentDraft = current.get(path);
      if (currentDraft?.text === text && currentDraft.savedText === text) return current;
      const next = new Map(current);
      next.set(path, { text, savedText: text });
      return next;
    });
    setWorkspaceSaveErrors((current) => {
      if (!current.has(path)) return current;
      const next = new Map(current);
      next.delete(path);
      return next;
    });
  }, []);

  const updateWorkspaceSaveError = useCallback((path: string, error?: string) => {
    setWorkspaceSaveErrors((current) => {
      const currentError = current.get(path);
      if (currentError === error || (!currentError && !error)) return current;
      const next = new Map(current);
      if (error) next.set(path, error);
      else next.delete(path);
      return next;
    });
  }, []);

  async function uploadWorkspaceFiles(fileList: FileList | readonly File[] | undefined | null) {
    if (!fileList || !onUploadWorkspaceFile) return;
    const uploadFiles = Array.from(fileList).filter((file) => file.size >= 0);
    if (uploadFiles.length === 0) return;
    const base = workspaceUploadDirectory(workspaceReady, workspaceCurrentPath);
    setUploadStatus({ tone: "ready", text: uploadFiles.length === 1 ? `Uploading ${joinWorkspacePath(base, uploadFiles[0].name)}...` : `Uploading ${uploadFiles.length} files to ${base === "." ? "workspace root" : base}...` });
    try {
      let lastTarget = "";
      for (const file of uploadFiles) {
        const targetPath = joinWorkspacePath(base, file.webkitRelativePath || file.name);
        const text = await readUploadFileText(file);
        await onUploadWorkspaceFile(targetPath, text);
        lastTarget = targetPath;
      }
      setUploadStatus({ tone: "ready", text: uploadFiles.length === 1 ? `Uploaded ${lastTarget}` : `Uploaded ${uploadFiles.length} files to ${base === "." ? "workspace root" : base}` });
    } catch (err) {
      setUploadStatus({ tone: "error", text: compactError(err instanceof Error ? err.message : String(err)) });
    } finally {
      if (uploadInputRef.current) uploadInputRef.current.value = "";
    }
  }

  async function createWorkspaceFile() {
    if (!onUploadWorkspaceFile || !onOpenWorkspacePath || creatingWorkspaceFile) return;
    const base = workspaceUploadDirectory(workspaceReady, workspaceCurrentPath);
    const targetPath = nextUntitledWorkspacePath(workspaceDirectory, base);
    setCreatingWorkspaceFile(true);
    setUploadStatus({ tone: "ready", text: `Creating ${targetPath}...` });
    try {
      await onUploadWorkspaceFile(targetPath, "");
      setOpenWorkspacePaths((current) => current.includes(targetPath) ? current : [...current, targetPath]);
      setUploadStatus({ tone: "ready", text: `Created ${targetPath}` });
      onOpenWorkspacePath(targetPath);
    } catch (err) {
      setUploadStatus({ tone: "error", text: compactError(err instanceof Error ? err.message : String(err)) });
    } finally {
      setCreatingWorkspaceFile(false);
    }
  }

  function handleUploadDragEnter(event: ReactDragEvent<HTMLElement>) {
    if (!canManageWorkspaceFile) return;
    event.preventDefault();
    dragDepthRef.current += 1;
    setDraggingUpload(true);
  }

  function handleUploadDragOver(event: ReactDragEvent<HTMLElement>) {
    if (!canManageWorkspaceFile) return;
    event.preventDefault();
    event.dataTransfer.dropEffect = "copy";
  }

  function handleUploadDragLeave(event: ReactDragEvent<HTMLElement>) {
    if (!canManageWorkspaceFile) return;
    event.preventDefault();
    dragDepthRef.current = Math.max(0, dragDepthRef.current - 1);
    if (dragDepthRef.current === 0) setDraggingUpload(false);
  }

  function handleUploadDrop(event: ReactDragEvent<HTMLElement>) {
    if (!canManageWorkspaceFile) return;
    event.preventDefault();
    dragDepthRef.current = 0;
    setDraggingUpload(false);
    void uploadWorkspaceFiles(event.dataTransfer.files);
  }

  return (
    <section className="session-skills-panel session-files-panel" data-testid="session-files-panel" data-surface="true">
      <div className="session-skills-body">
        <div
          ref={filesIdeRef}
          className="session-files-ide"
          data-testid="session-files-ide"
          data-dragging={draggingUpload ? "true" : "false"}
          onDragEnter={handleUploadDragEnter}
          onDragOver={handleUploadDragOver}
          onDragLeave={handleUploadDragLeave}
          onDrop={handleUploadDrop}
        >
          {canManageWorkspaceFile ? (
            <div className="session-files-drop-overlay" aria-hidden={!draggingUpload}>
              <strong>Drop files to upload</strong>
              <span>{workspaceUploadDirectory(workspaceReady, workspaceCurrentPath) === "." ? "workspace root" : workspaceUploadDirectory(workspaceReady, workspaceCurrentPath)}</span>
            </div>
          ) : null}
          {canManageWorkspaceFile ? (
            <input
              ref={uploadInputRef}
              className="visually-hidden"
              type="file"
              multiple
              onChange={(event) => void uploadWorkspaceFiles(event.target.files)}
            />
          ) : null}
          <aside className="session-files-explorer" aria-label="File explorer">
            {!canOpenWorkspacePath ? (
              <div className="session-files-explorer-status" data-state="unbound">
                <span>Workspace unavailable</span>
                <strong>Recorded evidence only</strong>
                {onOpenWorkspacePanel ? <button type="button" className="ghost-action" onClick={onOpenWorkspacePanel}>Open Workspace</button> : null}
              </div>
            ) : null}
            {workspaceBrowser?.state === "loading" ? <div className="session-skills-empty">Loading {workspaceBrowser.path}...</div> : null}
            {workspaceMissing ? <div className="session-files-tree-placeholder">Workspace path unavailable</div> : null}
            {workspaceBrowser?.state === "error" && !workspaceMissing ? <div className="session-skills-empty">{workspaceBrowserErrorMessage(workspaceBrowser)}</div> : null}
            {uploadStatus ? <div className="session-files-upload-status" data-tone={uploadStatus.tone}>{uploadStatus.text}</div> : null}
            {!workspaceMissing ? (
              <div className="session-files-explorer-tools">
                <label className="session-files-explorer-filter">
                  <span className="visually-hidden">Filter files</span>
                  <input aria-label="Filter files" value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Filter files" />
                </label>
                <span className="session-files-explorer-tool-actions">
                  <button
                    type="button"
                    className="session-workspace-icon-action"
                    data-icon="hidden"
                    data-active={showWorkspaceHidden ? "true" : "false"}
                    aria-label={showWorkspaceHidden ? "Hide hidden files" : "Show hidden files"}
                    title={showWorkspaceHidden ? "Hide hidden files" : "Show hidden files"}
                    onClick={() => setShowWorkspaceHidden((value) => !value)}
                  >
                    <span className="visually-hidden">{showWorkspaceHidden ? "Hide hidden files" : "Show hidden files"}</span>
                  </button>
                  {trimmedQuery ? (
                    <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                      Clear
                    </button>
                  ) : null}
                </span>
              </div>
            ) : null}
            {workspaceTreeDirectory ? (
              <WorkspaceDirectory
                file={workspaceTreeDirectory}
                parent={undefined}
                rootLabel={workspaceRootPath}
                workspaceRootPath={workspaceRootPath}
                query={trimmedQuery}
                selectedPath={workspaceReady?.path}
                directoryCache={workspaceDirectoryCache}
                expandedPaths={expandedWorkspacePaths}
                openPaths={openWorkspacePaths}
                dirtyPaths={dirtyWorkspacePaths}
                saveErrorPaths={workspaceSaveErrors}
                showHidden={showWorkspaceHidden}
                onToggleDirectory={toggleWorkspaceDirectory}
                onOpenPath={onOpenWorkspacePath}
              />
            ) : null}
            {!canOpenWorkspacePath ? (
              <details className="session-files-evidence-drawer" open={!canOpenWorkspacePath}>
                <summary>
                  <span>Agent evidence</span>
                  <strong>{files.items.length}</strong>
                </summary>
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
              </details>
            ) : null}
          </aside>
          <section className="session-files-editor" aria-label="File editor preview">
            {openWorkspacePaths.length > 0 ? (
              <WorkspaceOpenTabs
                paths={openWorkspacePaths}
                activePath={workspaceReady?.kind === "file" ? workspaceReady.path : undefined}
                dirtyPaths={dirtyWorkspacePaths}
                saveErrorPaths={workspaceSaveErrors}
                onSelectPath={onOpenWorkspacePath}
                onClosePath={closeWorkspaceTab}
              />
            ) : null}
            {pendingDirtyClosePath && dirtyWorkspacePaths.has(pendingDirtyClosePath) ? (
              <WorkspaceDirtyClosePrompt
                path={pendingDirtyClosePath}
                onCancel={() => setPendingDirtyClosePath(undefined)}
                onDiscard={() => discardWorkspaceTab(pendingDirtyClosePath)}
              />
            ) : null}
            <div className="session-files-editor-chrome" data-testid="session-files-editor-chrome">
              <div>
                <span>{workspaceReady ? "Workspace" : selectedItem ? "Snapshot" : selectedEvidence ? "Evidence" : "Editor"}</span>
                <strong title={editorTitle}>{editorTitle}</strong>
              </div>
              <small>{editorDetail}</small>
            </div>
            {workspaceReady?.kind === "directory" ? (
              <WorkspaceDirectoryPreview
                file={workspaceReady}
                parent={workspaceParent}
                workspaceRootPath={workspaceRootPath}
                query={trimmedQuery}
                onOpenPath={onOpenWorkspacePath}
                onCreateFile={canManageWorkspaceFile ? () => void createWorkspaceFile() : undefined}
                onUploadFiles={canManageWorkspaceFile ? () => uploadInputRef.current?.click() : undefined}
                  creatingFile={creatingWorkspaceFile}
                  showHidden={showWorkspaceHidden}
                  onToggleHidden={() => setShowWorkspaceHidden((value) => !value)}
                />
              ) : workspaceReady?.kind === "file" ? (
              <WorkspaceFilePreview
                file={workspaceReady}
                parent={workspaceParent}
                workspaceRootPath={workspaceRootPath}
                onOpenPath={onOpenWorkspacePath}
                onWriteFile={onUploadWorkspaceFile}
                draft={activeWorkspaceDraft}
                saveError={activeWorkspaceSaveError}
                onDraftChange={updateWorkspaceDraft}
                onDraftSaved={markWorkspaceDraftSaved}
                onSaveError={updateWorkspaceSaveError}
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
                        Open file
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
              />
            ) : (
              <div className="session-files-editor-empty" data-testid="session-files-editor-empty">
                <strong>No file open</strong>
                <span>{workspaceMissing ? "Workspace unavailable." : "Select a file reference or open a workspace path."}</span>
                {!workspaceMissing && (canOpenWorkspacePath || onOpenWorkspacePanel) ? (
                  <span className="session-files-empty-actions">
                    {canManageWorkspaceFile ? (
                      <button type="button" className="ghost-action primary-run-action" disabled={creatingWorkspaceFile} onClick={() => void createWorkspaceFile()}>
                        New file
                      </button>
                    ) : null}
                    <button type="button" className="ghost-action" onClick={() => (canOpenWorkspacePath ? onOpenWorkspacePath?.(".") : onOpenWorkspacePanel?.())}>
                      {canOpenWorkspacePath ? "Open root" : "Open Workspace"}
                    </button>
                  </span>
                ) : null}
              </div>
            )}
          </section>
        </div>
      </div>
    </section>
  );
}

function workspaceUploadDirectory(file: WorkspaceFileView | undefined, currentPath: string): string {
  if (file?.kind === "directory") return file.path;
  if (file?.kind === "file") return parentWorkspacePath(file.path) ?? ".";
  return parentWorkspacePath(currentPath) ?? ".";
}

function workspaceDisplayPath(path: string): string {
  return path === "." ? "Project root" : path;
}

function workspaceAbsoluteDisplayPath(path: string, rootPath: string): string {
  const cleanRoot = rootPath.trim().replace(/\/+$/, "");
  if (!cleanRoot || cleanRoot === "Workspace not attached" || cleanRoot === "Recorded file evidence") {
    return workspaceDisplayPath(path);
  }
  if (path === ".") return cleanRoot;
  return `${cleanRoot}/${path.replace(/^\/+/, "")}`;
}

function workspaceAncestorDirectoryPaths(path: string, includeSelf: boolean): string[] {
  if (path === ".") return ["."];
  const parts = path.replace(/^\/+|\/+$/g, "").split("/").filter(Boolean);
  const directoryDepth = includeSelf ? parts.length : Math.max(0, parts.length - 1);
  const ancestors = ["."];
  for (let index = 1; index <= directoryDepth; index += 1) {
    ancestors.push(parts.slice(0, index).join("/"));
  }
  return ancestors;
}

function revealWorkspaceTreePath(root: HTMLElement | null, path: string) {
  const entries = root?.querySelectorAll<HTMLElement>('[data-testid="session-workspace-browser-entry"]');
  const target = Array.from(entries ?? []).find((entry) => entry.dataset.path === path);
  target?.scrollIntoView?.({ block: "nearest" });
}

function workspaceDirectorySummary(file: WorkspaceFileView, showHidden = false): string {
  if (file.kind !== "directory") return file.detail;
  const entries = workspaceDirectoryEntries(file, undefined, showHidden);
  if (entries.length === 0) return workspaceHiddenEntryCount(file) > 0 ? "Only hidden files" : "Ready";
  const files = entries.filter((entry) => entry.kind === "file").length;
  const directories = entries.filter((entry) => entry.kind === "directory").length;
  return [
    directories ? `${directories} ${directories === 1 ? "folder" : "folders"}` : "",
    files ? `${files} ${files === 1 ? "file" : "files"}` : "",
  ].filter(Boolean).join(" · ") || "Ready";
}

function joinWorkspacePath(base: string | undefined, name: string): string {
  const cleanName = cleanUploadRelativePath(name);
  const cleanBase = (base ?? ".").replace(/\\/g, "/").replace(/^\/+/, "").replace(/\/+$/, "") || ".";
  return cleanBase === "." ? cleanName : `${cleanBase}/${cleanName}`;
}

function cleanUploadRelativePath(name: string): string {
  const clean = name.replace(/\\/g, "/").split("/").filter((part) => part && part !== "." && part !== "..").join("/");
  return clean || "upload.txt";
}

function nextUntitledWorkspacePath(directory: WorkspaceFileView | undefined, base: string): string {
  const existingNames = new Set(
    directory?.kind === "directory"
      ? directory.entries.map((entry) => entry.name.toLowerCase())
      : [],
  );
  let name = "untitled.txt";
  let index = 1;
  while (existingNames.has(name.toLowerCase())) {
    name = `untitled-${index}.txt`;
    index += 1;
  }
  return joinWorkspacePath(base, name);
}

function workspaceTextDownloadHref(text: string): string {
  return `data:text/plain;charset=utf-8,${encodeURIComponent(text)}`;
}

function workspaceDownloadName(path: string): string {
  return path.split("/").filter(Boolean).at(-1) ?? "workspace-file.txt";
}

function workspaceLanguageForPath(path: string): string {
  const fileName = workspaceDownloadName(path).toLowerCase();
  if (fileName === "dockerfile" || fileName.endsWith(".dockerfile")) return "dockerfile";
  if (fileName === "makefile" || fileName.endsWith(".mk")) return "makefile";
  const extension = fileName.split(".").at(-1) ?? "";
  const byExtension: Record<string, string> = {
    c: "c",
    cc: "cpp",
    cpp: "cpp",
    cs: "csharp",
    css: "css",
    diff: "diff",
    go: "go",
    h: "cpp",
    hpp: "cpp",
    html: "html",
    java: "java",
    js: "javascript",
    json: "json",
    jsx: "javascript",
    kt: "kotlin",
    less: "less",
    lua: "lua",
    md: "markdown",
    mjs: "javascript",
    php: "php",
    py: "python",
    rb: "ruby",
    rs: "rust",
    scss: "scss",
    sh: "shell",
    sql: "sql",
    svelte: "html",
    toml: "toml",
    ts: "typescript",
    tsx: "typescript",
    txt: "plaintext",
    vue: "html",
    xml: "xml",
    yaml: "yaml",
    yml: "yaml",
    zig: "zig",
  };
  return byExtension[extension] ?? "plaintext";
}

function workspaceLanguageDisplay(language: string): string {
  const labels: Record<string, string> = {
    cpp: "C++",
    csharp: "C#",
    css: "CSS",
    html: "HTML",
    javascript: "JavaScript",
    json: "JSON",
    markdown: "Markdown",
    plaintext: "Text",
    shell: "Shell",
    sql: "SQL",
    typescript: "TypeScript",
    xml: "XML",
    yaml: "YAML",
  };
  return labels[language] ?? language.replace(/^\w/, (char) => char.toUpperCase());
}

function readUploadFileText(file: File): Promise<string> {
  if (typeof file.text === "function") return file.text();
  return new Promise((resolve, reject) => {
    const reader = new FileReader();
    reader.onload = () => resolve(String(reader.result ?? ""));
    reader.onerror = () => reject(reader.error ?? new Error("Could not read file"));
    reader.readAsText(file);
  });
}

function countTextMatches(text: string, query: string): number {
  if (!query) return 0;
  const escaped = query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
  return text.match(new RegExp(escaped, "gi"))?.length ?? 0;
}

function WorkspaceOpenTabs({
  paths,
  activePath,
  dirtyPaths,
  saveErrorPaths,
  onSelectPath,
  onClosePath,
}: {
  paths: readonly string[];
  activePath?: string;
  dirtyPaths: ReadonlySet<string>;
  saveErrorPaths: ReadonlyMap<string, string>;
  onSelectPath?: (path: string) => void;
  onClosePath: (path: string) => void;
}) {
  return (
    <div className="session-files-open-tabs" aria-label="Open files">
      {paths.map((path) => {
        const dirty = dirtyPaths.has(path);
        const saveError = saveErrorPaths.has(path);
        return (
        <span key={path} className="session-files-open-tab" data-active={path === activePath ? "true" : "false"} data-dirty={dirty ? "true" : "false"} data-error={saveError ? "true" : "false"}>
          <button type="button" title={path} onClick={() => onSelectPath?.(path)} disabled={!onSelectPath}>
            <span>{workspaceDownloadName(path)}</span>
            {saveError ? <span className="visually-hidden"> save failed</span> : null}
            {dirty ? <span className="visually-hidden"> unsaved</span> : null}
          </button>
          <button type="button" aria-label={`Close ${path}`} onClick={() => onClosePath(path)}>
            ×
          </button>
        </span>
        );
      })}
    </div>
  );
}

function WorkspaceDirtyClosePrompt({
  path,
  onCancel,
  onDiscard,
}: {
  path: string;
  onCancel: () => void;
  onDiscard: () => void;
}) {
  return (
    <div className="session-files-close-prompt" data-testid="session-files-close-prompt">
      <span>
        Unsaved <strong title={path}>{workspaceDownloadName(path)}</strong>
      </span>
      <button type="button" className="ghost-action" onClick={onCancel}>
        Cancel
      </button>
      <button type="button" className="ghost-action danger-action" onClick={onDiscard}>
        Discard
      </button>
    </div>
  );
}

function WorkspaceDirectory({
  file,
  parent,
  rootLabel,
  workspaceRootPath,
  query,
  selectedPath,
  directoryCache,
  expandedPaths,
  openPaths,
  dirtyPaths,
  saveErrorPaths,
  showHidden,
  onToggleDirectory,
  onOpenPath,
}: {
  file: WorkspaceFileView;
  parent?: string;
  rootLabel: string;
  workspaceRootPath: string;
  query?: string;
  selectedPath?: string;
  directoryCache: ReadonlyMap<string, WorkspaceFileView>;
  expandedPaths: ReadonlySet<string>;
  openPaths: readonly string[];
  dirtyPaths: ReadonlySet<string>;
  saveErrorPaths: ReadonlyMap<string, string>;
  showHidden: boolean;
  onToggleDirectory: (path: string) => void;
  onOpenPath?: (path: string) => void;
}) {
  const entries = workspaceDirectoryEntries(file, query, showHidden);
  const openPathSet = useMemo(() => new Set(openPaths), [openPaths]);
  return (
    <div className="session-workspace-browser-body">
      {parent ? (
        <span className="session-evidence-actions">
          <button type="button" className="ghost-action" onClick={() => onOpenPath?.(parent)}>
            Up
          </button>
        </span>
      ) : null}
      {entries.length > 0 ? (
        <ol className="session-workspace-browser-list" data-testid="session-workspace-browser-list" aria-label="Workspace directory tree">
          <li className="session-workspace-browser-entry" data-testid="session-workspace-browser-entry" data-path="." data-kind="directory" data-selected={selectedPath === "." ? "true" : "false"} data-expanded="true">
            <span className="session-workspace-browser-row session-workspace-browser-root" style={{ "--depth": 0 } as CSSProperties}>
              <button
                type="button"
                className="session-workspace-browser-toggle"
                aria-label="Open workspace root"
                onClick={() => onOpenPath?.(".")}
              />
              <span className="session-workspace-browser-icon session-files-tree-icon" aria-hidden="true">
                <span data-icon="directory" />
              </span>
              <button type="button" className="session-workspace-browser-name" onClick={() => onOpenPath?.(".")} disabled={!onOpenPath}>
                <strong title={rootLabel}>{rootLabel}</strong>
              </button>
              <span className="session-workspace-browser-actions">
                <CopyButton
                  label="Copy workspace root path"
                  displayLabel="Copy"
                  value={workspaceAbsoluteDisplayPath(".", workspaceRootPath)}
                  className="session-workspace-tree-action"
                  title="Copy path"
                />
              </span>
            </span>
          </li>
          {entries.map((entry) => (
            <WorkspaceEntry
              key={entry.path}
              entry={entry}
              depth={1}
              workspaceRootPath={workspaceRootPath}
              selectedPath={selectedPath}
              directoryCache={directoryCache}
              expandedPaths={expandedPaths}
              openPaths={openPathSet}
              dirtyPaths={dirtyPaths}
              saveErrorPaths={saveErrorPaths}
              showHidden={showHidden}
              onToggleDirectory={onToggleDirectory}
              onOpenPath={onOpenPath}
            />
          ))}
        </ol>
      ) : (
        <div className="session-skills-empty">{query ? `No entries matching "${query}".` : "No files yet."}</div>
      )}
      {file.hasMore ? <small className="session-workspace-browser-more">More entries are available; open a narrower path.</small> : null}
    </div>
  );
}

function WorkspaceDirectoryPreview({
  file,
  parent,
  workspaceRootPath,
  query,
  onOpenPath,
  onCreateFile,
  onUploadFiles,
  creatingFile = false,
  showHidden = false,
  onToggleHidden,
}: {
  file: WorkspaceFileView;
  parent?: string;
  workspaceRootPath: string;
  query?: string;
  onOpenPath?: (path: string) => void;
  onCreateFile?: () => void;
  onUploadFiles?: () => void;
  creatingFile?: boolean;
  showHidden?: boolean;
  onToggleHidden?: () => void;
}) {
  const entries = workspaceDirectoryEntries(file, query, showHidden);
  const hiddenCount = workspaceHiddenEntryCount(file, query);
  return (
    <div className="session-file-preview session-workspace-directory-preview" data-testid="session-workspace-directory-preview">
      <div className="session-workspace-directory-toolbar">
        <span className="session-workspace-toolbar-group">
          {parent ? (
            <button type="button" className="session-workspace-text-action" data-icon="parent" aria-label="Parent directory" title="Parent directory" onClick={() => onOpenPath?.(parent)}>
              <span>Up</span>
            </button>
          ) : null}
          <CopyButton label="Copy current path" displayLabel="Copy" value={workspaceAbsoluteDisplayPath(file.path, workspaceRootPath)} className="session-workspace-text-action" title="Copy path" />
        </span>
        {onCreateFile || onUploadFiles ? (
          <span className="session-workspace-toolbar-group">
          {onCreateFile ? (
            <button type="button" className="session-workspace-text-action" data-icon="new" aria-label={creatingFile ? "Creating file" : "New file"} title={creatingFile ? "Creating file" : "New file"} disabled={creatingFile} onClick={onCreateFile}>
              <span>{creatingFile ? "Creating" : "New"}</span>
            </button>
          ) : null}
          {onUploadFiles ? (
            <button type="button" className="session-workspace-text-action" aria-label="Upload files" title="Upload files" onClick={onUploadFiles}>
              <span>Upload</span>
            </button>
          ) : null}
          </span>
        ) : null}
        {hiddenCount > 0 && onToggleHidden ? (
          <span className="session-workspace-toolbar-group">
            <button
              type="button"
              className="session-workspace-text-action"
              data-icon="hidden"
              data-active={showHidden ? "true" : "false"}
              aria-label={showHidden ? "Hide hidden files" : "Show hidden files"}
              title={showHidden ? "Hide hidden files" : "Show hidden files"}
              onClick={onToggleHidden}
            >
              <span>{showHidden ? "Hide hidden" : "Hidden"}</span>
            </button>
          </span>
        ) : null}
      </div>
      {entries.length > 0 ? (
        <div className="session-workspace-directory-grid" data-testid="session-workspace-directory-table">
          <div className="session-workspace-directory-header" aria-hidden="true">
            <span>Name</span>
            <span>Size</span>
            <span>Modified</span>
            <span />
          </div>
          <ol className="session-workspace-directory-table" aria-label="Workspace directory entries">
            {entries.map((entry) => (
              <WorkspaceDirectoryPreviewEntry key={entry.path} entry={entry} workspaceRootPath={workspaceRootPath} onOpenPath={onOpenPath} />
            ))}
          </ol>
        </div>
      ) : (
        <div className="session-files-editor-empty session-workspace-ide-empty" data-testid="session-workspace-empty-directory">
          <strong>{query ? "No matching files" : hiddenCount > 0 ? "Only hidden files" : "Ready for files"}</strong>
          <span>{query ? `No entries match "${query}".` : hiddenCount > 0 ? "Show hidden files to inspect runtime or dot directories." : "Create a file, upload files, or drop them into this panel."}</span>
          {onCreateFile || onUploadFiles ? (
            <span className="session-files-empty-actions">
              {onCreateFile ? (
                <button type="button" className="ghost-action primary-run-action" disabled={creatingFile} onClick={onCreateFile}>
                  {creatingFile ? "Creating" : "New file"}
                </button>
              ) : null}
              {onUploadFiles ? (
                <button type="button" className="ghost-action" onClick={onUploadFiles}>
                  Upload files
                </button>
              ) : null}
              {hiddenCount > 0 && onToggleHidden ? (
                <button type="button" className="ghost-action" onClick={onToggleHidden}>
                  Show hidden
                </button>
              ) : null}
            </span>
          ) : null}
        </div>
      )}
      {file.hasMore ? <small className="session-workspace-browser-more">More entries are available; open a narrower path.</small> : null}
    </div>
  );
}

function workspaceDirectoryEntries(file: WorkspaceFileView, query?: string, showHidden = false): WorkspaceFileEntryView[] {
  const entries = [...file.entries].sort(compareWorkspaceEntries);
  const cleanQuery = query?.trim();
  const visibleEntries = showHidden || cleanQuery ? entries : entries.filter((entry) => !isHiddenWorkspaceEntry(entry));
  if (!cleanQuery) return visibleEntries;
  return visibleEntries.filter((entry) => workspaceEntryMatchesQuery(entry, cleanQuery));
}

function workspaceHiddenEntryCount(file: WorkspaceFileView, query?: string): number {
  const cleanQuery = query?.trim();
  return file.entries.filter((entry) => isHiddenWorkspaceEntry(entry) && (!cleanQuery || workspaceEntryMatchesQuery(entry, cleanQuery))).length;
}

function workspaceEntryMatchesQuery(entry: WorkspaceFileEntryView, query: string): boolean {
  const haystack = [entry.name, entry.path, entry.kind, entry.size].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
}

function WorkspaceDirectoryPreviewEntry({
  entry,
  workspaceRootPath,
  onOpenPath,
}: {
  entry: WorkspaceFileEntryView;
  workspaceRootPath: string;
  onOpenPath?: (path: string) => void;
}) {
  const size = workspaceDirectoryEntrySize(entry);
  const modified = workspaceDirectoryEntryModified(entry);
  return (
    <li className="session-workspace-directory-row" data-testid="session-workspace-directory-row" data-kind={entry.kind} data-path={entry.path}>
      <button type="button" className="session-workspace-directory-open" onClick={() => onOpenPath?.(entry.path)} disabled={!onOpenPath}>
        <span className="session-files-tree-icon" aria-hidden="true">
          <span data-icon={entry.kind === "directory" ? "directory" : "file"} />
        </span>
        <strong title={entry.path}>{entry.name}</strong>
      </button>
      <small>{size}</small>
      <time dateTime={entry.modTime ?? undefined}>{modified}</time>
      <span className="session-workspace-directory-row-actions">
        <button
          type="button"
          className="session-workspace-row-action"
          aria-label={`Open ${entry.path}`}
          title="Open"
          onClick={() => onOpenPath?.(entry.path)}
          disabled={!onOpenPath}
        >
          Open
        </button>
        <CopyButton
          label="Copy path"
          displayLabel="Copy"
          value={workspaceAbsoluteDisplayPath(entry.path, workspaceRootPath)}
          className="session-workspace-row-action"
          title="Copy path"
        />
      </span>
    </li>
  );
}

function workspaceDirectoryEntrySize(entry: WorkspaceFileEntryView): string {
  return entry.kind === "file" ? entry.size ?? "" : "";
}

function workspaceDirectoryEntryModified(entry: WorkspaceFileEntryView): string {
  if (!entry.modTime) return "";
  const date = new Date(entry.modTime);
  if (Number.isNaN(date.getTime())) return "";
  const mm = String(date.getUTCMonth() + 1).padStart(2, "0");
  const dd = String(date.getUTCDate()).padStart(2, "0");
  const hh = String(date.getUTCHours()).padStart(2, "0");
  const min = String(date.getUTCMinutes()).padStart(2, "0");
  return `${mm}-${dd} ${hh}:${min}`;
}

function WorkspaceEntry({
  entry,
  depth,
  workspaceRootPath,
  selectedPath,
  directoryCache,
  expandedPaths,
  openPaths,
  dirtyPaths,
  saveErrorPaths,
  showHidden,
  onToggleDirectory,
  onOpenPath,
}: {
  entry: WorkspaceFileEntryView;
  depth: number;
  workspaceRootPath: string;
  selectedPath?: string;
  directoryCache: ReadonlyMap<string, WorkspaceFileView>;
  expandedPaths: ReadonlySet<string>;
  openPaths: ReadonlySet<string>;
  dirtyPaths: ReadonlySet<string>;
  saveErrorPaths: ReadonlyMap<string, string>;
  showHidden: boolean;
  onToggleDirectory: (path: string) => void;
  onOpenPath?: (path: string) => void;
}) {
  const selected = selectedPath === entry.path;
  const open = openPaths.has(entry.path);
  const dirty = dirtyPaths.has(entry.path);
  const saveError = saveErrorPaths.has(entry.path);
  const stateLabel = saveError ? "Save failed" : dirty ? "Unsaved" : open ? "Open" : undefined;
  const cachedDirectory = entry.kind === "directory" ? directoryCache.get(entry.path) : undefined;
  const expanded = entry.kind === "directory" && expandedPaths.has(entry.path);
  const nestedEntries = cachedDirectory ? workspaceDirectoryEntries(cachedDirectory, undefined, showHidden) : [];
  const displayDepth = Math.min(depth, 6);
  function handleDirectoryToggle() {
    if (entry.kind !== "directory") return;
    if (!cachedDirectory && !expanded) {
      onOpenPath?.(entry.path);
      return;
    }
    onToggleDirectory(entry.path);
  }
  return (
    <li className="session-workspace-browser-entry" data-testid="session-workspace-browser-entry" data-path={entry.path} data-kind={entry.kind} data-selected={selected ? "true" : "false"} data-open={open ? "true" : "false"} data-dirty={dirty ? "true" : "false"} data-error={saveError ? "true" : "false"} data-expanded={expanded ? "true" : "false"}>
      <span className="session-workspace-browser-row" style={{ "--depth": displayDepth } as CSSProperties}>
        <button
          type="button"
          className="session-workspace-browser-toggle"
          aria-label={expanded ? "Collapse directory" : "Expand directory"}
          disabled={entry.kind !== "directory"}
          onClick={handleDirectoryToggle}
        />
        <span className="session-workspace-browser-icon session-files-tree-icon" aria-hidden="true">
          <span data-icon={entry.kind === "directory" ? "directory" : "file"} />
        </span>
        <button type="button" className="session-workspace-browser-name" aria-current={selected ? "true" : undefined} onClick={() => onOpenPath?.(entry.path)} disabled={!onOpenPath}>
          <strong title={entry.path}>{entry.name}</strong>
        </button>
        {stateLabel ? (
          <span className="session-workspace-browser-state" data-state={saveError ? "error" : dirty ? "dirty" : "open"} title={stateLabel}>
            <span className="visually-hidden">{stateLabel}</span>
          </span>
        ) : null}
        <span className="session-workspace-browser-actions">
          <CopyButton
            label="Copy path"
            displayLabel="Copy"
            value={workspaceAbsoluteDisplayPath(entry.path, workspaceRootPath)}
            className="session-workspace-tree-action"
            title="Copy path"
          />
        </span>
      </span>
      {expanded && nestedEntries.length > 0 ? (
        <ol>
          {nestedEntries.map((child) => (
            <WorkspaceEntry
              key={child.path}
              entry={child}
              depth={depth + 1}
              workspaceRootPath={workspaceRootPath}
              selectedPath={selectedPath}
              directoryCache={directoryCache}
              expandedPaths={expandedPaths}
              openPaths={openPaths}
              dirtyPaths={dirtyPaths}
              saveErrorPaths={saveErrorPaths}
              showHidden={showHidden}
              onToggleDirectory={onToggleDirectory}
              onOpenPath={onOpenPath}
            />
          ))}
        </ol>
      ) : null}
    </li>
  );
}

function WorkspaceFilePreview({
  file,
  parent,
  workspaceRootPath,
  onOpenPath,
  onWriteFile,
  draft,
  saveError,
  onDraftChange,
  onDraftSaved,
  onSaveError,
}: {
  file: WorkspaceFileView;
  parent?: string;
  workspaceRootPath: string;
  onOpenPath?: (path: string) => void;
  onWriteFile?: (path: string, text: string) => Promise<void> | void;
  draft?: WorkspaceDraftState;
  saveError?: string;
  onDraftChange?: (path: string, draft: WorkspaceDraftState) => void;
  onDraftSaved?: (path: string, text: string) => void;
  onSaveError?: (path: string, error?: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [lineJump, setLineJump] = useState("");
  const [wrapLines, setWrapLines] = useState(true);
  const [saveState, setSaveState] = useState<"idle" | "saving" | "saved" | "error">("idle");
  const textAreaRef = useRef<HTMLTextAreaElement | null>(null);
  const codeEditorRef = useRef<MonacoEditor.IStandaloneCodeEditor | null>(null);
  const baseText = file.text ?? "";
  const draftText = draft?.text ?? baseText;
  const savedText = draft?.savedText ?? baseText;
  const draftLines = useMemo(() => draftText.replace(/\r\n/g, "\n").split("\n"), [draftText]);
  const dirty = draftText !== savedText;
  const matchCount = query.trim() ? countTextMatches(draftText, query.trim()) : undefined;
  const language = workspaceLanguageForPath(file.path);
  const saveLabel = saveError
    ? "Save failed"
    : dirty
      ? "Unsaved"
      : saveState === "saving"
      ? "Saving"
      : saveState === "saved"
        ? "Saved"
        : undefined;
  const statusState = saveError ? "error" : dirty ? "dirty" : saveState;

  useEffect(() => {
    setSaveState("idle");
    setQuery("");
    setLineJump("");
  }, [file.path]);

  function jumpToLine() {
    const lineNumber = Number.parseInt(lineJump, 10);
    if (!Number.isFinite(lineNumber) || draftLines.length === 0) return;
    const targetLine = Math.max(1, Math.min(draftLines.length, lineNumber));
    const codeEditor = codeEditorRef.current;
    if (codeEditor) {
      codeEditor.focus();
      codeEditor.setPosition({ lineNumber: targetLine, column: 1 });
      codeEditor.revealLineInCenterIfOutsideViewport(targetLine);
      return;
    }
    const start = draftLines.slice(0, targetLine - 1).join("\n").length + (targetLine > 1 ? 1 : 0);
    textAreaRef.current?.focus();
    textAreaRef.current?.setSelectionRange(start, start);
  }

  async function saveFile() {
    if (!onWriteFile || !dirty || saveState === "saving") return;
    setSaveState("saving");
    onSaveError?.(file.path, undefined);
    try {
      await onWriteFile(file.path, draftText);
      onDraftSaved?.(file.path, draftText);
      setSaveState("saved");
    } catch (err) {
      setSaveState("error");
      onSaveError?.(file.path, compactError(err instanceof Error ? err.message : String(err)));
    }
  }

  return (
    <div className="session-file-preview session-workspace-file-preview" data-testid="session-workspace-file-preview">
      <div className="session-file-preview-toolbar session-workspace-file-toolbar">
        <div className="session-workspace-command-strip" data-testid="session-workspace-command-strip">
        <label className="session-workspace-find">
          <span className="visually-hidden">Search file</span>
          <input
            aria-label="Search workspace file"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder="Find"
          />
        </label>
          {matchCount != null ? <span className="session-files-match-count">{matchCount} match{matchCount === 1 ? "" : "es"}</span> : null}
          <span className="session-file-line-jump session-workspace-toolbar-group">
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
            <button type="button" className="session-workspace-text-action" aria-label="Go to line" title="Go to line" onClick={jumpToLine}>
              <span>Go</span>
            </button>
          </span>
        <span className="session-workspace-file-actions">
          <span className="session-workspace-toolbar-group">
            {parent ? (
              <button type="button" className="session-workspace-text-action" data-icon="parent" aria-label="Up" title="Parent directory" onClick={() => onOpenPath?.(parent)}>
                <span>Up</span>
              </button>
            ) : null}
            <CopyButton label="Copy path" displayLabel="Copy path" value={workspaceAbsoluteDisplayPath(file.path, workspaceRootPath)} className="session-workspace-text-action" title="Copy path" />
          </span>
          {onWriteFile ? (
            <span className="session-workspace-toolbar-group">
              <button type="button" className="session-workspace-text-action" aria-label={saveState === "saving" ? "Saving file" : "Save"} title={saveState === "saving" ? "Saving file" : "Save"} disabled={!dirty || saveState === "saving"} onClick={saveFile}>
                <span>{saveState === "saving" ? "Saving" : "Save"}</span>
              </button>
              <button type="button" className="session-workspace-text-action" aria-label="Revert" title="Revert" disabled={!dirty || saveState === "saving"} onClick={() => {
                onDraftChange?.(file.path, { text: savedText, savedText });
                setSaveState("idle");
                onSaveError?.(file.path, undefined);
              }}>
                <span>Revert</span>
              </button>
            </span>
          ) : null}
          <span className="session-workspace-toolbar-group">
            <CopyButton label="Copy file" displayLabel="Copy file" value={draftText} className="session-workspace-text-action" title="Copy file" />
            <a className="session-workspace-text-action" aria-label="Download" title="Download" href={workspaceTextDownloadHref(draftText)} download={workspaceDownloadName(file.path)}>
              <span>Download</span>
            </a>
            <button type="button" className="session-workspace-text-action" aria-label="Wrap" title="Wrap lines" aria-pressed={wrapLines} onClick={() => setWrapLines((value) => !value)}>
              <span>Wrap</span>
            </button>
          </span>
        </span>
        </div>
      </div>
      <div className="session-workspace-file-status" data-testid="session-workspace-file-status" data-state={statusState}>
        <span>
          {saveLabel ? <strong>{saveLabel}</strong> : null}
          <small>{workspaceLanguageDisplay(language)}</small>
          <small>{draftLines.length} {draftLines.length === 1 ? "line" : "lines"}</small>
          {file.hasMore ? <small>Partial</small> : null}
        </span>
        {saveError ? <small title={saveError}>{saveError}</small> : null}
      </div>
      <WorkspaceCodeEditor
        path={file.path}
        language={language}
        value={draftText}
        wrapLines={wrapLines}
        textAreaRef={textAreaRef}
        onMount={(editorInstance) => {
          codeEditorRef.current = editorInstance;
        }}
        onSave={() => void saveFile()}
        onChange={(nextText) => {
          onDraftChange?.(file.path, { text: nextText, savedText });
          setSaveState("idle");
          onSaveError?.(file.path, undefined);
        }}
      />
      {file.hasMore ? <small className="session-workspace-browser-more">Preview truncated; open through the agent before making broad edits.</small> : null}
    </div>
  );
}

function WorkspaceCodeEditor({
  path,
  language,
  value,
  wrapLines,
  textAreaRef,
  onChange,
  onSave,
  onMount,
}: {
  path: string;
  language: string;
  value: string;
  wrapLines: boolean;
  textAreaRef: RefObject<HTMLTextAreaElement | null>;
  onChange: (value: string) => void;
  onSave: () => void;
  onMount: (editor: MonacoEditor.IStandaloneCodeEditor) => void;
}) {
  const saveRef = useRef(onSave);

  useEffect(() => {
    saveRef.current = onSave;
  }, [onSave]);

  const handleMount: OnMount = (editorInstance, monaco) => {
    onMount(editorInstance);
    editorInstance.addCommand(monaco.KeyMod.CtrlCmd | monaco.KeyCode.KeyS, () => {
      saveRef.current();
    });
  };

  if (import.meta.env.MODE === "test") {
    return (
      <div className="session-workspace-code-editor" data-wrap={wrapLines ? "true" : "false"} data-testid="session-workspace-text-editor">
        <textarea
          ref={textAreaRef}
          aria-label="Workspace file editor"
          value={value}
          spellCheck={false}
          wrap={wrapLines ? "soft" : "off"}
          onKeyDown={(event) => {
            if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "s") {
              event.preventDefault();
              onSave();
            }
          }}
          onChange={(event) => onChange(event.target.value)}
        />
      </div>
    );
  }

  return (
    <div className="session-workspace-code-editor" data-wrap={wrapLines ? "true" : "false"} data-testid="session-workspace-text-editor">
      <Suspense fallback={<div className="session-workspace-code-loading">Loading editor...</div>}>
        <MonacoReactEditor
          key={path}
          path={path}
          value={value}
          language={language}
          theme="vs-dark"
          loading={<div className="session-workspace-code-loading">Loading editor...</div>}
          onMount={handleMount}
          onChange={(nextValue) => onChange(nextValue ?? "")}
          options={{
            automaticLayout: true,
            contextmenu: true,
            cursorBlinking: "smooth",
            fixedOverflowWidgets: true,
            folding: true,
            fontFamily: "var(--font-mono)",
            fontLigatures: false,
            fontSize: 12,
            glyphMargin: true,
            lineHeight: 20,
            lineNumbersMinChars: 3,
            minimap: { enabled: true, maxColumn: 80, renderCharacters: false },
            padding: { top: 10, bottom: 12 },
            renderLineHighlight: "all",
            renderWhitespace: "selection",
            scrollBeyondLastLine: false,
            smoothScrolling: true,
            tabSize: 2,
            wordWrap: wrapLines ? "on" : "off",
          }}
        />
      </Suspense>
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
    item.artifactPath ? { label: "Evidence", value: "Output captured" } : undefined,
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
            Open file
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
    item.artifactPath ? "evidence output captured" : undefined,
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
  if (item.status === "failed") return "Use evidence";
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

function isHiddenWorkspaceEntry(entry: WorkspaceFileEntryView): boolean {
  const pathParts = entry.path.split("/").filter(Boolean);
  const name = entry.name || pathParts.at(-1) || "";
  if (!name) return false;
  if (name.startsWith(".")) return true;
  return HIDDEN_WORKSPACE_NAMES.has(name);
}

const HIDDEN_WORKSPACE_NAMES = new Set([
  "browser-cache",
  "build",
  "coverage",
  "dist",
  "node_modules",
  "session-state",
  "sessions",
]);
