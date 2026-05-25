import type { SessionSummary } from "../api/sessions";
import type { SessionState } from "../store/sessionState";
import { conversationTopicFromTurns } from "./continuationPrompt";

const noMessagesYet = "No messages yet";

export type SessionListFilter = "all" | "active" | "saved" | "artifacts" | "memory";
export type SessionRowTone = "running" | "saved" | "muted" | "error" | "warning";

export interface SessionRowView {
  id: string;
  title: string;
  detail?: string;
  meta: string[];
  status: string;
  tone: SessionRowTone;
  updated: string;
  metrics: string[];
  chips: string[];
  searchText: string;
}

export function buildSessionRows(sessions: readonly SessionSummary[]): SessionRowView[] {
  return [...sessions].sort(compareSessionsForChatList).map((session) => {
    const status = session.active ? "Live" : session.durable ? "Saved" : "Ephemeral";
    const chips = featureChips(session);
    const metrics = usageMetrics(session);
    const titleSource = session.topic_user_message || session.latest_user_message;
    const title = titleSource ? summarizeSessionTitle(titleSource) : fallbackSessionTitle(session);
    const detail = summarizeSessionDetail(session, title);
    const updated = session.last_used_at ?? session.created_at ? formatTimestamp(session.last_used_at ?? session.created_at) : noMessagesYet;
    const searchText = [session.id, title, detail, session.topic_user_message, session.latest_user_message, status, updated, ...metrics, ...chips].join(" ").toLowerCase();

    return {
      id: session.id,
      title,
      detail,
      meta: buildRowMeta(session.id, updated, {
        empty: !titleSource && !session.has_conversation && !session.has_events,
        includeId: !titleSource,
      }),
      status,
      tone: session.active ? "running" : session.durable ? "saved" : "muted",
      updated,
      metrics,
      chips,
      searchText,
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
): SessionRowView[] {
  if (!selectedId || !session || session.turns.length === 0) return [...rows];
  return rows.map((row) => (row.id === selectedId ? mergeCurrentSession(row, session) : row));
}

export function filterSessionRows(
  rows: readonly SessionRowView[],
  filter: SessionListFilter,
  query: string,
): SessionRowView[] {
  const search = query.trim().toLowerCase();
  return rows.filter((row) => matchesFilter(row, filter) && (!search || row.searchText.includes(search)));
}

function mergeCurrentSession(row: SessionRowView, session: SessionState): SessionRowView {
  const latestTurn = session.turns.at(-1);
  const title = currentSessionTitle(row, session);
  const detail = currentSessionDetail(session, title);
  const hasTopicTitle = Boolean(conversationTopicFromTurns(session.turns));
  const metrics = currentSessionMetrics(session);
  const chips = mergeChips(row.chips, currentSessionChips(session));
  const status = currentSessionStatus(session, row.status);
  const userSearchText = session.turns.map((turn) => turn.userText).join(" ");
  const searchText = [row.id, title, detail, status, userSearchText, ...metrics, ...chips].join(" ").toLowerCase();
  const updated = latestTurn?.userText && row.updated === noMessagesYet ? "" : row.updated;

  return {
    ...row,
    title,
    detail,
    meta: buildRowMeta(row.id, updated, { includeId: !hasTopicTitle }),
    status,
    tone: currentSessionTone(session, row.tone),
    updated,
    metrics,
    chips,
    searchText,
  };
}

function currentSessionTitle(row: SessionRowView, session: SessionState): string {
  const topic = conversationTopicFromTurns(session.turns);
  return topic ? summarizeSessionTitle(topic) : row.title;
}

function currentSessionDetail(session: SessionState, title: string): string | undefined {
  const topic = conversationTopicFromTurns(session.turns);
  const latest = [...session.turns].reverse().find((turn) => Boolean(turn.userText?.trim()))?.userText;
  if (!topic || !latest || latest === topic) return undefined;
  return summarizeLatestRequestDetail(latest, title);
}

function currentSessionStatus(session: SessionState, fallback: string): string {
  if (session.status === "running") return "Live";
  if (session.status === "completed") return "Done";
  if (session.status === "max_turns") return "No final answer";
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
  const metrics = [`${session.turns.length} message${session.turns.length === 1 ? "" : "s"}`];
  if (toolCount > 0) metrics.push(`${toolCount} action${toolCount === 1 ? "" : "s"}`);
  if (currentIssueCount > 0) metrics.push(`${currentIssueCount} issue${currentIssueCount === 1 ? "" : "s"}`);
  if (continuedCount > 0) metrics.push(`${continuedCount} continued`);
  if (priorIssueCount > 0) metrics.push(`${priorIssueCount} prior issue${priorIssueCount === 1 ? "" : "s"}`);
  if (toolIssueCount > 0) metrics.push(`${toolIssueCount} tool issue${toolIssueCount === 1 ? "" : "s"}`);
  return metrics;
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
  if (session.unknownEventCount > 0) chips.push("log notes");
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
  };
}

function matchesFilter(row: SessionRowView, filter: SessionListFilter): boolean {
  if (filter === "all") return true;
  if (filter === "active") return row.status === "Live";
  if (filter === "saved") return row.status === "Saved";
  if (filter === "artifacts") return row.chips.includes("files") || row.chips.includes("artifacts");
  if (filter === "memory") return row.chips.includes("memory");
  return true;
}

function usageMetrics(session: SessionSummary): string[] {
  const metrics: string[] = [];
  const turns = session.usage?.turns ?? 0;
  if (turns > 0) metrics.push(`${turns} message${turns === 1 ? "" : "s"}`);
  if (session.browser && session.browser.network_fetch > 0) metrics.push(`${session.browser.network_fetch} web`);
  return metrics;
}

function featureChips(session: SessionSummary): string[] {
  const chips: string[] = [];
  if (session.has_artifacts) chips.push("files");
  if (session.has_memory) chips.push("memory");
  if (session.has_runtime_skills) chips.push("skills");
  return chips;
}

function shortenSessionId(id: string): string {
  if (id.length <= 18) return id;
  return `${id.slice(0, 8)}...${id.slice(-6)}`;
}

function fallbackSessionTitle(session: SessionSummary): string {
  const hasWork = session.has_conversation || session.has_events;
  if (session.active) return hasWork ? "Live chat" : "New live chat";
  if (session.durable) return hasWork ? "Saved chat" : "New chat";
  return hasWork ? "Recent chat" : "New chat";
}

function summarizeSessionDetail(session: SessionSummary, title: string): string | undefined {
  if (!session.topic_user_message || !session.latest_user_message) return undefined;
  if (session.topic_user_message === session.latest_user_message) return undefined;
  return summarizeLatestRequestDetail(session.latest_user_message, title);
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

function buildRowMeta(id: string, updated: string, opts: { empty?: boolean; includeId?: boolean } = {}): string[] {
  const meta: string[] = [];
  if (opts.includeId) meta.push(shortenSessionId(id));
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
