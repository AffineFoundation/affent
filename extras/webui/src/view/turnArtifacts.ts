import type { ToolCallState, TurnState } from "../store/sessionState";

export interface TurnArtifact {
  path: string;
  name: string;
  source: string;
  summary?: string;
  truncated: boolean;
}

export function buildTurnArtifacts(turn: TurnState): TurnArtifact[] {
  const seen = new Set<string>();
  const artifacts: TurnArtifact[] = [];

  for (const call of turn.toolCalls) {
    if (!call.resultArtifactPath || seen.has(call.resultArtifactPath)) continue;
    seen.add(call.resultArtifactPath);
    artifacts.push({
      path: call.resultArtifactPath,
      name: artifactName(call.resultArtifactPath),
      source: toolSource(call),
      summary: call.resultSummary,
      truncated: call.resultTruncated,
    });
  }

  return artifacts;
}

function artifactName(path: string): string {
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
