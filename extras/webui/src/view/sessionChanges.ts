import type { SessionState, ToolCallState } from "../store/sessionState";
import type { ChangeDiffLine } from "../store/changeDiff";
import { summarizePreview } from "./textPreview";

export type SessionChangeStatus = "running" | "changed" | "failed";

export interface SessionChangedFile {
  path: string;
  operation: "write" | "edit";
  status: SessionChangeStatus;
  turnNumber: number;
  actionCount: number;
  additions?: number;
  deletions?: number;
  detail?: string;
  artifactPath?: string;
  diffPreview?: SessionChangeDiffLine[];
  diffTruncated?: boolean;
}

export interface SessionChangesView {
  files: SessionChangedFile[];
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

export type SessionChangeDiffLine = ChangeDiffLine;

interface SessionChangedFileInternal extends SessionChangedFile {
  sequence: number;
}

export function buildSessionChanges(session: SessionState): SessionChangesView {
  const byPath = new Map<string, SessionChangedFileInternal>();
  let sequence = 0;
  session.turns.forEach((turn, turnIndex) => {
    for (const call of turn.toolCalls) {
      sequence += 1;
      const change = changeFromCall(call, turnIndex + 1, sequence);
      if (!change) continue;
      const previous = byPath.get(change.path);
      byPath.set(change.path, previous ? mergeChange(previous, change) : change);
    }
  });
  const files = [...byPath.values()]
    .sort((a, b) =>
      changePriority(a) - changePriority(b)
      || changeEvidencePriority(a) - changeEvidencePriority(b)
      || b.turnNumber - a.turnNumber
      || b.sequence - a.sequence
      || a.path.localeCompare(b.path)
    )
    .map(({ sequence: _sequence, ...file }) => file);
  const failed = files.filter((file) => file.status === "failed").length;
  const running = files.filter((file) => file.status === "running").length;
  const changed = files.filter((file) => file.status === "changed").length;
  return {
    files,
    summary: changesSummary(files.length, { changed, failed, running }),
    detail: changesDetail(files.length, { changed, failed, running }),
    tone: failed > 0 ? "error" : running > 0 ? "warning" : undefined,
  };
}

export function changedFileDiffText(file: SessionChangedFile): string {
  if (!file.diffPreview || file.diffPreview.length === 0) return "";
  const lines = [`Diff for ${file.path}`, ...file.diffPreview.map((line) => line.text)];
  if (file.diffTruncated) lines.push("Diff preview truncated");
  return lines.join("\n");
}

export function changedFileDraft(file: SessionChangedFile): string {
  const diff = changedFileDiffText(file);
  if (!diff) return `Review and adjust this changed file: ${file.path}`;
  return `Review and adjust this changed file:\nPath: ${file.path}\n\n${diff}`;
}

function changePriority(file: SessionChangedFile): number {
  if (file.status === "failed") return 0;
  if (file.status === "running") return 1;
  return 2;
}

function changeEvidencePriority(file: SessionChangedFile): number {
  return file.diffPreview && file.diffPreview.length > 0 ? 0 : 1;
}

function changeFromCall(call: ToolCallState, turnNumber: number, sequence: number): SessionChangedFileInternal | undefined {
  const operation = changeOperation(call.tool);
  if (!operation) return undefined;
  const path = normalizeChangePath(stringArg(call, "path") ?? stringArg(call, "file") ?? stringArg(call, "filename"));
  if (!path) return undefined;
  return {
    path,
    operation,
    status: changeStatus(call),
    turnNumber,
    sequence,
    actionCount: 1,
    additions: call.changeDiff?.additions,
    deletions: call.changeDiff?.deletions,
    diffPreview: call.changeDiff?.preview,
    diffTruncated: call.changeDiff?.truncated,
    detail: changeDetail(call),
    artifactPath: call.resultArtifactPath,
  };
}

function mergeChange(previous: SessionChangedFileInternal, next: SessionChangedFileInternal): SessionChangedFileInternal {
  return {
    ...previous,
    ...next,
    actionCount: previous.actionCount + 1,
    detail: next.detail ?? previous.detail,
    artifactPath: next.artifactPath ?? previous.artifactPath,
    additions: next.additions ?? previous.additions,
    deletions: next.deletions ?? previous.deletions,
    diffPreview: next.diffPreview ?? previous.diffPreview,
    diffTruncated: next.diffTruncated ?? previous.diffTruncated,
  };
}

function changeOperation(tool: string): SessionChangedFile["operation"] | undefined {
  if (tool === "write_file") return "write";
  if (tool === "edit_file") return "edit";
  return undefined;
}

function changeStatus(call: ToolCallState): SessionChangeStatus {
  if (call.status === "running") return "running";
  return call.status === "error" || (call.exitCode != null && call.exitCode !== 0) ? "failed" : "changed";
}

function changeDetail(call: ToolCallState): string | undefined {
  const source = call.resultSummary || call.result || call.failureKind;
  if (!source) return undefined;
  return summarizePreview(stripDiffLines(source), 120);
}

function stripDiffLines(text: string): string {
  const lines = text.split(/\r?\n/);
  const start = lines.findIndex((line) => /^diff --git\s|^---\s/.test(line));
  if (start === -1) return text;
  const before = lines.slice(0, start).join("\n").trim();
  return before || "Unified diff available";
}

function changesSummary(total: number, counts: { changed: number; failed: number; running: number }): string {
  if (total === 0) return "No file changes";
  if (counts.failed > 0) return `${counts.failed} ${plural("file issue", counts.failed)}`;
  if (counts.running > 0) return `${counts.running} pending ${plural("change", counts.running)}`;
  return `${total} changed ${plural("file", total)}`;
}

function changesDetail(total: number, counts: { changed: number; failed: number; running: number }): string {
  if (total === 0) return "No write or edit actions in this chat.";
  const parts: string[] = [];
  if (counts.changed > 0) parts.push(`${counts.changed} changed`);
  if (counts.running > 0) parts.push(`${counts.running} pending`);
  if (counts.failed > 0) parts.push(`${counts.failed} failed`);
  return parts.join(" · ");
}

function plural(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function stringArg(call: ToolCallState, key: string): string | undefined {
  const value = call.args[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function normalizeChangePath(path: string | undefined): string | undefined {
  if (!path) return undefined;
  const normalized = path.trim().replace(/\\/g, "/");
  const workspaceMatch = normalized.match(/^(?:\/)?workspace\/sessions\/[^/]+\/(.+)$/);
  return workspaceMatch?.[1] || normalized;
}
