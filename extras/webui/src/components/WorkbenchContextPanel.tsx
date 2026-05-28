import { displaySessionOverviewMetrics, type SessionOverview } from "../view/sessionOverview";
import type { SessionChangesView } from "../view/sessionChanges";
import type { UseAsDraft } from "../view/draftSource";
import type { SessionFilesView } from "../view/sessionFiles";
import type { SessionRunView } from "../view/sessionRun";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import type { TurnArtifact } from "../view/turnArtifacts";
import {
  buildWorkbenchContextEvidence,
  workbenchContextEvidenceDraft,
  workbenchContextEvidenceText,
  workbenchContextStatusDetail,
  workbenchContextSummary,
} from "../view/workbenchContext";
import type { WorkbenchAttention } from "../view/workbenchAttention";
import type { WorkbenchTab } from "../view/workbenchNav";
import { CopyButton } from "./CopyButton";
import { RunDetails } from "./RunDetails";

export function WorkbenchContextPanel({
  overview,
  hasSelectedSession,
  attention,
  workspace,
  files,
  changes,
  artifacts,
  run,
  automationTitle,
  automationDetail,
  onSelectSection,
  onUseAsDraft,
  defaultOpen = false,
}: {
  overview: SessionOverview;
  hasSelectedSession: boolean;
  attention?: WorkbenchAttention;
  workspace?: SessionWorkspaceView;
  files?: SessionFilesView;
  changes?: SessionChangesView;
  artifacts?: readonly TurnArtifact[];
  run?: SessionRunView;
  automationTitle?: string;
  automationDetail?: string;
  onSelectSection?: (tab: WorkbenchTab) => void;
  onUseAsDraft?: UseAsDraft;
  defaultOpen?: boolean;
}) {
  const metrics = displaySessionOverviewMetrics(overview.metrics);
  const summary = workbenchContextSummary(overview, hasSelectedSession);
  const detail = hasSelectedSession ? overview.headline : "Start or open a chat";
  const contextInput = { overview, hasSelectedSession, attention, workspace, changes, artifacts, files, run, automationTitle, automationDetail };
  const statusDetail = workbenchContextStatusDetail(contextInput);
  const evidence = buildWorkbenchContextEvidence(contextInput);

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
        {hasSelectedSession ? (
          <span className="workbench-context-actions">
            <CopyButton label="Copy context" value={workbenchContextEvidenceText(contextInput)} className="ghost-action" />
            {onUseAsDraft ? (
              <button type="button" className="ghost-action" onClick={() => onUseAsDraft(workbenchContextEvidenceDraft(contextInput), "evidence")}>
                Use context as draft
              </button>
            ) : null}
          </span>
        ) : null}
        {evidence.length > 0 ? (
          <div className="workbench-context-evidence" data-testid="workbench-context-evidence">
            {evidence.map((item) => (
              <button
                key={item.target}
                type="button"
                className="workbench-context-evidence-item"
                data-tone={item.tone}
                onClick={() => onSelectSection?.(item.target)}
                aria-label={`Open ${item.label}`}
              >
                <strong>{item.label}</strong>
                <span>{item.summary}</span>
                <small>{item.detail}</small>
              </button>
            ))}
          </div>
        ) : null}
        {automationTitle ? (
          <button
            type="button"
            className="workbench-context-link"
            data-testid="workbench-context-automation"
            onClick={() => onSelectSection?.("automation")}
          >
            <strong>Automation</strong>
            <span>{automationTitle}</span>
            {automationDetail ? <small>{automationDetail}</small> : null}
          </button>
        ) : null}
        {!hasSelectedSession && metrics.length === 0 ? <div className="session-skills-empty">Start a task or open a saved chat before inspecting session evidence.</div> : null}
      </div>
    </details>
  );
}
