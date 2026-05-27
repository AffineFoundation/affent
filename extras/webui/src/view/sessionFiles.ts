import type { SessionState, ToolCallState } from "../store/sessionState";
import { summarizePreview } from "./textPreview";

export type SessionFileStatus = "running" | "available" | "failed";
export type SessionFileAction = "read" | "listed" | "changed";

export interface SessionFileEvidence {
  path: string;
  actions: SessionFileAction[];
  status: SessionFileStatus;
  turnNumber: number;
  actionCount: number;
  detail?: string;
  next?: string;
  artifactPath?: string;
}

export interface SessionFilesView {
  items: SessionFileEvidence[];
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

interface SessionFileEvidenceInternal extends SessionFileEvidence {
  sequence: number;
}

export function buildSessionFiles(session: SessionState): SessionFilesView {
  const byPath = new Map<string, SessionFileEvidenceInternal>();
  let sequence = 0;
  session.turns.forEach((turn, turnIndex) => {
    for (const call of turn.toolCalls) {
      sequence += 1;
      const evidence = fileEvidenceFromCall(call, turnIndex + 1, sequence);
      if (!evidence) continue;
      const previous = byPath.get(evidence.path);
      byPath.set(evidence.path, previous ? mergeEvidence(previous, evidence) : evidence);
    }
  });

  const items = [...byPath.values()]
    .sort((a, b) => b.turnNumber - a.turnNumber || b.sequence - a.sequence || a.path.localeCompare(b.path))
    .map(({ sequence: _sequence, ...item }) => item);
  const failed = items.filter((item) => item.status === "failed").length;
  const running = items.filter((item) => item.status === "running").length;
  return {
    items,
    summary: filesSummary(items.length, { failed, running }),
    detail: filesDetail(items),
    tone: failed > 0 ? "error" : running > 0 ? "warning" : undefined,
  };
}

function fileEvidenceFromCall(
  call: ToolCallState,
  turnNumber: number,
  sequence: number,
): SessionFileEvidenceInternal | undefined {
  const action = fileAction(call.tool);
  if (!action) return undefined;
  const path = stringArg(call, "path") ?? stringArg(call, "file") ?? stringArg(call, "filename");
  if (!path) return undefined;
  const detailSource = call.resultSummary || call.result || call.failureKind;
  return {
    path,
    actions: [action],
    status: fileStatus(call),
    turnNumber,
    sequence,
    actionCount: 1,
    detail: detailSource ? summarizePreview(stripRecoveryLines(detailSource), 120) : undefined,
    next: nextHint(call.resultSummary, call.result),
    artifactPath: call.resultArtifactPath,
  };
}

function mergeEvidence(
  previous: SessionFileEvidenceInternal,
  next: SessionFileEvidenceInternal,
): SessionFileEvidenceInternal {
  return {
    ...next,
    actions: mergeActions(previous.actions, next.actions),
    actionCount: previous.actionCount + 1,
    artifactPath: next.artifactPath ?? previous.artifactPath,
    next: next.next ?? previous.next,
  };
}

function mergeActions(previous: SessionFileAction[], next: SessionFileAction[]): SessionFileAction[] {
  const order: SessionFileAction[] = ["read", "listed", "changed"];
  const seen = new Set([...previous, ...next]);
  return order.filter((action) => seen.has(action));
}

function fileAction(tool: string): SessionFileAction | undefined {
  if (tool === "read_file") return "read";
  if (tool === "list_files") return "listed";
  if (tool === "write_file" || tool === "edit_file") return "changed";
  return undefined;
}

function fileStatus(call: ToolCallState): SessionFileStatus {
  if (call.status === "running") return "running";
  return call.status === "error" || (call.exitCode != null && call.exitCode !== 0) ? "failed" : "available";
}

function nextHint(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const value = match?.[1]?.trim();
  return value ? summarizePreview(value, 120) : undefined;
}

function stripRecoveryLines(text: string): string {
  return text
    .split(/\r?\n/)
    .filter((line) => !/^\s*(Next|Failure):/i.test(line))
    .join("\n");
}

function filesSummary(total: number, counts: { failed: number; running: number }): string {
  if (total === 0) return "No file evidence";
  if (counts.failed > 0) return `${counts.failed} file ${counts.failed === 1 ? "issue" : "issues"}`;
  if (counts.running > 0) return `${counts.running} pending file ${counts.running === 1 ? "action" : "actions"}`;
  return `${total} file ${total === 1 ? "reference" : "references"}`;
}

function filesDetail(items: SessionFileEvidence[]): string {
  if (items.length === 0) return "No read, list, write, or edit actions in this chat.";
  const counts = {
    read: items.filter((item) => item.actions.includes("read")).length,
    listed: items.filter((item) => item.actions.includes("listed")).length,
    changed: items.filter((item) => item.actions.includes("changed")).length,
  };
  return [
    counts.read > 0 ? `${counts.read} read` : undefined,
    counts.listed > 0 ? `${counts.listed} listed` : undefined,
    counts.changed > 0 ? `${counts.changed} changed` : undefined,
  ].filter(Boolean).join(" · ");
}

function stringArg(call: ToolCallState, key: string): string | undefined {
  const value = call.args[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}
