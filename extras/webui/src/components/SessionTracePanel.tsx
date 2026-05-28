import { useMemo, useState } from "react";
import { EventType, type ToolResultPayload } from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { filterEventTraceEvents } from "../view/eventTrace";
import {
  sessionTraceDraft,
  sessionTraceEvidenceText,
  type SessionTraceView,
} from "../view/sessionTrace";
import type { DraftSource } from "../view/draftSource";
import { CopyButton } from "./CopyButton";
import { EventTrace } from "./EventTrace";

export function SessionTracePanel({
  trace,
  events,
  defaultOpen = false,
  onOpenArtifact,
  onUseAsDraft,
}: {
  trace: SessionTraceView;
  events: readonly NormalizedEvent[];
  defaultOpen?: boolean;
  onOpenArtifact?: (path: string) => void;
  onUseAsDraft?: (draft: string, source: DraftSource) => void;
}) {
  const [query, setQuery] = useState("");
  const [filter, setFilter] = useState<TraceFilter>("all");
  const trimmedQuery = query.trim();
  const filters = useMemo(() => traceFilters(events, trace.toolIssueCount), [events, trace.toolIssueCount]);
  const visibleEvents = useMemo(
    () => {
      const source = filterEventsByTraceFilter(events, filter);
      return trimmedQuery ? filterEventTraceEvents(source, trimmedQuery) : source;
    },
    [events, filter, trimmedQuery],
  );
  const searchHelp = "Search supports plain text plus type:, tool:, call:, turn:, id:, status:failed, artifact:, path:.";

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
            <div className="session-trace-actions">
              <CopyButton label="Copy trace evidence" value={sessionTraceEvidenceText(trace)} className="node-action" />
              {onUseAsDraft ? (
                <button type="button" className="node-action" onClick={() => onUseAsDraft(sessionTraceDraft(trace), "trace")}>
                  Use trace as draft
                </button>
              ) : null}
            </div>
            {events.length > 1 ? (
              <div className="session-skills-controls">
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
            {events.length > 1 ? <p className="session-trace-search-help" id="session-trace-search-help">{searchHelp}</p> : null}
            <div className="session-trace-metrics" data-testid="session-trace-metrics">
              <span><strong>Entries</strong>{trace.eventCount}</span>
              <span><strong>Records</strong>{trace.recordCount}</span>
              {trace.toolIssueCount > 0 ? <span data-tone="error"><strong>Tool issues</strong>{trace.toolIssueCount}</span> : null}
              {trimmedQuery ? <span><strong>Matching</strong>{visibleEvents.length}</span> : null}
              {filter !== "all" ? <span><strong>Filter</strong>{filterLabel(filter)}</span> : null}
              {trace.schemaVersion ? <span><strong>Schema</strong>v{trace.schemaVersion}</span> : null}
              {trace.unknownCount > 0 ? <span data-tone="warning"><strong>Unclassified</strong>{trace.unknownCount}</span> : null}
            </div>
            {!trimmedQuery && trace.latest ? (
              <div className="session-trace-latest" data-testid="session-trace-latest">
                <strong>{trace.latest.label}</strong>
                <span>{trace.latest.detail}</span>
              </div>
            ) : null}
            {visibleEvents.length > 0 ? (
              <EventTrace events={visibleEvents} onOpenArtifact={onOpenArtifact} />
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

type TraceFilter = "all" | "issues" | "actions" | "commands" | "files" | "memory" | "context" | "loop";

interface TraceFilterItem {
  key: TraceFilter;
  label: string;
  count: number;
}

function traceFilters(events: readonly NormalizedEvent[], toolIssueCount: number): TraceFilterItem[] {
  return [
    { key: "all", label: "All", count: events.length },
    { key: "issues", label: "Tool issues", count: toolIssueCount },
    { key: "actions", label: "Actions", count: countFilter(events, "actions") },
    { key: "commands", label: "Commands", count: countFilter(events, "commands") },
    { key: "files", label: "Files", count: countFilter(events, "files") },
    { key: "memory", label: "Memory", count: countFilter(events, "memory") },
    { key: "context", label: "Context", count: countFilter(events, "context") },
    { key: "loop", label: "Loop", count: countFilter(events, "loop") },
  ];
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
  return "All";
}

function emptyStateLabel(filter: TraceFilter, query: string): string {
  const filterText = filter === "all" ? "" : filterLabel(filter);
  if (query) return filterText ? `${filterText} and "${query}"` : `"${query}"`;
  return filterText || "the selected filter";
}

function eventMatchesFilter(event: NormalizedEvent, filter: TraceFilter, callTools: Map<string, string>): boolean {
  if (filter === "actions") return event.type === EventType.ToolRequest || event.type === EventType.ToolResult;
  if (filter === "context") return event.type === EventType.ContextInjected || event.type === EventType.ContextCompacted || event.type === EventType.Usage;
  if (filter === "loop") return event.type.startsWith("loop.");
  if (event.type !== EventType.ToolRequest && event.type !== EventType.ToolResult) return false;
  const tool = toolName(event, callTools);
  if (filter === "commands") return tool === "shell";
  if (filter === "files") return tool === "read_file" || tool === "write_file" || tool === "edit_file" || tool === "list_files";
  if (filter === "memory") return tool === "memory" || tool === "session_search";
  return false;
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
