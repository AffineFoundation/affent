import type { SessionState, ToolCallState } from "../store/sessionState";
import { summarizePreview } from "./textPreview";
import { isToolResultStoragePath } from "./toolResultDisplay";

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
  contentStale?: boolean;
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
  staleSnapshots: number;
}

export interface SessionFilesReview {
  label: string;
  title: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

export interface SessionFilesReviewItem extends SessionFilesReview {
  id: string;
  item: SessionFileEvidence;
  action: "open_current" | "view_snapshot" | "recover_path" | "wait";
}

export interface SessionFilesFact {
  label: string;
  value: string;
  detail: string;
  tone?: "ok" | "attention" | "danger" | "neutral";
}

interface SessionFileEvidenceInternal extends SessionFileEvidence {
  sequence: number;
  contentSequence?: number;
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
    .map(({ sequence: _sequence, contentSequence: _contentSequence, ...item }) => item);
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
  if (item.artifactPath) lines.push(`Evidence output: ${fileEvidenceOutputReference(item.artifactPath)}`);
  if (item.contentPreview) {
    lines.push(`Loaded snapshot: ${item.contentTruncated ? "partial read_file output" : "read_file output"}`);
    if (item.contentStale) lines.push("Snapshot freshness: predates the latest write/edit action");
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
    ? "Check this missing file path before continuing"
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

export function fileRangeText(item: SessionFileEvidence, startLine: number, endLine: number): string {
  const lines = fileLines(item);
  const start = Math.max(1, Math.min(startLine, endLine));
  const end = Math.min(lines.length, Math.max(startLine, endLine));
  const selected = lines.slice(start - 1, end).join("\n");
  return [
    `File range for ${item.path}`,
    `Lines: ${start}-${end}`,
    "",
    selected,
  ].join("\n");
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

export function fileRangeDraft(
  item: SessionFileEvidence,
  startLine: number,
  endLine: number,
  intent: "ask" | "edit",
): string {
  const lines = fileLines(item);
  const start = Math.max(1, Math.min(startLine, endLine));
  const end = Math.min(lines.length, Math.max(startLine, endLine));
  const lead = intent === "edit"
    ? "Edit this selected file range in the next step:"
    : "Review this selected file range in the next step:";
  return [
    lead,
    `File: ${item.path}`,
    `Lines: ${start}-${end}`,
    "",
    boundedDraftContent(lines.slice(start - 1, end).join("\n")),
  ].join("\n");
}

export function fileLines(item: SessionFileEvidence): string[] {
  const content = item.contentPreview ?? "";
  if (!content) return [];
  const lines = content.split(/\r?\n/);
  if (lines.length > 1 && lines.at(-1) === "") return lines.slice(0, -1);
  return lines;
}

export function filesReviewFocus(items: readonly SessionFileEvidence[]): SessionFilesReview {
  if (items.length === 0) {
    return {
      label: "Idle",
      title: "No file evidence recorded",
      detail: "Read, list, write, and edit actions will appear here.",
      tone: "neutral",
    };
  }
  const failed = items.find((item) => item.status === "failed");
  if (failed) {
    return {
      label: "Path issue",
      title: failed.path,
      detail: failed.next ? `Next check: ${failed.next}` : failed.detail ?? "A file action failed; verify the path before retrying.",
      tone: "danger",
    };
  }
  const running = items.find((item) => item.status === "running");
  if (running) {
    return {
      label: "Pending",
      title: running.path,
      detail: running.detail ?? "A file action is still running.",
      tone: "attention",
    };
  }
  const changed = items.find((item) => item.actions.includes("changed"));
  if (changed) {
    return {
      label: "Changed file",
      title: changed.path,
      detail: changed.contentStale
        ? "Loaded snapshot predates the latest change; open the current workspace file before review."
        : changed.contentPreview
          ? "Changed file has a loaded snapshot for review."
          : changed.detail ?? "Review the changed file before approving.",
      tone: changed.contentPreview && !changed.contentStale ? "ok" : "attention",
    };
  }
  const snapshot = items.find((item) => item.contentPreview);
  if (snapshot) {
    return {
      label: "Snapshot ready",
      title: snapshot.path,
      detail: snapshot.contentTruncated ? "Partial read_file output is available." : "Loaded read_file output is available.",
      tone: "ok",
    };
  }
  const listed = items.find((item) => item.actions.includes("listed"));
  if (listed) {
    return {
      label: "Directory evidence",
      title: listed.path,
      detail: "Directory listing evidence is available; open the workspace browser for current contents.",
      tone: "neutral",
    };
  }
  return {
    label: "File evidence",
    title: `${items.length} ${plural("file reference", items.length)}`,
    detail: "Inspect file evidence before asking for targeted edits.",
    tone: "neutral",
  };
}

export function filesReviewFacts(items: readonly SessionFileEvidence[]): SessionFilesFact[] {
  const stats = filesStats([...items]);
  return [
    {
      label: "Files",
      value: String(stats.total),
      detail: stats.total === 1 ? "referenced path" : "referenced paths",
      tone: stats.total > 0 ? "neutral" : "neutral",
    },
    {
      label: "Read",
      value: String(stats.read),
      detail: "file snapshots",
      tone: stats.read > 0 ? "ok" : "neutral",
    },
    {
      label: "Changed",
      value: String(stats.changed),
      detail: "write/edit paths",
      tone: stats.changed > 0 ? "attention" : "neutral",
    },
    {
      label: "Snapshots",
      value: stats.total > 0 ? `${stats.snapshots}/${stats.total}` : "0/0",
      detail: "loaded content",
      tone: stats.total === 0 ? "neutral" : stats.snapshots === stats.total ? "ok" : stats.snapshots > 0 ? "attention" : "neutral",
    },
    ...(stats.staleSnapshots > 0 ? [{
      label: "Stale",
      value: String(stats.staleSnapshots),
      detail: "verify current file",
      tone: "attention" as const,
    }] : []),
    {
      label: "Issues",
      value: String(stats.failed + stats.running),
      detail: stats.failed > 0 ? "path failures" : stats.running > 0 ? "pending actions" : "none",
      tone: stats.failed > 0 ? "danger" : stats.running > 0 ? "attention" : "neutral",
    },
  ];
}

export function filesReviewQueue(items: readonly SessionFileEvidence[]): SessionFilesReviewItem[] {
  return items.flatMap((item) => fileReviewItem(item) ?? []);
}

function fileReviewItem(item: SessionFileEvidence): SessionFilesReviewItem | undefined {
  if (item.status === "failed") {
    return {
      id: `${item.path}:failed`,
      label: "Check path",
      title: item.path,
      detail: item.next ?? item.detail ?? "The last file action failed; verify the path before retrying.",
      tone: "danger",
      item,
      action: "recover_path",
    };
  }
  if (item.status === "running") {
    return {
      id: `${item.path}:running`,
      label: "Pending file action",
      title: item.path,
      detail: item.detail ?? "The file action has not returned yet.",
      tone: "attention",
      item,
      action: "wait",
    };
  }
  if (item.actions.includes("changed")) {
    return {
      id: `${item.path}:changed`,
      label: item.contentStale ? "Verify current file" : "Review changed file",
      title: item.path,
      detail: item.contentStale
        ? "A loaded snapshot exists, but it predates the latest write/edit."
        : item.contentPreview
          ? "A changed-file snapshot is loaded for line review."
          : item.detail ?? "Agent wrote or edited this file.",
      tone: item.contentPreview && !item.contentStale ? "ok" : "attention",
      item,
      action: item.contentPreview && !item.contentStale ? "view_snapshot" : "open_current",
    };
  }
  if (item.contentPreview) {
    return {
      id: `${item.path}:snapshot`,
      label: "Review snapshot",
      title: item.path,
      detail: item.contentTruncated ? "Partial read_file output is loaded." : "read_file output is loaded.",
      tone: "ok",
      item,
      action: "view_snapshot",
    };
  }
  if (item.actions.includes("listed")) {
    return {
      id: `${item.path}:listed`,
      label: "Browse directory",
      title: item.path,
      detail: "Directory listing evidence exists; open current contents before relying on it.",
      tone: "neutral",
      item,
      action: "open_current",
    };
  }
  return undefined;
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
    contentStale: false,
    contentBytes: contentPreview ? call.resultBytes : undefined,
    contentSequence: contentPreview ? sequence : undefined,
  };
}

function fileEvidenceOutputReference(path: string): string {
  return isToolResultStoragePath(path) ? "captured" : path;
}

function mergeEvidence(
  previous: SessionFileEvidenceInternal,
  next: SessionFileEvidenceInternal,
): SessionFileEvidenceInternal {
  const nextResolved = next.status === "available";
  const contentPreview = next.contentPreview ?? previous.contentPreview;
  const contentSequence = next.contentPreview ? next.sequence : previous.contentSequence;
  const changedAfterSnapshot = Boolean(contentPreview && contentSequence != null && next.actions.includes("changed") && next.sequence > contentSequence);
  return {
    ...next,
    actions: mergeActions(previous.actions, next.actions),
    actionCount: previous.actionCount + 1,
    artifactPath: next.artifactPath ?? previous.artifactPath,
    next: nextResolved ? next.next : next.next ?? previous.next,
    contentPreview,
    contentSource: next.contentSource ?? previous.contentSource,
    contentTruncated: next.contentPreview ? next.contentTruncated : previous.contentTruncated,
    contentStale: next.contentPreview ? false : Boolean(previous.contentStale || changedAfterSnapshot),
    contentBytes: next.contentPreview ? next.contentBytes : previous.contentBytes,
    contentSequence,
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
    staleSnapshots: items.filter((item) => item.contentStale).length,
  };
}

function plural(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
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
