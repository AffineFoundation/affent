import { displaySessionOverviewMetrics, type SessionOverview } from "../view/sessionOverview";
import type { SessionChangesView } from "../view/sessionChanges";
import { changesReviewFocus } from "../view/sessionChanges";
import type { SessionContextSummary, SessionTaskStateAction, SessionTaskStateEvidence, SessionTaskStateFailure, SessionTaskStateSummary } from "../api/sessions";
import { formatByteCount } from "../view/byteFormat";
import type { DraftSource, UseAsDraft } from "../view/draftSource";
import type { SessionFilesView } from "../view/sessionFiles";
import { filesReviewFocus } from "../view/sessionFiles";
import type { SessionRunView } from "../view/sessionRun";
import { runReviewFocus } from "../view/sessionRun";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import type { TurnArtifact } from "../view/turnArtifacts";
import {
  buildWorkbenchContextEvidence,
  latestWorkbenchRequestMode,
  workbenchContextUsageSummary,
  type WorkbenchContextEvidenceItem,
  type WorkbenchRequestModeView,
  type WorkbenchContextUsageView,
  workbenchContextStatusDetail,
  workbenchArtifactContextDetail,
} from "../view/workbenchContext";
import type { WorkbenchAttention } from "../view/workbenchAttention";
import type { WorkbenchTab } from "../view/workbenchNav";
import type { SessionState, ToolCallState } from "../store/sessionState";

export function WorkbenchContextPanel({
  overview,
  hasSelectedSession,
  attention,
  workspace,
  files,
  changes,
  artifacts,
  run,
  session,
  usage,
  contextSummary,
  taskState,
  automationTitle,
  automationDetail,
  onSelectSection,
  onUseAsDraft,
}: {
  overview: SessionOverview;
  hasSelectedSession: boolean;
  attention?: WorkbenchAttention;
  workspace?: SessionWorkspaceView;
  files?: SessionFilesView;
  changes?: SessionChangesView;
  artifacts?: readonly TurnArtifact[];
  run?: SessionRunView;
  session?: SessionState;
  usage?: WorkbenchContextUsageView;
  contextSummary?: SessionContextSummary;
  taskState?: SessionTaskStateSummary;
  automationTitle?: string;
  automationDetail?: string;
  onSelectSection?: (tab: WorkbenchTab) => void;
  onUseAsDraft?: UseAsDraft;
  defaultOpen?: boolean;
}) {
  const requestMode = latestWorkbenchRequestMode(session);
  const contextInput = { overview, hasSelectedSession, attention, workspace, changes, artifacts, files, run, usage, requestMode, taskState, automationTitle, automationDetail };
  const statusDetail = workbenchContextStatusDetail(contextInput);
  const evidence = buildWorkbenchContextEvidence(contextInput);
  const sourceLinks = taskSourceLinks(evidence, taskState);
  const hasSourceLinks = sourceLinks.length > 0;
  const hasConcreteTaskState = hasTaskState(taskState);
  const brief = hasSelectedSession ? buildContextBrief({
    overview,
    statusDetail,
    workspace,
    files,
    changes,
    artifacts,
    run,
    usage,
    contextSummary,
    taskState,
    requestMode,
  }) : undefined;
  const snapshot = hasSelectedSession ? contextSnapshotCards({
    metrics: displaySessionOverviewMetrics(overview.metrics),
    run,
    session,
  }) : [];
  const actionSnapshots = brief?.drilldown ? [] : snapshot;

  return (
    <section className="session-skills-panel workbench-context-panel" data-testid="workbench-context-panel" data-surface="true">
      <header className="workbench-task-surface-head">
        <span className="session-skills-kicker">Task</span>
        <strong>{hasSelectedSession ? "Current task" : "No task selected"}</strong>
      </header>
      <div className="session-skills-body">
        {overview.tone === "error" ? (
          <div className="workbench-context-status" data-tone="error" data-testid="workbench-context-status">
            <div>
              <strong>{overview.headline}</strong>
              <span>{statusDetail}</span>
            </div>
            <span className="state-pill" data-tone="error">
              {overview.stateLabel}
            </span>
          </div>
        ) : null}
        {!hasConcreteTaskState && brief ? <ContextBriefCard brief={brief} onSelectSection={onSelectSection} /> : null}
        {hasConcreteTaskState ? <TaskStateCard taskState={taskState} onSelectSection={onSelectSection} onUseAsDraft={onUseAsDraft} /> : null}
        {actionSnapshots.length > 0 ? (
          <section className="workbench-context-snapshot" data-testid="workbench-context-snapshot" aria-label="Action needed">
            <div className="workbench-context-snapshot-head">
              <strong>Action needed</strong>
              <span>Open the source tab</span>
            </div>
            <div className="workbench-context-snapshot-grid">
              {actionSnapshots.map((item) => {
                const target = item.target;
                const content = (
                  <>
                    <small>{item.label}</small>
                    <strong>{item.title}</strong>
                    {item.detail ? <span>{item.detail}</span> : null}
                    {item.meta ? <b>{item.meta}</b> : null}
                  </>
                );
                return target ? (
                  <button
                    key={item.key}
                    type="button"
                    className="workbench-context-snapshot-item"
                    data-tone={item.tone === "error" ? "error" : undefined}
                    onClick={() => onSelectSection?.(target)}
                    aria-label={`Open ${item.label}`}
                  >
                    {content}
                  </button>
                ) : (
                  <div key={item.key} className="workbench-context-snapshot-item" data-tone={item.tone === "error" ? "error" : undefined}>
                    {content}
                  </div>
                );
              })}
            </div>
          </section>
        ) : null}
        {hasSelectedSession && shouldShowTaskUsageCard(usage, contextSummary) ? <WorkbenchUsageCard usage={usage} contextSummary={contextSummary} /> : null}
        {hasSourceLinks && !hasConcreteTaskState ? (
          <div className="workbench-context-evidence" data-testid="workbench-context-evidence">
            {sourceLinks.map((item) => (
              <button
                key={`${item.target}:${item.label}`}
                type="button"
                className="workbench-context-evidence-item"
                data-tone={item.tone === "error" ? "error" : undefined}
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
        {!hasSelectedSession ? <div className="session-skills-empty">Start or open a chat to see the objective, next step, and source tabs.</div> : null}
      </div>
    </section>
  );
}

interface ContextSnapshotCard {
  key: string;
  label: string;
  title: string;
  detail?: string;
  meta?: string;
  tone?: SessionOverview["tone"];
  target?: WorkbenchTab;
}

interface ContextBriefView {
  title: string;
  detail?: string;
  facts: ContextBriefFact[];
  drilldown?: ContextBriefFact & { target: WorkbenchTab };
}

interface ContextBriefFact {
  label: string;
  value: string;
  detail?: string;
  tone?: "ready" | "attention" | "error";
  target?: WorkbenchTab;
}

function ContextBriefCard({
  brief,
  onSelectSection,
}: {
  brief: ContextBriefView;
  onSelectSection?: (tab: WorkbenchTab) => void;
}) {
  return (
    <section className="workbench-context-brief" data-testid="workbench-context-brief" aria-label="Current task">
      <div className="workbench-context-brief-main">
        <span>Task</span>
        <strong>{brief.title}</strong>
        {brief.detail ? <p>{brief.detail}</p> : null}
      </div>
      <div className="workbench-context-brief-facts">
        {brief.facts.map((fact) => {
          const content = (
            <>
              <small>{fact.label}</small>
              <strong>{fact.value}</strong>
              {fact.detail ? <span>{fact.detail}</span> : null}
            </>
          );
          return fact.target ? (
            <button
              key={`${fact.label}:${fact.value}`}
              type="button"
              className="workbench-context-brief-fact"
              data-tone={fact.tone}
              onClick={() => onSelectSection?.(fact.target ?? "context")}
              aria-label={`Open ${fact.label}`}
            >
              {content}
            </button>
          ) : (
            <div key={`${fact.label}:${fact.value}`} className="workbench-context-brief-fact" data-tone={fact.tone}>
              {content}
            </div>
          );
        })}
      </div>
      {brief.drilldown ? (
        <button
          type="button"
          className="workbench-context-brief-drilldown"
          data-tone={brief.drilldown.tone}
          onClick={() => onSelectSection?.(brief.drilldown?.target ?? "context")}
          aria-label={`Open ${brief.drilldown.value}`}
        >
          <span>Open first</span>
          <strong>{brief.drilldown.value}</strong>
          {brief.drilldown.detail ? <small>{brief.drilldown.detail}</small> : null}
        </button>
      ) : null}
    </section>
  );
}

function buildContextBrief({
  overview,
  statusDetail,
  workspace,
  files,
  changes,
  artifacts,
  run,
  usage,
  contextSummary,
  taskState,
  requestMode,
}: {
  overview: SessionOverview;
  statusDetail: string;
  workspace?: SessionWorkspaceView;
  files?: SessionFilesView;
  changes?: SessionChangesView;
  artifacts?: readonly TurnArtifact[];
  run?: SessionRunView;
  usage?: WorkbenchContextUsageView;
  contextSummary?: SessionContextSummary;
  taskState?: SessionTaskStateSummary;
  requestMode?: WorkbenchRequestModeView;
}): ContextBriefView {
  const facts = compact([
    taskStateBriefFact(taskState),
    requestModeBriefFact(requestMode),
    workspaceBriefFact(workspace),
    runBriefFact(run),
    changesBriefFact(changes),
    filesBriefFact(files),
    artifactsBriefFact(artifacts),
    contextBriefFact(contextSummary, usage),
  ]).slice(0, 6);

  return {
    title: overview.headline || "Chat ready",
    detail: statusDetail && statusDetail !== overview.headline ? statusDetail : undefined,
    facts,
    drilldown: bestContextDrilldown({ workspace, run, changes, files, artifacts, contextSummary, taskState }),
  };
}

function taskStateBriefFact(taskState?: SessionTaskStateSummary): ContextBriefFact | undefined {
  if (!hasTaskState(taskState)) return undefined;
  const status = taskStatusLabel(taskState.status);
  return {
    label: "Task state",
    value: status,
    detail: taskState.current_step || taskState.next_step || taskState.objective,
    tone: taskStateTone(taskState),
    target: "context",
  };
}

function workspaceBriefFact(workspace?: SessionWorkspaceView): ContextBriefFact | undefined {
  if (!workspace?.hasData) return undefined;
  const tone = workspace.verification === "mismatch" ? "error" : workspace.verification === "missing_binding" ? "attention" : "ready";
  return {
    label: "Workspace",
    value: workspace.summary,
    detail: workspace.path || workspace.lastAgentCwd || workspace.detail,
    tone,
    target: "files",
  };
}

function requestModeBriefFact(requestMode?: WorkbenchRequestModeView): ContextBriefFact | undefined {
  if (!requestMode) return undefined;
  return {
    label: "Request",
    value: requestMode.label,
    detail: requestMode.detail,
    tone: requestMode.source === "schedule" ? "attention" : "ready",
    target: "trace",
  };
}

function runBriefFact(run?: SessionRunView): ContextBriefFact | undefined {
  if (!run || run.commands.length === 0) return undefined;
  const review = runReviewFocus(run.commands);
  return {
    label: "Verification",
    value: review.label,
    detail: review.title,
    tone: review.tone === "danger" ? "error" : review.tone === "attention" ? "attention" : "ready",
    target: "trace",
  };
}

function changesBriefFact(changes?: SessionChangesView): ContextBriefFact | undefined {
  if (!changes || changes.files.length === 0) return undefined;
  const review = changesReviewFocus(changes.files);
  return {
    label: "Changes",
    value: review.title,
    detail: review.detail,
    tone: review.tone === "danger" ? "error" : review.tone === "attention" ? "attention" : "ready",
    target: "files",
  };
}

function filesBriefFact(files?: SessionFilesView): ContextBriefFact | undefined {
  if (!files || files.items.length === 0) return undefined;
  const review = filesReviewFocus(files.items);
  return {
    label: "Files",
    value: review.title,
    detail: review.detail,
    tone: review.tone === "danger" ? "error" : review.tone === "attention" ? "attention" : "ready",
    target: "files",
  };
}

function artifactsBriefFact(artifacts?: readonly TurnArtifact[]): ContextBriefFact | undefined {
  if (!artifacts?.length) return undefined;
  return {
    label: "Artifacts",
    value: `${artifacts.length} captured`,
    detail: workbenchArtifactContextDetail(artifacts),
    tone: "ready",
    target: "trace",
  };
}

function contextBriefFact(context?: SessionContextSummary, usage?: WorkbenchContextUsageView): ContextBriefFact | undefined {
  const tokens = workbenchContextUsageSummary(usage);
  if (!context || context.compact_trigger <= 0) return undefined;
  const percent = Math.max(0, Math.min(100, Math.round(context.compact_percent)));
  const tone = percent >= 95 ? "error" : percent >= 72 ? "attention" : "ready";
  if (tone === "ready") return undefined;
  const pressure = dominantContextPressure(context);
  return {
    label: "Context",
    value: `${percent}% used`,
    detail: tokens ? `${tokens} · ${pressure.remaining}` : pressure.remaining,
    tone,
  };
}

function bestContextDrilldown({
  workspace,
  run,
  changes,
  files,
  artifacts,
  contextSummary,
  taskState,
}: {
  workspace?: SessionWorkspaceView;
  run?: SessionRunView;
  changes?: SessionChangesView;
  files?: SessionFilesView;
  artifacts?: readonly TurnArtifact[];
  contextSummary?: SessionContextSummary;
  taskState?: SessionTaskStateSummary;
}): (ContextBriefFact & { target: WorkbenchTab }) | undefined {
  if (taskState?.open_questions?.length) {
    return { label: "Best drilldown", value: "Task state", detail: taskState.open_questions.at(-1), tone: "attention", target: "context" };
  }
  if (workspace?.hasData && (workspace.verification === "mismatch" || workspace.verification === "missing_binding")) {
    return { label: "Best drilldown", value: "Files", detail: workspace.issue ?? "Confirm the real working directory before trusting file operations.", tone: "attention", target: "files" };
  }
  if (run?.commands.length) {
    const review = runReviewFocus(run.commands);
    if (review.tone === "danger" || review.tone === "attention") {
      return { label: "Best drilldown", value: "Trace", detail: review.detail, tone: review.tone === "danger" ? "error" : "attention", target: "trace" };
    }
  }
  if (changes?.files.length) {
    const review = changesReviewFocus(changes.files);
    if (review.tone === "danger" || review.tone === "attention") {
      return { label: "Best drilldown", value: "Files", detail: review.detail, tone: review.tone === "danger" ? "error" : "attention", target: "files" };
    }
  }
  if (contextSummary && contextSummary.compact_trigger > 0 && contextSummary.compact_percent >= 72) {
    return { label: "Best drilldown", value: "Context", detail: "Context pressure is high; keep future turns concise or expect compaction.", tone: contextSummary.compact_percent >= 95 ? "error" : "attention", target: "context" };
  }
  if (taskStateHasCurrentFailure(taskState)) {
    const latest = taskState?.failed_actions?.at(-1);
    return { label: "Best drilldown", value: "Trace", detail: taskStateFailureSummary(latest), tone: "error", target: "trace" };
  }
  if (files?.items.length) return { label: "Best drilldown", value: "Files", detail: files.summary, tone: "ready", target: "files" };
  if (artifacts?.length) return { label: "Best drilldown", value: "Trace", detail: `${artifacts.length} captured`, tone: "ready", target: "trace" };
  if (run?.commands.length) return { label: "Best drilldown", value: "Trace", detail: run.summary, tone: "ready", target: "trace" };
  return undefined;
}

function TaskStateCard({
  taskState,
  onSelectSection,
  onUseAsDraft,
}: {
  taskState?: SessionTaskStateSummary;
  onSelectSection?: (tab: WorkbenchTab) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  if (!hasTaskState(taskState)) return null;
  const tone = taskStateTone(taskState);
  const nextAction = taskNextAction(taskState);
  const rows = compact([
    taskState.verification_state && taskState.verification_state !== "unknown" ? { label: "Verification", value: verificationStateLabel(taskState.verification_state) } : undefined,
    shouldShowTaskModeFact(taskState) ? { label: "Mode", value: requestModeSummary(taskState) } : undefined,
    taskState.request_source === "schedule" ? { label: "Origin", value: requestSourceSummary(taskState) } : undefined,
  ]);
  const latestFailures = [...(taskState.failed_actions ?? [])].slice(-3).reverse();
  const latestEvidence = [...(taskState.evidence ?? [])].slice(-3).reverse();
  const latestActions = [...(taskState.attempted_actions ?? [])].slice(-3).reverse();
  const changedFiles = [...(taskState.changed_files ?? [])].slice(-3).reverse();
  const currentFailure = taskStateHasCurrentFailure(taskState);
  const evidenceActions = taskEvidenceActions(taskState);
  const traceEvidenceAction = evidenceActions.some((action) => action.target === "trace");
  const filesEvidenceAction = evidenceActions.some((action) => action.target === "files");

  return (
    <section className="workbench-task-state" data-tone={tone} data-testid="workbench-task-state" aria-label="Task state">
      <header className="workbench-task-state-head">
        <div>
          <strong>{taskState.objective || taskStatusLabel(taskState.status)}</strong>
        </div>
        <b data-tone={tone}>{taskStateBadgeLabel(taskState)}</b>
      </header>
      <section className="workbench-task-next" data-tone={nextAction.tone} data-testid="workbench-task-next">
        <div>
          <small>{nextAction.label}</small>
          <strong>{nextAction.title}</strong>
          {nextAction.detail ? <span>{nextAction.detail}</span> : null}
        </div>
        {(nextAction.target && onSelectSection) || (nextAction.draft && onUseAsDraft) ? (
          <div className="workbench-task-next-actions">
            {nextAction.target && onSelectSection ? (
              <button type="button" onClick={() => nextAction.target && onSelectSection(nextAction.target)}>
                {nextAction.actionLabel}
              </button>
            ) : null}
            {nextAction.draft && onUseAsDraft ? (
              <button type="button" onClick={() => nextAction.draft && onUseAsDraft(nextAction.draft.content, nextAction.draft.source)}>
                {nextAction.draft.label}
              </button>
            ) : null}
          </div>
        ) : null}
      </section>
      {rows.length > 0 ? (
        <div className="workbench-task-state-grid">
          {rows.map((row) => (
            <div key={`${row.label}:${row.value}`} className="workbench-task-state-fact">
              <small>{row.label}</small>
              <strong>{row.value}</strong>
            </div>
          ))}
        </div>
      ) : null}
      {evidenceActions.length > 0 ? <TaskEvidenceStrip actions={evidenceActions} onSelectSection={onSelectSection} /> : null}
      {latestFailures.length > 0 && !currentFailure && !traceEvidenceAction ? (
        <TaskStateList
          title="Recent failed actions"
          items={latestFailures.map((item) => taskStateFailureSummary(item))}
          actionLabel={nextAction.target === "trace" ? undefined : "Open trace"}
          onAction={nextAction.target === "trace" ? undefined : () => onSelectSection?.("trace")}
        />
      ) : null}
      {latestActions.length > 0 && !currentFailure && !traceEvidenceAction ? (
        <TaskStateList
          title="Recent actions"
          items={latestActions.map((item) => taskStateActionSummary(item))}
        />
      ) : null}
      {latestEvidence.length > 0 && !traceEvidenceAction ? (
        <TaskStateList
          title="Evidence"
          items={latestEvidence.map((item) => taskStateEvidenceSummary(item))}
        />
      ) : null}
      {changedFiles.length > 0 && !filesEvidenceAction ? (
        <TaskStateList
          title="Changed files"
          items={changedFiles.map((item) => [item.action, item.path].filter(Boolean).join(" "))}
          actionLabel={nextAction.target === "files" ? undefined : "Open files"}
          onAction={nextAction.target === "files" ? undefined : () => onSelectSection?.("files")}
        />
      ) : null}
    </section>
  );
}

interface TaskEvidenceAction {
  target: WorkbenchTab;
  label: string;
  title: string;
  detail?: string;
  tone?: "ready" | "attention" | "error";
}

function TaskEvidenceStrip({
  actions,
  onSelectSection,
}: {
  actions: readonly TaskEvidenceAction[];
  onSelectSection?: (tab: WorkbenchTab) => void;
}) {
  return (
    <section className="workbench-task-evidence-strip" aria-label="Task evidence">
      {actions.map((action) => (
        <button
          key={`${action.target}:${action.label}`}
          type="button"
          data-tone={action.tone}
          onClick={() => onSelectSection?.(action.target)}
        >
          <small>{action.label}</small>
          <strong>{action.title}</strong>
          {action.detail ? <span>{action.detail}</span> : null}
        </button>
      ))}
    </section>
  );
}

function taskEvidenceActions(taskState: SessionTaskStateSummary): TaskEvidenceAction[] {
  const actions: TaskEvidenceAction[] = [];
  const failureCount = taskState.failed_actions?.length ?? 0;
  const attemptedCount = taskState.attempted_actions?.length ?? 0;
  const evidenceCount = taskState.evidence?.length ?? 0;
  const changedCount = taskState.changed_files?.length ?? 0;
  const latestFailure = taskState.failed_actions?.at(-1);
  const latestAction = taskState.attempted_actions?.at(-1);
  const latestFile = taskState.changed_files?.at(-1);

  if (failureCount > 0 || attemptedCount > 0 || evidenceCount > 0) {
    actions.push({
      target: "trace",
      label: "Trace",
      title: taskStateHasCurrentFailure(taskState)
        ? `${failureCount || 1} ${failureCount === 1 ? "failure" : "failures"}`
        : compact([
          attemptedCount > 0 ? `${attemptedCount} ${attemptedCount === 1 ? "action" : "actions"}` : undefined,
          evidenceCount > 0 ? `${evidenceCount} evidence` : undefined,
        ]).join(" · "),
      detail: latestFailure
        ? taskStateFailureSummary(latestFailure)
        : latestAction
          ? taskStateActionSummary(latestAction)
          : taskState.evidence?.at(-1)
            ? taskStateEvidenceSummary(taskState.evidence.at(-1) as SessionTaskStateEvidence)
            : undefined,
      tone: taskStateHasCurrentFailure(taskState) ? "error" : failureCount > 0 ? "attention" : "ready",
    });
  }

  if (changedCount > 0) {
    actions.push({
      target: "files",
      label: "Files",
      title: `${changedCount} changed ${changedCount === 1 ? "file" : "files"}`,
      detail: latestFile ? [latestFile.action, latestFile.path].filter(Boolean).join(" ") : undefined,
      tone: "ready",
    });
  }

  if (taskState.request_source === "schedule") {
    actions.push({
      target: "automation",
      label: "Automation",
      title: requestSourceSummary(taskState) || requestModeSummary(taskState),
      detail: requestSourceSummary(taskState) || undefined,
      tone: "attention",
    });
  }

  return actions.slice(0, 3);
}

interface TaskNextAction {
  label: string;
  title: string;
  detail?: string;
  tone?: "ready" | "attention" | "error";
  target?: WorkbenchTab;
  actionLabel?: string;
  draft?: {
    label: string;
    content: string;
    source: DraftSource;
  };
}

function taskNextAction(taskState: SessionTaskStateSummary): TaskNextAction {
  const question = taskState.open_questions?.at(-1)?.trim();
  if (question) {
    return {
      label: "Answer needed",
      title: summarizeTaskStateText(question) ?? "Question waiting",
      detail: "Reply in chat before the task can continue.",
      tone: "attention",
      draft: {
        label: "Draft answer",
        content: taskAnswerDraft(taskState, question),
        source: "answer",
      },
    };
  }
  if (taskStateHasCurrentFailure(taskState)) {
    const failure = taskState.failed_actions?.at(-1);
    const failureView = taskStateFailureView(failure);
    return {
      label: "Fix first",
      title: failureView.title,
      detail: failureView.detail ?? "Open Trace to inspect the failed runtime event.",
      tone: "error",
      target: "trace",
      actionLabel: "Open trace",
      draft: {
        label: "Draft retry",
        content: taskRetryDraft(taskState, failureView),
        source: "retry_reply",
      },
    };
  }
  const status = taskState.status?.trim().toLowerCase();
  if (status === "completed") {
    const changed = taskState.changed_files?.length ?? 0;
    return {
      label: taskState.verification_state && taskState.verification_state !== "unknown" ? "Verified" : "Completed",
      title: taskState.verification_state && taskState.verification_state !== "unknown" ? verificationStateLabel(taskState.verification_state) : "Task completed",
      detail: changed > 0 ? `${changed} changed ${changed === 1 ? "file" : "files"} available in Files.` : undefined,
      tone: "ready",
      target: changed > 0 ? "files" : undefined,
      actionLabel: "Open files",
    };
  }
  if (taskState.next_step?.trim()) {
    return {
      label: "Next step",
      title: summarizeTaskStateText(taskState.next_step) ?? "Continue task",
      detail: taskState.current_step ? `Current: ${summarizeTaskStateText(taskState.current_step)}` : undefined,
      tone: "attention",
      draft: {
        label: "Draft next",
        content: taskNextStepDraft(taskState),
        source: "continuation",
      },
    };
  }
  if ((taskState.changed_files?.length ?? 0) > 0) {
    return {
      label: "Review",
      title: `${taskState.changed_files?.length} changed ${taskState.changed_files?.length === 1 ? "file" : "files"}`,
      detail: taskState.changed_files?.slice(-2).map((item) => [item.action, item.path].filter(Boolean).join(" ")).join(" · "),
      tone: "ready",
      target: "files",
      actionLabel: "Open files",
    };
  }
  if (taskState.current_step?.trim()) {
    return {
      label: "In progress",
      title: summarizeTaskStateText(taskState.current_step) ?? "Task running",
      tone: "ready",
    };
  }
  return {
    label: "Ready",
    title: taskStatusLabel(taskState.status),
    detail: taskState.objective ? summarizeTaskStateText(taskState.objective) : undefined,
    tone: taskStateTone(taskState),
  };
}

function taskAnswerDraft(taskState: SessionTaskStateSummary, question: string): string {
  return compact([
    taskState.objective ? `Objective: ${taskState.objective}` : undefined,
    "Answer the blocking question so this task can continue:",
    question,
    "",
    "My answer:",
  ]).join("\n");
}

function taskRetryDraft(taskState: SessionTaskStateSummary, failure: TaskFailureView): string {
  return compact([
    taskState.objective ? `Objective: ${taskState.objective}` : undefined,
    "Retry this task after addressing the current failure.",
    `Failure: ${failure.title}`,
    failure.detail ? `Next: ${failure.detail}` : undefined,
    taskState.changed_files?.length ? `Changed files: ${taskState.changed_files.slice(-3).map((item) => item.path).join(", ")}` : undefined,
  ]).join("\n");
}

function taskNextStepDraft(taskState: SessionTaskStateSummary): string {
  return compact([
    taskState.objective ? `Objective: ${taskState.objective}` : undefined,
    taskState.current_step ? `Current: ${summarizeTaskStateText(taskState.current_step)}` : undefined,
    taskState.next_step ? `Next: ${summarizeTaskStateText(taskState.next_step)}` : undefined,
    "Continue from this state and report the result.",
  ]).join("\n");
}

function TaskStateList({
  title,
  items,
  actionLabel,
  onAction,
  tone,
}: {
  title: string;
  items: string[];
  actionLabel?: string;
  onAction?: () => void;
  tone?: "attention" | "error";
}) {
  if (items.length === 0) return null;
  return (
    <div className="workbench-task-state-list" data-tone={tone}>
      <div className="workbench-task-state-list-head">
        <strong>{title}</strong>
        {actionLabel && onAction ? (
          <button type="button" onClick={onAction}>
            {actionLabel}
          </button>
        ) : null}
      </div>
      <ul>
        {items.map((item, index) => (
          <li key={`${index}:${item}`}>{item}</li>
        ))}
      </ul>
    </div>
  );
}

function hasTaskState(taskState?: SessionTaskStateSummary): taskState is SessionTaskStateSummary {
  if (!taskState) return false;
  return Boolean(
    taskState.objective?.trim()
      || taskState.current_step?.trim()
      || taskState.request_mode?.trim()
      || taskState.request_source?.trim()
      || taskState.schedule_id?.trim()
      || taskState.schedule_kind?.trim()
      || taskState.next_step?.trim()
      || (taskState.status && taskState.status !== "unknown")
      || taskState.constraints?.length
      || taskState.known_facts?.length
      || taskState.changed_files?.length
      || taskState.attempted_actions?.length
      || taskState.failed_actions?.length
      || taskState.evidence?.length
      || taskState.open_questions?.length
      || taskState.sources?.length,
  );
}

function shouldShowTaskModeFact(taskState: SessionTaskStateSummary): boolean {
  const mode = taskState.request_mode?.trim().toLowerCase();
  return Boolean(mode && mode !== "normal" && mode !== "execute_plan");
}

function requestModeSummary(taskState: SessionTaskStateSummary): string {
  const mode = taskState.request_mode?.trim().toLowerCase();
  if (mode === "execute_plan") return "Execute plan";
  if (mode === "plan_only") return "Plan only";
  if (mode === "loop_setup") return "Loop setup";
  return mode?.replace(/_/g, " ") || "Normal";
}

function requestSourceSummary(taskState: SessionTaskStateSummary): string {
  const source = taskState.request_source?.trim().toLowerCase();
  const kind = taskState.schedule_kind?.trim().toLowerCase();
  if (source === "schedule") return compact(["Scheduled", scheduleKindSummary(kind)]).join(" ");
  return compact([
    taskState.request_source?.trim(),
    scheduleKindSummary(kind),
  ]).join(" · ");
}

function scheduleKindSummary(kind: string | undefined): string | undefined {
  if (!kind) return undefined;
  if (kind === "loop_tick") return "loop tick";
  if (kind === "daily_checkin") return "daily check-in";
  if (kind === "checkin") return "check-in";
  if (kind === "custom") return "timer";
  return kind.replace(/_/g, " ");
}

function taskStateTone(taskState: SessionTaskStateSummary): "ready" | "attention" | "error" {
  const status = taskState.status?.trim().toLowerCase();
  const verification = taskState.verification_state?.trim().toLowerCase();
  if (status === "failed" || verification === "failed") return "error";
  if ((taskState.open_questions ?? []).length > 0 || status === "blocked" || status === "cancelled") return "attention";
  return "ready";
}

function taskStateHasCurrentFailure(taskState?: SessionTaskStateSummary): boolean {
  if (!taskState) return false;
  const status = taskState.status?.trim().toLowerCase();
  const verification = taskState.verification_state?.trim().toLowerCase();
  return status === "failed" || verification === "failed";
}

function taskStatusLabel(status?: string): string {
  const normalized = status?.trim().toLowerCase();
  if (!normalized || normalized === "unknown") return "Observed";
  if (normalized === "completed") return "Completed";
  if (normalized === "running") return "Running";
  if (normalized === "blocked") return "Blocked";
  if (normalized === "failed") return "Failed";
  if (normalized === "cancelled") return "Cancelled";
  return normalized.replace(/_/g, " ");
}

function taskStateBadgeLabel(taskState: SessionTaskStateSummary): string {
  if (taskStateHasCurrentFailure(taskState)) return "Needs review";
  if ((taskState.open_questions ?? []).length > 0) return "Waiting";
  return taskStatusLabel(taskState.status);
}

function verificationStateLabel(status: string): string {
  const normalized = status.trim().toLowerCase();
  if (normalized === "last_shell_passed") return "Last shell check passed";
  if (normalized === "failed") return "Latest verification failed";
  return normalized.replace(/_/g, " ");
}

function taskStateFailureSummary(item?: SessionTaskStateFailure): string {
  const failure = taskStateFailureView(item);
  return compact([failure.title, failure.detail]).join(" · ") || "Failed action";
}

interface TaskFailureView {
  title: string;
  detail?: string;
}

function taskStateFailureView(item?: SessionTaskStateFailure): TaskFailureView {
  if (!item) return { title: "Failed action" };
  const title = taskStateFailureTitle(item);
  const detail = item.next ? summarizeTaskStateText(item.next) : undefined;
  return { title, detail };
}

function taskStateFailureTitle(item: SessionTaskStateFailure): string {
  const summary = summarizeTaskStateText(item.summary);
  const normalizedSummary = summary?.toLowerCase() ?? "";
  const kind = item.kinds?.[0]?.trim().toLowerCase();
  const action = failureActionLabel(item.tool);

  if (normalizedSummary.includes("context budget") || normalizedSummary.includes("context limit")) {
    return `${action} exceeded context budget`;
  }
  if (kind === "test_failed") {
    return summary ? `Test failed: ${summary}` : "Test failed";
  }
  if (kind === "command_failed") {
    return summary ? `Command failed: ${summary}` : "Command failed";
  }
  if (kind === "timeout" || normalizedSummary.includes("timed out") || normalizedSummary.includes("timeout")) {
    return `${action} timed out`;
  }
  if (kind === "network" || kind === "network_error") {
    return `${action} hit a network error`;
  }
  if (kind) {
    return `${action} failed: ${sentenceCase(failureKindLabel(kind))}`;
  }
  if (summary && !looksLikeRuntimeFailureSummary(summary)) {
    return `${action} failed: ${summary}`;
  }
  return `${action} failed`;
}

function failureActionLabel(tool?: string): string {
  const normalized = tool?.trim().toLowerCase();
  if (!normalized) return "Action";
  if (normalized === "shell") return "Command";
  if (normalized === "browser_click") return "Browser click";
  if (normalized === "browser_open" || normalized === "browser_navigate") return "Browser navigation";
  if (normalized?.startsWith("browser_")) return "Browser action";
  if (normalized === "web_search" || normalized === "search_query") return "Search";
  return sentenceCase(normalized.replace(/_/g, " "));
}

function looksLikeRuntimeFailureSummary(summary: string): boolean {
  const normalized = summary.toLowerCase();
  return normalized.includes("tool failed")
    || normalized.includes("no-tool answer requested")
    || normalized.includes("call_")
    || normalized.includes("tool result context");
}

function sentenceCase(value: string): string {
  const cleaned = value.trim();
  return cleaned ? `${cleaned.charAt(0).toUpperCase()}${cleaned.slice(1)}` : cleaned;
}

function taskStateActionSummary(item: SessionTaskStateAction): string {
  return compact([toolNameLabel(item.tool), summarizeTaskStateText(item.summary)]).join(" · ") || "Action attempted";
}

function taskStateEvidenceSummary(item: SessionTaskStateEvidence): string {
  return compact([sourceLabel(item.source), summarizeTaskStateText(item.summary)]).join(" · ") || "Evidence captured";
}

function summarizeTaskStateText(text?: string): string | undefined {
  const cleaned = text?.replace(/\s+/g, " ").trim();
  if (!cleaned) return undefined;
  return cleaned.length > 120 ? `${cleaned.slice(0, 119).trimEnd()}...` : cleaned;
}

function toolNameLabel(tool?: string): string | undefined {
  const cleaned = tool?.trim();
  return cleaned || undefined;
}

function sourceLabel(source?: string): string | undefined {
  const cleaned = source?.trim();
  if (!cleaned) return undefined;
  if (cleaned === "runtime_workspace") return "runtime workspace";
  if (cleaned === "runtime_surface") return "runtime surface";
  return cleaned.replace(/_/g, " ");
}

function contextSnapshotCards({
  metrics,
  run,
  session,
}: {
  metrics: ReturnType<typeof displaySessionOverviewMetrics>;
  run?: SessionRunView;
  session?: SessionState;
}): ContextSnapshotCard[] {
  const cards: ContextSnapshotCard[] = [];

  const attention = concreteAttentionSnapshot(metrics, run, session);
  if (attention) cards.push(attention);

  return cards.slice(0, 5);
}

function concreteAttentionSnapshot(metrics: ReturnType<typeof displaySessionOverviewMetrics>, run?: SessionRunView, session?: SessionState): ContextSnapshotCard | undefined {
  const issue = metrics.find((metric) => metric.label === "Tool issue" || metric.label === "Tool issues" || metric.label === "Issue" || metric.label === "Issues");
  if (!issue) return undefined;
  const detail = toolIssueDetail(run, session);
  return {
    key: "attention",
    label: issue.label,
    title: `${issue.value} current ${Number(issue.value) === 1 ? "issue" : "issues"}`,
    detail,
    tone: issue.tone === "error" || run?.tone === "error" ? "error" : undefined,
    target: run?.commands.length ? "run" : "trace",
  };
}

function toolIssueDetail(run?: SessionRunView, session?: SessionState): string | undefined {
  const failed = run?.commands.find((command) => command.status === "failed");
  if (failed) return [failed.command, failed.detail, failed.next ? `Next: ${failed.next}` : undefined].filter(Boolean).join(" · ");
  const running = run?.commands.find((command) => command.status === "running");
  if (running) return [running.command, running.detail].filter(Boolean).join(" · ");
  const failedTool = latestFailedTool(session);
  if (failedTool) {
    return [
      toolLabel(failedTool),
      failedTool.failureKind ? failureKindLabel(failedTool.failureKind) : undefined,
      summarizeToolResult(failedTool),
    ].filter(Boolean).join(" · ");
  }
  return "Open trace to inspect the failed tool event.";
}

function latestFailedTool(session?: SessionState): ToolCallState | undefined {
  for (const turn of [...(session?.turns ?? [])].reverse()) {
    for (const call of [...turn.toolCalls].reverse()) {
      if (call.status === "error" || (call.exitCode != null && call.exitCode !== 0) || call.failureKind || call.failureKinds?.length) return call;
    }
  }
  return undefined;
}

function toolLabel(call: ToolCallState): string {
  return call.originalTool && call.originalTool !== call.tool ? `${call.tool} (${call.originalTool})` : call.tool;
}

function summarizeToolResult(call: ToolCallState): string | undefined {
  const text = (call.resultSummary || call.result || "").replace(/\s+/g, " ").trim();
  if (!text) return undefined;
  const cleaned = text
    .replace(/\bNext:\s*.+?(?=\sFailure:|$)/i, "")
    .replace(/\bFailure:\s*kind=[^\s]+/i, "")
    .replace(/\s+/g, " ")
    .trim();
  if (!cleaned) return undefined;
  return cleaned.length > 120 ? `${cleaned.slice(0, 119).trimEnd()}...` : cleaned;
}

function failureKindLabel(kind: string): string {
  const normalized = kind.trim().toLowerCase();
  if (!normalized) return "other";
  if (normalized === "invalid_args") return "invalid request";
  if (normalized === "blocked") return "blocked";
  if (normalized === "timeout") return "timeout";
  if (normalized === "empty_response") return "empty response";
  if (normalized === "dynamic_shell") return "dynamic page";
  if (normalized === "no_results") return "no results";
  if (normalized === "no_matches") return "no matches";
  if (normalized === "network" || normalized === "network_error") return "network";
  if (normalized === "upstream_5xx") return "provider error";
  if (normalized === "llm_timeout") return "model timeout";
  if (normalized === "loop_guard_no_budget") return "action budget";
  if (normalized === "loop_guard_call_cap") return "action limit";
  if (normalized === "loop_guard_repeated_call") return "repeated action";
  if (normalized === "loop_guard_repeated_failures") return "repeated failures";
  if (normalized === "loop_guard_repeated_failed_input") return "repeated failed input";
  if (normalized === "loop_guard_halted_tool") return "halted action";
  if (normalized === "loop_guard_no_new_evidence") return "no new evidence";
  if (normalized === "loop_guard_direct_reader_warning") return "source warning";
  return normalized.replace(/^loop_guard_/, "").replace(/_/g, " ");
}

function taskSourceLinks(items: readonly WorkbenchContextEvidenceItem[], taskState?: SessionTaskStateSummary): WorkbenchContextEvidenceItem[] {
  const links = [...items];
  if (!hasTaskState(taskState)) return links;

  const changedFileCount = taskState.changed_files?.length ?? 0;
  if (changedFileCount > 0 && !hasSourceTarget(links, "files")) {
    links.push({
      target: "files",
      label: "Changed files",
      summary: `${changedFileCount} ${changedFileCount === 1 ? "file" : "files"}`,
      detail: taskState.changed_files?.slice(-3).map((item) => [item.action, item.path].filter(Boolean).join(" ")).join(" · ") || "Task state recorded changed files.",
    });
  }

  const actionCount = taskState.attempted_actions?.length ?? 0;
  const failureCount = taskState.failed_actions?.length ?? 0;
  const evidenceCount = taskState.evidence?.length ?? 0;
  if (actionCount + failureCount + evidenceCount > 0 && !hasSourceTarget(links, "trace")) {
    const latestFailure = taskState.failed_actions?.at(-1);
    const latestAction = taskState.attempted_actions?.at(-1);
    const latestEvidence = taskState.evidence?.at(-1);
    links.push({
      target: "trace",
      label: "Execution record",
      summary: compact([
        actionCount > 0 ? `${actionCount} ${actionCount === 1 ? "action" : "actions"}` : undefined,
        failureCount > 0 ? `${failureCount} ${failureCount === 1 ? "failure" : "failures"}` : undefined,
        evidenceCount > 0 ? `${evidenceCount} evidence` : undefined,
      ]).join(" · "),
      detail: latestFailure ? taskStateFailureSummary(latestFailure) : latestAction ? taskStateActionSummary(latestAction) : taskStateEvidenceSummary(latestEvidence ?? {}),
      tone: taskStateHasCurrentFailure(taskState) ? "error" : failureCount > 0 ? "warning" : undefined,
    });
  }

  return links;
}

function hasSourceTarget(items: readonly WorkbenchContextEvidenceItem[], target: WorkbenchTab): boolean {
  return items.some((item) => item.target === target);
}

function shouldShowTaskUsageCard(usage?: WorkbenchContextUsageView, contextSummary?: SessionContextSummary): boolean {
  if (!contextSummary || contextSummary.compact_trigger <= 0) return false;
  return contextHealthView(contextSummary, workbenchContextUsageSummary(usage)).tone !== "ready";
}

function WorkbenchUsageCard({ usage, contextSummary }: { usage?: WorkbenchContextUsageView; contextSummary?: SessionContextSummary }) {
  const usageItems = usage?.items ?? [];
  const trend = usage?.trend ?? [];
  const total = workbenchContextUsageSummary(usage);
  const contextHealth = contextHealthView(contextSummary, total);
  const contextBreakdown = contextPressureBreakdownItems(contextSummary);

  return (
    <section className="workbench-usage-card" data-testid="workbench-usage-card" aria-label="Token usage">
      <ContextHealthCard health={contextHealth} />
      <div className="workbench-usage-head">
        <div>
          <strong>Token usage</strong>
          <span>{trend.length > 1 ? "Recent turns" : trend.length === 1 ? trend[0].detail ?? "Session total" : "Waiting for usage"}</span>
        </div>
        <b>{total ?? "0.0000M tokens"}</b>
      </div>
      {trend.length > 0 ? <UsageSparkline points={trend} /> : <div className="workbench-usage-empty">Usage appears after a turn ends or when the session index reports totals.</div>}
      {contextBreakdown.length > 0 ? (
        <div className="workbench-usage-breakdown" aria-label="Context pressure breakdown">
          {contextBreakdown.map((item) => (
            <div key={item.label} className="workbench-usage-item">
              <strong>{item.label}</strong>
              <span>{item.value}</span>
              {item.detail ? <small>{item.detail}</small> : null}
            </div>
          ))}
        </div>
      ) : null}
      {usageItems.length > 0 ? (
        <div className="workbench-usage-breakdown">
          {usageItems.slice(0, 3).map((item) => (
            <div key={`${item.label}:${item.value}:${item.detail ?? ""}`} className="workbench-usage-item">
              <strong>{item.label}</strong>
              <span>{item.value}</span>
              {item.detail ? <small>{item.detail}</small> : null}
            </div>
          ))}
        </div>
      ) : null}
    </section>
  );
}

function contextPressureBreakdownItems(context?: SessionContextSummary): Array<{ label: string; value: string; detail?: string }> {
  if (!context) return [];
  const items: Array<{ label: string; value: string; detail?: string }> = [];
  if ((context.estimated_conversation_tokens ?? 0) > 0 || (context.conversation_bytes ?? context.context_bytes ?? 0) > 0) {
    items.push({
      label: "Conversation",
      value: `${formatEstimatedTokenCount(context.estimated_conversation_tokens ?? Math.ceil((context.conversation_bytes ?? context.context_bytes ?? 0) / 4))} estimated tokens`,
      detail: formatByteCount(context.conversation_bytes ?? context.context_bytes ?? 0),
    });
  }
  if ((context.estimated_tool_schema_tokens ?? 0) > 0 || (context.tool_schema_bytes ?? 0) > 0) {
    items.push({
      label: "Tool schema",
      value: `${formatEstimatedTokenCount(context.estimated_tool_schema_tokens ?? Math.ceil((context.tool_schema_bytes ?? 0) / 4))} estimated tokens`,
      detail: formatByteCount(context.tool_schema_bytes ?? 0),
    });
  }
  return items;
}

interface ContextHealthView {
  percent?: number;
  label: string;
  detail: string;
  remaining?: string;
  tokenSummary?: string;
  tone: "ready" | "attention" | "error";
  estimated?: boolean;
  modelContextWindowSource?: string;
}

function contextHealthView(context?: SessionContextSummary, tokenSummary?: string): ContextHealthView {
  if (!context || context.compact_trigger <= 0) {
    return {
      label: "Context not measured",
      detail: "No conversation context summary is available yet.",
      tokenSummary,
      tone: "ready",
    };
  }
  const percent = Math.max(0, Math.min(100, Math.round(context.compact_percent)));
  const pressure = dominantContextPressure(context);
  const tone = percent >= 95 ? "error" : percent >= 72 ? "attention" : "ready";
  const label = percent >= 95
    ? "Compaction likely"
    : percent >= 72
      ? "Context is getting tight"
      : "Context has room";
  return {
    percent,
    label,
    detail: pressure.detail,
    remaining: pressure.remaining,
    tokenSummary,
    tone,
    estimated: Boolean((context as SessionContextSummary & { estimated?: boolean }).estimated),
    modelContextWindowSource: context.model_context_window_source?.trim() || undefined,
  };
}

function dominantContextPressure(context: SessionContextSummary): { detail: string; remaining: string } {
  const messagePercent = Math.max(0, Math.round(context.message_compact_percent ?? context.compact_percent));
  const bytePercent = Math.max(0, Math.round(context.byte_compact_percent ?? 0));
  const requestPercent = Math.max(0, Math.round(context.request_input_compact_percent ?? 0));
  if (requestPercent >= bytePercent && requestPercent > messagePercent && (context.compact_trigger_input_tokens ?? 0) > 0 && (context.estimated_request_input_tokens ?? 0) > 0) {
    return {
      detail: `${formatEstimatedTokenCount(context.estimated_request_input_tokens ?? 0)} estimated input tokens of ${formatEstimatedTokenCount(context.compact_trigger_input_tokens ?? 0)} before the next request.`,
      remaining: (context.request_input_tokens_until_compact ?? 0) > 0
        ? `${formatEstimatedTokenCount(context.request_input_tokens_until_compact ?? 0)} estimated input tokens before compaction`
        : "Request input compaction threshold reached",
    };
  }
  if (bytePercent > messagePercent && (context.compact_trigger_bytes ?? 0) > 0 && (context.context_bytes ?? 0) > 0) {
    return {
      detail: `${formatByteCount(context.context_bytes ?? 0)} of ${formatByteCount(context.compact_trigger_bytes ?? 0)} context bytes are loaded.`,
      remaining: (context.bytes_until_compact ?? 0) > 0
        ? `${formatByteCount(context.bytes_until_compact ?? 0)} before compaction`
        : "Compaction byte threshold reached",
    };
  }
  return {
    detail: `${formatContextCount(context.message_count)} of ${formatContextCount(context.compact_trigger)} context messages are loaded.`,
    remaining: context.messages_until_compact > 0
      ? `${formatContextCount(context.messages_until_compact)} messages before compaction`
      : "Compaction threshold reached",
  };
}

function ContextHealthCard({ health }: { health: ContextHealthView }) {
  return (
    <div className="workbench-context-health" data-tone={health.tone} data-testid="workbench-context-health">
      <ContextHealthRing percent={health.percent} />
      <div className="workbench-context-health-copy">
        <span>Current context</span>
        <strong>{health.label}</strong>
        <p>{health.detail}</p>
        <div className="workbench-context-health-meta">
          {health.remaining ? <small>{health.remaining}</small> : null}
          {health.tokenSummary ? <small>{health.tokenSummary}</small> : null}
          {health.modelContextWindowSource ? <small>window source: {formatContextSource(health.modelContextWindowSource)}</small> : null}
          {health.estimated ? <small>estimated from loaded trace</small> : null}
        </div>
      </div>
    </div>
  );
}

function formatContextSource(source: string): string {
  return source.trim().replace(/[_-]+/g, " ");
}

function ContextHealthRing({ percent }: { percent?: number }) {
  const value = percent == null ? 0 : Math.max(0, Math.min(100, percent));
  const radius = 30;
  const circumference = 2 * Math.PI * radius;
  const dash = (value / 100) * circumference;
  return (
    <span className="workbench-context-health-ring" aria-label={percent == null ? "Context usage unavailable" : `Context usage ${value}%`}>
      <svg viewBox="0 0 76 76" aria-hidden="true">
        <circle className="workbench-context-health-ring-track" cx="38" cy="38" r={radius} />
        <circle
          className="workbench-context-health-ring-value"
          cx="38"
          cy="38"
          r={radius}
          strokeDasharray={`${dash} ${circumference - dash}`}
        />
      </svg>
      <b>{percent == null ? "--" : `${value}%`}</b>
    </span>
  );
}

function formatContextCount(value: number): string {
  if (value >= 1000) return `${(value / 1000).toFixed(value >= 10000 ? 0 : 1)}k`;
  return String(value);
}

function formatEstimatedTokenCount(value: number): string {
  return Math.max(0, Math.round(value)).toLocaleString("en-US");
}

function UsageSparkline({ points }: { points: WorkbenchContextUsageView["trend"] }) {
  const width = 320;
  const height = 94;
  const padX = 14;
  const padY = 16;
  const max = Math.max(...points.map((point) => point.value), 1);
  const cumulative = points.reduce<Array<{ label: string; value: number; valueLabel: string; detail?: string }>>((items, point) => {
    const previous = items.at(-1)?.value ?? 0;
    const value = previous + point.value;
    items.push({ ...point, value, valueLabel: formatTokenCountMillions(value) });
    return items;
  }, []);
  const cumulativeMax = Math.max(...cumulative.map((point) => point.value), 1);
  const coords = points.map((point, index) => {
    const x = points.length === 1 ? width / 2 : padX + (index * (width - padX * 2)) / (points.length - 1);
    const y = height - padY - (point.value / max) * (height - padY * 2);
    return { x, y, point };
  });
  const cumulativeCoords = cumulative.map((point, index) => {
    const x = cumulative.length === 1 ? width / 2 : padX + (index * (width - padX * 2)) / (cumulative.length - 1);
    const y = height - padY - (point.value / cumulativeMax) * (height - padY * 2);
    return { x, y, point };
  });
  const linePoints = coords.length === 1
    ? `${padX},${coords[0].y} ${width - padX},${coords[0].y}`
    : coords.map(({ x, y }) => `${x},${y}`).join(" ");
  const cumulativeLinePoints = cumulativeCoords.length === 1
    ? `${padX},${cumulativeCoords[0].y} ${width - padX},${cumulativeCoords[0].y}`
    : cumulativeCoords.map(({ x, y }) => `${x},${y}`).join(" ");
  const areaPoints = `${padX},${height - padY} ${cumulativeLinePoints} ${width - padX},${height - padY}`;
  const latest = points[points.length - 1];
  const latestCumulative = cumulative[cumulative.length - 1];

  return (
    <div className="workbench-usage-chart">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`Recent token usage, latest ${latest.valueLabel}`}>
        <line className="workbench-usage-grid" x1={padX} y1={padY} x2={width - padX} y2={padY} />
        <line className="workbench-usage-grid" x1={padX} y1={(height / 2)} x2={width - padX} y2={(height / 2)} />
        <line className="workbench-usage-axis" x1={padX} y1={height - padY} x2={width - padX} y2={height - padY} />
        <polygon className="workbench-usage-area" points={areaPoints} />
        <polyline className="workbench-usage-cumulative-line" points={cumulativeLinePoints} />
        <polyline className="workbench-usage-line-shadow" points={linePoints} />
        <polyline className="workbench-usage-line" points={linePoints} />
        {coords.map(({ x, y, point }) => (
          <g key={`${point.label}:${point.value}:${point.detail ?? ""}`} className="workbench-usage-point">
            <circle className="workbench-usage-point-dot" cx={x} cy={y} r="2.2">
              <title>{`${point.label}: ${point.valueLabel}${point.detail ? ` · ${point.detail}` : ""}`}</title>
            </circle>
          </g>
        ))}
      </svg>
      <div className="workbench-usage-chart-labels">
        <span>{points[0]?.label}</span>
        <b>{latestCumulative.valueLabel} over time</b>
        <span>{latest.label}</span>
      </div>
    </div>
  );
}

function formatTokenCountMillions(value: number): string {
  const millions = value / 1_000_000;
  if (value < 10_000) return `${millions.toFixed(4)}M`;
  if (value < 100_000) return `${millions.toFixed(3)}M`;
  return `${millions.toFixed(2)}M`;
}

function compact<T>(items: readonly (T | undefined | null | false)[]): T[] {
  return items.filter(Boolean) as T[];
}
