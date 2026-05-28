import type { MemoryUpdateMeta } from "../api/events";
import type { SessionContextCompactionSummary, SessionContextSummary, SessionPlanSummary, SessionSummary } from "../api/sessions";
import type { SessionState } from "../store/sessionState";
import { contextCompactionSummaryLabel } from "./contextCompaction";
import { conversationTopicFromTurns } from "./continuationPrompt";
import { summarizeUserError } from "./errorSummary";
import { sessionArtifactLabel } from "./sessionArtifacts";
import { summarizeAnswerPreview } from "./textPreview";
import { buildTurnActivity } from "./turnActivity";

const noMessagesYet = "No messages yet";
const cnTitleActions = [
  "新增", "添加", "实现", "开发", "构建", "创建", "写", "编写", "修复", "解决", "优化", "重构",
  "完善", "改进", "设计", "理解", "查看", "检查", "审查", "收集", "检索", "查询", "查找",
  "搜索", "调研", "研究", "介绍", "分析", "总结", "梳理", "说明", "整理",
].join("|");
const enTitleActions = [
  "review", "research", "inspect", "summarize", "analyze", "explain", "fix", "debug",
  "improve", "refactor", "implement", "build", "create", "design", "understand",
].join("|");

export type SessionListFilter = "all" | "active" | "saved" | "artifacts" | "memory" | "plan" | "automation" | "evidence" | "guard" | "issues";
export type SessionRowTone = "running" | "saved" | "muted" | "error" | "warning";
type SessionTitleSource = "provided" | "topic" | "fallback";

export interface SessionRowView {
  id: string;
  title: string;
  detail?: string;
  preview?: string;
  stats?: string;
  meta: string[];
  status: string;
  tone: SessionRowTone;
  updated: string;
  metrics: string[];
  chips: string[];
  searchText: string;
  titleSource: SessionTitleSource;
}

export function buildSessionRows(sessions: readonly SessionSummary[]): SessionRowView[] {
  return [...sessions].sort(compareSessionsForChatList).map((session) => {
    const status = session.active ? "Live" : session.durable ? "Saved" : "Ephemeral";
    const chips = featureChips(session);
    const metrics = usageMetrics(session);
    const titleSource = displayUserMessage(session.topic_user_message) || displayUserMessage(session.latest_user_message);
    const providedTitle = providedSessionTitle(session);
    const title = providedTitle ?? (titleSource ? summarizeSessionTitle(titleSource) : fallbackSessionTitle(session));
    const titleKind: SessionTitleSource = providedTitle ? "provided" : titleSource ? "topic" : "fallback";
    const detail = summarizeSessionDetail(session, title);
    const preview = summarizeSessionPreview(session, title, detail);
    const updated = session.last_used_at ?? session.created_at ? formatTimestamp(session.last_used_at ?? session.created_at) : noMessagesYet;
    const stats = summarizeSessionStats(metrics);
    const searchText = [
      session.id,
      title,
      detail,
      preview,
      session.title,
      session.summary_title,
      session.generated_title,
      session.topic_user_message,
      session.latest_user_message,
      status,
      updated,
      ...metrics,
      ...chips,
    ].join(" ").toLowerCase();

    return {
      id: session.id,
      title,
      detail,
      preview,
      stats,
      meta: buildRowMeta(updated, {
        empty: !titleSource && !session.has_conversation && !session.has_events,
      }),
      status,
      tone: session.active ? "running" : session.durable ? "saved" : "muted",
      updated,
      metrics,
      chips,
      searchText,
      titleSource: titleKind,
    };
  });
}

function compareSessionsForChatList(a: SessionSummary, b: SessionSummary): number {
  if (a.active !== b.active) return a.active ? -1 : 1;
  const aTime = sessionActivityTime(a);
  const bTime = sessionActivityTime(b);
  if (aTime !== bTime) return bTime - aTime;
  return a.id.localeCompare(b.id);
}

function sessionActivityTime(session: SessionSummary): number {
  const value = session.last_used_at ?? session.created_at;
  if (!value) return 0;
  const time = Date.parse(value);
  return Number.isFinite(time) ? time : 0;
}

export function mergeCurrentSessionRow(
  rows: readonly SessionRowView[],
  selectedId: string | undefined,
  session: SessionState | undefined,
  pendingTask?: string,
): SessionRowView[] {
  const pending = pendingTask?.trim();
  if (!selectedId || (!pending && (!session || session.turns.length === 0))) return [...rows];
  return rows.map((row) => (row.id === selectedId ? mergeCurrentSession(row, session, pendingTask) : row));
}

export function filterSessionRows(
  rows: readonly SessionRowView[],
  filter: SessionListFilter,
  query: string,
): SessionRowView[] {
  const search = query.trim().toLowerCase();
  return rows.filter((row) => matchesFilter(row, filter) && (!search || row.searchText.includes(search)));
}

export function displaySessionRowChips(row: SessionRowView, opts: { selected: boolean }): string[] {
  if (!opts.selected) return [];
  return row.chips.filter(isDisplaySessionChip);
}

function isDisplaySessionChip(chip: string): boolean {
  return chip === "unclassified";
}

function mergeCurrentSession(row: SessionRowView, session: SessionState | undefined, pendingTask?: string): SessionRowView {
  const latestTurn = session?.turns.at(-1);
  const pending = pendingTask?.replace(/\s+/g, " ").trim();
  const title = currentSessionTitle(row, session, pending);
  const pendingDetail = pending ? `Sending · ${summarizeLatestPendingTask(pending, title)}` : undefined;
  const detail = pendingDetail ?? (session ? currentSessionDetail(session, title) : row.detail);
  const preview = pending
    ? "Waiting for the next update."
    : session
      ? currentSessionPreview(session, title, detail)
      : row.preview;
  const metrics = session ? mergeCurrentDurableMetrics(currentSessionMetrics(session), row.metrics) : row.metrics;
  const stats = session ? summarizeSessionStats(metrics) : row.stats;
  const searchMetrics = session ? mergeCurrentDurableMetrics(currentSessionSearchMetrics(session), row.metrics) : row.metrics;
  const chips = session ? mergeChips(row.chips, currentSessionChips(session)) : row.chips;
  const status = pending ? "Live" : session ? currentSessionStatus(session, row.status) : row.status;
  const userSearchText = session?.turns.map((turn) => turn.userText).join(" ") ?? "";
  const searchText = [row.id, title, detail, preview, stats, status, userSearchText, pending, ...searchMetrics, ...chips].join(" ").toLowerCase();
  const updated = latestTurn?.userText && row.updated === noMessagesYet ? "" : row.updated;
  const { stats: _stats, metrics: _metrics, chips: _chips, searchText: _searchText, ...base } = row;

  return {
    ...base,
    title,
    detail,
    preview,
    stats,
    meta: buildRowMeta(updated),
    status,
    tone: pending ? "running" : session ? currentSessionTone(session, row.tone) : row.tone,
    updated,
    metrics,
    chips,
    searchText,
  };
}

function mergeCurrentDurableMetrics(current: string[], durable: string[]): string[] {
  const out = [...current];
  const seen = new Set(out);
  for (const metric of durable) {
    if (!metric.startsWith("Automation ") || seen.has(metric)) continue;
    out.push(metric);
    seen.add(metric);
  }
  return out;
}

function currentSessionTitle(row: SessionRowView, session: SessionState | undefined, pending?: string): string {
  if (row.titleSource === "provided") return row.title;
  const topic = session ? conversationTopicFromTurns(session.turns) : undefined;
  if (topic) return summarizeSessionTitle(topic);
  return pending ? summarizeSessionTitle(pending) : row.title;
}

function summarizeLatestPendingTask(text: string, title: string): string {
  const summary = summarizeSessionTitle(stripContinuationPrefix(text));
  if (summary && summary !== title) return summarize(summary, 72);
  return summarize(text, 72);
}

function currentSessionDetail(session: SessionState, title: string): string | undefined {
  const topic = conversationTopicFromTurns(session.turns);
  const latest = [...session.turns].reverse().find((turn) => Boolean(turn.userText?.trim()))?.userText;
  if (!topic || !latest || latest === topic) return undefined;
  return summarizeLatestRequestDetail(latest, title);
}

function currentSessionPreview(session: SessionState, title: string, detail?: string): string | undefined {
  const latestTurn = session.turns.at(-1);
  if (!latestTurn) return detail;
  const activity = buildTurnActivity(latestTurn);
  if (latestTurn.status === "running" && activity?.digest.summary && activity.digest.summary !== "No activity yet.") {
    return `Now · ${summarize(activity.digest.summary, 96)}`;
  }
  const issue = currentTurnIssuePreview(latestTurn);
  if (issue) return issue;
  if (latestTurn.assistantText.trim()) {
    return `Answer · ${summarizeAnswerPreview(latestTurn.assistantText, 96)}`;
  }
  if (detail) return detail;
  const userText = latestTurn.userText?.trim();
  if (userText) return latestRequestPreview(userText, title);
  return undefined;
}

function currentTurnIssuePreview(turn: SessionState["turns"][number]): string | undefined {
  if (turn.status === "max_turns") {
    return "Needs final answer · Action limit reached before a final reply.";
  }
  if (turn.error) {
    return `Issue · ${summarizeUserError(turn.error.code, turn.error.message).title}`;
  }
  if (turn.status === "error") {
    return `Issue · ${summarize(firstToolIssue(turn) ?? "Request failed", 96)}`;
  }
  if (turn.status === "completed" && turn.assistantText.trim()) return undefined;
  const failedTool = firstToolIssue(turn);
  return failedTool ? `Issue · ${summarize(failedTool, 96)}` : undefined;
}

function firstToolIssue(turn: SessionState["turns"][number]): string | undefined {
  const call = turn.toolCalls.find((item) => item.status === "error");
  return call?.resultSummary || call?.result || (call ? `${call.tool} failed` : undefined);
}

function currentSessionStatus(session: SessionState, fallback: string): string {
  if (session.status === "running") return "Live";
  if (session.status === "completed") {
    const latestTurn = session.turns.at(-1);
    if (latestTurn && turnNeedsAttention(latestTurn)) return "Blocked";
    return "Done";
  }
  if (session.status === "max_turns") return "Needs final answer";
  if (session.status === "error") return "Blocked";
  if (session.status === "cancelled") return "Cancelled";
  return fallback;
}

function currentSessionTone(session: SessionState, fallback: SessionRowTone): SessionRowTone {
  if (session.status === "running") return "running";
  const latestTurn = session.turns.at(-1);
  if (latestTurn?.status === "max_turns") return "warning";
  if (latestTurn && turnNeedsAttention(latestTurn)) return "error";
  if (session.status === "completed") return "saved";
  if (session.status === "max_turns") return "warning";
  return fallback;
}

function currentSessionMetrics(session: SessionState): string[] {
  const latestTurn = session.turns.at(-1);
  const toolCount = session.turns.reduce((sum, turn) => sum + turn.toolCalls.length, 0);
  const currentIssueCount = latestTurn && turnNeedsAttention(latestTurn) ? 1 : 0;
  const continuedCount = session.turns.reduce((sum, turn) => sum + (turn !== latestTurn && turn.status === "max_turns" ? 1 : 0), 0);
  const priorIssueCount = session.turns.reduce((sum, turn) => sum + (turn !== latestTurn && turn.status !== "max_turns" && turnNeedsAttention(turn) ? 1 : 0), 0);
  const toolIssueCount = session.turns.reduce((sum, turn) => sum + settledToolIssueCount(turn), 0);
  const guardMetric = loopGuardMetric(currentSessionLoopGuardStats(session));
  const sourceMetric = sourceAccessMetric(currentSessionSourceAccessStats(session));
  const recallMetric = sessionSearchMetric(currentSessionRecallStats(session));
  const artifactMetric = currentSessionArtifactMetric(session);
  const compactionMetric = currentSessionCompactionMetric(session);
  const recoveryMetric = currentSessionRecoveryMetric(session);
  return [
    summarizeSessionMetrics({
      messages: session.turns.length,
      actions: toolCount,
      currentIssues: currentIssueCount,
      continued: continuedCount,
      priorIssues: priorIssueCount,
      toolIssues: toolIssueCount,
    }),
    ...(recoveryMetric ? [recoveryMetric] : []),
    ...(guardMetric ? [guardMetric] : []),
    ...(sourceMetric ? [sourceMetric] : []),
    ...(recallMetric ? [recallMetric] : []),
    ...(compactionMetric ? [compactionMetric] : []),
    ...(artifactMetric ? [artifactMetric] : []),
  ];
}

function summarizeSessionMetrics({
  messages,
  actions,
  currentIssues,
  continued,
  priorIssues,
  toolIssues,
}: {
  messages: number;
  actions: number;
  currentIssues: number;
  continued: number;
  priorIssues: number;
  toolIssues: number;
}): string {
  const parts = [`${messages} message${messages === 1 ? "" : "s"}`];
  if (actions > 0) parts.push(`${actions} action${actions === 1 ? "" : "s"}`);
  if (currentIssues > 0) {
    parts.push(`${currentIssues} issue${currentIssues === 1 ? "" : "s"}`);
  } else if (continued > 0) {
    parts.push(`${continued} continued`);
  } else {
    const issueCount = priorIssues + toolIssues;
    if (issueCount > 0) parts.push(`${issueCount} issue${issueCount === 1 ? "" : "s"}`);
  }
  return parts.join(" · ");
}

function summarizeSessionStats(metrics: readonly string[]): string | undefined {
  const value = metrics.flatMap(displaySessionStatMetric).join(" · ");
  if (!value) return undefined;
  return value;
}

function displaySessionStatMetric(metric: string): string[] {
  return metric
    .split(" · ")
    .map((part) => part.trim())
    .filter((part) => part && !isLowSignalSessionStatPart(part));
}

function isLowSignalSessionStatPart(part: string): boolean {
  return /^\d+ messages?$/.test(part) || /^\d+ actions?$/.test(part);
}

function currentSessionSearchMetrics(session: SessionState): string[] {
  const latestTurn = session.turns.at(-1);
  const toolCount = session.turns.reduce((sum, turn) => sum + turn.toolCalls.length, 0);
  const currentIssueCount = latestTurn && turnNeedsAttention(latestTurn) ? 1 : 0;
  const continuedCount = session.turns.reduce((sum, turn) => sum + (turn !== latestTurn && turn.status === "max_turns" ? 1 : 0), 0);
  const priorIssueCount = session.turns.reduce((sum, turn) => sum + (turn !== latestTurn && turn.status !== "max_turns" && turnNeedsAttention(turn) ? 1 : 0), 0);
  const toolIssueCount = session.turns.reduce((sum, turn) => sum + settledToolIssueCount(turn), 0);
  const guardMetric = loopGuardMetric(currentSessionLoopGuardStats(session));
  const sourceMetric = sourceAccessMetric(currentSessionSourceAccessStats(session));
  const recallMetric = sessionSearchMetric(currentSessionRecallStats(session));
  const artifactMetric = currentSessionArtifactMetric(session);
  const compactionMetric = currentSessionCompactionMetric(session);
  const recoveryMetric = currentSessionRecoveryMetric(session);
  const metrics = [`${session.turns.length} message${session.turns.length === 1 ? "" : "s"}`];
  if (toolCount > 0) metrics.push(`${toolCount} action${toolCount === 1 ? "" : "s"}`);
  if (currentIssueCount > 0) metrics.push(`${currentIssueCount} issue${currentIssueCount === 1 ? "" : "s"}`);
  if (continuedCount > 0) metrics.push(`${continuedCount} continued`);
  if (priorIssueCount > 0) metrics.push(`${priorIssueCount} prior issue${priorIssueCount === 1 ? "" : "s"}`);
  if (toolIssueCount > 0) metrics.push(`${toolIssueCount} tool issue${toolIssueCount === 1 ? "" : "s"}`);
  if (recoveryMetric) metrics.push(recoveryMetric);
  if (guardMetric) metrics.push(guardMetric);
  if (sourceMetric) metrics.push(sourceMetric);
  if (recallMetric) metrics.push(recallMetric);
  if (compactionMetric) metrics.push(compactionMetric);
  if (artifactMetric) metrics.push(artifactMetric);
  return metrics;
}

function currentSessionLoopGuardStats(session: SessionState): Required<LoopGuardStats> {
  return session.turns.reduce<Required<LoopGuardStats>>((stats, turn) => {
    const toolStats = turn.toolStats;
    if (!toolStats) return stats;
    stats.loop_guard_interventions += toolStats.loop_guard_interventions ?? 0;
    stats.forced_no_tools += toolStats.forced_no_tools ?? 0;
    return stats;
  }, emptyLoopGuardStats());
}

function currentSessionSourceAccessStats(session: SessionState): Required<SourceAccessStats> {
  return session.turns.reduce<Required<SourceAccessStats>>((stats, turn) => {
    const toolStats = turn.toolStats;
    if (!toolStats) return stats;
    stats.source_access_results += toolStats.source_access_results ?? 0;
    stats.source_access_verified += toolStats.source_access_verified ?? 0;
    stats.source_access_discovery_only += toolStats.source_access_discovery_only ?? 0;
    stats.source_access_network += toolStats.source_access_network ?? 0;
    stats.source_access_dynamic_partial += toolStats.source_access_dynamic_partial ?? 0;
    return stats;
  }, emptySourceAccessStats());
}

function currentSessionRecallStats(session: SessionState): Required<SessionSearchStats> {
  return session.turns.reduce<Required<SessionSearchStats>>((stats, turn) => {
    const toolStats = turn.toolStats;
    if (!toolStats) return stats;
    stats.session_search_calls += toolStats.session_search_calls ?? 0;
    stats.session_search_results += toolStats.session_search_results ?? 0;
    stats.session_search_context_hits += toolStats.session_search_context_hits ?? 0;
    stats.session_search_matched_terms += toolStats.session_search_matched_terms ?? 0;
    return stats;
  }, emptySessionSearchStats());
}

function currentSessionArtifactMetric(session: SessionState): string | undefined {
  return sessionArtifactLabel(session);
}

function currentSessionCompactionMetric(session: SessionState): string | undefined {
  const count = session.contextCompactions.length;
  if (count === 0) return undefined;
  const latest = session.contextCompactions.at(-1);
  return formatCompactionMetric({
    count,
    latestReactive: latest?.reactive,
    removedMessages: latest?.removed_messages ?? 0,
    summaryLabel: latest ? contextCompactionSummaryLabel(latest) : undefined,
  });
}

function currentSessionRecoveryMetric(session: SessionState): string | undefined {
  for (const turn of [...session.turns].reverse()) {
    for (const call of [...turn.toolCalls].reverse()) {
      const failed = call.status === "error" || (call.exitCode ?? 0) !== 0 || Boolean(call.failureKind) || Boolean(call.failureKinds?.length);
      if (!failed) continue;
      const next = toolNextHint(call.resultSummary, call.result);
      if (next) return recoveryMetric(next);
    }
  }
  return undefined;
}

function toolNextHint(summary?: string, result?: string): string | undefined {
  const text = [summary, result && result !== summary ? result : undefined].filter(Boolean).join("\n");
  const match = text.match(/(?:^|\n)Next:\s*([\s\S]*?)(?:\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/);
  const next = match?.[1]?.trim();
  return next || undefined;
}

function recoveryMetric(hint: string): string | undefined {
  const value = hint.replace(/\s+/g, " ").trim();
  return value ? `Recovery ${summarize(value, 72)}` : undefined;
}

function turnNeedsAttention(turn: SessionState["turns"][number]): boolean {
  if (turn.status === "error" || turn.error) return true;
  if (turn.status === "max_turns") return true;
  if (turn.status === "completed" && turn.assistantText.trim()) return false;
  return turn.toolCalls.some((call) => call.status === "error");
}

function settledToolIssueCount(turn: SessionState["turns"][number]): number {
  if (turnNeedsAttention(turn)) return 0;
  if (turn.status !== "completed" || !turn.assistantText.trim()) return 0;
  return turn.toolCalls.filter((call) => call.status === "error").length;
}

function currentSessionChips(session: SessionState): string[] {
  const chips: string[] = [];
  if (session.turns.some((turn) => turn.toolCalls.some((call) => call.resultArtifactPath))) chips.push("files");
  if (session.unknownEventCount > 0) chips.push("unclassified");
  return chips;
}

function mergeChips(existing: readonly string[], incoming: readonly string[]): string[] {
  return Array.from(new Set([...existing, ...incoming]));
}

export function countSessionsByFilter(rows: readonly SessionRowView[]): Record<SessionListFilter, number> {
  return {
    all: rows.length,
    active: rows.filter((row) => row.status === "Live").length,
    saved: rows.filter((row) => row.status === "Saved").length,
    artifacts: rows.filter((row) => row.chips.includes("files") || row.chips.includes("artifacts")).length,
    memory: rows.filter((row) => row.chips.includes("memory")).length,
    plan: rows.filter((row) => row.chips.includes("plan")).length,
    automation: rows.filter((row) => row.chips.includes("automation")).length,
    evidence: rows.filter(hasEvidenceMetric).length,
    guard: rows.filter(hasGuardMetric).length,
    issues: rows.filter(needsAttention).length,
  };
}

function matchesFilter(row: SessionRowView, filter: SessionListFilter): boolean {
  if (filter === "all") return true;
  if (filter === "active") return row.status === "Live";
  if (filter === "saved") return row.status === "Saved";
  if (filter === "artifacts") return row.chips.includes("files") || row.chips.includes("artifacts");
  if (filter === "memory") return row.chips.includes("memory");
  if (filter === "plan") return row.chips.includes("plan");
  if (filter === "automation") return row.chips.includes("automation");
  if (filter === "evidence") return hasEvidenceMetric(row);
  if (filter === "guard") return hasGuardMetric(row);
  if (filter === "issues") return needsAttention(row);
  return true;
}

function hasEvidenceMetric(row: SessionRowView): boolean {
  return row.metrics.some((metric) => metric.startsWith("Evidence "));
}

function hasGuardMetric(row: SessionRowView): boolean {
  return row.metrics.some((metric) => metric.startsWith("Guard "));
}

function needsAttention(row: SessionRowView): boolean {
  if (row.tone === "error" || row.tone === "warning") return true;
  if (row.status === "Blocked" || row.status === "Needs final answer") return true;
  return row.metrics.some((metric) => /\bissues?\b/i.test(metric) || /\btool issues?\b/i.test(metric) || /\bprior issues?\b/i.test(metric) || metric.startsWith("Recovery "));
}

function usageMetrics(session: SessionSummary): string[] {
  const metrics: string[] = [];
  const turns = session.usage?.turns ?? 0;
  if (turns > 0) metrics.push(`${turns} message${turns === 1 ? "" : "s"}`);
  const toolRequests = session.tools?.tool_requests ?? 0;
  if (toolRequests > 0) metrics.push(`${toolRequests} action${toolRequests === 1 ? "" : "s"}`);
  const toolErrors = session.tools?.tool_errors ?? 0;
  if (toolErrors > 0) metrics.push(`${toolErrors} issue${toolErrors === 1 ? "" : "s"}`);
  const recovery = session.latest_recovery_hint ? recoveryMetric(session.latest_recovery_hint) : undefined;
  if (recovery) metrics.push(recovery);
  if (session.browser && session.browser.network_fetch > 0) metrics.push(`${session.browser.network_fetch} web`);
  const guardMetric = loopGuardMetric(session.tools);
  if (guardMetric) metrics.push(guardMetric);
  const sourceMetric = sourceAccessMetric(session.tools);
  if (sourceMetric) metrics.push(sourceMetric);
  const recallMetric = sessionSearchMetric(session.tools);
  if (recallMetric) metrics.push(recallMetric);
  const memoryMetric = memoryUpdateMetric(session.tools, session.latest_memory_update);
  if (memoryMetric) metrics.push(memoryMetric);
  const contextMetric = sessionContextMetric(session.context);
  if (contextMetric) metrics.push(contextMetric);
  const compactionMetric = sessionCompactionMetric(session.context_compactions);
  if (compactionMetric) metrics.push(compactionMetric);
  const planMetric = sessionPlanMetric(session.plan_summary);
  if (planMetric) metrics.push(planMetric);
  const automationLoopMetric = sessionAutomationLoopMetric(session);
  if (automationLoopMetric) metrics.push(automationLoopMetric);
  const scheduleMetric = sessionAutomationScheduleMetric(session);
  if (scheduleMetric) metrics.push(scheduleMetric);
  return metrics;
}

interface LoopGuardStats {
  loop_guard_interventions?: number;
  forced_no_tools?: number;
}

function emptyLoopGuardStats(): Required<LoopGuardStats> {
  return {
    loop_guard_interventions: 0,
    forced_no_tools: 0,
  };
}

function loopGuardMetric(stats: LoopGuardStats | undefined): string | undefined {
  const interventions = stats?.loop_guard_interventions ?? 0;
  if (interventions <= 0) return undefined;
  const forced = stats?.forced_no_tools ?? 0;
  const parts = [`Guard ${interventions}`];
  if (forced > 0) parts.push(`${forced} no-tools`);
  return parts.join(", ");
}

interface SourceAccessStats {
  source_access_results?: number;
  source_access_verified?: number;
  source_access_discovery_only?: number;
  source_access_network?: number;
  source_access_dynamic_partial?: number;
}

function emptySourceAccessStats(): Required<SourceAccessStats> {
  return {
    source_access_results: 0,
    source_access_verified: 0,
    source_access_discovery_only: 0,
    source_access_network: 0,
    source_access_dynamic_partial: 0,
  };
}

function sourceAccessMetric(stats: SourceAccessStats | undefined): string | undefined {
  const total = stats?.source_access_results ?? 0;
  if (total <= 0) return undefined;
  const verified = stats?.source_access_verified ?? 0;
  const network = stats?.source_access_network ?? 0;
  const partial = stats?.source_access_dynamic_partial ?? 0;
  const discovery = stats?.source_access_discovery_only ?? 0;
  const parts = [`Evidence ${verified}/${total} verified`];
  if (network > 0) parts.push(`${network} network`);
  if (partial > 0) parts.push(`${partial} partial`);
  if (discovery > 0) parts.push(`${discovery} discovery`);
  return parts.join(", ");
}

interface SessionSearchStats {
  session_search_calls?: number;
  session_search_results?: number;
  session_search_context_hits?: number;
  session_search_matched_terms?: number;
}

interface MemoryUpdateStats {
  memory_updates?: number;
  memory_update_add?: number;
  memory_update_replace?: number;
  memory_update_remove?: number;
}

function memoryUpdateMetric(stats: MemoryUpdateStats | undefined, latest?: MemoryUpdateMeta): string | undefined {
  const updates = stats?.memory_updates ?? 0;
  if (updates <= 0 && !latest) return undefined;
  const parts = [updates > 0 ? `Memory ${updates} ${updates === 1 ? "update" : "updates"}` : "Memory updated"];
  if ((stats?.memory_update_add ?? 0) > 0) parts.push(`${stats?.memory_update_add} add`);
  if ((stats?.memory_update_replace ?? 0) > 0) parts.push(`${stats?.memory_update_replace} replace`);
  if ((stats?.memory_update_remove ?? 0) > 0) parts.push(`${stats?.memory_update_remove} remove`);
  const detail = latestMemoryUpdateDetail(latest);
  if (detail) parts.push(detail);
  return parts.join(", ");
}

function latestMemoryUpdateDetail(update?: MemoryUpdateMeta): string | undefined {
  if (!update) return undefined;
  const location = update.location || [update.target, update.topic].filter(Boolean).join(":");
  const preview = update.preview || update.next_preview || update.previous_preview;
  const parts = [memoryActionLabel(update.action), location, preview ? summarize(preview, 84) : undefined].filter(Boolean);
  return parts.join(" ");
}

function memoryActionLabel(action: string): string {
  if (action === "add") return "added";
  if (action === "replace") return "replaced";
  if (action === "remove") return "removed";
  return action;
}

function emptySessionSearchStats(): Required<SessionSearchStats> {
  return {
    session_search_calls: 0,
    session_search_results: 0,
    session_search_context_hits: 0,
    session_search_matched_terms: 0,
  };
}

function sessionSearchMetric(stats: SessionSearchStats | undefined): string | undefined {
  const calls = stats?.session_search_calls ?? 0;
  const results = stats?.session_search_results ?? 0;
  const contextHits = stats?.session_search_context_hits ?? 0;
  const matchedTerms = stats?.session_search_matched_terms ?? 0;
  if (calls <= 0 && results <= 0 && contextHits <= 0 && matchedTerms <= 0) return undefined;
  const parts = [`Recall ${results} hit${results === 1 ? "" : "s"}`];
  if (calls > 1 || results === 0) parts.push(`${calls} search${calls === 1 ? "" : "es"}`);
  if (contextHits > 0) parts.push(`${contextHits} context`);
  if (matchedTerms > 0) parts.push(`${matchedTerms} terms`);
  return parts.join(", ");
}

function sessionContextMetric(context: SessionContextSummary | undefined): string | undefined {
  if (!context || context.compact_trigger <= 0) return undefined;
  const percent = context.compact_percent > 0
    ? Math.round(context.compact_percent)
    : Math.round((context.message_count / context.compact_trigger) * 100);
  const remaining = context.messages_until_compact;
  if (percent < 80 && remaining > 10) return undefined;
  const parts = [`Context ${Math.max(0, percent)}%`];
  if (remaining >= 0 && remaining <= 10) {
    parts.push(`${remaining} msg${remaining === 1 ? "" : "s"} left`);
  }
  return parts.join(", ");
}

function sessionCompactionMetric(summary: SessionContextCompactionSummary | undefined): string | undefined {
  if (!summary || summary.count <= 0) return undefined;
  return formatCompactionMetric({
    count: summary.count,
    latestReactive: summary.latest_reactive,
    removedMessages: summary.removed_messages,
    tailOnly: summary.tail_only,
    summaryLabel: durableCompactionSummaryLabel(summary),
  });
}

function durableCompactionSummaryLabel(summary: SessionContextCompactionSummary): string | undefined {
  if (summary.latest_summary_state === "missing") return "summary missing";
  if (summary.latest_summary_state === "empty") return "summary empty";
  if ((summary.summary_missing ?? 0) > 0) {
    const count = summary.summary_missing ?? 0;
    return `${count} missing ${count === 1 ? "summary" : "summaries"}`;
  }
  if ((summary.summary_empty ?? 0) > 0) {
    const count = summary.summary_empty ?? 0;
    return `${count} empty ${count === 1 ? "summary" : "summaries"}`;
  }
  return undefined;
}

function formatCompactionMetric({
  count,
  latestReactive,
  removedMessages,
  tailOnly,
  summaryLabel,
}: {
  count: number;
  latestReactive?: boolean;
  removedMessages?: number;
  tailOnly?: boolean;
  summaryLabel?: string;
}): string {
  const parts = [`${tailOnly ? "recent " : ""}${count} ${count === 1 ? "compaction" : "compactions"}`];
  if (latestReactive) parts.push("reactive");
  if (removedMessages && removedMessages > 0) parts.push(`-${removedMessages} msgs`);
  if (summaryLabel) parts.push(summaryLabel);
  return parts.join(", ");
}

function sessionPlanMetric(plan: SessionPlanSummary | undefined): string | undefined {
  if (!plan) return undefined;
  if (plan.error) return "Plan unreadable";
  if (plan.total_steps <= 0) return undefined;
  const parts = [`Plan ${plan.completed_steps}/${plan.total_steps}`];
  if (plan.current_step_index && !plan.done) {
    parts.push(`step ${plan.current_step_index} ${planStatusLabel(plan)}`);
  } else if (plan.done) {
    parts.push("done");
  } else if (plan.last_completed_step_index) {
    parts.push(`last step ${plan.last_completed_step_index}`);
  }
  return parts.join(", ");
}

function sessionAutomationLoopMetric(session: SessionSummary): string | undefined {
  if (!session.has_loop_protocol && !session.has_loop_state) return undefined;
  const state = session.loop_protocol?.state ?? session.loop_state;
  const status = session.loop_protocol?.status?.trim() ?? state?.status?.trim();
  const updates = state?.protocol_updates;
  const feeds = state?.protocol_feeds;
  const initialGoal = state?.initial_goal_preview?.trim();
  const plan = state?.last_plan_label?.trim();
  const planStepIndex = state?.last_plan_step_index;
  const planStepStatus = state?.last_plan_step_status?.trim();
  const memoryUpdates = state?.memory_update_events;
  const lastMemoryAction = state?.last_memory_update_action?.trim();
  const lastMemoryLocation = state?.last_memory_update_location?.trim();
  const decisions = state?.loop_decisions;
  const lastDecisionKind = state?.last_decision_kind?.trim();
  const lastDecision = state?.last_decision?.trim();
  const turnReason = state?.last_turn_end_reason?.trim();
  const eventSummary = state?.last_event_summary?.trim();
  const parts = status ? [`Automation ${status}`] : [];
  if (initialGoal) parts.push(`goal ${initialGoal}`);
  if (updates && updates > 0) parts.push(`${updates} ${updates === 1 ? "update" : "updates"}`);
  if (feeds && feeds > 0) parts.push(`${feeds} ${feeds === 1 ? "feed" : "feeds"}`);
  if (plan) parts.push(planStepIndex ? `${plan} step ${planStepIndex}${planStepStatus ? ` ${planStepStatus}` : ""}` : plan);
  if (memoryUpdates && memoryUpdates > 0) {
    parts.push(lastMemoryAction && lastMemoryLocation ? `memory ${lastMemoryAction} ${lastMemoryLocation}` : `${memoryUpdates} memory ${memoryUpdates === 1 ? "update" : "updates"}`);
  }
  if (decisions && decisions > 0) {
    const decisionName = lastDecisionKind === "research_checkpoint" ? "research checkpoint" : "decision";
    parts.push(lastDecisionKind && lastDecision ? `${decisionName} ${loopDecisionMetricResult(lastDecisionKind, lastDecision)}` : `${decisions} ${decisions === 1 ? decisionName : `${decisionName}s`}`);
  }
  if (turnReason) parts.push(`last turn ${turnReason}`);
  if (eventSummary) parts.push(eventSummary);
  if (parts.length === 0) return undefined;
  return parts.join(", ");
}

function loopDecisionMetricResult(kind: string, decision: string): string {
  if (kind === "research_checkpoint") return decision;
  return `${kind}:${decision}`;
}

function sessionAutomationScheduleMetric(session: SessionSummary): string | undefined {
  const summary = session.schedules;
  if (!summary || summary.count <= 0) return undefined;
  const pendingLoopTimers = pendingLoopTimerCount(session);
  const parts = [pendingLoopTimers > 0 ? `Automation timer ${pendingLoopTimers} pending/${summary.count}` : `Automation timer ${summary.enabled}/${summary.count}`];
  if ((summary.error_count ?? 0) > 0) parts.push(`${summary.error_count} error${summary.error_count === 1 ? "" : "s"}`);
  if (summary.last_error) parts.push(`last ${summarize(summary.last_error, 72)}`);
  if (pendingLoopTimers > 0) parts.push("waiting for LOOP.md activation");
  if (summary.next_run_at) parts.push(`next ${formatTimestamp(summary.next_run_at)}`);
  return parts.join(", ");
}

function pendingLoopTimerCount(session: SessionSummary): number {
  const summary = session.schedules;
  if (!summary || summary.enabled <= 0) return 0;
  if (loopProtocolRunning(session)) return 0;
  if ((summary.pending_loop_ticks ?? 0) > 0) return summary.pending_loop_ticks ?? 0;
  if (summary.next_schedule_kind === "loop_tick") return summary.enabled;
  const preview = summary.next_prompt_preview?.trim().toLowerCase() ?? "";
  if (preview.includes("scheduled loop tick")) return summary.enabled;
  return 0;
}

function loopProtocolRunning(session: SessionSummary): boolean {
  const status = session.loop_protocol?.state?.status?.trim() || session.loop_state?.status?.trim() || session.loop_protocol?.status?.trim();
  return status?.toLowerCase() === "running";
}

function planStatusLabel(plan: SessionPlanSummary): string {
  const status = plan.current_step_status?.trim();
  if (status === "in_progress") return "active";
  if (status === "blocked") return "blocked";
  if (status === "completed") return "done";
  if (status === "pending") return "pending";
  if (plan.active) return "active";
  if (plan.blocked) return "blocked";
  return "pending";
}

function featureChips(session: SessionSummary): string[] {
  const chips: string[] = [];
  if (session.has_artifacts) chips.push("files");
  if (session.has_memory) chips.push("memory");
  if (session.has_plan) chips.push("plan");
  if (session.has_loop_protocol || session.has_loop_state || session.has_schedules) chips.push("automation");
  if (session.has_runtime_skills) chips.push("skills");
  return chips;
}

function fallbackSessionTitle(session: SessionSummary): string {
  const hasWork = session.has_conversation || session.has_events;
  if (session.active) return hasWork ? "Live chat" : "New live chat";
  if (!hasWork && session.has_memory) return "Memory chat";
  if (!hasWork && session.has_plan) return "Planned chat";
  if (!hasWork && session.has_artifacts) return "Files chat";
  if (session.durable) return hasWork ? "Saved chat" : "New chat";
  return hasWork ? "Recent chat" : "New chat";
}

function providedSessionTitle(session: SessionSummary): string | undefined {
  const titles = [session.summary_title, session.generated_title, session.title]
    .map((value) => value?.replace(/\s+/g, " ").trim())
    .filter((value): value is string => Boolean(value))
    .filter((value) => !isPlaceholderTitle(value))
    .filter((value) => !isInternalRuntimePrompt(value));
  const rawSources = [session.topic_user_message, session.latest_user_message]
    .map((value) => value?.replace(/\s+/g, " ").trim())
    .filter((value): value is string => Boolean(value));
  for (const title of titles) {
    if (rawSources.some((source) => isRawPromptTitle(title, source))) continue;
    return summarize(title, 58);
  }
  return undefined;
}

function displayUserMessage(value?: string): string | undefined {
  const text = value?.replace(/\s+/g, " ").trim();
  if (!text || isPlaceholderTitle(text) || isInternalRuntimePrompt(text)) return undefined;
  return text;
}

function isPlaceholderTitle(text: string): boolean {
  const normalized = normalizeComparableTitle(text).replace(/^[()[\]{}'"“”‘’]+|[()[\]{}'"“”‘’]+$/g, "");
  return normalized === "empty" || normalized === "untitled" || normalized === "null" || normalized === "undefined";
}

function isInternalRuntimePrompt(text: string): boolean {
  const normalized = normalizeComparableTitle(text);
  return normalized.startsWith("tools are disabled for the rest of this turn") ||
    normalized.startsWith("do not call tools.") ||
    normalized.startsWith("do not call tools again.") ||
    normalized.startsWith("do not execute the task yet.") ||
    normalized.includes("previous assistant step still requested another tool") ||
    normalized.includes("use only existing tool results");
}

function isRawPromptTitle(title: string, source: string): boolean {
  const normalizedTitle = normalizeComparableTitle(title);
  const normalizedSource = normalizeComparableTitle(source);
  if (!normalizedTitle || !normalizedSource) return false;
  const generated = summarizeSessionTitle(source);
  const normalizedGenerated = normalizeComparableTitle(generated);
  if (normalizedGenerated === normalizedTitle) return false;
  if (normalizedTitle === normalizedSource) return true;
  if (normalizedSource.startsWith(normalizedTitle)) return true;
  const ellipsisFreeTitle = normalizeComparableTitle(title.replace(/[.。…]+$/g, ""));
  if (ellipsisFreeTitle && normalizedSource.startsWith(ellipsisFreeTitle)) return true;
  return looksLikeInstructionPrompt(title) && normalizedGenerated !== normalizedTitle;
}

function normalizeComparableTitle(text: string): string {
  return text.replace(/\s+/g, " ").trim().toLowerCase();
}

function looksLikeInstructionPrompt(text: string): boolean {
  return /(?:请你?|麻烦|帮我|帮忙|而不是|不要|需要|要求|please\b|can you\b|could you\b|instead of|rather than)/i.test(text);
}

function summarizeSessionDetail(session: SessionSummary, title: string): string | undefined {
  const topic = displayUserMessage(session.topic_user_message);
  const latest = displayUserMessage(session.latest_user_message);
  if (!topic || !latest) return undefined;
  if (topic === latest) return undefined;
  return summarizeLatestRequestDetail(latest, title);
}

function summarizeSessionPreview(session: SessionSummary, title: string, detail?: string): string | undefined {
  if (detail) return detail;
  const source = displayUserMessage(session.latest_user_message) || displayUserMessage(session.topic_user_message);
  if (!source) return undefined;
  return latestRequestPreview(source, title);
}

function latestRequestPreview(text: string, title: string): string | undefined {
  const cleaned = stripContinuationPrefix(text.replace(/\s+/g, " ").trim());
  if (!cleaned) return undefined;
  const summary = summarizeSessionTitle(cleaned);
  if (!summary || summary === title) return undefined;
  return `Recent · ${summarize(summary, 72)}`;
}

function summarizeLatestRequestDetail(text: string, title: string): string | undefined {
  const cleaned = stripContinuationPrefix(text.replace(/\s+/g, " ").trim());
  if (!cleaned) return undefined;
  if (summarizeSessionTitle(cleaned) === title) return undefined;
  const summary = summarize(cleaned, 48);
  if (summary === title) return undefined;
  return `Latest · ${summary}`;
}

function stripContinuationPrefix(text: string): string {
  let value = text.trim();
  let changed = true;
  while (changed) {
    const next = value
      .replace(/^(请继续|继续|接着)(?:同一个任务|这个任务|当前任务)?[。,.，、\s]*/i, "")
      .replace(/^同一个任务[。,.，、\s]*/i, "")
      .replace(/^(please\s+)?(?:continue|resume)(?:\s+(?:the\s+)?(?:same\s+)?(?:task|work|chat))?[。,.，、\s]*/i, "")
      .replace(/^this (?:task|work|chat) from where (?:it|we) stopped[:：]?\s*/i, "")
      .replace(/^from where (?:it|we) stopped[:：]?\s*/i, "")
      .replace(/^based on (?:the )?(?:existing|previous|collected) /i, "based on ")
      .trim();
    changed = next !== value;
    value = next;
  }
  return value;
}

export function summarizeSessionTitle(text: string): string {
  const cleaned = text.replace(/\s+/g, " ").trim();
  if (!cleaned) return "Saved chat";
  const directReply = summarizeDirectReplyPrompt(cleaned);
  if (directReply) return summarize(directReply, 42);
  const intentTitle = summarizeIntentTitle(cleaned);
  if (intentTitle) return summarize(intentTitle, 42);
  const actionTitle = summarizeActionRequest(cleaned);
  if (actionTitle) return summarize(actionTitle, 42);
  const firstLine = cleaned.split(/\n+/)[0] ?? cleaned;
  const primaryClause = firstLine
    .split(/(?:[。！？；;]+|[.!?]+(?=\s|$))/)
    .map((part) => part.trim())
    .find(Boolean) ?? firstLine;
  const beforeInstruction = primaryClause
    .split(/[,，]/)
    .map((part) => part.trim())
    .find((part) => part && !/^(请|请你|帮我|麻烦|please\b|can you\b|could you\b|continue\b|继续)/i.test(part)) ?? primaryClause;
  const normalized = trimTopicSuffix(stripTopicPrefix(beforeInstruction));
  const topicTitle = summarizeTopicStatement(normalized) ?? prettyTopicName(normalized);
  return summarize(topicTitle || cleaned, 42);
}

export function isGenericChatTitle(title: string | undefined): boolean {
  if (!title) return false;
  const normalized = title.trim().toLowerCase();
  return (
    normalized === "chat" ||
    normalized === "new chat" ||
    normalized === "live chat" ||
    normalized === "saved chat" ||
    normalized === "current conversation" ||
    normalized === "selected chat"
  );
}

export function formatLoadingChatTitle(title?: string): string {
  if (!title || isGenericChatTitle(title)) return "Loading chat";
  return `Loading ${title}`;
}

function summarizeDirectReplyPrompt(text: string): string | undefined {
  const fixedReply = text.match(/^(?:只(?:回复|回答)|仅(?:回复|回答)|回复|回答|only\s+reply|reply\s+with|respond\s+with|say)\s*[：:]\s*(.+)$/i);
  if (!fixedReply) return undefined;
  const topic = fixedReply[1]
    .replace(/^(?:ok|okay|done|yes|好的|可以|完成)\b[\s:：-]*/i, "")
    .replace(/^[“"']+|[”"']+$/g, "")
    .trim();
  if (!topic) return "Reply check";
  return `${capitalizeLeadingAscii(prettyTopicName(topic))} check`;
}

function summarizeIntentTitle(text: string): string | undefined {
  const metaTitle = summarizeTitleFeedback(text);
  if (metaTitle) return metaTitle;
  const focusTitle = summarizeFocusPhrase(text);
  if (focusTitle) return focusTitle;
  return undefined;
}

function summarizeActionRequest(text: string): string | undefined {
  const patterns = [
    new RegExp(`^(?:请你?|麻烦你?|帮我|帮忙|真实地?|真实|实际地?|完整地?|详细地?|认真地?)?\\s*(?:${cnTitleActions})\\s*(?:一下|下|一个|一种|一类|一份|这个|当前)?\\s*([^。！？；;\\n]{2,120})`, "i"),
    new RegExp(`^(?:please\\s+|can you\\s+|could you\\s+)?(?:${enTitleActions})\\s+(?:the\\s+|a\\s+|an\\s+|current\\s+)?([^!?;\\n]{2,120})`, "i"),
  ];
  for (const pattern of patterns) {
    const match = text.match(pattern);
    if (!match) continue;
    const title = normalizeActionTitle(match[1]);
    if (title) return title;
  }
  return undefined;
}

function normalizeActionTitle(text: string): string {
  const scoped = text
    .split(/(?:，|,)\s*(?:要求|需要|并且|同时|顺便|然后|再|so that|with|and then)\s*/i)[0]
    .replace(/^(?:一下|下|一个|一种|一类|一份|这个|当前)\s*/i, "")
    .replace(/^(?:the|a|an)\s+/i, "")
    .trim();
  return normalizeTitlePhrase(scoped);
}

function summarizeTitleFeedback(text: string): string | undefined {
  const namesTitle = /(?:会话|聊天|session|chat).*(?:标题|title)/i.test(text);
  const asksForSummary = /(?:总结|摘要|归纳|概括|summar|generated?|derived?)/i.test(text);
  if (namesTitle && asksForSummary) {
    return /[a-z]/i.test(text) && !/[\u3400-\u9fff]/.test(text) ? "Summarized chat titles" : "会话标题摘要";
  }
  return undefined;
}

function summarizeFocusPhrase(text: string): string | undefined {
  const patterns = [
    /(?:重点关注|主要关注|优先关注|关注|围绕|关于|针对)\s*([^。.!?；;，,\n]{2,80})/i,
    /(?:focus(?:ing)? on|focused on|around|about|regarding)\s+([^。.!?；;，,\n]{2,80})/i,
  ];
  for (const pattern of patterns) {
    const match = text.match(pattern);
    if (!match) continue;
    const title = normalizeTitlePhrase(match[1]);
    if (title) return title;
  }
  return undefined;
}

function capitalizeLeadingAscii(text: string): string {
  return text.replace(/^[a-z]/, (letter) => letter.toUpperCase());
}

function summarizeTopicStatement(text: string): string | undefined {
  const value = text.trim();
  if (!value) return undefined;
  const cn = value.match(/^(.{1,40}?)\s*(?:是|为)\s*(.{1,50}?)\s*的\s*(?:一个|一种|一类)?\s*(子网|项目|协议|平台|工具|框架|网络)\s*$/i);
  if (cn) {
    return `${prettyTopicName(cn[1])}（${prettyTopicName(cn[2])} ${cn[3]}）`;
  }
  const en = value.match(/^(.{1,40}?)\s+(?:is|as)\s+(?:an?\s+)?(.{1,50}?)\s+(subnet|project|protocol|platform|tool|framework|network)\s*$/i);
  if (en) {
    return `${prettyTopicName(en[1])} (${prettyTopicName(en[2])} ${en[3].toLowerCase()})`;
  }
  return undefined;
}

function prettyTopicName(text: string): string {
  let value = text
    .replace(/^[“"']+|[”"']+$/g, "")
    .replace(/\s+/g, " ")
    .trim();
  if (!value) return "";
  const replacements: Array<[RegExp, string]> = [
    [/\baffine\b/gi, "Affine"],
    [/\bbittensor\b/gi, "Bittensor"],
    [/\bwebui\b/gi, "WebUI"],
    [/\bapi\b/gi, "API"],
    [/\bmcp\b/gi, "MCP"],
    [/\bllm\b/gi, "LLM"],
    [/\btao\b/gi, "TAO"],
  ];
  for (const [pattern, replacement] of replacements) value = value.replace(pattern, replacement);
  return value;
}

function stripTopicPrefix(text: string): string {
  let value = text.trim();
  let changed = true;
  while (changed) {
    const next = value
      .replace(/^(请你?|麻烦你?|帮我|帮忙|please\s+|can you\s+|could you\s+)/i, "")
      .replace(/^(真实地?|实际地?|完整地?|详细地?|认真地?)\s*/i, "")
      .replace(/^(收集|检索|查询|查找|搜索|调研|研究|介绍|分析|总结|梳理|说明|整理|获取|输出|生成|review|research|inspect|summarize|analyze|explain)\s*/i, "")
      .replace(/^(the|a|an)\s+/i, "")
      .replace(/^关于\s*/, "")
      .trim();
    changed = next !== value;
    value = next;
  }
  return value;
}

function trimTopicSuffix(text: string): string {
  return text
    .replace(/(?:的)?(?:相关)?(?:信息|资料|内容|数据)(?:并.*|，.*|,.*|$)/, "")
    .replace(/(?:并)?(?:向我)?(?:介绍|说明|分析|总结|输出|生成).*/, "")
    .replace(/(?:是什么|是啥|是什麼)\s*$/, "")
    .trim();
}

function normalizeTitlePhrase(text: string): string {
  const title = trimTopicSuffix(stripTopicPrefix(text))
    .replace(/的(?=[\u3400-\u9fffA-Za-z0-9])/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  return prettyTopicName(title);
}

function buildRowMeta(updated: string, opts: { empty?: boolean } = {}): string[] {
  const meta: string[] = [];
  if (updated && updated !== noMessagesYet) meta.push(updated);
  if (opts.empty) meta.push(noMessagesYet);
  return meta;
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1))}...`;
}

function formatTimestamp(value?: string): string {
  if (!value) return noMessagesYet;
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  const pad = (part: number) => String(part).padStart(2, "0");
  const month = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"][date.getUTCMonth()];
  return `${month} ${date.getUTCDate()} ${pad(date.getUTCHours())}:${pad(date.getUTCMinutes())} UTC`;
}
