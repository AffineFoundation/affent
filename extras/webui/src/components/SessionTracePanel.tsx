import { useMemo, useState } from "react";
import {
  EventType,
  type ToolRequestPayload,
  type ToolResultPayload,
} from "../api/events";
import type { NormalizedEvent } from "../normalize/normalizeEvent";
import { filterEventTraceEvents } from "../view/eventTrace";
import { type SessionTraceView } from "../view/sessionTrace";
import {
  describeSourceAccess,
  sourceEvidenceLabel,
} from "../view/sourceAccess";
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
  const [filter, setFilter] = useState<TraceFilter>("all");
  const [controlsOpen, setControlsOpen] = useState(false);
  const [activeIssueId, setActiveIssueId] = useState<string | undefined>();
  const trimmedQuery = query.trim();
  const filters = useMemo(
    () => traceFilters(events, trace.toolIssueCount),
    [events, trace.toolIssueCount],
  );
  const evidenceNavItems = useMemo(
    () => traceEvidenceNavigatorItems(filters, events),
    [events, filters],
  );
  const searchShortcuts = useMemo(
    () => traceSearchShortcuts(events, trace),
    [events, trace],
  );
  const visibleIssues = useMemo(
    () => filterTraceIssues(trace.toolIssues, filter, trimmedQuery),
    [filter, trace.toolIssues, trimmedQuery],
  );
  const issueGroups = useMemo(
    () => traceToolIssueGroups(visibleIssues),
    [visibleIssues],
  );
  const headerDigest = useMemo(() => traceHeaderDigest(trace), [trace]);
  const activeIssue =
    visibleIssues.find((issue) => issue.id === activeIssueId) ??
    issueGroups[0]?.issues[0];
  const focusedIssue = trace.toolIssues.length > 0 ? activeIssue : undefined;
  const eventEvidenceQuery = trimmedQuery;
  const hasActiveNarrowing = filter !== "all" || Boolean(trimmedQuery);
  const visibleEvents = useMemo(() => {
    const source = filterEventsByTraceFilter(events, filter);
    return eventEvidenceQuery
      ? filterEventTraceEvents(source, eventEvidenceQuery)
      : source;
  }, [events, eventEvidenceQuery, filter]);
  const evidenceEvents = useMemo(() => visibleEvents, [visibleEvents]);
  const visibleIssueCount = visibleIssues.reduce(
    (sum, issue) => sum + issue.occurrences,
    0,
  );
  const selectionSummary = useMemo(
    () => traceSelectionSummary(events, visibleEvents),
    [events, visibleEvents],
  );
  const focusedIssueEvidence = useMemo(
    () =>
      focusedIssue ? traceFailureEvidence(focusedIssue, events) : undefined,
    [events, focusedIssue],
  );
  const defaultFilter: TraceFilter = "all";
  const showTraceControls =
    controlsOpen || Boolean(trimmedQuery) || filter !== defaultFilter;
  const applySearch = (nextQuery: string, nextFilter: TraceFilter = "all") => {
    setControlsOpen(true);
    setFilter(nextFilter);
    setQuery(nextQuery);
  };

  return (
    <section
      className="session-trace-panel"
      data-testid="session-trace-panel"
      data-open={defaultOpen ? "true" : "false"}
      data-has-issues={trace.toolIssueCount > 0 ? "true" : "false"}
    >
      <header className="session-trace-header">
        <div className="session-trace-title">
          <span>Trace</span>
          <strong>{headerDigest.primary}</strong>
          <small>{headerDigest.secondary}</small>
        </div>
      </header>
      <div className="session-trace-body">
        {trace.eventCount > 0 ? (
          <>
            {events.length > 1 ? (
              <>
                <div className="session-trace-controlbar">
                  <button
                    type="button"
                    className="ghost-action"
                    onClick={() => setControlsOpen((open) => !open)}
                  >
                    {controlsOpen ? "Hide filters" : "Filter trace"}
                  </button>
                  <span>
                    {traceControlSummary(
                      filter,
                      trimmedQuery,
                      visibleEvents.length,
                      events.length,
                      focusedIssue,
                    )}
                  </span>
                </div>
                {showTraceControls ? (
                  <div className="session-trace-toolbar">
                    <label className="session-skills-search">
                      <span className="visually-hidden">Search events</span>
                      <input
                        value={query}
                        onChange={(event) => setQuery(event.target.value)}
                        placeholder="Search trace: tool:shell, status:failed, exit:1"
                        aria-describedby={
                          searchShortcuts.length > 0
                            ? "session-trace-search-help"
                            : undefined
                        }
                      />
                    </label>
                    <div
                      className="session-trace-filter-group"
                      role="group"
                      aria-label="Trace filters"
                    >
                      {filters.map((item) => (
                        <button
                          key={item.key}
                          type="button"
                          className="session-trace-filter"
                          aria-pressed={filter === item.key}
                          disabled={item.count === 0 && item.key !== "all"}
                          onClick={() =>
                            setFilter((current) =>
                              current === item.key && item.key !== "all"
                                ? "all"
                                : item.key,
                            )
                          }
                        >
                          {item.label}
                          {item.count > 0 ? ` ${item.count}` : ""}
                        </button>
                      ))}
                    </div>
                    {searchShortcuts.length > 0 ? (
                      <div
                        className="session-trace-query-tools"
                        id="session-trace-search-help"
                        aria-label="Trace search shortcuts"
                      >
                        <span className="visually-hidden">
                          Suggested trace queries
                        </span>
                        {searchShortcuts.map((shortcut) => (
                          <button
                            key={shortcut.label}
                            type="button"
                            onClick={() =>
                              applySearch(shortcut.query, shortcut.filter)
                            }
                          >
                            {shortcut.label}
                          </button>
                        ))}
                      </div>
                    ) : null}
                    {trimmedQuery ? (
                      <button
                        type="button"
                        className="ghost-action"
                        onClick={() => setQuery("")}
                      >
                        Clear search
                      </button>
                    ) : null}
                  </div>
                ) : null}
              </>
            ) : null}
            <div
              className="session-trace-debugger"
              data-has-issues={trace.toolIssues.length > 0 ? "true" : "false"}
            >
              <aside
                className="session-trace-rail"
                aria-label="Trace navigation"
              >
                {trace.toolIssues.length > 0 ? (
                  <div
                    className="session-trace-issues"
                    data-testid="session-trace-issues"
                  >
                    <div className="session-trace-issues-head">
                      <div>
                        <strong>Failures</strong>
                        <span>
                          {visibleIssueCount}{" "}
                          {visibleIssueCount === 1 ? "failure" : "failures"} ·{" "}
                          {issueGroups.length}{" "}
                          {issueGroups.length === 1 ? "tool" : "tools"}
                        </span>
                      </div>
                    </div>
                    {visibleIssues.length > 0 ? (
                      <div
                        className="session-trace-issue-list"
                        aria-label="Trace failures"
                      >
                        {issueGroups.map((group) => (
                          <section
                            key={group.tool}
                            className="session-trace-issue-group"
                          >
                            <button
                              type="button"
                              className="session-trace-issue-group-head"
                              onClick={() => {
                                setFilter("all");
                                setQuery("");
                                setActiveIssueId(group.issues[0]?.id);
                              }}
                            >
                              <strong>{group.tool}</strong>
                              <span>{group.count}</span>
                            </button>
                            <div className="session-trace-issue-group-list">
                              {traceToolRequestIssueGroups(group.issues).map(
                                (requestGroup) => (
                                  <section
                                    key={`${group.tool}:${requestGroup.turnNumber}`}
                                    className="session-trace-request-group"
                                  >
                                    <button
                                      type="button"
                                      className="session-trace-request-head"
                                      onClick={() => {
                                        setFilter("all");
                                        setQuery("");
                                        setActiveIssueId(
                                          requestGroup.issues[0]?.id,
                                        );
                                      }}
                                    >
                                      <strong>
                                        Request {requestGroup.turnNumber}
                                      </strong>
                                      <span>
                                        {requestGroup.count}{" "}
                                        {requestGroup.count === 1
                                          ? "failure"
                                          : "failures"}
                                      </span>
                                    </button>
                                    <div className="session-trace-request-issues">
                                      {requestGroup.issues.map((issue) => (
                                        <button
                                          key={`${issue.id}:${issue.title}`}
                                          type="button"
                                          className="session-trace-issue"
                                          data-selected={
                                            focusedIssue?.id === issue.id
                                              ? "true"
                                              : "false"
                                          }
                                          onClick={() => {
                                            setActiveIssueId(issue.id);
                                            setFilter("all");
                                            setQuery("");
                                          }}
                                        >
                                          <span title={issue.title}>
                                            {traceIssueCauseLabel(issue)}
                                          </span>
                                          <small title={issue.detail}>
                                            {traceIssueDetailLabel(issue)}
                                          </small>
                                          {issue.badges.length > 0 ? (
                                            <span
                                              className="session-trace-issue-badges"
                                              aria-hidden="true"
                                            >
                                              {issue.badges
                                                .slice(0, 3)
                                                .map((badge) => (
                                                  <b key={badge}>
                                                    {traceIssueBadgeLabel(
                                                      badge,
                                                    )}
                                                  </b>
                                                ))}
                                            </span>
                                          ) : null}
                                        </button>
                                      ))}
                                    </div>
                                  </section>
                                ),
                              )}
                            </div>
                          </section>
                        ))}
                      </div>
                    ) : (
                      <div className="session-skills-empty">
                        No failures matching the current trace filter.
                      </div>
                    )}
                  </div>
                ) : evidenceNavItems.length > 0 ? (
                  <div
                    className="session-trace-evidence-nav"
                    data-testid="session-trace-evidence-nav"
                  >
                    <div className="session-trace-evidence-nav-head">
                      <strong>Evidence</strong>
                      <span>
                        {evidenceNavItems.length}{" "}
                        {evidenceNavItems.length === 1 ? "scope" : "scopes"}
                      </span>
                    </div>
                    <div
                      className="session-trace-evidence-list"
                      aria-label="Trace evidence"
                    >
                      {evidenceNavItems.map((item) => (
                        <section
                          key={item.key}
                          className="session-trace-evidence-entry"
                        >
                          <button
                            type="button"
                            className="session-trace-evidence-item"
                            data-selected={
                              filter === item.key ? "true" : "false"
                            }
                            onClick={() => {
                              setFilter((current) =>
                                current === item.key ? "all" : item.key,
                              );
                              setQuery("");
                              setActiveIssueId(undefined);
                            }}
                          >
                            <span>{item.label}</span>
                            <strong>{item.count}</strong>
                            <small>{item.detail}</small>
                          </button>
                          {item.children.length > 0 &&
                          (filter === item.key ||
                            evidenceNavItems.length === 1) ? (
                            <div className="session-trace-evidence-children">
                              {item.children.map((child) => (
                                <button
                                  key={`${item.key}:${child.query}`}
                                  type="button"
                                  className="session-trace-evidence-child"
                                  data-selected={
                                    filter === item.key && query === child.query
                                      ? "true"
                                      : "false"
                                  }
                                  onClick={() => {
                                    setFilter(item.key);
                                    setQuery(child.query);
                                    setActiveIssueId(undefined);
                                  }}
                                >
                                  <span>{child.label}</span>
                                  <small>{child.detail}</small>
                                </button>
                              ))}
                            </div>
                          ) : null}
                        </section>
                      ))}
                    </div>
                  </div>
                ) : (
                  <div className="session-trace-clear">
                    <strong>No failed tool calls</strong>
                    {trace.latest ? (
                      <span>
                        {trace.latest.label}: {trace.latest.detail}
                      </span>
                    ) : (
                      <span>Trace events are available.</span>
                    )}
                  </div>
                )}
              </aside>
              <section className="session-trace-main" aria-label="Trace detail">
                {focusedIssue ? (
                  <div
                    className="session-trace-issue-focus"
                    data-testid="session-trace-issue-focus"
                  >
                    <div className="session-trace-issue-focus-head">
                      <span>Failure cause</span>
                      <strong>{traceIssueCauseLabel(focusedIssue)}</strong>
                      {focusedIssue.detail ? (
                        <small>{traceIssueDetailLabel(focusedIssue)}</small>
                      ) : null}
                    </div>
                    {focusedIssue.next ? (
                      <div
                        className="session-trace-issue-next"
                        data-testid="session-trace-issue-next"
                      >
                        <span>Next</span>
                        <p>{focusedIssue.next}</p>
                      </div>
                    ) : null}
                    {focusedIssueEvidence ? (
                      <TraceFailureEvidenceCard
                        evidence={focusedIssueEvidence}
                        onOpenArtifact={onOpenArtifact}
                      />
                    ) : null}
                    <div className="session-trace-issue-facts">
                      <TraceIssueFact label="Tool" value={focusedIssue.tool} />
                      <TraceIssueFact
                        label="Request"
                        value={String(focusedIssue.turnNumber)}
                      />
                      {focusedIssue.exitCode != null ? (
                        <TraceIssueFact
                          label="Exit"
                          value={String(focusedIssue.exitCode)}
                        />
                      ) : null}
                      {focusedIssue.durationMs != null ? (
                        <TraceIssueFact
                          label="Duration"
                          value={formatTraceDuration(focusedIssue.durationMs)}
                        />
                      ) : null}
                      {focusedIssue.occurrences > 1 ? (
                        <TraceIssueFact
                          label="Repeats"
                          value={`${focusedIssue.occurrences}x`}
                        />
                      ) : null}
                    </div>
                    <div className="session-trace-issue-actions">
                      <button
                        type="button"
                        className="ghost-action"
                        onClick={() => {
                          setFilter("issues");
                          setQuery(focusedIssue.query);
                          setControlsOpen(true);
                        }}
                      >
                        Only this call
                      </button>
                      <button
                        type="button"
                        className="ghost-action"
                        onClick={() => {
                          setFilter("all");
                          setQuery(focusedIssue.requestQuery);
                          setControlsOpen(true);
                        }}
                      >
                        Whole request
                      </button>
                      {focusedIssue.artifactPath && onOpenArtifact ? (
                        <button
                          type="button"
                          className="ghost-action"
                          onClick={() =>
                            onOpenArtifact(focusedIssue.artifactPath ?? "")
                          }
                        >
                          Open artifact
                        </button>
                      ) : null}
                    </div>
                  </div>
                ) : null}
                <div
                  className="session-trace-resultbar"
                  data-testid="session-trace-resultbar"
                >
                  <div>
                    <strong>{visibleEvents.length}</strong>
                    <span>
                      {resultCountLabel(
                        visibleEvents.length,
                        hasActiveNarrowing,
                        events.length,
                      )}
                    </span>
                  </div>
                  <div
                    className="session-trace-active-scopes"
                    aria-label="Active trace scopes"
                  >
                    {filter !== "all" ? (
                      <span>{filterLabel(filter)}</span>
                    ) : null}
                    {trimmedQuery ? <span>Search: {trimmedQuery}</span> : null}
                  </div>
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
                <TraceScopeSummaryView
                  summary={selectionSummary}
                  trace={trace}
                />
                {!focusedIssue && !hasActiveNarrowing && trace.latest ? (
                  <div
                    className="session-trace-latest"
                    data-testid="session-trace-latest"
                  >
                    <strong>{trace.latest.label}</strong>
                    <span>{trace.latest.detail}</span>
                  </div>
                ) : null}
                {visibleEvents.length > 0 ? (
                  <div className="session-trace-results">
                    <EventTrace
                      events={evidenceEvents}
                      onOpenArtifact={onOpenArtifact}
                      showCount={false}
                    />
                  </div>
                ) : (
                  <div className="session-skills-empty">
                    No trace entries matching{" "}
                    {emptyStateLabel(filter, trimmedQuery)}.
                  </div>
                )}
              </section>
            </div>
          </>
        ) : (
          <div
            className="session-skills-empty"
            data-testid="session-trace-empty"
          >
            No persisted trace loaded for this chat.
          </div>
        )}
      </div>
    </section>
  );
}

type TraceFilter =
  | "all"
  | "issues"
  | "actions"
  | "commands"
  | "files"
  | "memory"
  | "context"
  | "loop"
  | "sources"
  | "artifacts"
  | "repairs"
  | "truncated"
  | "unclassified";

interface TraceFilterItem {
  key: TraceFilter;
  label: string;
  count: number;
}

interface TraceEvidenceNavigatorItem {
  key: TraceFilter;
  label: string;
  count: number;
  detail: string;
  children: TraceEvidenceNavigatorChild[];
}

interface TraceEvidenceNavigatorChild {
  label: string;
  detail: string;
  query: string;
}

interface TraceToolIssueGroup {
  tool: string;
  count: number;
  issues: SessionTraceView["toolIssues"];
}

interface TraceToolRequestIssueGroup {
  turnNumber: number;
  count: number;
  issues: SessionTraceView["toolIssues"];
}

interface TraceSearchShortcut {
  label: string;
  query: string;
  filter: TraceFilter;
}

interface TraceSelectionSummary {
  eventSpan: string;
  requestSpan: string;
  failedActions: number;
  repairCount: number;
  truncatedCount: number;
  toolCount: number;
  topTools: string[];
}

interface TraceFailureEvidence {
  tool: string;
  requestLabel: string;
  requestValue?: string;
  output?: string;
  artifactPath?: string;
  artifactLabel?: string;
  meta: string[];
  related: TraceRelatedEvidence[];
}

interface TraceRelatedEvidence {
  kind: string;
  label: string;
  detail: string;
}

function TraceFailureEvidenceCard({
  evidence,
  onOpenArtifact,
}: {
  evidence: TraceFailureEvidence;
  onOpenArtifact?: (path: string) => void;
}) {
  return (
    <div className="session-trace-problem" data-testid="session-trace-problem">
      <div className="session-trace-problem-head">
        <span>Problem details</span>
        <strong>{evidence.tool}</strong>
        {evidence.meta.length > 0 ? (
          <small>{evidence.meta.join(" · ")}</small>
        ) : null}
      </div>
      {evidence.requestValue ? (
        <div className="session-trace-problem-block">
          <span>{evidence.requestLabel}</span>
          <code>{evidence.requestValue}</code>
        </div>
      ) : null}
      {evidence.output ? (
        <div className="session-trace-problem-block" data-tone="error">
          <span>Failure output</span>
          <p>{evidence.output}</p>
        </div>
      ) : null}
      {evidence.artifactPath ? (
        <div className="session-trace-problem-artifact">
          <span>Full output</span>
          <strong>{evidence.artifactLabel ?? "stored artifact"}</strong>
          {onOpenArtifact ? (
            <button
              type="button"
              className="ghost-action"
              onClick={() => onOpenArtifact(evidence.artifactPath ?? "")}
            >
              Open full output
            </button>
          ) : null}
        </div>
      ) : null}
      {evidence.related.length > 0 ? (
        <div
          className="session-trace-related"
          data-testid="session-trace-related"
        >
          <span>Related evidence</span>
          <div>
            {evidence.related.map((item) => (
              <small
                key={`${item.kind}:${item.label}:${item.detail}`}
                data-kind={item.kind}
              >
                <strong>{item.label}</strong>
                <em>{item.detail}</em>
              </small>
            ))}
          </div>
        </div>
      ) : null}
    </div>
  );
}

function traceFailureEvidence(
  issue: SessionTraceView["toolIssues"][number],
  events: readonly NormalizedEvent[],
): TraceFailureEvidence | undefined {
  const request = events.find(
    (event) =>
      event.type === EventType.ToolRequest &&
      readEventString(event, "call_id") === issue.id,
  );
  const result = events.find(
    (event) =>
      event.type === EventType.ToolResult &&
      readEventString(event, "call_id") === issue.id,
  );
  if (!request && !result) return undefined;
  const requestPayload = request?.data as ToolRequestPayload | undefined;
  const resultPayload = result?.data as ToolResultPayload | undefined;
  const requestSummary = traceRequestSummary(issue.tool, requestPayload?.args);
  const output = traceFailureOutput(resultPayload);
  const meta = compactTraceParts([
    resultPayload?.duration_ms != null
      ? formatTraceDuration(resultPayload.duration_ms)
      : undefined,
    resultPayload?.exit_code != null
      ? `exit ${resultPayload.exit_code}`
      : undefined,
    resultPayload?.result_truncated ? "truncated" : undefined,
    resultPayload?.context_omitted_bytes ? "context trimmed" : undefined,
  ]);
  return {
    tool: issue.tool,
    requestLabel: requestSummary.label,
    requestValue: requestSummary.value,
    output,
    artifactPath: resultPayload?.result_artifact_path,
    artifactLabel: resultPayload?.result_artifact_path
      ? traceArtifactLabel(resultPayload.result_artifact_path)
      : undefined,
    meta,
    related: traceRelatedEvidence(
      issue,
      events,
      request?.turnId ?? result?.turnId,
    ),
  };
}

function traceRelatedEvidence(
  issue: SessionTraceView["toolIssues"][number],
  events: readonly NormalizedEvent[],
  turnId: string | undefined,
): TraceRelatedEvidence[] {
  if (!turnId) return [];
  const related: TraceRelatedEvidence[] = [];
  for (const event of events) {
    if (event.turnId !== turnId) continue;
    const callId = readEventString(event, "call_id");
    if (!callId || callId === issue.id) continue;
    const item = traceRelatedEvidenceItem(event);
    if (!item) continue;
    pushRelatedEvidence(related, item);
    if (related.length >= 4) break;
  }
  return related;
}

function traceRelatedEvidenceItem(
  event: NormalizedEvent,
): TraceRelatedEvidence | undefined {
  if (event.type === EventType.ToolResult) {
    const source = describeSourceAccess(eventResultText(event));
    if (source) {
      return {
        kind: "source",
        label: readableURLHost(source.accessedUrl),
        detail: compactTraceParts([
          sourceEvidenceLabel(source),
          source.ref ? `ref ${source.ref}` : undefined,
          source.httpStatus ? `http ${source.httpStatus}` : undefined,
        ]).join(" · "),
      };
    }
    const artifactPath = readEventString(event, "result_artifact_path");
    if (artifactPath) {
      return {
        kind: "artifact",
        label: traceArtifactLabel(artifactPath),
        detail: "stored output",
      };
    }
    return undefined;
  }
  if (event.type !== EventType.ToolRequest) return undefined;
  const tool = readEventString(event, "tool") ?? "tool";
  const args = readEventObject(event, "args");
  if (tool === "shell") {
    const command =
      readRecordString(args, "command") ?? readRecordString(args, "cmd");
    return command
      ? { kind: "command", label: compactCommand(command), detail: "shell" }
      : undefined;
  }
  if (
    tool === "read_file" ||
    tool === "write_file" ||
    tool === "edit_file" ||
    tool === "list_files"
  ) {
    const path =
      readRecordString(args, "path") ?? readRecordString(args, "cwd");
    return path
      ? { kind: "file", label: compactPath(path), detail: tool }
      : undefined;
  }
  return undefined;
}

function pushRelatedEvidence(
  items: TraceRelatedEvidence[],
  item: TraceRelatedEvidence,
) {
  if (
    items.some(
      (current) =>
        current.kind === item.kind &&
        current.label === item.label &&
        current.detail === item.detail,
    )
  )
    return;
  items.push(item);
}

function traceRequestSummary(
  tool: string,
  args: Record<string, unknown> | undefined,
): { label: string; value?: string } {
  if (!args) return { label: "Request" };
  const command =
    readRecordString(args, "command") ?? readRecordString(args, "cmd");
  if (command)
    return { label: "Command", value: compactTraceText(command, 220) };
  const path =
    readRecordString(args, "path") ??
    readRecordString(args, "file") ??
    readRecordString(args, "target_path");
  if (path) return { label: "Path", value: compactTraceText(path, 180) };
  const url =
    readRecordString(args, "url") ?? readRecordString(args, "requested_url");
  if (url) return { label: "URL", value: compactTraceText(url, 220) };
  const ref = readRecordString(args, "ref");
  if (ref) return { label: "Ref", value: compactTraceText(ref, 120) };
  const json = JSON.stringify(args);
  return {
    label: tool === "shell" ? "Command" : "Args",
    value: compactTraceText(json, 220),
  };
}

function traceFailureOutput(
  result: ToolResultPayload | undefined,
): string | undefined {
  const raw = [
    result?.result_summary,
    result?.result && result.result !== result.result_summary
      ? result.result
      : undefined,
  ]
    .filter(
      (value): value is string =>
        typeof value === "string" && value.trim().length > 0,
    )
    .join("\n");
  if (!raw) return undefined;
  const cleaned = raw
    .replace(
      /(?:^|\n)Next:\s*[\s\S]*?(?=\nFailure:|\n[A-Z][A-Za-z _-]{0,40}:|$)/gi,
      "\n",
    )
    .split("\n")
    .map((line) => line.trim())
    .filter(
      (line) =>
        line && !/^Failure:/i.test(line) && !/^\[?exit\s+\d+\]?$/i.test(line),
    )
    .join("\n");
  return compactTraceText(cleaned || raw, 360);
}

function traceArtifactLabel(path: string): string {
  const parts = path.replace(/\\/g, "/").split("/").filter(Boolean);
  return parts.at(-1) ?? path;
}

function compactTraceText(
  value: string | undefined,
  limit: number,
): string | undefined {
  const compacted = value?.replace(/\s+/g, " ").trim();
  if (!compacted) return undefined;
  return compacted.length > limit
    ? `${compacted.slice(0, limit - 1).trimEnd()}...`
    : compacted;
}

function compactTraceParts(values: Array<string | undefined>): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const value of values) {
    const trimmed = value?.trim();
    if (!trimmed || seen.has(trimmed)) continue;
    seen.add(trimmed);
    out.push(trimmed);
  }
  return out;
}

function traceControlSummary(
  filter: TraceFilter,
  query: string,
  visibleCount: number,
  totalCount: number,
  _issue?: SessionTraceView["toolIssues"][number],
): string {
  if (query) return `${visibleCount} of ${totalCount} entries · ${query}`;
  if (filter !== "all")
    return `${visibleCount} of ${totalCount} entries · ${filterLabel(filter)}`;
  return `${totalCount} trace entries`;
}

function traceToolIssueGroups(
  issues: SessionTraceView["toolIssues"],
): TraceToolIssueGroup[] {
  const groups = new Map<string, TraceToolIssueGroup>();
  for (const issue of issues) {
    const current = groups.get(issue.tool) ?? {
      tool: issue.tool,
      count: 0,
      issues: [],
    };
    current.count += issue.occurrences;
    current.issues.push(issue);
    groups.set(issue.tool, current);
  }
  return [...groups.values()].sort(
    (a, b) => b.count - a.count || a.tool.localeCompare(b.tool),
  );
}

function traceToolRequestIssueGroups(
  issues: SessionTraceView["toolIssues"],
): TraceToolRequestIssueGroup[] {
  const groups = new Map<number, TraceToolRequestIssueGroup>();
  for (const issue of issues) {
    const current = groups.get(issue.turnNumber) ?? {
      turnNumber: issue.turnNumber,
      count: 0,
      issues: [],
    };
    current.count += issue.occurrences;
    current.issues.push(issue);
    groups.set(issue.turnNumber, current);
  }
  return [...groups.values()].sort((a, b) => a.turnNumber - b.turnNumber);
}

function traceIssueCauseLabel(
  issue: SessionTraceView["toolIssues"][number],
): string {
  const primaryBadges = issue.badges
    .filter((badge) => !/^exit\s+\d+$/i.test(badge))
    .slice(0, 2);
  const parts =
    primaryBadges.length > 0
      ? primaryBadges.map(traceIssueBadgeLabel)
      : issue.exitCode != null
        ? [`exit ${issue.exitCode}`]
        : ["failed action"];
  const base = parts.join(" · ");
  return issue.occurrences > 1 ? `${base} · ${issue.occurrences}x` : base;
}

function traceIssueDetailLabel(
  issue: SessionTraceView["toolIssues"][number],
): string {
  const causeTerms = new Set(issue.badges.map(normalizeTraceIssuePart));
  const parts = issue.detail
    .split(" · ")
    .map((part) => part.trim())
    .filter(Boolean)
    .filter((part) => !causeTerms.has(normalizeTraceIssuePart(part)));
  return parts.length > 0
    ? parts.map(traceIssueBadgeLabel).join(" · ")
    : traceIssueBadgeLabel(issue.detail);
}

function normalizeTraceIssuePart(value: string): string {
  return value
    .trim()
    .toLowerCase()
    .replace(/[_\s-]+/g, "_");
}

function traceIssueBadgeLabel(value: string): string {
  const trimmed = value.trim();
  const normalized = normalizeTraceIssuePart(trimmed);
  if (/^exit_\d+$/.test(normalized))
    return trimmed.replace(/^exit\s+/i, "Exit ");
  if (normalized === "invalid_args") return "Invalid request";
  if (normalized === "command_failed") return "Command failed";
  if (normalized === "test_failed") return "Test failed";
  if (normalized === "context_budget" || normalized === "loop_guard_no_budget")
    return "Context budget";
  if (normalized === "timeout") return "Timeout";
  if (normalized === "network" || normalized === "network_error")
    return "Network";
  if (normalized === "permission_denied") return "Permission denied";
  return trimmed.includes("_")
    ? sentenceCase(trimmed.replace(/_/g, " "))
    : trimmed;
}

function sentenceCase(value: string): string {
  const cleaned = value.trim();
  return cleaned
    ? `${cleaned.charAt(0).toUpperCase()}${cleaned.slice(1)}`
    : cleaned;
}

interface TraceHeaderDigest {
  primary: string;
  secondary: string;
}

function traceHeaderDigest(trace: SessionTraceView): TraceHeaderDigest {
  if (trace.eventCount === 0) {
    return {
      primary: "No persisted trace",
      secondary: "Runtime evidence will appear after this chat records events.",
    };
  }

  if (trace.toolIssueCount > 0) {
    const firstIssue = trace.toolIssues[0];
    const remaining = Math.max(
      0,
      trace.toolIssueCount - (firstIssue?.occurrences ?? 1),
    );
    const failureCount = `${trace.toolIssueCount} failed tool ${trace.toolIssueCount === 1 ? "call" : "calls"}`;
    const cause = firstIssue
      ? `${traceIssueCauseLabel(firstIssue)} in ${firstIssue.tool} · Request ${firstIssue.turnNumber}`
      : "Tool failure detected";
    return {
      primary: failureCount,
      secondary: compactTraceHeaderText(
        remaining > 0 ? `${cause} · ${remaining} more` : cause,
      ),
    };
  }

  if ((trace.toolRequests.skipped ?? 0) > 0) {
    const skipped = trace.toolRequests.skipped ?? 0;
    return {
      primary: `${skipped} skipped tool ${skipped === 1 ? "request" : "requests"}`,
      secondary:
        "Some actions were not dispatched; inspect the event evidence before retrying.",
    };
  }

  if (trace.unknownCount > 0) {
    return {
      primary: `${trace.unknownCount} unclassified trace ${trace.unknownCount === 1 ? "event" : "events"}`,
      secondary:
        "Runtime emitted events the WebUI does not fully understand yet.",
    };
  }

  if (trace.latest) {
    const latest = [trace.latest.label, trace.latest.detail]
      .filter(Boolean)
      .join(" · ");
    return {
      primary: "No failed tool calls",
      secondary: compactTraceHeaderText(`Latest evidence: ${latest}`),
    };
  }

  return {
    primary: "Trace evidence ready",
    secondary:
      "Search or filter events when you need to inspect runtime behavior.",
  };
}

function compactTraceHeaderText(value: string): string {
  const compact = value.replace(/\s+/g, " ").trim();
  return compact.length > 132 ? `${compact.slice(0, 131)}...` : compact;
}

function traceToolRequestStatsLabel(
  stats: SessionTraceView["toolRequests"],
): string {
  if (stats.admitted == null && stats.skipped == null)
    return String(stats.total);
  return `${stats.total} · ${stats.admitted ?? 0} admitted · ${stats.skipped ?? 0} skipped`;
}

function filterTraceIssues(
  issues: SessionTraceView["toolIssues"],
  filter: TraceFilter,
  query: string,
): SessionTraceView["toolIssues"] {
  const narrowed = issues.filter((issue) =>
    issueMatchesTraceFilter(issue, filter),
  );
  const terms = traceIssueSearchTerms(query);
  if (terms.length === 0) return narrowed;
  return narrowed.filter((issue) => {
    const text = traceIssueSearchText(issue);
    return terms.every((term) => text.includes(term));
  });
}

function issueMatchesTraceFilter(
  issue: SessionTraceView["toolIssues"][number],
  filter: TraceFilter,
): boolean {
  if (filter === "commands") return issue.tool === "shell";
  if (filter === "files")
    return (
      issue.tool === "read_file" ||
      issue.tool === "write_file" ||
      issue.tool === "edit_file" ||
      issue.tool === "list_files"
    );
  if (filter === "memory")
    return issue.tool === "memory" || issue.tool === "session_search";
  if (filter === "loop") return issue.tool.startsWith("loop");
  return true;
}

function traceIssueSearchText(
  issue: SessionTraceView["toolIssues"][number],
): string {
  return [
    issue.id,
    issue.query,
    issue.requestQuery,
    `tool:${issue.tool}`,
    `request:${issue.turnNumber}`,
    issue.exitCode != null ? `exit:${issue.exitCode}` : undefined,
    "status:failed",
    issue.title,
    issue.tool,
    issue.detail,
    issue.next,
    ...issue.badges,
  ]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function traceIssueSearchTerms(query: string): string[] {
  return (query.toLowerCase().match(/"[^"]+"|\S+/g) ?? [])
    .map((term) => term.replace(/^"|"$/g, "").trim())
    .filter(Boolean);
}

function traceDynamicSearches(
  issues: SessionTraceView["toolIssues"],
): TraceSearchShortcut[] {
  const text = issues.map(traceIssueSearchText).join("\n");
  const shortcuts: TraceSearchShortcut[] = [];
  if (/permission denied|publickey/.test(text)) {
    shortcuts.push({
      label: "permission denied",
      query: "permission denied",
      filter: "commands",
    });
  }
  if (/load key|invalid format|bad permissions/.test(text)) {
    shortcuts.push({
      label: "invalid key",
      query: "invalid format",
      filter: "commands",
    });
  }
  if (/git@github\.com|github\.com/.test(text)) {
    shortcuts.push({
      label: "github",
      query: "github.com",
      filter: "commands",
    });
  }
  return shortcuts;
}

function traceSearchShortcuts(
  events: readonly NormalizedEvent[],
  trace: SessionTraceView,
): TraceSearchShortcut[] {
  const shortcuts: TraceSearchShortcut[] = [];
  const exitCodes = [
    ...new Set(
      trace.toolIssues
        .map((issue) => issue.exitCode)
        .filter(
          (exitCode): exitCode is number =>
            typeof exitCode === "number" && exitCode !== 0,
        ),
    ),
  ].sort((a, b) => a - b);

  if (trace.toolIssueCount > 0) {
    shortcuts.push({
      label: "failed tools",
      query: "status:failed",
      filter: "issues",
    });
    for (const exitCode of exitCodes.slice(0, 3)) {
      shortcuts.push({
        label: `exit:${exitCode}`,
        query: `exit:${exitCode}`,
        filter: "issues",
      });
    }
  }

  if (countFilter(events, "commands") > 0)
    shortcuts.push({ label: "shell", query: "tool:shell", filter: "commands" });
  if (countFilter(events, "repairs") > 0)
    shortcuts.push({
      label: "repaired args",
      query: "repaired",
      filter: "repairs",
    });
  if (countFilter(events, "truncated") > 0)
    shortcuts.push({
      label: "truncated output",
      query: "truncated",
      filter: "truncated",
    });
  if (countFilter(events, "artifacts") > 0)
    shortcuts.push({
      label: "stored output",
      query: "artifact:",
      filter: "artifacts",
    });
  if (countFilter(events, "files") > 0)
    shortcuts.push({ label: "file actions", query: "path:", filter: "files" });
  if (trace.unknownCount > 0)
    shortcuts.push({
      label: "unclassified",
      query: "unclassified",
      filter: "unclassified",
    });

  const seen = new Set(shortcuts.map((shortcut) => shortcut.label));
  for (const shortcut of traceDynamicSearches(trace.toolIssues)) {
    if (seen.has(shortcut.label)) continue;
    shortcuts.push(shortcut);
    seen.add(shortcut.label);
  }
  return shortcuts;
}

function traceEvidenceNavigatorItems(
  filters: readonly TraceFilterItem[],
  events: readonly NormalizedEvent[],
): TraceEvidenceNavigatorItem[] {
  const priority: TraceFilter[] = [
    "sources",
    "files",
    "commands",
    "artifacts",
    "repairs",
    "truncated",
    "unclassified",
  ];
  const byKey = new Map(filters.map((item) => [item.key, item]));
  return priority
    .map((key) => byKey.get(key))
    .filter((item): item is TraceFilterItem => Boolean(item && item.count > 0))
    .map((item) => ({
      key: item.key,
      label: item.label,
      count: item.count,
      detail: traceEvidenceNavigatorDetail(item.key, events),
      children: traceEvidenceNavigatorChildren(item.key, events),
    }));
}

function traceEvidenceNavigatorDetail(
  filter: TraceFilter,
  events: readonly NormalizedEvent[],
): string {
  if (filter === "sources") return sourceEvidenceNavigatorDetail(events);
  if (filter === "files") return pathEvidenceNavigatorDetail(events);
  if (filter === "commands") return commandEvidenceNavigatorDetail(events);
  if (filter === "artifacts") return artifactEvidenceNavigatorDetail(events);
  if (filter === "repairs") return repairEvidenceNavigatorDetail(events);
  if (filter === "truncated") return truncationEvidenceNavigatorDetail(events);
  if (filter === "unclassified")
    return unclassifiedEvidenceNavigatorDetail(events);
  return "matching trace entries";
}

function sourceEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const parts: string[] = [];
  for (const event of events) {
    if (event.type !== EventType.ToolResult) continue;
    const info = describeSourceAccess(eventResultText(event));
    if (!info) continue;
    pushUnique(parts, readableURLHost(info.accessedUrl));
    if (info.ref) pushUnique(parts, `ref ${info.ref}`);
    if (info.httpStatus) pushUnique(parts, `http ${info.httpStatus}`);
    if (info.contentType) pushUnique(parts, info.contentType);
    if (parts.length >= 4) break;
  }
  return parts.length > 0
    ? compactNavigatorDetail(parts)
    : "source URLs and provenance";
}

function pathEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const paths: string[] = [];
  for (const event of events) {
    if (event.type !== EventType.ToolRequest) continue;
    const tool = readEventString(event, "tool");
    if (
      tool !== "read_file" &&
      tool !== "write_file" &&
      tool !== "edit_file" &&
      tool !== "list_files"
    )
      continue;
    const args = readEventObject(event, "args");
    const path =
      readRecordString(args, "path") ?? readRecordString(args, "cwd");
    if (path) pushUnique(paths, compactPath(path));
  }
  return paths.length > 0 ? compactNavigatorDetail(paths) : "workspace paths";
}

function commandEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const commands: string[] = [];
  for (const event of events) {
    if (
      event.type !== EventType.ToolRequest ||
      readEventString(event, "tool") !== "shell"
    )
      continue;
    const args = readEventObject(event, "args");
    const command =
      readRecordString(args, "command") ??
      readEventString(event, "original_args_summary");
    if (command) pushUnique(commands, compactCommand(command));
  }
  return commands.length > 0
    ? compactNavigatorDetail(commands)
    : "shell commands";
}

function artifactEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const artifacts: string[] = [];
  for (const event of events) {
    const artifactPath = readEventString(event, "result_artifact_path");
    if (artifactPath) pushUnique(artifacts, compactPath(artifactPath));
  }
  return artifacts.length > 0
    ? compactNavigatorDetail(artifacts)
    : "stored tool output";
}

function repairEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const tools: string[] = [];
  for (const event of events) {
    if (!eventHasRepair(event)) continue;
    pushUnique(tools, readEventString(event, "tool") ?? "tool args");
  }
  return tools.length > 0
    ? compactNavigatorDetail(tools)
    : "repaired tool args";
}

function truncationEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const parts: string[] = [];
  for (const event of events) {
    if (!eventHasTruncation(event)) continue;
    if (positiveNumber(readEventNumber(event, "result_omitted_bytes")))
      pushUnique(parts, "result omitted");
    else if (positiveNumber(readEventNumber(event, "args_omitted_bytes")))
      pushUnique(parts, "args omitted");
    else if (readEventBoolean(event, "result_truncated"))
      pushUnique(parts, "result truncated");
    else if (readEventBoolean(event, "args_truncated"))
      pushUnique(parts, "args truncated");
  }
  return parts.length > 0 ? compactNavigatorDetail(parts) : "trimmed output";
}

function unclassifiedEvidenceNavigatorDetail(
  events: readonly NormalizedEvent[],
): string {
  const types: string[] = [];
  for (const event of events) {
    if (!event.known) pushUnique(types, event.type);
  }
  return types.length > 0
    ? compactNavigatorDetail(types)
    : "unknown runtime events";
}

function traceEvidenceNavigatorChildren(
  filter: TraceFilter,
  events: readonly NormalizedEvent[],
): TraceEvidenceNavigatorChild[] {
  if (filter === "sources") return sourceEvidenceNavigatorChildren(events);
  if (filter === "files") return pathEvidenceNavigatorChildren(events);
  if (filter === "commands") return commandEvidenceNavigatorChildren(events);
  return [];
}

function sourceEvidenceNavigatorChildren(
  events: readonly NormalizedEvent[],
): TraceEvidenceNavigatorChild[] {
  const children: TraceEvidenceNavigatorChild[] = [];
  const seen = new Set<string>();
  for (const event of events) {
    if (event.type !== EventType.ToolResult) continue;
    const info = describeSourceAccess(eventResultText(event));
    if (!info) continue;
    const host = readableURLHost(info.accessedUrl);
    const query = info.ref ? `ref ${info.ref}` : host;
    if (seen.has(query)) continue;
    seen.add(query);
    children.push({
      label: host,
      detail: compactNavigatorDetail(
        [
          info.ref ? `ref ${info.ref}` : undefined,
          info.httpStatus ? `http ${info.httpStatus}` : undefined,
          info.contentType,
        ].filter((part): part is string => Boolean(part)),
      ),
      query,
    });
    if (children.length >= 4) break;
  }
  return children;
}

function pathEvidenceNavigatorChildren(
  events: readonly NormalizedEvent[],
): TraceEvidenceNavigatorChild[] {
  const children: TraceEvidenceNavigatorChild[] = [];
  const seen = new Set<string>();
  for (const event of events) {
    if (event.type !== EventType.ToolRequest) continue;
    const tool = readEventString(event, "tool");
    if (
      tool !== "read_file" &&
      tool !== "write_file" &&
      tool !== "edit_file" &&
      tool !== "list_files"
    )
      continue;
    const args = readEventObject(event, "args");
    const path =
      readRecordString(args, "path") ?? readRecordString(args, "cwd");
    if (!path || seen.has(path)) continue;
    seen.add(path);
    children.push({
      label: compactPath(path),
      detail: tool,
      query: `path:${path}`,
    });
    if (children.length >= 4) break;
  }
  return children;
}

function commandEvidenceNavigatorChildren(
  events: readonly NormalizedEvent[],
): TraceEvidenceNavigatorChild[] {
  const children: TraceEvidenceNavigatorChild[] = [];
  const seen = new Set<string>();
  for (const event of events) {
    if (
      event.type !== EventType.ToolRequest ||
      readEventString(event, "tool") !== "shell"
    )
      continue;
    const args = readEventObject(event, "args");
    const command =
      readRecordString(args, "command") ??
      readEventString(event, "original_args_summary");
    if (!command || seen.has(command)) continue;
    seen.add(command);
    children.push({
      label: compactCommand(command),
      detail: "shell",
      query: command,
    });
    if (children.length >= 4) break;
  }
  return children;
}

function compactNavigatorDetail(parts: readonly string[]): string {
  const value = parts.slice(0, 4).join(" · ");
  return value.length > 88 ? `${value.slice(0, 87)}...` : value;
}

function pushUnique(values: string[], value: string | undefined) {
  const next = value?.trim();
  if (!next || values.includes(next)) return;
  values.push(next);
}

function readableURLHost(value: string): string {
  try {
    return new URL(value).hostname.replace(/^www\./, "");
  } catch {
    return value.replace(/^https?:\/\//, "").split("/")[0] || value;
  }
}

function compactPath(value: string): string {
  const normalized = value.replace(/\\/g, "/");
  const parts = normalized.split("/").filter(Boolean);
  if (parts.length <= 2) return normalized;
  return `${parts[parts.length - 2]}/${parts[parts.length - 1]}`;
}

function compactCommand(value: string): string {
  const compact = value.replace(/\s+/g, " ").trim();
  return compact.length > 52 ? `${compact.slice(0, 51)}...` : compact;
}

function eventResultText(event: NormalizedEvent): string | undefined {
  return (
    readEventString(event, "result") ?? readEventString(event, "result_summary")
  );
}

function TraceScopeSummaryView({
  summary,
  trace,
}: {
  summary: TraceSelectionSummary;
  trace: SessionTraceView;
}) {
  const actionStats =
    trace.toolRequests.total > 0
      ? traceToolRequestStatsLabel(trace.toolRequests)
      : undefined;

  return (
    <div
      className="session-trace-selection"
      data-testid="session-trace-selection"
    >
      <TraceSelectionMetric label="Span" value={summary.eventSpan} />
      <TraceSelectionMetric label="Requests" value={summary.requestSpan} />
      <TraceSelectionMetric
        label="Failures"
        value={String(summary.failedActions)}
      />
      <TraceSelectionMetric
        label="Tools"
        value={
          summary.toolCount
            ? `${summary.toolCount} · ${summary.topTools.join(", ")}`
            : "0"
        }
        kind="tools"
      />
      {actionStats ? (
        <TraceSelectionMetric
          label="Actions"
          value={actionStats}
          kind="actions"
        />
      ) : null}
      {summary.truncatedCount > 0 ? (
        <TraceSelectionMetric
          label="Truncated"
          value={String(summary.truncatedCount)}
        />
      ) : null}
      {summary.repairCount > 0 ? (
        <TraceSelectionMetric
          label="Repairs"
          value={String(summary.repairCount)}
        />
      ) : null}
      {trace.unknownCount > 0 ? (
        <TraceSelectionMetric
          label="Unclassified"
          value={String(trace.unknownCount)}
        />
      ) : null}
      {trace.schemaVersion ? (
        <TraceSelectionMetric
          label="Schema"
          value={`v${trace.schemaVersion}`}
        />
      ) : null}
    </div>
  );
}

function TraceSelectionMetric({
  label,
  value,
  kind,
}: {
  label: string;
  value: string;
  kind?: string;
}) {
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

function traceSelectionSummary(
  allEvents: readonly NormalizedEvent[],
  visibleEvents: readonly NormalizedEvent[],
): TraceSelectionSummary {
  const requestLabels = requestLabelsByTurnId(allEvents);
  const toolCounts = new Map<string, number>();
  const callTools = toolNamesByCallId(allEvents);
  const requestSet = new Set<string>();
  let failedActions = 0;
  let repairCount = 0;
  let truncatedCount = 0;
  let firstId: number | undefined;
  let lastId: number | undefined;

  for (const event of visibleEvents) {
    if (typeof event.id === "number") {
      firstId = firstId == null ? event.id : Math.min(firstId, event.id);
      lastId = lastId == null ? event.id : Math.max(lastId, event.id);
    }
    if (event.turnId) requestSet.add(event.turnId);
    if (eventHasRepair(event)) repairCount += 1;
    if (eventHasTruncation(event)) truncatedCount += 1;
    if (
      event.type === EventType.ToolRequest ||
      event.type === EventType.ToolResult
    ) {
      const tool = toolName(event, callTools);
      if (tool) toolCounts.set(tool, (toolCounts.get(tool) ?? 0) + 1);
    }
    if (event.type === EventType.ToolResult) {
      const data = event.data as ToolResultPayload;
      if (
        (data.exit_code ?? 0) !== 0 ||
        data.failure_kind ||
        data.failure_kinds?.length
      )
        failedActions += 1;
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
    eventSpan:
      firstId == null || lastId == null
        ? "none"
        : firstId === lastId
          ? `#${firstId}`
          : `#${firstId}-#${lastId}`,
    requestSpan:
      requestNumbers.length === 0
        ? "none"
        : requestNumbers.length === 1
          ? `Request ${requestNumbers[0]}`
          : `Request ${requestNumbers[0]}-${requestNumbers[requestNumbers.length - 1]} · ${requestNumbers.length}`,
    failedActions,
    repairCount,
    truncatedCount,
    toolCount: toolCounts.size,
    topTools,
  };
}

function requestLabelsByTurnId(
  events: readonly NormalizedEvent[],
): Map<string, number> {
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

function traceFilters(
  events: readonly NormalizedEvent[],
  toolIssueCount: number,
): TraceFilterItem[] {
  return compactFilters([
    { key: "all", label: "All", count: events.length },
    { key: "issues", label: "Failures", count: toolIssueCount },
    {
      key: "commands",
      label: "Commands",
      count: countFilter(events, "commands"),
    },
    { key: "files", label: "Files", count: countFilter(events, "files") },
    { key: "sources", label: "Sources", count: countFilter(events, "sources") },
    {
      key: "artifacts",
      label: "Artifacts",
      count: countFilter(events, "artifacts"),
    },
    { key: "repairs", label: "Repairs", count: countFilter(events, "repairs") },
    {
      key: "truncated",
      label: "Truncated",
      count: countFilter(events, "truncated"),
    },
    {
      key: "unclassified",
      label: "Unclassified",
      count: countFilter(events, "unclassified"),
    },
  ]);
}

function compactFilters(filters: TraceFilterItem[]): TraceFilterItem[] {
  return filters.filter((item) => item.key === "all" || item.count > 0);
}

function filterEventsByTraceFilter(
  events: readonly NormalizedEvent[],
  filter: TraceFilter,
): NormalizedEvent[] {
  if (filter === "all") return [...events];
  if (filter === "issues") return filterToolIssueEvents(events);
  const callTools = toolNamesByCallId(events);
  return events.filter((event) => eventMatchesFilter(event, filter, callTools));
}

function countFilter(
  events: readonly NormalizedEvent[],
  filter: TraceFilter,
): number {
  return filterEventsByTraceFilter(events, filter).length;
}

function filterLabel(filter: TraceFilter): string {
  if (filter === "issues") return "Issues";
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

function resultCountLabel(
  count: number,
  narrowed: boolean,
  total: number,
): string {
  if (narrowed) return `${count === 1 ? "entry" : "entries"} of ${total}`;
  return count === 1 ? "trace entry loaded" : "trace entries loaded";
}

function eventMatchesFilter(
  event: NormalizedEvent,
  filter: TraceFilter,
  callTools: Map<string, string>,
): boolean {
  if (filter === "actions")
    return (
      event.type === EventType.ToolRequest ||
      event.type === EventType.ToolResult
    );
  if (filter === "context")
    return (
      event.type === EventType.ContextInjected ||
      event.type === EventType.ContextCompacted ||
      event.type === EventType.Usage
    );
  if (filter === "loop") return event.type.startsWith("loop.");
  if (filter === "unclassified") return !event.known;
  if (filter === "artifacts") return eventHasArtifact(event);
  if (filter === "sources") return eventHasSourceEvidence(event, callTools);
  if (filter === "repairs") return eventHasRepair(event);
  if (filter === "truncated") return eventHasTruncation(event);
  if (
    event.type !== EventType.ToolRequest &&
    event.type !== EventType.ToolResult
  )
    return false;
  const tool = toolName(event, callTools);
  if (filter === "commands") return tool === "shell";
  if (filter === "files")
    return (
      tool === "read_file" ||
      tool === "write_file" ||
      tool === "edit_file" ||
      tool === "list_files"
    );
  if (filter === "memory")
    return tool === "memory" || tool === "session_search";
  return false;
}

function eventHasArtifact(event: NormalizedEvent): boolean {
  if (!event.data || typeof event.data !== "object") return false;
  const artifactPath = (event.data as { result_artifact_path?: unknown })
    .result_artifact_path;
  return typeof artifactPath === "string" && artifactPath.trim().length > 0;
}

function eventHasRepair(event: NormalizedEvent): boolean {
  if (
    event.type !== EventType.ToolRequest ||
    !event.data ||
    typeof event.data !== "object"
  )
    return false;
  const data = event.data as Record<string, unknown>;
  return (
    data.args_repaired === true ||
    data.canonicalized === true ||
    (Array.isArray(data.repair_notes) && data.repair_notes.length > 0)
  );
}

function eventHasTruncation(event: NormalizedEvent): boolean {
  if (!event.data || typeof event.data !== "object") return false;
  const data = event.data as Record<string, unknown>;
  return (
    data.args_truncated === true ||
    data.result_truncated === true ||
    positiveNumber(data.args_omitted_bytes) ||
    positiveNumber(data.result_omitted_bytes)
  );
}

function positiveNumber(value: unknown): boolean {
  return typeof value === "number" && value > 0;
}

function eventHasSourceEvidence(
  event: NormalizedEvent,
  callTools: Map<string, string>,
): boolean {
  if (
    event.type !== EventType.ToolRequest &&
    event.type !== EventType.ToolResult
  )
    return false;
  const tool = toolName(event, callTools) ?? "";
  if (/^(web_|browser_)/.test(tool)) return true;
  if (!event.data || typeof event.data !== "object") return false;
  const text = [
    (event.data as { result_summary?: unknown }).result_summary,
    (event.data as { result?: unknown }).result,
  ]
    .filter((value): value is string => typeof value === "string")
    .join("\n");
  return /SourceAccess:|BROWSER_NETWORK|browser_network_|web_fetch/i.test(text);
}

function filterToolIssueEvents(
  events: readonly NormalizedEvent[],
): NormalizedEvent[] {
  const failedCallIds = new Set<string>();
  for (const event of events) {
    if (event.type !== EventType.ToolResult) continue;
    const data = event.data as ToolResultPayload;
    if (
      (data.exit_code ?? 0) !== 0 ||
      data.failure_kind ||
      data.failure_kinds?.length
    )
      failedCallIds.add(data.call_id);
  }
  return events.filter((event) => {
    if (
      event.type === EventType.ToolRequest ||
      event.type === EventType.ToolResult
    ) {
      const data = event.data as { call_id?: unknown };
      const callId = typeof data.call_id === "string" ? data.call_id : "";
      return failedCallIds.has(callId);
    }
    return false;
  });
}

function toolNamesByCallId(
  events: readonly NormalizedEvent[],
): Map<string, string> {
  const out = new Map<string, string>();
  for (const event of events) {
    if (
      event.type !== EventType.ToolRequest ||
      !event.data ||
      typeof event.data !== "object"
    )
      continue;
    const callID = (event.data as { call_id?: unknown }).call_id;
    const tool = (event.data as { tool?: unknown }).tool;
    if (typeof callID === "string" && typeof tool === "string")
      out.set(callID, tool);
  }
  return out;
}

function toolName(
  event: NormalizedEvent,
  callTools: Map<string, string>,
): string | undefined {
  if (!event.data || typeof event.data !== "object") return undefined;
  const value = (event.data as { tool?: unknown }).tool;
  if (typeof value === "string") return value;
  if (event.type !== EventType.ToolResult) return undefined;
  const callID = (event.data as { call_id?: unknown }).call_id;
  if (typeof callID !== "string") return undefined;
  return callTools.get(callID);
}

function readEventString(
  event: NormalizedEvent,
  key: string,
): string | undefined {
  if (!event.data || typeof event.data !== "object") return undefined;
  return readRecordString(event.data as Record<string, unknown>, key);
}

function readEventNumber(
  event: NormalizedEvent,
  key: string,
): number | undefined {
  if (!event.data || typeof event.data !== "object") return undefined;
  const value = (event.data as Record<string, unknown>)[key];
  return typeof value === "number" && Number.isFinite(value)
    ? value
    : undefined;
}

function readEventBoolean(event: NormalizedEvent, key: string): boolean {
  if (!event.data || typeof event.data !== "object") return false;
  return (event.data as Record<string, unknown>)[key] === true;
}

function readEventObject(
  event: NormalizedEvent,
  key: string,
): Record<string, unknown> | undefined {
  if (!event.data || typeof event.data !== "object") return undefined;
  const value = (event.data as Record<string, unknown>)[key];
  return value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : undefined;
}

function readRecordString(
  record: Record<string, unknown> | undefined,
  key: string,
): string | undefined {
  const value = record?.[key];
  return typeof value === "string" && value.trim() ? value : undefined;
}
