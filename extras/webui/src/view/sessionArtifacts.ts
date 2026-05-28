import type { SessionState } from "../store/sessionState";
import { artifactCountLabel, artifactSizeLabel, buildTurnArtifacts, type TurnArtifact } from "./turnArtifacts";

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
  return `Use this artifact in the next step:\n${artifactEvidenceText(artifact)}`;
}
