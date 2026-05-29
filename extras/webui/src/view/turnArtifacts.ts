import { formatBytes } from "./byteFormat";
import type { ToolCallState, TurnState } from "../store/sessionState";
import { showsChatArtifact } from "./toolResultDisplay";

export interface TurnArtifact {
  path: string;
  name: string;
  source: string;
  tool?: string;
  turnNumber?: number;
  callIndex?: number;
  summary?: string;
  truncated: boolean;
  status?: ToolCallState["status"];
  exitCode?: number;
  durationMs?: number;
  failureKind?: string;
  failureKinds?: string[];
  bytes?: number;
  omittedBytes?: number;
  capBytes?: number;
}

export function buildTurnArtifacts(turn: TurnState, context: { turnNumber?: number } = {}): TurnArtifact[] {
  const seen = new Set<string>();
  const artifacts: TurnArtifact[] = [];

  for (const [index, call] of turn.toolCalls.entries()) {
    if (!call.resultArtifactPath || seen.has(call.resultArtifactPath)) continue;
    seen.add(call.resultArtifactPath);
    artifacts.push({
      path: call.resultArtifactPath,
      name: artifactName(call.resultArtifactPath),
      source: toolSource(call),
      tool: call.tool,
      turnNumber: context.turnNumber,
      callIndex: index + 1,
      summary: call.resultSummary,
      truncated: call.resultTruncated,
      status: call.status,
      exitCode: call.exitCode,
      durationMs: call.durationMs,
      failureKind: call.failureKind,
      failureKinds: call.failureKinds,
      bytes: call.resultBytes,
      omittedBytes: call.resultOmittedBytes,
      capBytes: call.resultCapBytes,
    });
  }

  return artifacts;
}

export function chatVisibleTurnArtifacts(turn: TurnState, context: { turnNumber?: number } = {}): TurnArtifact[] {
  return buildTurnArtifacts(turn, context).filter(isChatVisibleArtifact);
}

export function isChatVisibleArtifact(artifact: TurnArtifact): boolean {
  return showsChatArtifact(artifact);
}

export function artifactSizeLabel(artifact: TurnArtifact): string {
  return formatBytes(artifact.bytes, artifact.omittedBytes, artifact.capBytes, artifact.truncated);
}

export function artifactDisplayLabel(artifact: TurnArtifact): string {
  const size = artifactSizeLabel(artifact);
  return `${artifact.name}${size ? ` ${size}` : ""}`;
}

export function artifactAggregateLabel(artifacts: readonly TurnArtifact[]): string | undefined {
  if (artifacts.length === 0) return undefined;
  if (artifacts.length === 1) return artifactDisplayLabel(artifacts[0]);
  let bytes = 0;
  let omittedBytes = 0;
  let hasBytes = false;
  let truncated = false;
  for (const artifact of artifacts) {
    if (artifact.bytes != null) {
      bytes += artifact.bytes;
      hasBytes = true;
    }
    if (artifact.omittedBytes != null) omittedBytes += artifact.omittedBytes;
    truncated = truncated || artifact.truncated;
  }
  const size = formatBytes(hasBytes ? bytes : undefined, omittedBytes, undefined, truncated);
  return size ? `${artifacts.length} files ${size}` : `${artifacts.length} files`;
}

export function artifactCountLabel(artifacts: readonly TurnArtifact[]): string | undefined {
  if (artifacts.length === 0) return undefined;
  let bytes = 0;
  let omittedBytes = 0;
  let hasBytes = false;
  let truncated = false;
  for (const artifact of artifacts) {
    if (artifact.bytes != null) {
      bytes += artifact.bytes;
      hasBytes = true;
    }
    if (artifact.omittedBytes != null) omittedBytes += artifact.omittedBytes;
    truncated = truncated || artifact.truncated;
  }
  const size = formatBytes(hasBytes ? bytes : undefined, omittedBytes, undefined, truncated);
  const count = `${artifacts.length} ${artifacts.length === 1 ? "file" : "files"}`;
  return size ? `${count} ${size}` : count;
}

export function artifactName(path: string): string {
  const normalized = path.replace(/\\/g, "/");
  const name = normalized.split("/").filter(Boolean).at(-1);
  return name || path;
}

function toolSource(call: ToolCallState): string {
  if (call.tool === "shell") {
    const command = call.args.command;
    return typeof command === "string" && command.trim() ? command.trim() : "Command output";
  }
  if (call.tool === "read_file") {
    const path = call.args.path;
    return typeof path === "string" && path.trim() ? path.trim() : "File read";
  }
  return call.tool;
}
