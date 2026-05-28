import { useState } from "react";
import type { UseAsDraft } from "../view/draftSource";
import {
  artifactEvidenceDraft,
  artifactEvidenceText,
  artifactKind,
  artifactKindLabel,
  artifactLineageLabel,
  artifactReviewDetail,
  artifactReviewFocus,
  artifactReviewFacts,
  artifactReviewStats,
  artifactReviewSummary,
  artifactSummaryPreview,
  type SessionArtifactKind,
} from "../view/sessionArtifacts";
import { artifactSizeLabel, type TurnArtifact } from "../view/turnArtifacts";
import { CopyButton } from "./CopyButton";

type ArtifactFilter = "all" | SessionArtifactKind;

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
  const [filter, setFilter] = useState<ArtifactFilter>("all");
  const trimmedQuery = query.trim();
  const stats = artifactReviewStats(artifacts);
  const filteredArtifacts = filter === "all" ? artifacts : artifacts.filter((artifact) => artifactKind(artifact) === filter);
  const visibleArtifacts = trimmedQuery ? filteredArtifacts.filter((artifact) => artifactMatchesQuery(artifact, trimmedQuery)) : filteredArtifacts;
  const focus = artifactReviewFocus(artifacts);
  const reviewFacts = artifactReviewFacts(artifacts);
  const focusDownloadUrl = focus ? downloadHref?.(focus.path) : undefined;
  return (
    <details className="session-skills-panel session-artifacts-panel" data-testid="session-artifacts-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Artifacts</span>
        <strong>{artifactReviewSummary(artifacts)}</strong>
        <span>{artifactReviewDetail(artifacts)}</span>
      </summary>
      <div className="session-skills-body">
        {artifacts.length > 0 ? (
          <div className="session-artifacts-overview" aria-label="Artifact evidence summary">
            <div>
              <span>Evidence files</span>
              <strong>{artifactReviewSummary(artifacts)}</strong>
              <small>{artifactReviewDetail(artifacts)}</small>
            </div>
            {focus ? (
              <div className="session-artifacts-focus" data-testid="session-artifacts-focus">
                <div className="session-artifacts-focus-main">
                  <small>{[artifactKindLabel(focus), artifactLineageLabel(focus)].filter(Boolean).join(" · ")}</small>
                  <strong title={focus.path}>{focus.name}</strong>
                  <span className="session-artifacts-focus-source" title={focus.source}>{focus.source}</span>
                  {artifactSummaryPreview(focus, 120) ? (
                    <span className="session-artifacts-focus-summary">{artifactSummaryPreview(focus, 120)}</span>
                  ) : null}
                  <b>{artifactSizeLabel(focus) || "recorded"}</b>
                </div>
                <div className="session-artifacts-focus-actions">
                  {onOpenArtifact ? (
                    <button type="button" className="ghost-action" onClick={() => onOpenArtifact(focus.path)}>
                      Open artifact
                    </button>
                  ) : null}
                  {focusDownloadUrl ? (
                    <a className="ghost-action" href={focusDownloadUrl} download={focus.name}>
                      Download
                    </a>
                  ) : null}
                  <CopyButton label="Copy path" value={focus.path} className="ghost-action" />
                  <CopyButton label="Copy details" value={artifactEvidenceText(focus)} className="ghost-action" />
                  {onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(artifactEvidenceDraft(focus), "artifact")}>
                      Reference
                    </button>
                  ) : null}
                </div>
              </div>
            ) : null}
            <div className="session-artifacts-facts" aria-label="Artifact review facts">
              {reviewFacts.map((fact) => (
                <span key={fact.label} data-tone={fact.tone ?? "neutral"}>
                  <small>{fact.label}</small>
                  <strong>{fact.value}</strong>
                  <b>{fact.detail}</b>
                </span>
              ))}
            </div>
            <div className="session-artifacts-filterbar" role="group" aria-label="Artifact filters">
              <ArtifactFilterButton label="All" value={stats.total} active={filter === "all"} onClick={() => setFilter("all")} />
              <ArtifactFilterButton label="Deliverables" value={stats.deliverables} active={filter === "deliverable"} onClick={() => setFilter("deliverable")} />
              <ArtifactFilterButton label="Full output" value={stats.fullOutputs} active={filter === "full_output"} onClick={() => setFilter("full_output")} />
            </div>
          </div>
        ) : null}
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
              const summaryPreview = artifactSummaryPreview(artifact);
              return (
                <li key={artifact.path} className="session-artifacts-item">
                  <div className="session-artifacts-main">
                    <strong title={artifact.path}>{artifact.name}</strong>
                    <span>{artifactMeta(artifact)}</span>
                    {summaryPreview ? <small className="session-artifacts-summary">{summaryPreview}</small> : null}
                    <small className="session-artifacts-path" title={artifact.path}>{artifact.path}</small>
                  </div>
                  <span className="session-evidence-actions">
                    {onOpenArtifact ? (
                      <button type="button" className="ghost-action" onClick={() => onOpenArtifact(artifact.path)}>
                        Open
                      </button>
                    ) : null}
                    {downloadUrl ? (
                      <a className="ghost-action" href={downloadUrl} download={artifact.name}>
                        Download
                      </a>
                    ) : null}
                    <CopyButton label="Copy path" value={artifact.path} className="ghost-action" />
                    <CopyButton label="Copy details" value={artifactEvidenceText(artifact)} className="ghost-action" />
                    {onUseAsDraft ? (
                      <button type="button" className="ghost-action" onClick={() => onUseAsDraft(artifactEvidenceDraft(artifact), "artifact")}>
                        Reference
                      </button>
                    ) : null}
                  </span>
                </li>
              );
            })}
          </ol>
        ) : artifacts.length > 0 ? (
          <div className="session-skills-empty">No {filter === "all" ? "artifacts" : artifactFilterLabel(filter).toLowerCase()} matching "{trimmedQuery}".</div>
        ) : (
          <div className="session-artifacts-empty">
            <strong>No artifacts yet</strong>
            <span>When a tool stores a full output or the agent creates a deliverable, it will appear here with open, download, and reference actions.</span>
          </div>
        )}
      </div>
    </details>
  );
}

function ArtifactFilterButton({
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
    <button type="button" className="session-artifacts-filter" data-active={active ? "true" : "false"} onClick={onClick}>
      <span>{label}</span>
      <strong>{value}</strong>
    </button>
  );
}

function artifactMatchesQuery(artifact: TurnArtifact, query: string): boolean {
  const haystack = [
    artifact.name,
    artifact.path,
    artifact.source,
    artifact.tool,
    artifactLineageLabel(artifact),
    artifact.summary,
    artifactKindLabel(artifact),
    artifactSizeLabel(artifact),
  ].filter(Boolean).join("\n").toLowerCase();
  return haystack.includes(query.toLowerCase());
}

function artifactMeta(artifact: TurnArtifact): string {
  const parts = [
    artifactKindLabel(artifact),
    artifactLineageLabel(artifact),
    artifact.source,
    artifactSizeLabel(artifact) || undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function artifactFilterLabel(filter: ArtifactFilter): string {
  if (filter === "full_output") return "Full output";
  if (filter === "deliverable") return "Deliverables";
  return "Artifacts";
}
