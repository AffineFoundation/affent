import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import { displaySessionOverviewMetrics, type SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import type { SessionWorkspaceView } from "./sessionWorkspace";
import type { WorkbenchAttention } from "./workbenchAttention";
import type { WorkbenchTab } from "./workbenchNav";

export interface WorkbenchContextEvidenceItem {
  target: WorkbenchTab;
  label: string;
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

export interface WorkbenchContextEvidenceInput {
  overview: SessionOverview;
  hasSelectedSession: boolean;
  attention?: WorkbenchAttention;
  workspace?: SessionWorkspaceView;
  changes?: SessionChangesView;
  files?: SessionFilesView;
  run?: SessionRunView;
  automationTitle?: string;
  automationDetail?: string;
}

export function workbenchContextSummary(overview: SessionOverview, hasSelectedSession: boolean): string {
  if (overview.active) return overview.stateLabel;
  if (!hasSelectedSession) return "Fresh task";
  if (overview.tone === "error") return "Needs attention";
  if (overview.tone === "warning") return "Review needed";
  return overview.stateLabel || "Chat ready";
}

export function workbenchContextStatusDetail(input: Pick<WorkbenchContextEvidenceInput, "overview" | "attention">): string {
  return input.attention?.target === "context" ? input.attention.detail : input.overview.detail;
}

export function buildWorkbenchContextEvidence({
  workspace,
  changes,
  files,
  run,
}: Pick<WorkbenchContextEvidenceInput, "workspace" | "changes" | "files" | "run">): WorkbenchContextEvidenceItem[] {
  const items: WorkbenchContextEvidenceItem[] = [];
  if (workspace?.hasData) {
    items.push({
      target: "workspace",
      label: "Workspace",
      summary: workspace.summary,
      detail: workspace.detail,
      tone: workspace.tone,
    });
  }
  if (changes && changes.files.length > 0) {
    items.push({
      target: "changes",
      label: "Changes",
      summary: changes.summary,
      detail: changes.detail,
      tone: changes.tone,
    });
  }
  if (files && files.items.length > 0) {
    items.push({
      target: "files",
      label: "Files",
      summary: files.summary,
      detail: files.detail,
      tone: files.tone,
    });
  }
  if (run && run.commands.length > 0) {
    items.push({
      target: "run",
      label: "Run",
      summary: run.summary,
      detail: run.detail,
      tone: run.tone,
    });
  }
  return items;
}

export function workbenchContextEvidenceText(input: WorkbenchContextEvidenceInput): string {
  const lines = [
    "Workbench context evidence",
    `State: ${input.overview.stateLabel || "Ready"}`,
    `Headline: ${input.overview.headline}`,
    `Detail: ${workbenchContextStatusDetail(input)}`,
  ];
  const metrics = displaySessionOverviewMetrics(input.overview.metrics);
  for (const metric of metrics) lines.push(`${metric.label}: ${metric.value}`);
  for (const item of buildWorkbenchContextEvidence(input)) lines.push(`${item.label}: ${item.summary} · ${item.detail}`);
  if (input.automationTitle) {
    lines.push(`Automation: ${input.automationTitle}${input.automationDetail ? ` · ${input.automationDetail}` : ""}`);
  }
  return lines.filter((line) => line.trim()).join("\n");
}

export function workbenchContextEvidenceDraft(input: WorkbenchContextEvidenceInput): string {
  return `Use this current chat context in the next step:\n${workbenchContextEvidenceText(input)}`;
}
