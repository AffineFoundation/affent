import { useState } from "react";
import {
  artifactKind,
  artifactKindLabel,
  artifactLineageLabel,
  artifactOutcomeLabel,
  artifactReviewDetail,
  artifactReviewFocus,
  artifactReviewStats,
  artifactReviewSummary,
  artifactSourceGroupKey,
  artifactSourceGroups,
  artifactSummaryPreview,
  type SessionArtifactStats,
  type SessionArtifactKind,
} from "../view/sessionArtifacts";
import { artifactSizeLabel, type TurnArtifact } from "../view/turnArtifacts";
import { CopyButton } from "./CopyButton";

type ArtifactFilter = "all" | SessionArtifactKind;

export function SessionArtifactsPanel({
  artifacts,
  defaultOpen = false,
  onOpenArtifact,
}: {
  artifacts: readonly TurnArtifact[];
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<ArtifactFilter>("all");
  const [sourceKey, setSourceKey] = useState<string | undefined>();
  const trimmedQuery = query.trim();
  const stats = artifactReviewStats(artifacts);
  const kindFilteredArtifacts = filter === "all" ? artifacts : artifacts.filter((artifact) => artifactKind(artifact) === filter);
  const focus = artifactReviewFocus(artifacts);
  const sourceGroups = artifactSourceGroups(artifacts);
  const activeSource = sourceGroups.find((group) => group.key === sourceKey);
  const sourceFilteredArtifacts = sourceKey
    ? kindFilteredArtifacts.filter((artifact) => artifactSourceGroupKey(artifact) === sourceKey)
    : kindFilteredArtifacts;
  const visibleArtifacts = trimmedQuery ? sourceFilteredArtifacts.filter((artifact) => artifactMatchesQuery(artifact, trimmedQuery)) : sourceFilteredArtifacts;
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
                  <CopyButton label="Copy path" value={focus.path} className="ghost-action" />
                </div>
              </div>
            ) : null}
            <div className="session-artifacts-statline" data-testid="session-artifacts-statline" aria-label="Artifact review summary">
              {artifactStatLineItems(stats).map((item) => (
                <span key={item}>{item}</span>
              ))}
            </div>
            {sourceGroups.length > 1 ? (
              <div className="session-artifacts-source-index" aria-label="Artifact source index">
                <span>
                  Sources
                  {activeSource ? (
                    <button type="button" onClick={() => setSourceKey(undefined)}>
                      Clear source
                    </button>
                  ) : null}
                </span>
                {sourceGroups.slice(0, 5).map((group) => (
                  <button
                    key={group.key}
                    type="button"
                    data-active={sourceKey === group.key ? "true" : "false"}
                    onClick={() => {
                      setFilter("all");
                      setSourceKey((current) => current === group.key ? undefined : group.key);
                    }}
                  >
                    <strong title={group.fullLabel}>{group.label}</strong>
                    <small>{group.count} {group.count === 1 ? "file" : "files"} · {group.kindLabel} · {group.turns}{group.sizeLabel ? ` · ${group.sizeLabel}` : ""}</small>
                  </button>
                ))}
              </div>
            ) : null}
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
              const summaryPreview = artifactSummaryPreview(artifact);
              return (
                <li key={artifact.path} className="session-artifacts-item">
                  <div className="session-artifacts-main">
                    <strong title={artifact.path}>{artifact.name}</strong>
                    <span title={artifact.source}>{artifactMeta(artifact)}</span>
                    {summaryPreview ? <small className="session-artifacts-summary">{summaryPreview}</small> : null}
                    <small className="session-artifacts-path" title={artifact.path}>{artifact.path}</small>
                  </div>
                  <span className="session-evidence-actions">
                    {onOpenArtifact ? (
                      <button type="button" className="ghost-action" onClick={() => onOpenArtifact(artifact.path)}>
                        Open
                      </button>
                    ) : null}
                    <CopyButton label="Copy path" value={artifact.path} className="ghost-action" />
                  </span>
                </li>
              );
            })}
          </ol>
        ) : artifacts.length > 0 ? (
          <div className="session-skills-empty">No {artifactEmptyLabel(filter, activeSource?.label, trimmedQuery)}.</div>
        ) : (
          <div className="session-artifacts-empty">
            <strong>No artifacts yet</strong>
            <span>No generated files or stored full outputs in this chat.</span>
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

function artifactStatLineItems(stats: SessionArtifactStats): string[] {
  const items = [
    stats.deliverables > 0 ? `${stats.deliverables} deliverable${stats.deliverables === 1 ? "" : "s"}` : undefined,
    stats.fullOutputs > 0 ? `${stats.fullOutputs} full output${stats.fullOutputs === 1 ? "" : "s"}` : undefined,
    stats.failedOutputs > 0 ? `${stats.failedOutputs} failed` : undefined,
    stats.partialOutputs > 0 ? `${stats.partialOutputs} partial` : undefined,
    stats.recordedBytes > 0 ? `${Math.ceil(stats.recordedBytes / 1024)} KiB recorded` : undefined,
  ].filter((item): item is string => Boolean(item));
  return items.length ? items : ["No recorded artifact evidence"];
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
    artifactOutcomeLabel(artifact),
    compactArtifactSource(artifact.source),
    artifactSizeLabel(artifact) || undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function compactArtifactSource(source: string): string | undefined {
  const compact = source.replace(/\s+/g, " ").trim();
  if (!compact) return undefined;
  if (compact.length <= 96) return compact;
  return `${compact.slice(0, 93).trimEnd()}...`;
}

function artifactFilterLabel(filter: ArtifactFilter): string {
  if (filter === "full_output") return "Full output";
  if (filter === "deliverable") return "Deliverables";
  return "Artifacts";
}

function artifactEmptyLabel(filter: ArtifactFilter, source: string | undefined, query: string): string {
  const parts = [
    filter === "all" ? "artifacts" : artifactFilterLabel(filter).toLowerCase(),
    source ? `from ${source}` : undefined,
    query ? `matching "${query}"` : undefined,
  ].filter(Boolean);
  return parts.join(" ");
}
