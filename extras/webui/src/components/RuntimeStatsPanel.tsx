import type { ServerAggregateStats, ServerStatsResponse, StatsRuntimeSnapshot, StatsToolSnapshot } from "../api/stats";
import { formatByteCount } from "../view/byteFormat";

export function RuntimeStatsPanel({
  stats,
  loading = false,
  error,
  defaultOpen = false,
}: {
  stats?: ServerStatsResponse;
  loading?: boolean;
  error?: string;
  defaultOpen?: boolean;
}) {
  const summary = loading ? "Loading runtime" : error ? "Runtime unavailable" : runtimeSummary(stats);
  const detail = loading ? "Reading server diagnostics." : error ? "Open for route, proxy, or build details." : runtimeDetail(stats);
  const metrics = stats ? runtimeMetrics(stats) : [];

  return (
    <details className="session-skills-panel runtime-stats-panel" data-testid="runtime-stats-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Runtime</span>
        <strong>{summary}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? <div className="session-skills-empty">Loading runtime diagnostics...</div> : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {!loading && !error ? (
          <div className="runtime-stats-grid" data-testid="runtime-stats-grid">
            {metrics.map((metric) => (
              <span key={`${metric.label}:${metric.value}`} className="session-tools-runtime-chip" data-tone={metric.tone}>
                <strong>{metric.label}</strong>
                {metric.value}
              </span>
            ))}
          </div>
        ) : null}
      </div>
    </details>
  );
}

type RuntimeMetricTone = "ready" | "warning" | "muted" | "unknown";

interface RuntimeMetric {
  label: string;
  value: string;
  tone: RuntimeMetricTone;
}

function runtimeSummary(stats?: ServerStatsResponse): string {
  if (!stats) return "No runtime snapshot";
  if (stats.shutting_down) return "Shutting down";
  const model = stats.model?.trim();
  return model || "Runtime ready";
}

function runtimeDetail(stats?: ServerStatsResponse): string {
  if (!stats) return "Open Workbench while connected to inspect server health.";
  const parts: string[] = [];
  const sessions = stats.active_sessions ?? stats.sessions?.length ?? 0;
  parts.push(`${sessions} ${sessions === 1 ? "session" : "sessions"}`);
  const running = stats.running_turns ?? 0;
  if (running > 0) parts.push(`${running} running`);
  if (stats.eval_mode) parts.push(evalModeDetail(stats));
  if (stats.executor_mode) parts.push(`executor ${stats.executor_mode}`);
  return parts.join(" · ");
}

function runtimeMetrics(stats: ServerStatsResponse): RuntimeMetric[] {
  const aggregate = stats.aggregate;
  const tools = aggregate?.tools;
  const runtime = aggregate?.runtime;
  const metrics: RuntimeMetric[] = [
    { label: "Mode", value: stats.eval_mode ? evalModeDetail(stats) : "standard", tone: stats.eval_mode ? "warning" : "ready" },
    { label: "Tools", value: toolSurface(stats), tone: stats.eval_all_tools ? "warning" : "ready" },
  ];
  if (aggregate) metrics.push({ label: "Tokens", value: tokenSummary(aggregate), tone: "muted" });
  const source = sourceMetric(tools);
  if (source) metrics.push(source);
  const recall = recallMetric(tools);
  if (recall) metrics.push(recall);
  const memory = memoryMetric(tools);
  if (memory) metrics.push(memory);
  const compaction = compactionMetric(runtime);
  if (compaction) metrics.push(compaction);
  const toolContext = toolContextMetric(tools);
  if (toolContext) metrics.push(toolContext);
  const loop = loopMetric(tools, runtime);
  if (loop) metrics.push(loop);
  const errors = errorMetric(tools, runtime);
  if (errors) metrics.push(errors);
  const browser = browserMetric(aggregate);
  if (browser) metrics.push(browser);
  return metrics;
}

function evalModeDetail(stats: ServerStatsResponse): string {
  if (stats.eval_all_tools) return "eval · all tools";
  const tools = stats.eval_tools?.trim();
  if (tools) return `eval · ${tools}`;
  return "eval · no default tools";
}

function toolSurface(stats: ServerStatsResponse): string {
  const enabled = [
    stats.enable_builtins ? "workspace" : undefined,
    stats.enable_web_search ? "web search" : stats.enable_web ? "web" : undefined,
    stats.enable_browser ? "browser" : undefined,
    stats.enable_memory ? (stats.shared_user_memory ? "memory (shared user)" : "memory") : undefined,
    stats.enable_subagent ? "subagent" : undefined,
    stats.enable_focused_tasks ? "focused" : undefined,
  ].filter((item): item is string => !!item);
  return enabled.length > 0 ? enabled.join(" · ") : "minimal";
}

function tokenSummary(aggregate: ServerAggregateStats): string {
  const total = aggregate.input_tokens + aggregate.output_tokens;
  const turns = aggregate.turns;
  const parts = [formatCount(total)];
  if (turns > 0) parts.push(`${turns} turns`);
  return parts.join(" · ");
}

function sourceMetric(tools?: StatsToolSnapshot): RuntimeMetric | undefined {
  const total = tools?.source_access_results ?? 0;
  if (total <= 0) return undefined;
  const verified = tools?.source_access_verified ?? 0;
  const parts = [`${verified}/${total} verified`];
  if ((tools?.source_access_network ?? 0) > 0) parts.push(`${tools?.source_access_network} network`);
  if ((tools?.source_access_dynamic_partial ?? 0) > 0) parts.push(`${tools?.source_access_dynamic_partial} partial`);
  if ((tools?.source_access_discovery_only ?? 0) > 0) parts.push(`${tools?.source_access_discovery_only} discovery`);
  return { label: "Evidence", value: parts.join(" · "), tone: verified < total ? "warning" : "ready" };
}

function recallMetric(tools?: StatsToolSnapshot): RuntimeMetric | undefined {
  const calls = tools?.session_search_calls ?? 0;
  const results = tools?.session_search_results ?? 0;
  if (calls <= 0 && results <= 0) return undefined;
  const parts = [`${results} ${results === 1 ? "hit" : "hits"}`];
  if (calls > 1 || results === 0) parts.push(`${calls} ${calls === 1 ? "search" : "searches"}`);
  if ((tools?.session_search_context_hits ?? 0) > 0) parts.push(`${tools?.session_search_context_hits} context`);
  if ((tools?.session_search_matched_terms ?? 0) > 0) parts.push(`${tools?.session_search_matched_terms} terms`);
  return { label: "Recall", value: parts.join(" · "), tone: results > 0 ? "ready" : "warning" };
}

function memoryMetric(tools?: StatsToolSnapshot): RuntimeMetric | undefined {
  const updates = tools?.memory_updates ?? 0;
  if (updates <= 0) return undefined;
  const parts = [`${updates} ${updates === 1 ? "update" : "updates"}`];
  if ((tools?.memory_update_add ?? 0) > 0) parts.push(`${tools?.memory_update_add} add`);
  if ((tools?.memory_update_replace ?? 0) > 0) parts.push(`${tools?.memory_update_replace} replace`);
  if ((tools?.memory_update_remove ?? 0) > 0) parts.push(`${tools?.memory_update_remove} remove`);
  return { label: "Memory", value: parts.join(" · "), tone: "ready" };
}

function compactionMetric(runtime?: StatsRuntimeSnapshot): RuntimeMetric | undefined {
  const count = runtime?.context_compactions ?? 0;
  if (count <= 0) return undefined;
  const parts = [`${count} ${count === 1 ? "compaction" : "compactions"}`];
  if ((runtime?.context_compactions_reactive ?? 0) > 0) parts.push(`${runtime?.context_compactions_reactive} reactive`);
  if ((runtime?.context_compaction_removed_messages ?? 0) > 0) parts.push(`-${runtime?.context_compaction_removed_messages} msgs`);
  if ((runtime?.context_compaction_summary_bytes ?? 0) > 0) parts.push(`${formatByteCount(runtime?.context_compaction_summary_bytes ?? 0)} summary`);
  if ((runtime?.context_compaction_summary_missing ?? 0) > 0) parts.push(`${runtime?.context_compaction_summary_missing} missing`);
  if ((runtime?.context_compaction_summary_empty ?? 0) > 0) parts.push(`${runtime?.context_compaction_summary_empty} empty`);
  const weakSummaries = (runtime?.context_compaction_summary_missing ?? 0) + (runtime?.context_compaction_summary_empty ?? 0);
  return { label: "Context", value: parts.join(" · "), tone: weakSummaries > 0 || (runtime?.context_compactions_reactive ?? 0) > 0 ? "warning" : "ready" };
}

function toolContextMetric(tools?: StatsToolSnapshot): RuntimeMetric | undefined {
  const truncated = tools?.tool_context_truncated ?? 0;
  if (truncated <= 0) return undefined;
  const parts = [`${truncated} ${truncated === 1 ? "trim" : "trims"}`];
  const omitted = tools?.tool_context_omitted_bytes ?? 0;
  if (omitted > 0) parts.push(`${formatByteCount(omitted)} omitted`);
  return { label: "Tool context", value: parts.join(" · "), tone: "warning" };
}

function loopMetric(tools?: StatsToolSnapshot, runtime?: StatsRuntimeSnapshot): RuntimeMetric | undefined {
  const interventions = tools?.loop_guard_interventions ?? 0;
  const forcedNoTools = tools?.forced_no_tools ?? 0;
  const maxTurns = runtime?.turn_end_by_reason?.max_turns ?? 0;
  if (interventions <= 0 && forcedNoTools <= 0 && maxTurns <= 0) return undefined;
  const parts: string[] = [];
  if (maxTurns > 0) parts.push(`${maxTurns} max-turn${maxTurns === 1 ? "" : "s"}`);
  if (interventions > 0) parts.push(`${interventions} guard${interventions === 1 ? "" : "s"}`);
  if ((tools?.forced_no_tools ?? 0) > 0) parts.push(`${tools?.forced_no_tools} no-tools`);
  return { label: "Loop", value: parts.join(" · "), tone: "warning" };
}

function errorMetric(tools?: StatsToolSnapshot, runtime?: StatsRuntimeSnapshot): RuntimeMetric | undefined {
  const toolErrors = tools?.tool_errors ?? 0;
  const runtimeErrors = runtime?.runtime_errors ?? 0;
  if (toolErrors <= 0 && runtimeErrors <= 0) return undefined;
  const parts: string[] = [];
  if (toolErrors > 0) parts.push(`${toolErrors} tool`);
  if (runtimeErrors > 0) parts.push(`${runtimeErrors} runtime`);
  return { label: "Errors", value: parts.join(" · "), tone: "warning" };
}

function browserMetric(aggregate?: ServerAggregateStats): RuntimeMetric | undefined {
  if (!aggregate || aggregate.network_fetch <= 0) return undefined;
  const parts = [`${aggregate.network_fetch} fetches`];
  if (aggregate.cache_hit > 0 || aggregate.cache_miss > 0) parts.push(`${aggregate.cache_hit}/${aggregate.cache_hit + aggregate.cache_miss} cache`);
  return { label: "Browser", value: parts.join(" · "), tone: "muted" };
}

function formatCount(value: number): string {
  if (value < 1000) return String(value);
  if (value < 10_000) return `${(value / 1000).toFixed(1)}k`;
  return `${Math.round(value / 1000)}k`;
}
