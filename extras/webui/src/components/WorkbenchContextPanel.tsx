import { displaySessionOverviewMetrics, type SessionOverview } from "../view/sessionOverview";
import type { SessionChangesView } from "../view/sessionChanges";
import { changesReviewFocus } from "../view/sessionChanges";
import type { SessionContextSummary } from "../api/sessions";
import { formatByteCount } from "../view/byteFormat";
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
  automationTitle,
  automationDetail,
  onSelectSection,
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
  session?: SessionState;
  usage?: WorkbenchContextUsageView;
  contextSummary?: SessionContextSummary;
  automationTitle?: string;
  automationDetail?: string;
  onSelectSection?: (tab: WorkbenchTab) => void;
  defaultOpen?: boolean;
}) {
  const requestMode = latestWorkbenchRequestMode(session);
  const contextInput = { overview, hasSelectedSession, attention, workspace, changes, artifacts, files, run, usage, requestMode, automationTitle, automationDetail };
  const statusDetail = workbenchContextStatusDetail(contextInput);
  const evidence = buildWorkbenchContextEvidence(contextInput);
  const hasEvidence = evidence.length > 0;
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
  }) : undefined;
  const snapshot = hasSelectedSession ? contextSnapshotCards({
    metrics: displaySessionOverviewMetrics(overview.metrics),
    run,
    requestMode,
    session,
  }) : [];

  return (
    <details className="session-skills-panel workbench-context-panel" data-testid="workbench-context-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Context</span>
        <strong>{hasSelectedSession ? "Conversation context" : "No chat selected"}</strong>
      </summary>
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
        {brief ? <ContextBriefCard brief={brief} onSelectSection={onSelectSection} /> : null}
        {snapshot.length > 0 ? (
          <section className="workbench-context-snapshot" data-testid="workbench-context-snapshot" aria-label="Runtime signals">
            <div className="workbench-context-snapshot-head">
              <strong>Runtime signals</strong>
              <span>Needs attention</span>
            </div>
            <div className="workbench-context-snapshot-grid">
              {snapshot.map((item) => {
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
        {hasSelectedSession ? <WorkbenchUsageCard usage={usage} contextSummary={contextSummary} /> : null}
        {hasEvidence ? (
          <div className="workbench-context-evidence" data-testid="workbench-context-evidence">
            {evidence.map((item) => (
              <button
                key={item.target}
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
        {!hasSelectedSession ? <div className="session-skills-empty">Start a task or open a saved chat before inspecting session evidence.</div> : null}
      </div>
    </details>
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
    <section className="workbench-context-brief" data-testid="workbench-context-brief" aria-label="Current situation">
      <div className="workbench-context-brief-main">
        <span>Current situation</span>
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
        >
          <span>Best drilldown</span>
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
}): ContextBriefView {
  const facts = compact([
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
    drilldown: bestContextDrilldown({ workspace, run, changes, files, artifacts, contextSummary }),
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
    target: "workspace",
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
    target: "run",
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
    target: "changes",
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
    target: "artifacts",
  };
}

function contextBriefFact(context?: SessionContextSummary, usage?: WorkbenchContextUsageView): ContextBriefFact | undefined {
  const tokens = workbenchContextUsageSummary(usage);
  if (!context || context.compact_trigger <= 0) {
    return tokens ? { label: "Context", value: tokens, detail: "Token total loaded from trace or session index.", tone: "ready" } : undefined;
  }
  const percent = Math.max(0, Math.min(100, Math.round(context.compact_percent)));
  const tone = percent >= 95 ? "error" : percent >= 72 ? "attention" : "ready";
  return {
    label: "Context",
    value: `${percent}% used`,
    detail: tokens ? `${tokens} · ${formatContextCount(context.messages_until_compact)} messages before compaction` : `${formatContextCount(context.messages_until_compact)} messages before compaction`,
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
}: {
  workspace?: SessionWorkspaceView;
  run?: SessionRunView;
  changes?: SessionChangesView;
  files?: SessionFilesView;
  artifacts?: readonly TurnArtifact[];
  contextSummary?: SessionContextSummary;
}): (ContextBriefFact & { target: WorkbenchTab }) | undefined {
  if (workspace?.hasData && (workspace.verification === "mismatch" || workspace.verification === "missing_binding")) {
    return { label: "Best drilldown", value: "Workspace", detail: workspace.issue ?? "Confirm the real working directory before trusting file operations.", tone: "attention", target: "workspace" };
  }
  if (run?.commands.length) {
    const review = runReviewFocus(run.commands);
    if (review.tone === "danger" || review.tone === "attention") {
      return { label: "Best drilldown", value: "Run", detail: review.detail, tone: review.tone === "danger" ? "error" : "attention", target: "run" };
    }
  }
  if (changes?.files.length) {
    const review = changesReviewFocus(changes.files);
    if (review.tone === "danger" || review.tone === "attention") {
      return { label: "Best drilldown", value: "Changes", detail: review.detail, tone: review.tone === "danger" ? "error" : "attention", target: "changes" };
    }
  }
  if (contextSummary && contextSummary.compact_trigger > 0 && contextSummary.compact_percent >= 72) {
    return { label: "Best drilldown", value: "Context", detail: "Context pressure is high; keep future turns concise or expect compaction.", tone: contextSummary.compact_percent >= 95 ? "error" : "attention", target: "context" };
  }
  if (files?.items.length) return { label: "Best drilldown", value: "Files", detail: files.summary, tone: "ready", target: "files" };
  if (artifacts?.length) return { label: "Best drilldown", value: "Artifacts", detail: `${artifacts.length} captured`, tone: "ready", target: "artifacts" };
  if (run?.commands.length) return { label: "Best drilldown", value: "Run", detail: run.summary, tone: "ready", target: "run" };
  return undefined;
}

function contextSnapshotCards({
  metrics,
  run,
  requestMode,
  session,
}: {
  metrics: ReturnType<typeof displaySessionOverviewMetrics>;
  run?: SessionRunView;
  requestMode?: WorkbenchRequestModeView;
  session?: SessionState;
}): ContextSnapshotCard[] {
  const cards: ContextSnapshotCard[] = [];

  const attention = concreteAttentionSnapshot(metrics, run, session);
  if (attention) cards.push(attention);

  if (requestMode) {
    cards.push({
      key: "request",
      label: "Request mode",
      title: requestMode.label,
      detail: requestMode.detail,
      meta: requestMode.source === "schedule" ? "scheduled" : undefined,
      target: "trace",
    });
  }

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

function WorkbenchUsageCard({ usage, contextSummary }: { usage?: WorkbenchContextUsageView; contextSummary?: SessionContextSummary }) {
  const usageItems = usage?.items ?? [];
  const trend = usage?.trend ?? [];
  const total = workbenchContextUsageSummary(usage);
  const contextHealth = contextHealthView(contextSummary, total);

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

interface ContextHealthView {
  percent?: number;
  label: string;
  detail: string;
  remaining?: string;
  tokenSummary?: string;
  tone: "ready" | "attention" | "error";
  estimated?: boolean;
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
  const bytePercent = Math.max(0, Math.round(context.byte_compact_percent ?? 0));
  const messagePercent = Math.max(0, Math.round(context.message_compact_percent ?? context.compact_percent));
  const byteDominant = bytePercent > messagePercent && (context.compact_trigger_bytes ?? 0) > 0 && (context.context_bytes ?? 0) > 0;
  const tone = percent >= 95 ? "error" : percent >= 72 ? "attention" : "ready";
  const label = percent >= 95
    ? "Compaction likely"
    : percent >= 72
      ? "Context is getting tight"
      : "Context has room";
  const remaining = byteDominant && context.bytes_until_compact != null
    ? context.bytes_until_compact > 0
      ? `${formatByteCount(context.bytes_until_compact)} before compaction`
      : "Compaction byte threshold reached"
    : context.messages_until_compact > 0
    ? `${formatContextCount(context.messages_until_compact)} messages before compaction`
    : "Compaction threshold reached";
  const detail = byteDominant
    ? `${formatByteCount(context.context_bytes ?? 0)} of ${formatByteCount(context.compact_trigger_bytes ?? 0)} context bytes are loaded.`
    : `${formatContextCount(context.message_count)} of ${formatContextCount(context.compact_trigger)} context messages are loaded.`;
  return {
    percent,
    label,
    detail,
    remaining,
    tokenSummary,
    tone,
    estimated: Boolean((context as SessionContextSummary & { estimated?: boolean }).estimated),
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
          {health.estimated ? <small>estimated from loaded trace</small> : null}
        </div>
      </div>
    </div>
  );
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
