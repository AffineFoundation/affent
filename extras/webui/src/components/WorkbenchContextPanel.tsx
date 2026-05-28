import { displaySessionOverviewMetrics, type SessionOverview } from "../view/sessionOverview";
import type { SessionChangesView } from "../view/sessionChanges";
import type { SessionFilesView } from "../view/sessionFiles";
import type { SessionRunView } from "../view/sessionRun";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import type { WorkbenchAttention } from "../view/workbenchAttention";
import { RunDetails } from "./RunDetails";

export function WorkbenchContextPanel({
  overview,
  hasSelectedSession,
  attention,
  workspace,
  files,
  changes,
  run,
  automationTitle,
  automationDetail,
  defaultOpen = false,
}: {
  overview: SessionOverview;
  hasSelectedSession: boolean;
  attention?: WorkbenchAttention;
  workspace?: SessionWorkspaceView;
  files?: SessionFilesView;
  changes?: SessionChangesView;
  run?: SessionRunView;
  automationTitle?: string;
  automationDetail?: string;
  defaultOpen?: boolean;
}) {
  const metrics = displaySessionOverviewMetrics(overview.metrics);
  const summary = contextSummary(overview, hasSelectedSession);
  const detail = hasSelectedSession ? overview.headline : "Start or open a chat";
  const statusDetail = attention?.target === "context" ? attention.detail : overview.detail;
  const evidence = contextEvidence({ workspace, changes, files, run });

  return (
    <details className="session-skills-panel workbench-context-panel" data-testid="workbench-context-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Context</span>
        <strong>{summary}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-skills-body">
        <div className="workbench-context-status" data-tone={overview.tone} data-testid="workbench-context-status">
          <div>
            <strong>{overview.headline}</strong>
            <span>{statusDetail}</span>
          </div>
          <span className="state-pill" data-tone={overview.tone}>
            {overview.stateLabel}
          </span>
        </div>
        <RunDetails
          metrics={metrics}
          className="workbench-context-details"
          testId="workbench-context-details"
          ariaLabel="Workbench context metrics"
          summaryLabel="Context metrics"
          inlineLimit={2}
        />
        {evidence.length > 0 ? (
          <div className="workbench-context-evidence" data-testid="workbench-context-evidence">
            {evidence.map((item) => (
              <div key={item.label} className="workbench-context-evidence-item" data-tone={item.tone}>
                <strong>{item.label}</strong>
                <span>{item.summary}</span>
                <small>{item.detail}</small>
              </div>
            ))}
          </div>
        ) : null}
        {automationTitle ? (
          <div className="workbench-context-link" data-testid="workbench-context-automation">
            <strong>Automation</strong>
            <span>{automationTitle}</span>
            {automationDetail ? <small>{automationDetail}</small> : null}
          </div>
        ) : null}
        {!hasSelectedSession && metrics.length === 0 ? <div className="session-skills-empty">Start a task or open a saved chat before inspecting session evidence.</div> : null}
      </div>
    </details>
  );
}

function contextSummary(overview: SessionOverview, hasSelectedSession: boolean): string {
  if (overview.active) return overview.stateLabel;
  if (!hasSelectedSession) return "Fresh task";
  if (overview.tone === "error") return "Needs attention";
  if (overview.tone === "warning") return "Review needed";
  return overview.stateLabel || "Chat ready";
}

interface ContextEvidenceItem {
  label: string;
  summary: string;
  detail: string;
  tone?: "warning" | "error";
}

function contextEvidence({
  workspace,
  changes,
  files,
  run,
}: {
  workspace?: SessionWorkspaceView;
  changes?: SessionChangesView;
  files?: SessionFilesView;
  run?: SessionRunView;
}): ContextEvidenceItem[] {
  const items: ContextEvidenceItem[] = [];
  if (workspace?.hasData) {
    items.push({
      label: "Workspace",
      summary: workspace.summary,
      detail: workspace.detail,
      tone: workspace.tone,
    });
  }
  if (changes && changes.files.length > 0) {
    items.push({
      label: "Changes",
      summary: changes.summary,
      detail: changes.detail,
      tone: changes.tone,
    });
  }
  if (files && files.items.length > 0) {
    items.push({
      label: "Files",
      summary: files.summary,
      detail: files.detail,
      tone: files.tone,
    });
  }
  if (run && run.commands.length > 0) {
    items.push({
      label: "Run",
      summary: run.summary,
      detail: run.detail,
      tone: run.tone,
    });
  }
  return items;
}
