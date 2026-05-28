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

export interface SessionChangesReview {
  label: string;
  title: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export interface SessionChangesFact {
  label: string;
  value: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
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
  if (!diff) {
    return [
      "Inspect this changed file and decide whether it needs a follow-up edit:",
      `Path: ${file.path}`,
      `Operation: ${file.operation}`,
      `Status: ${file.status}`,
      file.detail ? `Latest evidence: ${file.detail}` : undefined,
      file.artifactPath ? `Evidence artifact: ${file.artifactPath}` : undefined,
      "",
      "No diff preview was captured, so read the current file before making changes.",
    ].filter((line): line is string => Boolean(line)).join("\n");
  }
  return `Review and revise this diff if needed:\nPath: ${file.path}\n\n${diff}`;
}

export function changesReviewFocus(files: readonly SessionChangedFile[]): SessionChangesReview {
  if (files.length === 0) {
    return {
      label: "Idle",
      title: "No file writes recorded",
      detail: "File changes will appear here after write or edit actions.",
      tone: "neutral",
    };
  }
  const failed = files.filter((file) => file.status === "failed");
  if (failed.length > 0) {
    return {
      label: "Fix needed",
      title: `${failed.length} failed ${plural("change", failed.length)}`,
      detail: failed[0]?.detail ?? `Start with ${failed[0]?.path ?? "the failed change"}.`,
      tone: "danger",
    };
  }
  const running = files.filter((file) => file.status === "running");
  if (running.length > 0) {
    return {
      label: "Changing now",
      title: `${running.length} pending ${plural("change", running.length)}`,
      detail: `Latest pending file: ${running[0]?.path ?? "unknown"}.`,
      tone: "attention",
    };
  }
  const withoutDiff = files.filter((file) => !file.diffPreview?.length);
  if (withoutDiff.length > 0) {
    return {
      label: "Review gap",
      title: `${withoutDiff.length} ${plural("file", withoutDiff.length)} ${withoutDiff.length === 1 ? "needs" : "need"} current-file review`,
      detail: `No diff preview for ${withoutDiff[0]?.path ?? "one changed file"}; inspect the current file before approving.`,
      tone: "attention",
    };
  }
  return {
    label: "Diff ready",
    title: `${files.length} changed ${plural("file", files.length)} ready to review`,
    detail: "Every changed file has a diff preview captured.",
    tone: "ok",
  };
}

export function changesReviewFacts(files: readonly SessionChangedFile[]): SessionChangesFact[] {
  const total = files.length;
  const diff = files.filter((file) => file.diffPreview?.length).length;
  const evidence = files.filter((file) => file.diffPreview?.length || file.artifactPath).length;
  const additions = sumKnown(files.map((file) => file.additions));
  const deletions = sumKnown(files.map((file) => file.deletions));
  return [
    {
      label: "Files",
      value: String(total),
      detail: total === 1 ? "changed file" : "changed files",
      tone: total > 0 ? "ok" : "neutral",
    },
    {
      label: "Diff",
      value: total > 0 ? `${diff}/${total}` : "0/0",
      detail: "preview captured",
      tone: total === 0 ? "neutral" : diff === total ? "ok" : "attention",
    },
    {
      label: "Evidence",
      value: total > 0 ? `${evidence}/${total}` : "0/0",
      detail: "diff or artifact",
      tone: total === 0 ? "neutral" : evidence === total ? "ok" : evidence > 0 ? "attention" : "danger",
    },
    {
      label: "Scale",
      value: additions != null || deletions != null ? `+${additions ?? 0} -${deletions ?? 0}` : "unknown",
      detail: additions != null || deletions != null ? "from diff metadata" : "diff stats unavailable",
      tone: additions != null || deletions != null ? "neutral" : total > 0 ? "attention" : "neutral",
    },
  ];
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

function sumKnown(values: Array<number | undefined>): number | undefined {
  let total = 0;
  let found = false;
  for (const value of values) {
    if (value == null) continue;
    found = true;
    total += value;
  }
  return found ? total : undefined;
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
