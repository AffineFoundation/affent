import type { SessionState } from "../store/sessionState";
import { artifactCountLabel, artifactSizeLabel, buildTurnArtifacts, type TurnArtifact } from "./turnArtifacts";

export type SessionArtifactKind = "deliverable" | "full_output";

export interface SessionArtifactStats {
  total: number;
  deliverables: number;
  fullOutputs: number;
  recordedBytes: number;
}

export function buildSessionArtifacts(session: SessionState): TurnArtifact[] {
  const seen = new Set<string>();
  const artifacts: TurnArtifact[] = [];
  for (const turn of session.turns) {
    for (const artifact of buildTurnArtifacts(turn)) {
      if (seen.has(artifact.path)) continue;
      seen.add(artifact.path);
      artifacts.push(artifact);
    }
  }
  return artifacts;
}

export function buildWorkbenchArtifacts(session: SessionState): TurnArtifact[] {
  return buildSessionArtifacts(session);
}

export function sessionArtifactLabel(session: SessionState): string | undefined {
  const artifacts = buildSessionArtifacts(session);
  if (artifacts.length === 0) return undefined;
  return artifactCountLabel(artifacts) ?? `${artifacts.length} file${artifacts.length === 1 ? "" : "s"}`;
}

export function artifactEvidenceText(artifact: TurnArtifact): string {
  const lines = [`Artifact evidence for ${artifact.path}`, `Source: ${artifact.source}`];
  const size = artifactSizeLabel(artifact);
  if (size) lines.push(`Size: ${size}`);
  if (artifact.truncated) lines.push("Full output available as artifact");
  if (artifact.summary) lines.push(`Summary: ${artifact.summary}`);
  return lines.join("\n");
}

export function artifactEvidenceDraft(artifact: TurnArtifact): string {
  return `Reference this artifact in the next step:\n${artifactEvidenceText(artifact)}`;
}

export function artifactKind(artifact: TurnArtifact): SessionArtifactKind {
  if (artifact.truncated || artifact.path.replace(/\\/g, "/").includes("/tool-results/")) return "full_output";
  return "deliverable";
}

export function artifactKindLabel(artifact: TurnArtifact): string {
  return artifactKind(artifact) === "full_output" ? "Full output" : "Deliverable";
}

export function artifactReviewStats(artifacts: readonly TurnArtifact[]): SessionArtifactStats {
  return {
    total: artifacts.length,
    deliverables: artifacts.filter((artifact) => artifactKind(artifact) === "deliverable").length,
    fullOutputs: artifacts.filter((artifact) => artifactKind(artifact) === "full_output").length,
    recordedBytes: artifacts.reduce((sum, artifact) => sum + (artifact.bytes ?? 0), 0),
  };
}

export function artifactReviewSummary(artifacts: readonly TurnArtifact[]): string {
  if (artifacts.length === 0) return "No artifacts";
  const stats = artifactReviewStats(artifacts);
  const parts = [
    stats.deliverables > 0 ? `${stats.deliverables} deliverable${stats.deliverables === 1 ? "" : "s"}` : undefined,
    stats.fullOutputs > 0 ? `${stats.fullOutputs} full output${stats.fullOutputs === 1 ? "" : "s"}` : undefined,
  ].filter(Boolean);
  return parts.length ? parts.join(" · ") : `${artifacts.length} ${artifacts.length === 1 ? "artifact" : "artifacts"}`;
}

export function artifactReviewDetail(artifacts: readonly TurnArtifact[]): string {
  if (artifacts.length === 0) return "No generated files or full outputs in this chat.";
  const stats = artifactReviewStats(artifacts);
  const parts = [`${stats.total} ${stats.total === 1 ? "file" : "files"}`];
  if (stats.recordedBytes > 0) parts.push(`${Math.ceil(stats.recordedBytes / 1024)} KiB recorded`);
  return parts.join(" · ");
}

export function artifactReviewFocus(artifacts: readonly TurnArtifact[]): TurnArtifact | undefined {
  return [...artifacts].reverse().find((artifact) => artifactKind(artifact) === "full_output") ?? artifacts.at(-1);
}
