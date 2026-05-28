import { useMemo, useState } from "react";
import { EventType, type ToolResultPayload } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { filterEventTraceEvents } from "../view/eventTrace";
import {
  type SessionTraceView,
} from "../view/sessionTrace";
import { CopyButton } from "./CopyButton";
import { EventTrace } from "./EventTrace";

export function SessionTracePanel({
  trace,
  events,
  defaultOpen = false,
  onOpenArtifact,
}: {
  trace: SessionTraceView;
  events: readonly NormalizedEvent[];
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
}) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<TraceFilter>(() => trace.toolIssueCount > 0 ? "issues" : "all");
  const [activeIssueId, setActiveIssueId] = useState<string | undefined>();
  const trimmedQuery = query.trim();
  const filters = useMemo(() => traceFilters(events, trace.toolIssueCount), [events, trace.toolIssueCount]);
  const issueGroups = useMemo(() => traceToolIssueGroups(trace.toolIssues), [trace.toolIssues]);
  const activeIssue = trace.toolIssues.find((issue) => issue.id === activeIssueId)
    ?? (filter === "issues" ? trace.toolIssues[0] : undefined);
  const hasActiveNarrowing = filter !== "all" || Boolean(trimmedQuery);
  const visibleEvents = useMemo(
    () => {
      const source = filterEventsByTraceFilter(events, filter);
      return trimmedQuery ? filterEventTraceEvents(source, trimmedQuery) : source;
    },
    [events, filter, trimmedQuery],
  );
  const selectionSummary = useMemo(() => traceSelectionSummary(events, visibleEvents), [events, visibleEvents]);
  const applySearch = (nextQuery: string, nextFilter: TraceFilter = "all") => {
    setFilter(nextFilter);
    setQuery(nextQuery);
  };

  return (
    <details className="session-skills-panel session-trace-panel" data-testid="session-trace-panel" open={defaultOpen}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Trace</span>
        <strong>{trace.summary}</strong>
        <span>{trace.detail}</span>
      </summary>
      <div className="session-skills-body session-trace-body">
        {trace.eventCount > 0 ? (
          <>
            {events.length > 1 ? (
              <div className="session-skills-controls session-trace-controls">
                <label className="session-skills-search">
                  <span>Search trace</span>
                  <input
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="plain text, tool:shell, status:failed, turn:t1"
                    aria-describedby="session-trace-search-help"
                  />
                </label>
                {trimmedQuery ? (
                  <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                    Clear
                  </button>
                ) : null}
                <div className="session-trace-filter-group" role="group" aria-label="Trace filters">
                  {filters.map((item) => (
                    <button
                      key={item.key}
                      type="button"
                      className="session-trace-filter"
                      aria-pressed={filter === item.key}
                      disabled={item.count === 0 && item.key !== "all"}
                      onClick={() => setFilter((current) => current === item.key && item.key !== "all" ? "all" : item.key)}
                    >
                      {item.label}{item.count > 0 ? ` ${item.count}` : ""}
                    </button>
                  ))}
                </div>
              </div>
            ) : null}
            {events.length > 1 ? (
              <div className="session-trace-query-tools" id="session-trace-search-help" aria-label="Trace search shortcuts">
                <span>Quick search</span>
                <button type="button" onClick={() => applySearch("status:failed", "all")}>status:failed</button>
                <button type="button" onClick={() => applySearch("tool:shell", "commands")}>tool:shell</button>
                <button type="button" onClick={() => applySearch("repaired", "repairs")}>repaired</button>
                <button type="button" onClick={() => applySearch("truncated", "truncated")}>truncated</button>
                <button type="button" onClick={() => applySearch("artifact:", "artifacts")}>artifact:</button>
                <button type="button" onClick={() => applySearch("path:", "files")}>path:</button>
                <button type="button" onClick={() => applySearch("unclassified", "unclassified")}>unclassified</button>
              </div>
            ) : null}
            <div className="session-trace-resultbar" data-testid="session-trace-resultbar">
              <div>
                <strong>{visibleEvents.length}</strong>
                <span>{resultCountLabel(visibleEvents.length, hasActiveNarrowing)}</span>
              </div>
              {filter !== "all" ? <span>Filter: {filterLabel(filter)}</span> : null}
              {trimmedQuery ? <span>Search: {trimmedQuery}</span> : null}
              {hasActiveNarrowing ? (
                <button
                  type="button"
                  onClick={() => {
                    setFilter("all");
                    setQuery("");
                    setActiveIssueId(undefined);
                  }}
                >
                  Reset
                </button>
              ) : null}
            </div>
            <TraceSelectionSummaryView summary={selectionSummary} />
            <div className="session-trace-metrics" data-testid="session-trace-metrics">
              <span><strong>Records</strong>{trace.recordCount}</span>
              {trace.schemaVersion ? <span><strong>Schema</strong>v{trace.schemaVersion}</span> : null}
              {trace.unknownCount > 0 ? <span data-tone="warning"><strong>Unclassified</strong>{trace.unknownCount}</span> : null}
            </div>
            {trace.toolIssues.length > 0 ? (
              <div className="session-trace-issues" data-testid="session-trace-issues">
                <div className="session-trace-issues-head">
                  <strong>Issue navigator</strong>
                  <span>{trace.toolIssueCount} {trace.toolIssueCount === 1 ? "issue" : "issues"} across {issueGroups.length} {issueGroups.length === 1 ? "tool" : "tools"}</span>
                </div>
                <div className="session-trace-issue-groups" role="group" aria-label="Tool issue groups">
                  {issueGroups.map((group) => (
                    <button
                      key={group.tool}
                      type="button"
                      onClick={() => {
                        setFilter("issues");
                        setQuery(`tool:${group.tool}`);
                        setActiveIssueId(trace.toolIssues.find((issue) => issue.tool === group.tool)?.id);
                      }}
                    >
                      <strong>{group.tool}</strong>
                      <span>{group.count}</span>
                    </button>
                  ))}
                </div>
                <div className="session-trace-issue-list">
                  {trace.toolIssues.map((issue) => (
                    <button
                      key={`${issue.id}:${issue.title}`}
                      type="button"
                      className="session-trace-issue"
                      data-selected={activeIssue?.id === issue.id ? "true" : "false"}
                      onClick={() => {
                        setActiveIssueId(issue.id);
                        setFilter("issues");
                        setQuery(issue.query);
                      }}
                    >
                      <span>{issue.title}</span>
                      <small>{issue.detail}</small>
                      {issue.badges.length > 0 ? (
                        <span className="session-trace-issue-badges" aria-hidden="true">
                          {issue.badges.slice(0, 3).map((badge) => <b key={badge}>{badge}</b>)}
                        </span>
                      ) : null}
                    </button>
                  ))}
                </div>
                {activeIssue ? (
                  <div className="session-trace-issue-focus" data-testid="session-trace-issue-focus">
                    <div className="session-trace-issue-focus-head">
                      <span>Selected issue</span>
                      <strong>{activeIssue.title}</strong>
                      <small>{activeIssue.detail}</small>
                    </div>
                    <div className="session-trace-issue-facts">
                      <TraceIssueFact label="Tool" value={activeIssue.tool} />
                      <TraceIssueFact label="Turn" value={String(activeIssue.turnNumber)} />
                      {activeIssue.exitCode != null ? <TraceIssueFact label="Exit" value={String(activeIssue.exitCode)} /> : null}
                      {activeIssue.durationMs != null ? <TraceIssueFact label="Duration" value={formatTraceDuration(activeIssue.durationMs)} /> : null}
                    </div>
                    {activeIssue.next ? (
                      <div className="session-trace-issue-next">
                        <span>Next from trace</span>
                        <p>{activeIssue.next}</p>
                      </div>
                    ) : null}
                    <div className="session-trace-issue-actions">
                      <button
                        type="button"
                        className="ghost-action"
                        onClick={() => {
                          setFilter("issues");
                          setQuery(activeIssue.query);
                        }}
                      >
                        Show event pair
                      </button>
                      <button
                        type="button"
                        className="ghost-action"
                        onClick={() => {
                          setFilter("all");
                          setQuery(activeIssue.requestQuery);
                        }}
                      >
                        Show request
                      </button>
                      {activeIssue.artifactPath && onOpenArtifact ? (
                        <button type="button" className="ghost-action" onClick={() => onOpenArtifact(activeIssue.artifactPath ?? "")}>
                          Open artifact
                        </button>
                      ) : null}
                      <CopyButton label="Copy query" value={activeIssue.query} className="ghost-action" />
                    </div>
                  </div>
                ) : null}
              </div>
            ) : null}
            {!hasActiveNarrowing && trace.latest ? (
              <div className="session-trace-latest" data-testid="session-trace-latest">
                <strong>{trace.latest.label}</strong>
                <span>{trace.latest.detail}</span>
              </div>
            ) : null}
            {visibleEvents.length > 0 ? (
              <div className="session-trace-results">
                <EventTrace events={visibleEvents} onOpenArtifact={onOpenArtifact} />
              </div>
            ) : (
              <div className="session-skills-empty">No trace entries matching {emptyStateLabel(filter, trimmedQuery)}.</div>
            )}
          </>
        ) : (
          <div className="session-skills-empty" data-testid="session-trace-empty">No persisted trace loaded for this chat.</div>
        )}
      </div>
    </details>
  );
}

type TraceFilter = "all" | "issues" | "actions" | "commands" | "files" | "memory" | "context" | "loop" | "sources" | "artifacts" | "repairs" | "truncated" | "unclassified";

interface TraceFilterItem {
  key: TraceFilter;
  label: string;
  count: number;
}

interface TraceToolIssueGroup {
  tool: string;
  count: number;
}

interface TraceSelectionSummary {
  eventSpan: string;
  requestSpan: string;
  failedActions: number;
  actionResults: number;
  repairCount: number;
  truncatedCount: number;
  toolCount: number;
  topTools: string[];
  artifactCount: number;
  unclassifiedCount: number;
}

function traceToolIssueGroups(issues: SessionTraceView["toolIssues"]): TraceToolIssueGroup[] {
  const counts = new Map<string, number>();
  for (const issue of issues) {
    counts.set(issue.tool, (counts.get(issue.tool) ?? 0) + issue.occurrences);
  }
  return [...counts.entries()]
    .map(([tool, count]) => ({ tool, count }))
    .sort((a, b) => b.count - a.count || a.tool.localeCompare(b.tool));
}

function TraceSelectionSummaryView({ summary }: { summary: TraceSelectionSummary }) {
  return (
    <div className="session-trace-selection" data-testid="session-trace-selection">
      <TraceSelectionMetric label="Span" value={summary.eventSpan} />
      <TraceSelectionMetric label="Requests" value={summary.requestSpan} />
      <TraceSelectionMetric label="Failures" value={String(summary.failedActions)} />
      {summary.repairCount > 0 ? <TraceSelectionMetric label="Repairs" value={String(summary.repairCount)} /> : null}
      {summary.truncatedCount > 0 ? <TraceSelectionMetric label="Truncated" value={String(summary.truncatedCount)} /> : null}
      <TraceSelectionMetric label="Results" value={String(summary.actionResults)} />
      <TraceSelectionMetric label="Tools" value={summary.toolCount ? `${summary.toolCount} · ${summary.topTools.join(", ")}` : "0"} kind="tools" />
      <TraceSelectionMetric label="Artifacts" value={String(summary.artifactCount)} />
      {summary.unclassifiedCount > 0 ? <TraceSelectionMetric label="Unclassified" value={String(summary.unclassifiedCount)} /> : null}
    </div>
  );
}

function TraceSelectionMetric({ label, value, kind }: { label: string; value: string; kind?: string }) {
  return (
    <span data-kind={kind}>
      <small>{label}</small>
      <strong>{value}</strong>
    </span>
  );
}

function TraceIssueFact({ label, value }: { label: string; value: string }) {
  return (
    <span>
      <small>{label}</small>
      <strong>{value}</strong>
    </span>
  );
}

function traceSelectionSummary(allEvents: readonly NormalizedEvent[], visibleEvents: readonly NormalizedEvent[]): TraceSelectionSummary {
  const requestLabels = requestLabelsByTurnId(allEvents);
  const toolCounts = new Map<string, number>();
  const callTools = toolNamesByCallId(allEvents);
  const requestSet = new Set<string>();
  let failedActions = 0;
  let actionResults = 0;
  let repairCount = 0;
  let truncatedCount = 0;
  let artifactCount = 0;
  let unclassifiedCount = 0;
  let firstId: number | undefined;
  let lastId: number | undefined;

  for (const event of visibleEvents) {
    if (typeof event.id === "number") {
      firstId = firstId == null ? event.id : Math.min(firstId, event.id);
      lastId = lastId == null ? event.id : Math.max(lastId, event.id);
    }
    if (event.turnId) requestSet.add(event.turnId);
    if (!event.known) unclassifiedCount += 1;
    if (eventHasRepair(event)) repairCount += 1;
    if (eventHasTruncation(event)) truncatedCount += 1;
    if (eventHasArtifact(event)) artifactCount += 1;
    if (event.type === EventType.ToolRequest || event.type === EventType.ToolResult) {
      const tool = toolName(event, callTools);
      if (tool) toolCounts.set(tool, (toolCounts.get(tool) ?? 0) + 1);
    }
    if (event.type === EventType.ToolResult) {
      actionResults += 1;
      const data = event.data as ToolResultPayload;
      if ((data.exit_code ?? 0) !== 0 || data.failure_kind || data.failure_kinds?.length) failedActions += 1;
    }
  }

  const topTools = [...toolCounts.entries()]
    .sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0]))
    .slice(0, 3)
    .map(([tool, count]) => `${tool} ${count}`);
  const requestNumbers = [...requestSet]
    .map((turnId) => requestLabels.get(turnId))
    .filter((label): label is number => typeof label === "number")
    .sort((a, b) => a - b);

  return {
    eventSpan: firstId == null || lastId == null ? "none" : firstId === lastId ? `#${firstId}` : `#${firstId}-#${lastId}`,
    requestSpan: requestNumbers.length === 0
      ? "none"
      : requestNumbers.length === 1
        ? `Request ${requestNumbers[0]}`
        : `Request ${requestNumbers[0]}-${requestNumbers[requestNumbers.length - 1]} · ${requestNumbers.length}`,
    failedActions,
    actionResults,
    repairCount,
    truncatedCount,
    toolCount: toolCounts.size,
    topTools,
    artifactCount,
    unclassifiedCount,
  };
}

function requestLabelsByTurnId(events: readonly NormalizedEvent[]): Map<string, number> {
  const labels = new Map<string, number>();
  for (const event of events) {
    if (!event.turnId || labels.has(event.turnId)) continue;
    labels.set(event.turnId, labels.size + 1);
  }
  return labels;
}

function formatTraceDuration(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  return `${(ms / 1000).toFixed(ms < 10_000 ? 2 : 1)}s`;
}

function traceFilters(events: readonly NormalizedEvent[], toolIssueCount: number): TraceFilterItem[] {
  return compactFilters([
    { key: "all", label: "All", count: events.length },
    { key: "issues", label: "Tool issues", count: toolIssueCount },
    { key: "actions", label: "Actions", count: countFilter(events, "actions") },
    { key: "commands", label: "Commands", count: countFilter(events, "commands") },
    { key: "files", label: "Files", count: countFilter(events, "files") },
    { key: "memory", label: "Memory", count: countFilter(events, "memory") },
    { key: "context", label: "Context", count: countFilter(events, "context") },
    { key: "loop", label: "Loop", count: countFilter(events, "loop") },
    { key: "sources", label: "Sources", count: countFilter(events, "sources") },
    { key: "artifacts", label: "Artifacts", count: countFilter(events, "artifacts") },
    { key: "repairs", label: "Repairs", count: countFilter(events, "repairs") },
    { key: "truncated", label: "Truncated", count: countFilter(events, "truncated") },
    { key: "unclassified", label: "Unclassified", count: countFilter(events, "unclassified") },
  ]);
}

function compactFilters(filters: TraceFilterItem[]): TraceFilterItem[] {
  return filters.filter((item) => item.key === "all" || item.count > 0);
}

function filterEventsByTraceFilter(events: readonly NormalizedEvent[], filter: TraceFilter): NormalizedEvent[] {
  if (filter === "all") return [...events];
  if (filter === "issues") return filterToolIssueEvents(events);
  const callTools = toolNamesByCallId(events);
  return events.filter((event) => eventMatchesFilter(event, filter, callTools));
}

function countFilter(events: readonly NormalizedEvent[], filter: TraceFilter): number {
  return filterEventsByTraceFilter(events, filter).length;
}

function filterLabel(filter: TraceFilter): string {
  if (filter === "issues") return "Tool issues";
  if (filter === "actions") return "Actions";
  if (filter === "commands") return "Commands";
  if (filter === "files") return "Files";
  if (filter === "memory") return "Memory";
  if (filter === "context") return "Context";
  if (filter === "loop") return "Loop";
  if (filter === "sources") return "Sources";
  if (filter === "artifacts") return "Artifacts";
  if (filter === "repairs") return "Repairs";
  if (filter === "truncated") return "Truncated";
  if (filter === "unclassified") return "Unclassified";
  return "All";
}

function emptyStateLabel(filter: TraceFilter, query: string): string {
  const filterText = filter === "all" ? "" : filterLabel(filter);
  if (query) return filterText ? `${filterText} and "${query}"` : `"${query}"`;
  return filterText || "the selected filter";
}

function resultCountLabel(count: number, narrowed: boolean): string {
  if (narrowed) return count === 1 ? "matching entry" : "matching entries";
  return count === 1 ? "trace entry loaded" : "trace entries loaded";
}

function eventMatchesFilter(event: NormalizedEvent, filter: TraceFilter, callTools: Map<string, string>): boolean {
  if (filter === "actions") return event.type === EventType.ToolRequest || event.type === EventType.ToolResult;
  if (filter === "context") return event.type === EventType.ContextInjected || event.type === EventType.ContextCompacted || event.type === EventType.Usage;
  if (filter === "loop") return event.type.startsWith("loop.");
  if (filter === "unclassified") return !event.known;
  if (filter === "artifacts") return eventHasArtifact(event);
  if (filter === "sources") return eventHasSourceEvidence(event, callTools);
  if (filter === "repairs") return eventHasRepair(event);
  if (filter === "truncated") return eventHasTruncation(event);
  if (event.type !== EventType.ToolRequest && event.type !== EventType.ToolResult) return false;
  const tool = toolName(event, callTools);
  if (filter === "commands") return tool === "shell";
  if (filter === "files") return tool === "read_file" || tool === "write_file" || tool === "edit_file" || tool === "list_files";
  if (filter === "memory") return tool === "memory" || tool === "session_search";
  return false;
}

function eventHasArtifact(event: NormalizedEvent): boolean {
  if (!event.data || typeof event.data !== "object") return false;
  const artifactPath = (event.data as { result_artifact_path?: unknown }).result_artifact_path;
  return typeof artifactPath === "string" && artifactPath.trim().length > 0;
}

function eventHasRepair(event: NormalizedEvent): boolean {
  if (event.type !== EventType.ToolRequest || !event.data || typeof event.data !== "object") return false;
  const data = event.data as Record<string, unknown>;
  return data.args_repaired === true
    || data.canonicalized === true
    || (Array.isArray(data.repair_notes) && data.repair_notes.length > 0);
}

function eventHasTruncation(event: NormalizedEvent): boolean {
  if (!event.data || typeof event.data !== "object") return false;
  const data = event.data as Record<string, unknown>;
  return data.args_truncated === true
    || data.result_truncated === true
    || positiveNumber(data.args_omitted_bytes)
    || positiveNumber(data.result_omitted_bytes);
}

function positiveNumber(value: unknown): boolean {
  return typeof value === "number" && value > 0;
}

function eventHasSourceEvidence(event: NormalizedEvent, callTools: Map<string, string>): boolean {
  if (event.type !== EventType.ToolRequest && event.type !== EventType.ToolResult) return false;
  const tool = toolName(event, callTools) ?? "";
  if (/^(web_|browser_)/.test(tool)) return true;
  if (!event.data || typeof event.data !== "object") return false;
  const text = [
    (event.data as { result_summary?: unknown }).result_summary,
    (event.data as { result?: unknown }).result,
  ].filter((value): value is string => typeof value === "string").join("\n");
  return /SourceAccess:|BROWSER_NETWORK|browser_network_|web_fetch/i.test(text);
}

function filterToolIssueEvents(events: readonly NormalizedEvent[]): NormalizedEvent[] {
  const failedCallIds = new Set<string>();
  for (const event of events) {
    if (event.type !== EventType.ToolResult) continue;
    const data = event.data as ToolResultPayload;
    if ((data.exit_code ?? 0) !== 0 || data.failure_kind || data.failure_kinds?.length) failedCallIds.add(data.call_id);
  }
  return events.filter((event) => {
    if (event.type === EventType.ToolRequest || event.type === EventType.ToolResult) {
      const data = event.data as { call_id?: unknown };
      const callId = typeof data.call_id === "string" ? data.call_id : "";
      return failedCallIds.has(callId);
    }
    return false;
  });
}

function toolNamesByCallId(events: readonly NormalizedEvent[]): Map<string, string> {
  const out = new Map<string, string>();
  for (const event of events) {
    if (event.type !== EventType.ToolRequest || !event.data || typeof event.data !== "object") continue;
    const callID = (event.data as { call_id?: unknown }).call_id;
    const tool = (event.data as { tool?: unknown }).tool;
    if (typeof callID === "string" && typeof tool === "string") out.set(callID, tool);
  }
  return out;
}

function toolName(event: NormalizedEvent, callTools: Map<string, string>): string | undefined {
  if (!event.data || typeof event.data !== "object") return undefined;
  const value = (event.data as { tool?: unknown }).tool;
  if (typeof value === "string") return value;
  if (event.type !== EventType.ToolResult) return undefined;
  const callID = (event.data as { call_id?: unknown }).call_id;
  if (typeof callID !== "string") return undefined;
  return callTools.get(callID);
}
