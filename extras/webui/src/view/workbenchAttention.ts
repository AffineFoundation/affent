import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import type { SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import type { SessionWorkspaceView } from "./sessionWorkspace";

export type WorkbenchAttentionTone = "error" | "warning" | "attention";
export type WorkbenchAttentionTarget = "context" | "workspace" | "files" | "changes" | "run" | "automation";

export interface WorkbenchAttention {
  label: string;
  detail: string;
  tone: WorkbenchAttentionTone;
  target: WorkbenchAttentionTarget;
}

export function buildWorkbenchAttention({
  overview,
  files,
  changes,
  run,
  workspace,
  automation,
}: {
  overview: SessionOverview;
  files: SessionFilesView;
  changes: SessionChangesView;
  run: SessionRunView;
  workspace?: SessionWorkspaceView;
  automation?: { title: string; detail: string };
}): WorkbenchAttention | undefined {
  const currentIssue = overview.metrics.find((metric) => (metric.label === "Issue" || metric.label === "Issues") && metric.value.trim());
  if (currentIssue) {
    const failedCommand = run.commands.find((command) => command.status === "failed" && command.detail);
    const issueDetail = failedCommand?.detail ?? overview.detail;
    const fact = currentIssueFact(currentIssue.value, currentIssue.label, issueDetail);
    return { label: withAction(fact, "View context"), detail: currentIssueDetail(issueDetail, failedCommand?.next), tone: "error", target: "context" };
  }

  if (workspace?.issue) return { label: withAction("Workspace mismatch", "View workspace"), detail: workspace.issue, tone: "warning", target: "workspace" };

  const failedCommands = run.commands.filter((command) => command.status === "failed").length;
  if (failedCommands > 0) return { label: withAction(failedCommandLabel(failedCommands), "Open trace"), detail: commandAttentionDetail(run.commands, "failed"), tone: "error", target: "run" };

  const failedChanges = changes.files.filter((file) => file.status === "failed").length;
  if (failedChanges > 0) return { label: withAction(fileIssueLabel(failedChanges), "Open files"), detail: changedFileAttentionDetail(changes.files, "failed"), tone: "error", target: "changes" };

  const failedFiles = files.items.filter((item) => item.status === "failed").length;
  if (failedFiles > 0) return { label: withAction(fileIssueLabel(failedFiles), "Review files"), detail: fileAttentionDetail(files.items, "failed"), tone: "error", target: "files" };

  const recovery = overview.metrics.find((metric) => (metric.label === "Next step" || metric.label === "Recovery") && metric.value.trim());
  if (recovery) return { label: withAction("Suggested next step", "View context"), detail: recovery.value, tone: "warning", target: "context" };

  const automationAttention = automation ? automationWorkbenchAttention(automation) : undefined;
  if (automationAttention) return automationAttention;

  const runningCommands = run.commands.filter((command) => command.status === "running").length;
  if (runningCommands > 0) return { label: withAction(runningCommandLabel(runningCommands), "Open trace"), detail: commandAttentionDetail(run.commands, "running"), tone: "warning", target: "run" };

  const pendingChanges = changes.files.filter((file) => file.status === "running").length;
  if (pendingChanges > 0) return { label: withAction(pendingChangeLabel(pendingChanges), "Open files"), detail: changedFileAttentionDetail(changes.files, "running"), tone: "warning", target: "changes" };

  const pendingFiles = files.items.filter((item) => item.status === "running").length;
  if (pendingFiles > 0) return { label: withAction(pendingFileLabel(pendingFiles), "Review files"), detail: fileAttentionDetail(files.items, "running"), tone: "warning", target: "files" };

  const changedFiles = changes.files.filter((file) => file.status === "changed").length;
  if (changedFiles > 0) return { label: withAction(changedFileLabel(changedFiles), "Open files"), detail: changedFileAttentionDetail(changes.files, "changed"), tone: "attention", target: "changes" };

  return undefined;
}

function automationWorkbenchAttention(automation: { title: string; detail: string }): WorkbenchAttention | undefined {
  const title = automation.title.trim();
  const normalized = title.toLowerCase();
  if (!title) return undefined;
  if (normalized.includes("failed") || normalized.includes("error")) {
    return { label: withAction(title, "Open automation"), detail: automation.detail, tone: "error", target: "automation" };
  }
  if (normalized.includes("waiting") || normalized.includes("review") || normalized.includes("pending")) {
    return { label: withAction(title, "Open automation"), detail: automation.detail, tone: "warning", target: "automation" };
  }
  return undefined;
}

function withAction(fact: string, action: string): string {
  return `${fact} · ${action}`;
}

function commandAttentionDetail(commands: SessionRunView["commands"], status: SessionRunView["commands"][number]["status"]): string {
  const matches = commands.filter((item) => item.status === status);
  const command = matches[0];
  if (!command) return status === "running" ? "Command is still running." : "Open command output and recovery actions.";
  const parts = [command.command, command.detail, command.next ? `Next: ${command.next}` : undefined].filter((item): item is string => !!item?.trim());
  return summarizeWithRemainder(parts.join(" · "), matches.length, 120);
}

function changedFileAttentionDetail(files: SessionChangesView["files"], status: SessionChangesView["files"][number]["status"]): string {
  const matches = files.filter((item) => item.status === status);
  const file = matches[0];
  if (!file) return status === "running" ? "Pending change evidence." : status === "failed" ? "Failed change evidence." : "Changed file evidence.";
  const parts = [file.path, file.detail].filter((item): item is string => !!item?.trim());
  return summarizeWithRemainder(parts.join(" · "), matches.length, 120);
}

function fileAttentionDetail(files: SessionFilesView["items"], status: SessionFilesView["items"][number]["status"]): string {
  const matches = files.filter((item) => item.status === status);
  const file = matches[0];
  if (!file) return status === "running" ? "Pending file evidence." : "Failed file evidence.";
  const parts = [file.path, file.detail, file.next ? `Next: ${file.next}` : undefined].filter((item): item is string => !!item?.trim());
  return summarizeWithRemainder(parts.join(" · "), matches.length, 120);
}

function currentIssueFact(value: string, label: string, detail: string): string {
  const issueDetail = issueDetailSummary(detail);
  if (issueDetail) return `Issue: ${issueDetail}`;
  return `${value} ${label.toLowerCase()}`;
}

function currentIssueDetail(detail: string, next?: string): string {
  const summary = issueDetailSummary(detail);
  if (!summary) return "Open current chat context and recovery evidence.";
  return next ? `${summary} · Next: ${next}` : summary;
}

function issueDetailSummary(detail: string): string | undefined {
  const normalized = detail.replace(/\s+/g, " ").trim();
  if (!normalized || isGenericIssueDetail(normalized)) return undefined;
  return summarize(normalized, 64);
}

function isGenericIssueDetail(detail: string): boolean {
  return /^(open|view|check) current chat context/i.test(detail) || /^describe the outcome you want/i.test(detail);
}

function failedCommandLabel(count: number): string {
  return `${count} failed ${plural("command", count)}`;
}

function runningCommandLabel(count: number): string {
  return `${count} running ${plural("command", count)}`;
}

function fileIssueLabel(count: number): string {
  return `${count} file ${count === 1 ? "issue" : "issues"}`;
}

function pendingChangeLabel(count: number): string {
  return `${count} pending ${plural("change", count)}`;
}

function pendingFileLabel(count: number): string {
  return `${count} pending file ${count === 1 ? "action" : "actions"}`;
}

function changedFileLabel(count: number): string {
  return `${count} changed ${plural("file", count)}`;
}

function plural(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}

function summarize(text: string, limit: number): string {
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 1)).trimEnd()}...`;
}

function summarizeWithRemainder(text: string, count: number, limit: number): string {
  if (count <= 1) return summarize(text, limit);
  const suffix = ` · +${count - 1} more`;
  return `${summarize(text, Math.max(1, limit - suffix.length))}${suffix}`;
}
