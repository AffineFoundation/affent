import type { ToolCallState, ToolCallStatus, TurnState } from "../store/sessionState";

export type ExecutionNodeKind = "tool" | "subagent" | "focused_task" | "mcp";

export interface ExecutionMetric {
  label: string;
  value: string;
}

export interface ExecutionTokenUsage {
  inputTokens?: number;
  outputTokens?: number;
  totalTokens: number;
  costUsd?: number;
}

export interface ExecutionFinding {
  title: string;
  detail?: string;
  source?: string;
  confidence?: string;
  severity?: string;
}

export interface ExecutionTreeNode {
  id: string;
  depth: number;
  kind: ExecutionNodeKind;
  tool: string;
  label: string;
  title: string;
  subtitle?: string;
  preview?: string;
  status: ToolCallStatus;
  callId?: string;
  exitCode?: number;
  durationMs?: number;
  args?: Record<string, unknown>;
  originalTool?: string;
  originalArgsSummary?: string;
  repairNotes?: string[];
  resultSummary?: string;
  resultText?: string;
  nextHint?: string;
  resultArtifactPath?: string;
  argsTruncated?: boolean;
  resultTruncated?: boolean;
  argsBytes?: number;
  argsOmittedBytes?: number;
  argsCapBytes?: number;
  resultBytes?: number;
  resultOmittedBytes?: number;
  resultCapBytes?: number;
  contextBytes?: number;
  contextOmittedBytes?: number;
  contextEstimatedTokens?: number;
  summary?: string;
  report?: string;
  taskType?: string;
  objective?: string;
  childSessionId?: string;
  turnEndReason?: string;
  tokenUsage?: ExecutionTokenUsage;
  mcpServer?: string;
  mcpTool?: string;
  metrics: ExecutionMetric[];
  findings: ExecutionFinding[];
  warnings: string[];
  notFound: string[];
  suggestedNext: string[];
  children: ExecutionTreeNode[];
}

type JsonObject = Record<string, unknown>;

interface ChildToolCall {
  tool?: unknown;
  args?: unknown;
  exit_code?: unknown;
  result_summary?: unknown;
  result?: unknown;
  tool_calls?: unknown;
}

const builtinTools = new Set([
  "read_file",
  "list_files",
  "write_file",
  "edit_file",
  "shell",
  "memory",
  "session_search",
  "browser_navigate",
  "browser_back",
  "browser_wait",
  "browser_snapshot",
  "browser_click",
  "browser_type",
  "browser_scroll",
  "browser_screenshot",
  "web_fetch",
  "web_search",
  "skill",
]);

export function buildExecutionTree(turn: TurnState): ExecutionTreeNode[] {
  return turn.toolCalls.map((call) => nodeFromToolCall(call, 0, call.callId));
}

export function searchableExecutionNodeText(node: ExecutionTreeNode): string[] {
  return [
    node.label,
    node.title,
    node.subtitle,
    node.preview,
    node.summary,
    node.report,
    node.objective,
    node.mcpServer,
    node.mcpTool,
    ...node.children.flatMap(searchableExecutionNodeText),
  ].filter((item): item is string => !!item);
}

function nodeFromToolCall(call: ToolCallState, depth: number, id: string): ExecutionTreeNode {
  const parsed = parseStructuredResult(call.result) ?? parseStructuredResult(call.resultSummary);
  const kind = classifyTool(call.tool, parsed);
  const mcp = kind === "mcp" ? splitMcpTool(call.tool) : undefined;
  const tokenUsage = readTokenUsage(parsed);
  const metrics = baseMetrics(call);
  const node: ExecutionTreeNode = {
    id,
    depth,
    kind,
    tool: call.tool,
    label: labelFor(kind, call.tool, parsed),
    title: titleFor(kind, call.tool, call.args, parsed),
    subtitle: subtitleFor(kind, call.tool, call.args, parsed),
    preview: previewFor(call.resultSummary, call.result, parsed),
    status: call.status,
    callId: call.callId,
    exitCode: call.exitCode,
    durationMs: call.durationMs,
    args: call.args,
    originalTool: call.originalTool,
    originalArgsSummary: call.originalArgsSummary,
    repairNotes: call.repairNotes,
    resultSummary: call.resultSummary,
    resultText: call.result,
    nextHint: nextHintFrom(call.result) ?? nextHintFrom(call.resultSummary),
    resultArtifactPath: call.resultArtifactPath,
    argsTruncated: call.argsTruncated,
    resultTruncated: call.resultTruncated,
    argsBytes: call.argsBytes,
    argsOmittedBytes: call.argsOmittedBytes,
    argsCapBytes: call.argsCapBytes,
    resultBytes: call.resultBytes,
    resultOmittedBytes: call.resultOmittedBytes,
    resultCapBytes: call.resultCapBytes,
    contextBytes: call.contextBytes,
    contextOmittedBytes: call.contextOmittedBytes,
    contextEstimatedTokens: call.contextEstimatedTokens,
    summary: readString(parsed, "summary"),
    report: readString(parsed, "report"),
    taskType: readString(parsed, "task_type") ?? readString(call.args, "task_type"),
    objective: readString(parsed, "objective") ?? readString(call.args, "objective") ?? readString(call.args, "task"),
    childSessionId: readString(parsed, "child_session_id"),
    turnEndReason: readString(parsed, "turn_end_reason"),
    tokenUsage,
    mcpServer: mcp?.server,
    mcpTool: mcp?.tool,
    metrics,
    findings: readFindings(parsed),
    warnings: readStringList(parsed, "warnings"),
    notFound: readStringList(parsed, "not_found"),
    suggestedNext: readStringList(parsed, "suggested_next"),
    children: readChildTools(parsed).map((child, idx) => nodeFromChildTool(child, depth + 1, `${id}.${idx}`)),
  };
  appendStructuredMetrics(node, parsed);
  return node;
}

function nodeFromChildTool(child: ChildToolCall, depth: number, id: string): ExecutionTreeNode {
  const tool = typeof child.tool === "string" && child.tool !== "" ? child.tool : "unknown_tool";
  const resultText = typeof child.result === "string" ? child.result : undefined;
  const resultSummary = typeof child.result_summary === "string" ? child.result_summary : undefined;
  const parsed = parseStructuredResult(resultText) ?? parseStructuredResult(resultSummary);
  const exitCode = typeof child.exit_code === "number" ? child.exit_code : undefined;
  const status: ToolCallStatus = exitCode == null || exitCode === 0 ? "success" : "error";
  const kind = classifyTool(tool, parsed);
  const mcp = kind === "mcp" ? splitMcpTool(tool) : undefined;
  const args = isObject(child.args) ? child.args : undefined;
  const tokenUsage = readTokenUsage(parsed);
  const node: ExecutionTreeNode = {
    id,
    depth,
    kind,
    tool,
    label: labelFor(kind, tool, parsed),
    title: titleFor(kind, tool, args, parsed),
    subtitle: subtitleFor(kind, tool, args, parsed),
    preview: previewFor(resultSummary, resultText, parsed),
    status,
    exitCode,
    args,
    resultSummary,
    resultText,
    nextHint: nextHintFrom(resultText) ?? nextHintFrom(resultSummary),
    summary: readString(parsed, "summary"),
    report: readString(parsed, "report"),
    taskType: readString(parsed, "task_type") ?? readString(args, "task_type"),
    objective: readString(parsed, "objective") ?? readString(args, "objective") ?? readString(args, "task"),
    childSessionId: readString(parsed, "child_session_id"),
    turnEndReason: readString(parsed, "turn_end_reason"),
    tokenUsage,
    mcpServer: mcp?.server,
    mcpTool: mcp?.tool,
    metrics: exitCode == null ? [] : [{ label: "exit", value: String(exitCode) }],
    findings: readFindings(parsed),
    warnings: readStringList(parsed, "warnings"),
    notFound: readStringList(parsed, "not_found"),
    suggestedNext: readStringList(parsed, "suggested_next"),
    children: readChildTools(parsed).map((nested, idx) => nodeFromChildTool(nested, depth + 1, `${id}.${idx}`)),
  };
  appendStructuredMetrics(node, parsed);
  return node;
}

function classifyTool(tool: string, parsed?: JsonObject): ExecutionNodeKind {
  if (tool === "subagent_run") return "subagent";
  if (tool === "run_task") return "focused_task";
  if (readString(parsed, "child_session_id") && readString(parsed, "task_type")) return "focused_task";
  if (readString(parsed, "child_session_id") || readString(parsed, "report")) return "subagent";
  return splitMcpTool(tool) ? "mcp" : "tool";
}

function labelFor(kind: ExecutionNodeKind, tool: string, parsed?: JsonObject): string {
  if (kind === "focused_task") {
    const taskType = readString(parsed, "task_type");
    return taskType ? `Focused work · ${taskType}` : "Focused work";
  }
  if (kind === "subagent") {
    return "Delegated work";
  }
  if (kind === "mcp") {
    return "MCP action";
  }
  return labelForTool(tool);
}

function titleFor(kind: ExecutionNodeKind, tool: string, args?: JsonObject, parsed?: JsonObject): string {
  const objective = readString(parsed, "objective") ?? readString(args, "objective") ?? readString(args, "task");
  if ((kind === "subagent" || kind === "focused_task") && objective) return objective;
  if (kind === "focused_task") return "Focused task";
  if (kind === "subagent") return "Subagent task";
  if (kind === "mcp") {
    const mcp = splitMcpTool(tool);
    return mcp ? prettyToolName(mcp.tool) : "MCP action";
  }
  if (tool === "shell") return shellCommandTitle(readString(args, "command"));
  if (tool === "web_fetch") {
    const url = readString(args, "url");
    return url ? `Fetch ${readableUrl(url)}` : "Fetch web page";
  }
  if (tool === "web_search") {
    const query = readString(args, "query") ?? readString(args, "q");
    return query ? `Search ${compactLine(query, 80)}` : "Search web";
  }
  if (tool === "read_file") return readString(args, "path") ?? "Read file";
  if (tool === "list_files") {
    const path = readString(args, "path");
    if (path === ".") return "List current directory";
    return path ? `List ${path}` : "List files";
  }
  if (tool === "write_file") return readString(args, "path") ?? "Write file";
  if (tool === "edit_file") return readString(args, "path") ?? "Edit file";
  if (tool.startsWith("browser_")) return prettyToolName(tool.replace(/^browser_/, "Browser "));
  if (tool.startsWith("web_")) return prettyToolName(tool);
  return prettyToolName(tool);
}

function shellCommandTitle(command?: string): string {
  const value = command?.trim();
  if (!value) return "Shell command";
  const listTarget = readableListCommandTarget(value);
  if (listTarget === ".") return "List current directory";
  if (listTarget) return `List ${listTarget}`;
  return value;
}

function readableListCommandTarget(command: string): string | undefined {
  if (!/^ls(?:\s|$)/.test(command)) return undefined;
  if (/[;&|`$<>]/.test(command)) return undefined;
  const parts = command.split(/\s+/).filter(Boolean).slice(1);
  const targets = parts.filter((part) => !part.startsWith("-"));
  if (targets.length === 0) return ".";
  if (targets.length === 1) return targets[0] === "./" ? "." : targets[0].replace(/\/+$/, "") || ".";
  return undefined;
}

function subtitleFor(kind: ExecutionNodeKind, tool: string, args?: JsonObject, parsed?: JsonObject): string | undefined {
  if (kind === "subagent") return "Delegated worker";
  if (kind === "focused_task") return "Focused worker";
  if (kind === "mcp") return "External MCP service";
  const path = readString(args, "path");
  if ((tool === "read_file" || tool === "write_file" || tool === "edit_file") && path) return undefined;
  const objective = readString(parsed, "objective") ?? readString(args, "objective") ?? readString(args, "task");
  if (objective && objective !== tool) return labelForTool(tool);
  return undefined;
}

function prettyToolName(value: string): string {
  return value
    .replace(/[_-]+/g, " ")
    .replace(/\s+/g, " ")
    .trim()
    .replace(/\b\w/g, (char) => char.toUpperCase());
}

function labelForTool(tool: string): string {
  if (tool === "shell") return "Command";
  if (tool === "read_file" || tool === "list_files" || tool === "write_file" || tool === "edit_file") {
    return "File action";
  }
  if (tool.startsWith("browser_")) return "Browser action";
  if (tool.startsWith("web_")) return "Web action";
  if (tool === "memory") return "Memory action";
  if (tool === "session_search") return "Search";
  if (tool === "skill") return "Skill";
  return "Action";
}

function previewFor(resultSummary?: string, resultText?: string, parsed?: JsonObject): string | undefined {
  const finding = readFindings(parsed)[0];
  const raw =
    readString(parsed, "summary") ??
    readString(parsed, "report") ??
    finding?.title ??
    resultSummary ??
    resultText;
  if (!raw) return undefined;
  return compactLine(raw, 132);
}

function nextHintFrom(text?: string): string | undefined {
  const line = text?.split(/\r?\n/).find((part) => part.trim().toLowerCase().startsWith("next:"));
  return line?.replace(/^next:\s*/i, "").trim();
}

function compactLine(value: string, maxChars: number): string | undefined {
  const line = value
    .replace(/\r?\n+/g, " ")
    .replace(/\s+/g, " ")
    .trim();
  if (!line) return undefined;
  if (line.length <= maxChars) return line;
  return `${line.slice(0, maxChars - 1)}...`;
}

function readableUrl(value: string): string {
  try {
    const url = new URL(value);
    const host = url.hostname.replace(/^www\./, "");
    const path = url.pathname.replace(/\/+$/, "");
    if (!path || path === "/") return host;
    return `${host}/${path.split("/").filter(Boolean).slice(0, 2).join("/")}`;
  } catch {
    return compactLine(value, 64) ?? value;
  }
}

function baseMetrics(call: ToolCallState): ExecutionMetric[] {
  const metrics: ExecutionMetric[] = [];
  if (call.callId) metrics.push({ label: "request id", value: call.callId });
  if (call.durationMs != null) metrics.push({ label: "duration", value: `${call.durationMs}ms` });
  if (call.exitCode != null) metrics.push({ label: "exit", value: String(call.exitCode) });
  if (call.contextEstimatedTokens && call.contextEstimatedTokens > 0) {
    metrics.push({ label: "merged", value: `~${call.contextEstimatedTokens} tokens` });
  }
  return metrics;
}

function appendStructuredMetrics(node: ExecutionTreeNode, parsed?: JsonObject) {
  const childSession = readString(parsed, "child_session_id");
  const turnEndReason = readString(parsed, "turn_end_reason");
  const depth = readNumber(parsed, "depth");
  const maxDepth = readNumber(parsed, "max_depth");
  if (childSession) node.metrics.push({ label: "child", value: childSession });
  if (turnEndReason) node.metrics.push({ label: "reason", value: turnEndReason });
  if (depth != null && maxDepth != null) node.metrics.push({ label: "depth", value: `${depth}/${maxDepth}` });
  if (node.tokenUsage) {
    node.metrics.push({ label: "tokens", value: formatTokenUsageCompact(node.tokenUsage) });
    if (node.tokenUsage.inputTokens != null) node.metrics.push({ label: "input", value: String(node.tokenUsage.inputTokens) });
    if (node.tokenUsage.outputTokens != null) node.metrics.push({ label: "output", value: String(node.tokenUsage.outputTokens) });
    if (node.tokenUsage.costUsd != null) node.metrics.push({ label: "cost", value: formatUsd(node.tokenUsage.costUsd) });
  }
}

export function formatTokenUsageCompact(usage: ExecutionTokenUsage): string {
  return `${usage.totalTokens} ${pluralize("token", usage.totalTokens)}`;
}

export function formatTokenUsageDetail(usage: ExecutionTokenUsage): string {
  const parts = [formatTokenUsageCompact(usage)];
  const split: string[] = [];
  if (usage.inputTokens != null) split.push(`${usage.inputTokens} in`);
  if (usage.outputTokens != null) split.push(`${usage.outputTokens} out`);
  if (split.length > 0) parts.push(`(${split.join(" / ")})`);
  return parts.join(" ");
}

function readTokenUsage(parsed?: JsonObject): ExecutionTokenUsage | undefined {
  const usage = readObject(parsed, "usage");
  if (!usage) return undefined;
  const inputTokens = readNumber(usage, "input_tokens");
  const outputTokens = readNumber(usage, "output_tokens");
  const explicitTotal = readNumber(usage, "total_tokens");
  const totalTokens = explicitTotal ?? (inputTokens ?? 0) + (outputTokens ?? 0);
  const costUsd =
    readNumber(usage, "cost_usd") ??
    readNumber(usage, "total_cost_usd") ??
    readNumber(usage, "estimated_cost_usd");
  if (!totalTokens && costUsd == null) return undefined;
  return { inputTokens, outputTokens, totalTokens, costUsd };
}

function formatUsd(value: number): string {
  if (value === 0) return "$0";
  if (value < 0.01) return `$${value.toFixed(4)}`;
  return `$${value.toFixed(2)}`;
}

function splitMcpTool(tool: string): { server: string; tool: string } | undefined {
  const idx = tool.indexOf("_");
  if (idx <= 0) return undefined;
  if (builtinTools.has(tool)) return undefined;
  const server = tool.slice(0, idx);
  const advertised = tool.slice(idx + 1);
  if (!server || !advertised) return undefined;
  if (server !== server.toUpperCase() && server.length < 3) return undefined;
  return { server, tool: advertised };
}

function parseStructuredResult(text?: string): JsonObject | undefined {
  if (!text) return undefined;
  const trimmed = text.trim();
  if (!trimmed.startsWith("{")) return undefined;
  try {
    const parsed: unknown = JSON.parse(trimmed);
    return isObject(parsed) ? parsed : undefined;
  } catch {
    const end = trimmed.lastIndexOf("}");
    if (end <= 0) return undefined;
    try {
      const parsed: unknown = JSON.parse(trimmed.slice(0, end + 1));
      return isObject(parsed) ? parsed : undefined;
    } catch {
      return undefined;
    }
  }
}

function readChildTools(parsed?: JsonObject): ChildToolCall[] {
  const raw = parsed?.tool_calls;
  if (!Array.isArray(raw)) return [];
  return raw.filter(isObject) as ChildToolCall[];
}

function readFindings(parsed?: JsonObject): ExecutionFinding[] {
  const raw = parsed?.findings;
  if (!Array.isArray(raw)) return [];
  return raw.filter(isObject).map((item) => ({
    title: readString(item, "claim") ?? readString(item, "title") ?? readString(item, "summary") ?? "finding",
    detail: readString(item, "evidence") ?? readString(item, "detail"),
    source: readString(item, "source"),
    confidence: readString(item, "confidence"),
    severity: readString(item, "severity"),
  }));
}

function readStringList(obj: JsonObject | undefined, key: string): string[] {
  const raw = obj?.[key];
  if (!Array.isArray(raw)) return [];
  return raw.filter((item): item is string => typeof item === "string" && item.trim() !== "");
}

function readObject(obj: JsonObject | undefined, key: string): JsonObject | undefined {
  const value = obj?.[key];
  return isObject(value) ? value : undefined;
}

function readString(obj: JsonObject | undefined, key: string): string | undefined {
  const value = obj?.[key];
  return typeof value === "string" && value.trim() !== "" ? value : undefined;
}

function readNumber(obj: JsonObject | undefined, key: string): number | undefined {
  const value = obj?.[key];
  return typeof value === "number" && Number.isFinite(value) ? value : undefined;
}

function isObject(value: unknown): value is JsonObject {
  return !!value && typeof value === "object" && !Array.isArray(value);
}

function pluralize(label: string, count: number): string {
  return count === 1 ? label : `${label}s`;
}
