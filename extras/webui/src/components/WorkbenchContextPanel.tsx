import { displaySessionOverviewMetrics, type SessionOverview } from "../view/sessionOverview";
import type { SessionChangesView } from "../view/sessionChanges";
import type { SessionContextSummary } from "../api/sessions";
import type { SessionFilesView } from "../view/sessionFiles";
import type { SessionRunView } from "../view/sessionRun";
import type { SessionWorkspaceView } from "../view/sessionWorkspace";
import type { TurnArtifact } from "../view/turnArtifacts";
import {
  buildWorkbenchContextEvidence,
  workbenchContextUsageSummary,
  type WorkbenchContextUsageView,
  workbenchContextStatusDetail,
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
  const actionMetrics = contextStatusCards(displaySessionOverviewMetrics(overview.metrics), run, session);
  const contextInput = { overview, hasSelectedSession, attention, workspace, changes, artifacts, files, run, usage, automationTitle, automationDetail };
  const statusDetail = workbenchContextStatusDetail(contextInput);
  const evidence = buildWorkbenchContextEvidence(contextInput);
  const hasEvidence = evidence.length > 0;

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
        {hasSelectedSession ? <WorkbenchUsageCard usage={usage} contextSummary={contextSummary} /> : null}
        {actionMetrics.length > 0 ? (
          <div className="workbench-context-actions-list" data-testid="workbench-context-actions-list">
            {actionMetrics.slice(0, 2).map((metric) => (
              <button
                key={`${metric.label}:${metric.value}`}
                type="button"
                className="workbench-context-action"
                data-tone={metric.tone === "error" ? "error" : undefined}
                onClick={metric.target ? () => {
                  if (metric.target) onSelectSection?.(metric.target);
                } : undefined}
                disabled={!metric.target}
              >
                <strong>{metric.label}</strong>
                <span>{metric.value}</span>
                {metric.detail ? <small>{metric.detail}</small> : null}
              </button>
            ))}
          </div>
        ) : null}
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
        {!hasSelectedSession && actionMetrics.length === 0 ? <div className="session-skills-empty">Start a task or open a saved chat before inspecting session evidence.</div> : null}
      </div>
    </details>
  );
}

interface ContextStatusCard {
  label: string;
  value: string;
  detail?: string;
  tone?: SessionOverview["tone"];
  target?: WorkbenchTab;
}

function contextStatusCards(metrics: ReturnType<typeof displaySessionOverviewMetrics>, run?: SessionRunView, session?: SessionState): ContextStatusCard[] {
  return metrics.flatMap((metric): ContextStatusCard[] => {
    if (
      metric.label === "Next step" ||
      metric.label === "Automation" ||
      metric.label === "Context" ||
      metric.label === "Work" ||
      metric.label === "Earlier work" ||
      metric.label === "Tool context" ||
      metric.label === "Tokens" ||
      metric.label === "Turn tokens" ||
      metric.label === "Session tokens"
    ) return [];
    if (metric.label === "Tool issue" || metric.label === "Tool issues") {
      const detail = toolIssueDetail(run, session);
      return [{
        label: metric.label,
        value: metric.value,
        detail,
        tone: metric.tone,
        target: run?.commands.length ? "run" : "trace",
      }];
    }
    return [{ label: metric.label, value: metric.value, tone: metric.tone }];
  });
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
  const tone = percent >= 95 ? "error" : percent >= 72 ? "attention" : "ready";
  const label = percent >= 95
    ? "Compaction likely"
    : percent >= 72
      ? "Context is getting tight"
      : "Context has room";
  const remaining = context.messages_until_compact > 0
    ? `${formatContextCount(context.messages_until_compact)} messages before compaction`
    : "Compaction threshold reached";
  return {
    percent,
    label,
    detail: `${formatContextCount(context.message_count)} of ${formatContextCount(context.compact_trigger)} context messages are loaded.`,
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
