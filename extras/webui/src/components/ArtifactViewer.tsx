import { useState } from "react";
import type { ArtifactChunk } from "../api/sessions";
import {
  artifactChunkEvidenceDraft,
  artifactChunkEvidenceText,
  artifactLoadedTextDraft,
  artifactMatchesDraft,
  artifactMatchesText,
  buildArtifactMatchPreviews,
  buildArtifactStats,
} from "../view/artifactViewer";
import { formatByteCount } from "../view/byteFormat";
import type { UseAsDraft } from "../view/draftSource";
import { CopyButton } from "./CopyButton";
import { CopyMenu } from "./CopyMenu";
import { HighlightText } from "./HighlightText";

export type ArtifactViewerState =
  | { state: "idle" }
  | { state: "loading"; path: string }
  | { state: "ready"; chunk: ArtifactChunk; query: string; loadingMore?: boolean; loadError?: string }
  | { state: "error"; path: string; message: string };

export function ArtifactViewer({
  artifact,
  onClose,
  onSearch,
  onLoadMore,
  onUseAsDraft,
  artifactDownloadHref,
}: {
  artifact: ArtifactViewerState;
  onClose: () => void;
  onSearch: (query: string) => void;
  onLoadMore: () => void;
  onUseAsDraft?: UseAsDraft;
  artifactDownloadHref?: string;
}) {
  const [viewMode, setViewMode] = useState<"text" | "json">("text");
  if (artifact.state === "idle") return null;
  const stats = artifact.state === "ready" ? buildArtifactStats(artifact.chunk, artifact.query) : undefined;
  const jsonPreview = artifact.state === "ready" ? formatJsonPreview(artifact.chunk.text) : undefined;
  const activeViewMode = jsonPreview && viewMode === "json" ? "json" : "text";
  const displayedText = artifact.state === "ready" && activeViewMode === "json" ? jsonPreview ?? artifact.chunk.text : artifact.state === "ready" ? artifact.chunk.text : "";
  const matchPreviews = artifact.state === "ready" ? buildArtifactMatchPreviews(displayedText, artifact.query) : [];

  return (
    <section className="artifact-viewer" data-state={artifact.state} data-testid="artifact-viewer">
      <header className="artifact-head">
        <div>
          <span className="artifact-eyebrow">File preview</span>
          <h3>{displayName(artifact.state === "ready" ? artifact.chunk.path : artifact.path)}</h3>
          <code>{artifact.state === "ready" ? artifact.chunk.path : artifact.path}</code>
          {artifact.state === "ready" ? (
            <small className="artifact-head-meta">
              {formatByteCount(stats?.loadedBytes ?? 0)} loaded of {formatByteCount(stats?.totalBytes ?? 0)} total
              {artifact.chunk.hasMore ? " · partial load" : " · complete file"}
            </small>
          ) : null}
        </div>
        <button type="button" className="node-action" onClick={onClose}>
          Close
        </button>
      </header>
      {artifact.state === "loading" ? (
        <div className="artifact-message">Loading output...</div>
      ) : null}
      {artifact.state === "error" ? (
        <div className="artifact-message error" role="alert">
          {artifact.message}
        </div>
      ) : null}
      {artifact.state === "ready" ? (
        <div className="artifact-body">
          <div className="artifact-progress" aria-label="Artifact loading progress">
            <span style={{ width: `${stats?.loadedPercent ?? 0}%` }} />
          </div>
          <div className="artifact-toolbar">
            <label className="artifact-search">
              <span>Search in file</span>
              <input
                value={artifact.query}
                onChange={(event) => onSearch(event.target.value)}
                placeholder="Search loaded text"
                data-testid="artifact-search"
              />
            </label>
            {jsonPreview ? (
              <div className="artifact-view-toggle" role="group" aria-label="Artifact view">
                <button type="button" aria-pressed={activeViewMode === "text"} onClick={() => setViewMode("text")}>
                  Text
                </button>
                <button type="button" aria-pressed={activeViewMode === "json"} onClick={() => setViewMode("json")}>
                  JSON
                </button>
              </div>
            ) : null}
            {onUseAsDraft ? (
              <>
                <button
                  type="button"
                  className="node-action"
                  onClick={() => onUseAsDraft(artifactChunkEvidenceDraft(artifact.chunk), "artifact")}
                >
                  Use artifact as draft
                </button>
                <button
                  type="button"
                  className="node-action"
                  onClick={() => onUseAsDraft(artifactLoadedTextDraft(artifact.chunk.path, artifact.chunk.text), "artifact_text")}
                  disabled={artifact.chunk.text.trim() === ""}
                >
                  Use text
                </button>
              </>
            ) : null}
            {artifactDownloadHref ? (
              <a className="node-action" href={artifactDownloadHref} download={displayName(artifact.chunk.path)}>
                Download
              </a>
            ) : null}
            <CopyMenu
              label="Copy file"
              className="artifact-copy-menu"
              panelClassName="artifact-copy-menu-panel"
              triggerClassName="node-action artifact-copy-trigger"
            >
              <CopyButton label="Copy path" value={artifact.chunk.path} className="node-action" />
              <CopyButton label="Copy evidence" value={artifactChunkEvidenceText(artifact.chunk)} className="node-action" />
              <CopyButton label="Copy text" value={artifact.chunk.text} className="node-action" />
            </CopyMenu>
          </div>
          <div className="artifact-stats">
            <span>{stats?.loadedPercent}% loaded</span>
            {artifact.query.trim() ? <span>{stats?.matchCount} match{stats?.matchCount === 1 ? "" : "es"}</span> : null}
            {artifact.chunk.hasMore ? <span>more available</span> : <span>complete</span>}
          </div>
          {artifact.query.trim() && matchPreviews.length > 0 ? (
            <div className="artifact-match-list" data-testid="artifact-match-list">
              <div>
                <span>Match context</span>
                <span className="artifact-match-tools">
                  {stats && stats.matchCount > matchPreviews.length ? <small>first {matchPreviews.length}</small> : null}
                  <CopyButton label="Copy matches" value={artifactMatchesText(artifact.chunk.path, artifact.query, matchPreviews)} />
                  {onUseAsDraft ? (
                    <button
                      type="button"
                      onClick={() => onUseAsDraft(artifactMatchesDraft(artifact.chunk.path, artifact.query, matchPreviews), "evidence")}
                    >
                      Use matches
                    </button>
                  ) : null}
                </span>
              </div>
              {matchPreviews.map((match) => (
                <p key={`${match.lineNumber}-${match.text}`}>
                  <b>Line {match.lineNumber}</b>
                  <span><HighlightText text={match.text} query={artifact.query} /></span>
                </p>
              ))}
            </div>
          ) : null}
          <details className="artifact-meta">
            <summary>File details</summary>
            <span>offset {artifact.chunk.offset}</span>
            <span>{stats?.loadedBytes} loaded</span>
            <span>{artifact.chunk.bytes} bytes total</span>
          </details>
          {artifact.loadError ? (
            <div className="artifact-message error compact" role="alert">
              {artifact.loadError}
            </div>
          ) : null}
          <pre className="code artifact-code" data-testid="artifact-content" data-view={activeViewMode}>
            <HighlightText text={displayedText} query={artifact.query} />
          </pre>
          {artifact.chunk.hasMore ? (
            <div className="artifact-footer">
              <button
                type="button"
                className="node-action"
                onClick={onLoadMore}
                disabled={artifact.loadingMore}
              >
                {artifact.loadingMore ? "Loading more" : "Load more"}
              </button>
              <span>
                Loaded {artifact.chunk.offset + artifact.chunk.text.length} of {artifact.chunk.bytes} bytes.
              </span>
            </div>
          ) : null}
        </div>
      ) : null}
    </section>
  );
}

function displayName(path: string): string {
  const clean = path.split(/[\\/]/).filter(Boolean).at(-1);
  return clean || path;
}

function formatJsonPreview(text: string): string | undefined {
  const trimmed = text.trim();
  if (!trimmed || (!trimmed.startsWith("{") && !trimmed.startsWith("["))) return undefined;
  try {
    return JSON.stringify(JSON.parse(trimmed), null, 2);
  } catch {
    return undefined;
  }
}
