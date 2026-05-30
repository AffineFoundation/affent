import type { SessionContextCompactionSummary, SessionContextSummary } from "../api/sessions";
import type { SessionState } from "../store/sessionState";
import type { WorkbenchContextUsageView } from "../view/workbenchContext";

export function SessionUsagePanel({
  usage,
  contextSummary,
  compactions,
  session,
  hasSelectedSession,
}: {
  usage?: WorkbenchContextUsageView;
  contextSummary?: SessionContextSummary;
  compactions?: SessionContextCompactionSummary;
  session?: SessionState;
  hasSelectedSession: boolean;
}) {
  const total = usage?.totalTokens ?? 0;
  const input = usage?.inputTokens ?? 0;
  const output = usage?.outputTokens ?? 0;
  const latestInput = usage?.latestTurnInputTokens ?? 0;
  const latestOutput = usage?.latestTurnOutputTokens ?? 0;
  const latestTotal = latestInput + latestOutput;
  const turns = usage?.trend.length ?? 0;
  const inputShare = total > 0 ? Math.round((input / total) * 100) : 0;
  const outputShare = total > 0 ? 100 - inputShare : 0;
  const contextPercent = contextSummary && contextSummary.compact_trigger > 0
    ? Math.max(0, Math.min(100, Math.round(contextSummary.compact_percent)))
    : undefined;
  const contextWindow = contextWindowView(contextSummary, compactions);
  const costParts = usageCostBreakdown({ usage, contextSummary, session });

  return (
    <section className="session-usage-panel" data-testid="session-usage-panel" aria-label="Session usage">
      <header className="session-usage-head">
        <div>
          <span className="session-skills-kicker">Usage</span>
          <strong>{hasSelectedSession ? "Session token usage" : "No chat selected"}</strong>
        </div>
        <b>{total > 0 ? formatTokenCount(total) : "No usage yet"}</b>
      </header>
      <div className="session-usage-body">
        {hasSelectedSession ? (
          <>
            <div className="session-usage-stats" aria-label="Token totals">
              <UsageStat label="Total" value={formatTokenCount(total)} detail={turns > 0 ? `${turns} measured ${turns === 1 ? "turn" : "turns"}` : "waiting for usage events"} tone="total" />
              <UsageStat label="Input" value={formatTokenCount(input)} detail={`${inputShare}% of session`} tone="input" />
              <UsageStat label="Output" value={formatTokenCount(output)} detail={`${outputShare}% of session`} tone="output" />
              <UsageStat label="Latest turn" value={latestTotal > 0 ? formatTokenCount(latestTotal) : "-"} detail={latestTotal > 0 ? `${formatTokenCount(latestInput)} in / ${formatTokenCount(latestOutput)} out` : "not reported"} tone="latest" />
            </div>
            <section className="session-usage-chart-card" data-testid="session-usage-chart-card" aria-label="Input and output token trend">
              <div className="session-usage-chart-head">
                <strong>Input / output trend</strong>
                <span>{turns > 1 ? "Recent turns" : turns === 1 ? "Single reported turn" : "Waiting for turn usage"}</span>
              </div>
              {usage?.trend.length ? <TokenUsageLineChart points={usage.trend} /> : <div className="session-usage-empty">Usage appears after a turn completes or when the session index reports totals.</div>}
            </section>
            {contextPercent != null ? (
              <section className="session-usage-context" data-tone={contextPercent >= 95 ? "error" : contextPercent >= 72 ? "attention" : "ready"}>
                <div>
                  <span>Context window</span>
                  <strong>{contextWindow.windowLabel}</strong>
                  <small>{contextWindow.source}</small>
                </div>
                <div>
                  <span>Compaction trigger</span>
                  <strong>{contextWindow.triggerLabel}</strong>
                  <small>{contextRemaining(contextSummary)}</small>
                </div>
                <div>
                  <span>Compactions</span>
                  <strong>{formatWhole(compactions?.count ?? session?.contextCompactions.length ?? 0)}</strong>
                  <small>{compactionDetail(compactions)}</small>
                </div>
              </section>
            ) : null}
            <section className="session-usage-cost" data-testid="session-usage-cost" aria-label="Token cost breakdown">
              <div className="session-usage-chart-head">
                <strong>Cost breakdown</strong>
                <span>Estimated parts of the current session/request</span>
              </div>
              <div className="session-usage-cost-list">
                {costParts.map((part) => (
                  <div key={part.key} className="session-usage-cost-item" data-kind={part.key}>
                    <div>
                      <strong>{part.label}</strong>
                      <span>{part.detail}</span>
                    </div>
                    <b>{part.valueLabel}</b>
                    <small>{part.percentLabel}</small>
                    <div className="session-usage-cost-bar" aria-hidden="true">
                      <span style={{ width: `${part.percent}%` }} />
                    </div>
                  </div>
                ))}
              </div>
            </section>
          </>
        ) : (
          <div className="session-usage-empty">Open a chat to see session input, output, and per-turn token trend.</div>
        )}
      </div>
    </section>
  );
}

interface UsageCostPart {
  key: "conversation" | "tool" | "thinking" | "output";
  label: string;
  value: number;
  valueLabel: string;
  percent: number;
  percentLabel: string;
  detail: string;
}

function UsageStat({
  label,
  value,
  detail,
  tone,
}: {
  label: string;
  value: string;
  detail: string;
  tone: "total" | "input" | "output" | "latest";
}) {
  return (
    <div className="session-usage-stat" data-tone={tone}>
      <span>{label}</span>
      <strong>{value}</strong>
      <small>{detail}</small>
    </div>
  );
}

function TokenUsageLineChart({ points }: { points: WorkbenchContextUsageView["trend"] }) {
  const width = 360;
  const height = 108;
  const padX = 18;
  const padY = 14;
  const max = Math.max(...points.flatMap((point) => [point.inputTokens, point.outputTokens]), 1);
  const inputCoords = chartCoords(points.map((point) => point.inputTokens), width, height, padX, padY, max);
  const outputCoords = chartCoords(points.map((point) => point.outputTokens), width, height, padX, padY, max);
  const inputLine = linePoints(inputCoords, width, padX);
  const outputLine = linePoints(outputCoords, width, padX);
  const latest = points[points.length - 1];

  return (
    <div className="session-usage-chart">
      <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`Latest input ${formatTokenCount(latest.inputTokens)}, output ${formatTokenCount(latest.outputTokens)}`}>
        <line className="session-usage-grid" x1={padX} y1={padY} x2={width - padX} y2={padY} />
        <line className="session-usage-grid" x1={padX} y1={height / 2} x2={width - padX} y2={height / 2} />
        <line className="session-usage-axis" x1={padX} y1={height - padY} x2={width - padX} y2={height - padY} />
        <polyline className="session-usage-line" data-kind="input" points={inputLine} />
        <polyline className="session-usage-line" data-kind="output" points={outputLine} />
        {points.map((point, index) => {
          const inputPoint = inputCoords[index];
          const outputPoint = outputCoords[index];
          return (
            <g key={`${point.label}:${point.detail ?? index}`}>
              <circle className="session-usage-dot" data-kind="input" cx={inputPoint.x} cy={inputPoint.y} r="2.4">
                <title>{`${point.label} input: ${formatTokenCount(point.inputTokens)}`}</title>
              </circle>
              <circle className="session-usage-dot" data-kind="output" cx={outputPoint.x} cy={outputPoint.y} r="2.4">
                <title>{`${point.label} output: ${formatTokenCount(point.outputTokens)}`}</title>
              </circle>
            </g>
          );
        })}
      </svg>
      <div className="session-usage-chart-legend">
        <span data-kind="input">Input</span>
        <span data-kind="output">Output</span>
        <b>{points[0]?.label} - {latest.label}</b>
      </div>
    </div>
  );
}

function chartCoords(values: readonly number[], width: number, height: number, padX: number, padY: number, max: number): Array<{ x: number; y: number }> {
  return values.map((value, index) => {
    const x = values.length === 1 ? width / 2 : padX + (index * (width - padX * 2)) / (values.length - 1);
    const y = height - padY - (value / max) * (height - padY * 2);
    return { x, y };
  });
}

function linePoints(coords: readonly { x: number; y: number }[], width: number, padX: number): string {
  if (coords.length === 0) return "";
  if (coords.length === 1) return `${padX},${coords[0].y} ${width - padX},${coords[0].y}`;
  return coords.map(({ x, y }) => `${x},${y}`).join(" ");
}

function contextRemaining(context?: SessionContextSummary): string {
  if (!context) return "No context summary";
  if ((context.request_input_tokens_until_compact ?? 0) > 0) return `${formatWhole(context.request_input_tokens_until_compact ?? 0)} input tokens before compaction`;
  if ((context.messages_until_compact ?? 0) > 0) return `${formatWhole(context.messages_until_compact)} messages before compaction`;
  return "Compaction threshold reached";
}

function contextWindowView(context?: SessionContextSummary, compactions?: SessionContextCompactionSummary): { windowLabel: string; triggerLabel: string; source: string } {
  const windowTokens = context?.model_context_window_tokens ?? compactions?.latest_model_context_window_tokens ?? 0;
  const triggerTokens = context?.compact_trigger_input_tokens ?? compactions?.latest_trigger_input_tokens ?? 0;
  const triggerPercent = context?.compact_trigger_input_percent ?? compactions?.latest_trigger_input_percent;
  const reserved = context?.reserved_output_tokens ?? compactions?.latest_reserved_output_tokens ?? 0;
  const source = context?.model_context_window_source ?? compactions?.latest_model_context_window_source;
  return {
    windowLabel: windowTokens > 0 ? `${formatTokenCount(windowTokens)} tokens` : "Unknown",
    triggerLabel: triggerTokens > 0
      ? `${formatTokenCount(triggerTokens)} input${triggerPercent ? ` · ${triggerPercent}%` : ""}`
      : triggerPercent ? `${triggerPercent}% of window` : "Unknown",
    source: [
      source ? `source: ${source.replace(/[_-]+/g, " ")}` : undefined,
      reserved > 0 ? `${formatTokenCount(reserved)} output reserved` : undefined,
    ].filter(Boolean).join(" · ") || "No model window metadata",
  };
}

function compactionDetail(compactions?: SessionContextCompactionSummary): string {
  if (!compactions || compactions.count <= 0) return "No context folding recorded";
  const parts = [
    compactions.reactive > 0 ? `${formatWhole(compactions.reactive)} reactive` : undefined,
    compactions.removed_messages > 0 ? `${formatWhole(compactions.removed_messages)} messages removed` : undefined,
    compactions.latest_reason ? `latest: ${compactions.latest_reason.replace(/[_-]+/g, " ")}` : undefined,
  ];
  return parts.filter(Boolean).join(" · ") || "Context was folded";
}

function usageCostBreakdown({
  usage,
  contextSummary,
  session,
}: {
  usage?: WorkbenchContextUsageView;
  contextSummary?: SessionContextSummary;
  session?: SessionState;
}): UsageCostPart[] {
  const conversation = contextSummary?.estimated_conversation_tokens ?? Math.max(0, (usage?.inputTokens ?? 0) - (contextSummary?.estimated_tool_schema_tokens ?? 0));
  const toolSchema = contextSummary?.estimated_tool_schema_tokens ?? contextSummary?.tool_schema_budget_tokens ?? 0;
  const toolResultContext = estimateToolResultContextTokens(session);
  const tool = toolSchema + toolResultContext;
  const thinking = estimateThinkingTokens(session);
  const output = usage?.outputTokens ?? 0;
  const denominator = Math.max(conversation + tool + thinking + output, usage?.totalTokens ?? 0, 1);
  return [
    {
      key: "conversation",
      label: "Conversation input",
      value: conversation,
      valueLabel: formatTokenCount(conversation),
      percent: percentOf(conversation, denominator),
      percentLabel: `${percentOf(conversation, denominator)}%`,
      detail: contextSummary?.estimated_conversation_tokens ? "estimated loaded conversation" : "derived from input total",
    },
    {
      key: "tool",
      label: "Tool context",
      value: tool,
      valueLabel: formatTokenCount(tool),
      percent: percentOf(tool, denominator),
      percentLabel: `${percentOf(tool, denominator)}%`,
      detail: toolResultContext > 0
        ? `${formatTokenCount(toolSchema)} schema + ${formatTokenCount(toolResultContext)} results`
        : toolSchema > 0 ? "tool schema estimate" : "not reported",
    },
    {
      key: "thinking",
      label: "Thinking",
      value: thinking,
      valueLabel: thinking > 0 ? `~${formatTokenCount(thinking)}` : "Not reported",
      percent: percentOf(thinking, denominator),
      percentLabel: thinking > 0 ? `${percentOf(thinking, denominator)}%` : "0%",
      detail: thinking > 0 ? "estimated from saved thinking text" : "provider usage does not split reasoning tokens",
    },
    {
      key: "output",
      label: "Visible output",
      value: output,
      valueLabel: formatTokenCount(output),
      percent: percentOf(output, denominator),
      percentLabel: `${percentOf(output, denominator)}%`,
      detail: "reported output tokens",
    },
  ];
}

function estimateToolResultContextTokens(session?: SessionState): number {
  if (!session) return 0;
  return session.turns.reduce((sum, turn) => {
    return sum + turn.toolCalls.reduce((toolSum, call) => toolSum + Math.max(0, call.contextEstimatedTokens ?? 0), 0);
  }, 0);
}

function estimateThinkingTokens(session?: SessionState): number {
  if (!session) return 0;
  return session.turns.reduce((sum, turn) => sum + estimateTextTokens(turn.thinkingText), 0);
}

function estimateTextTokens(text: string | undefined): number {
  const trimmed = text?.trim() ?? "";
  if (!trimmed) return 0;
  return Math.ceil(trimmed.length / 4);
}

function percentOf(value: number, total: number): number {
  if (!Number.isFinite(value) || value <= 0 || total <= 0) return 0;
  return Math.max(1, Math.min(100, Math.round((value / total) * 100)));
}

function formatTokenCount(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0";
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(value >= 10_000_000 ? 1 : 2)}M`;
  if (value >= 10_000) return `${(value / 1000) % 1 === 0 ? (value / 1000).toFixed(0) : (value / 1_000).toFixed(value >= 100_000 ? 0 : 1)}k`;
  return formatWhole(value);
}

function formatWhole(value: number): string {
  return Math.max(0, Math.round(value)).toLocaleString("en-US");
}
