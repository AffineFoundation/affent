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
  contentPreview?: string;
  contentSource?: "read_file";
  contentTruncated?: boolean;
  contentBytes?: number;
}

export interface SessionFilesView {
  items: SessionFileEvidence[];
  summary: string;
  detail: string;
  stats?: SessionFilesStats;
  tone?: "warning" | "error";
}

export interface SessionFilesStats {
  total: number;
  available: number;
  failed: number;
  running: number;
  read: number;
  listed: number;
  changed: number;
  snapshots: number;
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
    .sort((a, b) => filePriority(a) - filePriority(b) || b.turnNumber - a.turnNumber || b.sequence - a.sequence || a.path.localeCompare(b.path))
    .map(({ sequence: _sequence, ...item }) => item);
  const failed = items.filter((item) => item.status === "failed").length;
  const running = items.filter((item) => item.status === "running").length;
  const stats = filesStats(items);
  return {
    items,
    summary: filesSummary(items.length, { failed, running }),
    detail: filesDetail(items),
    stats,
    tone: failed > 0 ? "error" : running > 0 ? "warning" : undefined,
  };
}

export function fileEvidenceText(item: SessionFileEvidence): string {
  const lines = [`File evidence for ${item.path}`, `Actions: ${item.actions.join(", ")}`, `Status: ${item.status}`];
  if (item.detail) lines.push(`Detail: ${item.detail}`);
  if (item.next) lines.push(`Next: ${item.next}`);
  if (item.artifactPath) lines.push(`Evidence artifact: ${item.artifactPath}`);
  if (item.contentPreview) {
    lines.push(`Loaded snapshot: ${item.contentTruncated ? "partial read_file output" : "read_file output"}`);
  }
  return lines.join("\n");
}

export function filesEvidenceText(files: SessionFilesView): string {
  const lines = [
    "Session file evidence",
    `Summary: ${files.summary}`,
    `Detail: ${files.detail}`,
  ];
  if (files.items.length === 0) {
    lines.push("No file evidence recorded.");
    return lines.join("\n");
  }
  lines.push("");
  lines.push(...files.items.map(fileEvidenceText).join("\n\n").split("\n"));
  return lines.join("\n");
}

export function filesEvidenceDraft(files: SessionFilesView): string {
  return [
    "Use this file evidence to decide what to inspect, fix, or review next:",
    "",
    filesEvidenceText(files),
  ].join("\n");
}

export function fileEvidenceDraft(item: SessionFileEvidence): string {
  const lead = item.status === "failed"
    ? "Recover this file path before continuing"
    : item.actions.includes("changed")
      ? "Review this changed file in the next step"
      : item.actions.includes("listed")
        ? "Use this listed directory in the next step"
        : "Use this file evidence in the next step";
  return `${lead}:\n${fileEvidenceText(item)}`;
}

export function fileContentText(item: SessionFileEvidence): string {
  const content = item.contentPreview ?? "";
  const lines = [
    `File snapshot for ${item.path}`,
    `Source: ${item.contentSource ?? "read_file"}`,
    `Snapshot: ${item.contentTruncated ? "partial output" : "available output"}`,
  ];
  if (item.contentBytes != null) lines.push(`Bytes: ${item.contentBytes}`);
  lines.push("", content);
  return lines.join("\n");
}

export function fileContentDraft(item: SessionFileEvidence): string {
  const content = item.contentPreview ?? "";
  const snapshot = item.contentTruncated ? "partial read_file output" : "read_file output";
  return [
    "Use this loaded file snapshot in the next step:",
    `File: ${item.path}`,
    `Snapshot: ${snapshot}`,
    "",
    boundedDraftContent(content),
  ].join("\n");
}

function filePriority(item: SessionFileEvidence): number {
  if (item.status === "failed") return 0;
  if (item.status === "running") return 1;
  if (item.actions.includes("changed")) return 2;
  if (item.actions.includes("read")) return 3;
  return 4;
}

function fileEvidenceFromCall(
  call: ToolCallState,
  turnNumber: number,
  sequence: number,
): SessionFileEvidenceInternal | undefined {
  const action = fileAction(call.tool);
  if (!action) return undefined;
  const path = normalizeFilePath(stringArg(call, "path") ?? stringArg(call, "file") ?? stringArg(call, "filename"));
  if (!path) return undefined;
  const detailSource = call.resultSummary || call.result || call.failureKind;
  const contentPreview =
    call.tool === "read_file" && call.status === "success" && call.result ? call.result : undefined;
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
    contentPreview,
    contentSource: contentPreview ? "read_file" : undefined,
    contentTruncated: contentPreview ? call.resultTruncated : undefined,
    contentBytes: contentPreview ? call.resultBytes : undefined,
  };
}

function mergeEvidence(
  previous: SessionFileEvidenceInternal,
  next: SessionFileEvidenceInternal,
): SessionFileEvidenceInternal {
  const nextResolved = next.status === "available";
  return {
    ...next,
    actions: mergeActions(previous.actions, next.actions),
    actionCount: previous.actionCount + 1,
    artifactPath: next.artifactPath ?? previous.artifactPath,
    next: nextResolved ? next.next : next.next ?? previous.next,
    contentPreview: next.contentPreview ?? previous.contentPreview,
    contentSource: next.contentSource ?? previous.contentSource,
    contentTruncated: next.contentPreview ? next.contentTruncated : previous.contentTruncated,
    contentBytes: next.contentPreview ? next.contentBytes : previous.contentBytes,
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

function filesStats(items: SessionFileEvidence[]): SessionFilesStats {
  return {
    total: items.length,
    available: items.filter((item) => item.status === "available").length,
    failed: items.filter((item) => item.status === "failed").length,
    running: items.filter((item) => item.status === "running").length,
    read: items.filter((item) => item.actions.includes("read")).length,
    listed: items.filter((item) => item.actions.includes("listed")).length,
    changed: items.filter((item) => item.actions.includes("changed")).length,
    snapshots: items.filter((item) => item.contentPreview).length,
  };
}

function stringArg(call: ToolCallState, key: string): string | undefined {
  const value = call.args[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function normalizeFilePath(path: string | undefined): string | undefined {
  if (!path) return undefined;
  const normalized = path.trim().replace(/\\/g, "/");
  const workspaceMatch = normalized.match(/^(?:\/)?workspace\/sessions\/[^/]+\/(.+)$/);
  return workspaceMatch?.[1] || normalized;
}

function boundedDraftContent(text: string): string {
  const limit = 4000;
  if (text.length <= limit) return text;
  return `${text.slice(0, limit)}\n\n[Snapshot draft truncated: ${text.length - limit} characters omitted]`;
}
