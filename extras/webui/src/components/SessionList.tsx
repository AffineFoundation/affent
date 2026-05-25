import { useEffect, useMemo, useState } from "react";
import type { SessionSummary } from "../api/sessions";
import type { SessionState } from "../store/sessionState";
import {
  buildSessionRows,
  countSessionsByFilter,
  filterSessionRows,
  mergeCurrentSessionRow,
  type SessionListFilter,
} from "../view/sessionList";

const filters: { mode: SessionListFilter; label: string }[] = [
  { mode: "all", label: "All" },
  { mode: "active", label: "Running" },
  { mode: "saved", label: "Saved" },
  { mode: "artifacts", label: "Files" },
  { mode: "memory", label: "Memory" },
];

export function SessionList({
  sessions,
  selectedId,
  currentSession,
  demoActive,
  onSelect,
  onNew,
}: {
  sessions: readonly SessionSummary[];
  selectedId?: string;
  currentSession?: SessionState;
  demoActive: boolean;
  onSelect: (id: string) => void;
  onNew: () => void;
}) {
  const [filter, setFilter] = useState<SessionListFilter>("all");
  const [query, setQuery] = useState("");
  const [toolsOpen, setToolsOpen] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(!selectedId);
  const rows = useMemo(
    () => mergeCurrentSessionRow(buildSessionRows(sessions), selectedId, currentSession),
    [currentSession, selectedId, sessions],
  );
  const counts = useMemo(() => countSessionsByFilter(rows), [rows]);
  const visibleRows = useMemo(() => filterSessionRows(rows, filter, query), [filter, query, rows]);
  const toolsActive = filter !== "all" || query.trim() !== "";
  const toolsExpanded = toolsOpen || toolsActive;
  const compact = demoActive || rows.length <= 1;
  const showTools = !demoActive && rows.length > 1;
  const selectedRow = rows.find((row) => row.id === selectedId);

  useEffect(() => {
    setMobileOpen(!selectedId);
  }, [selectedId]);

  function reset() {
    setFilter("all");
    setQuery("");
    setToolsOpen(false);
  }

  return (
    <aside
      className="session-panel"
      aria-label="Chats"
      data-compact={compact}
      data-mobile-open={mobileOpen ? "true" : "false"}
      data-has-selection={selectedId ? "true" : "false"}
    >
      <div className="panel-head">
        <div>
          <h2>Chats</h2>
          <span>{demoActive ? "Read-only replay" : `${rows.length} total · ${counts.active} running`}</span>
        </div>
        {!demoActive ? (
          <button
            type="button"
            className="new-chat-action"
            title="Start a new chat"
            onClick={onNew}
          >
            <span aria-hidden="true">+</span>
            New
          </button>
        ) : null}
      </div>
      {!demoActive && rows.length > 0 ? (
        <button
          type="button"
          className="mobile-session-toggle"
          aria-label={mobileOpen ? "Hide chat list" : "Switch chats"}
          aria-expanded={mobileOpen}
          onClick={() => setMobileOpen((open) => !open)}
        >
          <span>
            <b>{selectedRow ? "Current chat" : "Chats"}</b>
            <small>{selectedRow?.title ?? `${rows.length} saved chats`}</small>
          </span>
          <strong>{mobileOpen ? "Hide" : "Switch"}</strong>
        </button>
      ) : null}
      {showTools ? (
        <div className="session-tools" data-expanded={toolsExpanded ? "true" : "false"} data-testid="session-tools">
          <label className="session-search">
            <span>Search chats</span>
            <input
              value={query}
              onChange={(event) => setQuery(event.target.value)}
              onFocus={() => setToolsOpen(true)}
              placeholder="Search chats"
              data-testid="session-search"
            />
            {toolsExpanded ? <small>{visibleRows.length}/{rows.length}</small> : null}
          </label>
          {toolsExpanded ? (
            <div className="session-filter" role="group" aria-label="Session filter">
              {filters.map(({ mode, label }) => (
                <button
                  key={mode}
                  type="button"
                  aria-pressed={filter === mode}
                  onClick={() => setFilter(mode)}
                >
                  <span>{label}</span>
                  <span>{counts[mode]}</span>
                </button>
              ))}
            </div>
          ) : null}
        </div>
      ) : null}
      <div className="session-list" data-testid="session-list">
        {demoActive ? (
          <button type="button" className="session-row is-selected" data-testid="demo-session-row">
            <span className="session-row-top">
              <span className="pulse-dot" data-status="completed" aria-hidden="true" />
              <span className="session-title">Offline preview</span>
            </span>
            <span className="session-meta">Read-only replay</span>
          </button>
        ) : null}
        {!demoActive && sessions.length === 0 ? <div className="session-empty">No chats yet. Type a request to start.</div> : null}
        {!demoActive && sessions.length > 0 && visibleRows.length === 0 ? (
          <div className="session-empty filtered" data-testid="session-filter-empty">
            <span>No matching chats</span>
            <button type="button" className="session-reset" onClick={reset}>
              Reset
            </button>
          </div>
        ) : null}
        {!demoActive
          ? visibleRows.map((row) => (
              <button
                key={row.id}
                type="button"
                className={`session-row${selectedId === row.id ? " is-selected" : ""}`}
                data-tone={row.tone}
                onClick={() => onSelect(row.id)}
              >
                <span className="session-row-top">
                  <span className="pulse-dot" data-status={dotStatus(row.tone)} aria-hidden="true" />
                  <span className="session-title" title={row.id}>
                    {row.title}
                  </span>
                  {shouldShowRowStatus(row.status) ? <span className="session-state">{row.status}</span> : null}
                </span>
                {row.detail ? <span className="session-detail">{row.detail}</span> : null}
                <span className="session-meta">
                  {row.meta.map((part) => (
                    <span key={part}>{part}</span>
                  ))}
                </span>
                <span className="session-chips">
                  {row.chips.map((chip) => (
                    <span key={chip}>{chip}</span>
                  ))}
                </span>
              </button>
            ))
          : null}
      </div>
    </aside>
  );
}

function dotStatus(tone: string): string {
  if (tone === "running") return "running";
  if (tone === "error") return "error";
  if (tone === "warning") return "max_turns";
  return "completed";
}

function shouldShowRowStatus(status: string): boolean {
  return status !== "Saved";
}
