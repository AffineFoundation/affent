import { useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import { artifactEvidenceDraft, artifactEvidenceText } from "../view/sessionArtifacts";
import { artifactSizeLabel, type TurnArtifact } from "../view/turnArtifacts";
import { CopyButton } from "./CopyButton";

export function SessionArtifactsPanel({
  artifacts,
  defaultOpen = false,
  downloadHref,
  onOpenArtifact,
  onUseAsDraft,
}: {
  artifacts: readonly TurnArtifact[];
  defaultOpen?: boolean;
  downloadHref?: (path: string) => string | undefined;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const trimmedQuery = query.trim();
  const visibleArtifacts = trimmedQuery ? artifacts.filter((artifact) => artifactMatchesQuery(artifact, trimmedQuery)) : artifacts;
  return (
    <details className="session-skills-panel session-artifacts-panel" data-testid="session-artifacts-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Artifacts</span>
        <strong>{artifactSummary(artifacts)}</strong>
        <span>{artifactDetail(artifacts)}</span>
      </summary>
      <div className="session-skills-body">
        {artifacts.length > 1 ? (
          <div className="session-skills-controls">
            <label className="session-skills-search">
              <span>Search artifacts</span>
              <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="name, source, or summary" />
            </label>
            {trimmedQuery ? (
              <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                Clear
              </button>
            ) : null}
          </div>
        ) : null}
        {visibleArtifacts.length > 0 ? (
          <ol className="session-artifacts-list" data-testid="session-artifacts-list">
            {visibleArtifacts.map((artifact) => {
              const downloadUrl = downloadHref?.(artifact.path);
              return (
                <li key={artifact.path} className="session-artifacts-item">
                  <div className="session-artifacts-main">
                    <strong title={artifact.path}>{artifact.name}</strong>
                    <span>{artifactMeta(artifact)}</span>
                    {artifact.summary ? <small>{artifact.summary}</small> : null}
                    <small title={artifact.path}>{artifact.path}</small>
                  </div>
                  <span className="session-evidence-actions">
                    <CopyButton label="Copy path" value={artifact.path} className="ghost-action" />
                    <CopyButton label="Copy evidence" value={artifactEvidenceText(artifact)} className="ghost-action" />
                    {onOpenArtifact ? (
                      <button type="button" className="ghost-action" onClick={() => onOpenArtifact(artifact.path)}>
                        Open artifact
                      </button>
                    ) : null}
                    {downloadUrl ? (
                      <a className="ghost-action" href={downloadUrl} download={artifact.name}>
                        Download
                      </a>
                    ) : null}
                    {onUseAsDraft ? (
                      <button type="button" className="ghost-action" onClick={() => onUseAsDraft(artifactEvidenceDraft(artifact), "artifact")}>
                        Use artifact as draft
                      </button>
                    ) : null}
                  </span>
                </li>
              );
            })}
          </ol>
        ) : artifacts.length > 0 ? (
          <div className="session-skills-empty">No artifacts matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-skills-empty">No artifacts in this chat.</div>
        )}
      </div>
    </details>
  );
}

function artifactMatchesQuery(artifact: TurnArtifact, query: string): boolean {
  const haystack = [
    artifact.name,
    artifact.path,
    artifact.source,
    artifact.summary,
    artifact.truncated ? "full output" : "file",
    artifactSizeLabel(artifact),
  ].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
}

function artifactSummary(artifacts: readonly TurnArtifact[]): string {
  if (artifacts.length === 0) return "No artifacts";
  return `${artifacts.length} ${artifacts.length === 1 ? "artifact" : "artifacts"}`;
}

function artifactDetail(artifacts: readonly TurnArtifact[]): string {
  if (artifacts.length === 0) return "No generated files in this chat.";
  const truncated = artifacts.filter((artifact) => artifact.truncated).length;
  const totalBytes = artifacts.reduce((sum, artifact) => sum + (artifact.bytes ?? 0), 0);
  const parts = [`${artifacts.length} ${artifacts.length === 1 ? "file" : "files"}`];
  if (truncated > 0) parts.push(`${truncated} full-output`);
  if (totalBytes > 0) parts.push(`${Math.ceil(totalBytes / 1024)} KiB recorded`);
  return parts.join(" · ");
}

function artifactMeta(artifact: TurnArtifact): string {
  const parts = [
    artifact.truncated ? "Full output" : "File",
    artifact.source,
    artifactSizeLabel(artifact) || undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}
