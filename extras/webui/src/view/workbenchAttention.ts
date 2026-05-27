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
    return { label: withAction(`${currentIssue.value} ${currentIssue.label.toLowerCase()}`, "View context"), detail: "Open current chat context and recovery evidence.", tone: "error", target: "context" };
  }

  if (workspace?.issue) return { label: withAction("Workspace mismatch", "View workspace"), detail: workspace.issue, tone: "warning", target: "workspace" };

  const failedCommands = run.commands.filter((command) => command.status === "failed").length;
  if (failedCommands > 0) return { label: withAction(failedCommandLabel(failedCommands), "View run"), detail: "Open command output and recovery actions.", tone: "error", target: "run" };

  const failedChanges = changes.files.filter((file) => file.status === "failed").length;
  if (failedChanges > 0) return { label: withAction(fileIssueLabel(failedChanges), "Review changes"), detail: "Open failed change evidence.", tone: "error", target: "changes" };

  const failedFiles = files.items.filter((item) => item.status === "failed").length;
  if (failedFiles > 0) return { label: withAction(fileIssueLabel(failedFiles), "Review files"), detail: "Open failed file evidence.", tone: "error", target: "files" };

  const recovery = overview.metrics.find((metric) => metric.label === "Recovery" && metric.value.trim());
  if (recovery) return { label: withAction("Recovery hint", "View context"), detail: recovery.value, tone: "warning", target: "context" };

  const automationAttention = automation ? automationWorkbenchAttention(automation) : undefined;
  if (automationAttention) return automationAttention;

  const runningCommands = run.commands.filter((command) => command.status === "running").length;
  if (runningCommands > 0) return { label: withAction(runningCommandLabel(runningCommands), "View run"), detail: "Open live command status.", tone: "warning", target: "run" };

  const pendingChanges = changes.files.filter((file) => file.status === "running").length;
  if (pendingChanges > 0) return { label: withAction(pendingChangeLabel(pendingChanges), "Review changes"), detail: "Open pending change evidence.", tone: "warning", target: "changes" };

  const pendingFiles = files.items.filter((item) => item.status === "running").length;
  if (pendingFiles > 0) return { label: withAction(pendingFileLabel(pendingFiles), "Review files"), detail: "Open pending file evidence.", tone: "warning", target: "files" };

  const changedFiles = changes.files.filter((file) => file.status === "changed").length;
  if (changedFiles > 0) return { label: withAction(changedFileLabel(changedFiles), "Review diff"), detail: "Open changed file evidence.", tone: "attention", target: "changes" };

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
