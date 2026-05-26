import { useEffect, useMemo, useRef, useState } from "react";
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
  { mode: "plan", label: "Plan" },
  { mode: "evidence", label: "Evidence" },
];

export function SessionList({
  sessions,
  selectedId,
  currentSession,
  pendingTask,
  demoActive,
  onSelect,
  onNew,
  onDelete,
  deletingId,
  onCollapse,
}: {
  sessions: readonly SessionSummary[];
  selectedId?: string;
  currentSession?: SessionState;
  pendingTask?: string;
  demoActive: boolean;
  onSelect: (id: string) => void;
  onNew: () => void;
  onDelete?: (id: string) => void | Promise<void>;
  deletingId?: string;
  onCollapse?: () => void;
}) {
  const [filter, setFilter] = useState<SessionListFilter>("all");
  const [query, setQuery] = useState("");
  const [toolsOpen, setToolsOpen] = useState(false);
  const [mobileOpen, setMobileOpen] = useState(false);
  const [confirmDeleteId, setConfirmDeleteId] = useState<string | undefined>();
  const searchInputRef = useRef<HTMLInputElement | null>(null);
  const rows = useMemo(
    () => mergeCurrentSessionRow(buildSessionRows(sessions), selectedId, currentSession, pendingTask),
    [currentSession, pendingTask, selectedId, sessions],
  );
  const counts = useMemo(() => countSessionsByFilter(rows), [rows]);
  const visibleRows = useMemo(() => filterSessionRows(rows, filter, query), [filter, query, rows]);
  const toolsActive = filter !== "all" || query.trim() !== "";
  const toolsExpanded = toolsOpen || toolsActive;
  const compact = demoActive || rows.length <= 1;
  const showTools = !demoActive && rows.length > 1;
  useEffect(() => {
    setMobileOpen(false);
  }, [selectedId]);

  useEffect(() => {
    if (!confirmDeleteId || rows.some((row) => row.id === confirmDeleteId)) return;
    setConfirmDeleteId(undefined);
  }, [confirmDeleteId, rows]);

  useEffect(() => {
    if (toolsExpanded) searchInputRef.current?.focus({ preventScroll: true });
  }, [toolsExpanded]);

  function reset() {
    setFilter("all");
    setQuery("");
    setToolsOpen(false);
  }

  return (
    <>
      {!demoActive && rows.length > 0 ? (
        <button
          type="button"
          className="mobile-session-launcher"
          aria-label={mobileOpen ? "Close chats" : "Open chats"}
          aria-expanded={mobileOpen}
          onClick={() => setMobileOpen((open) => !open)}
        >
          <span aria-hidden="true">☰</span>
          <b>{rows.length}</b>
        </button>
      ) : null}
      {mobileOpen ? (
        <button
          type="button"
          className="mobile-session-backdrop"
          aria-label="Close chats"
          onClick={() => setMobileOpen(false)}
        />
      ) : null}
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
          <span>{demoActive ? "Read-only replay" : chatListSummary(rows.length, counts.active)}</span>
        </div>
        {onCollapse ? (
          <button
            type="button"
            className="session-collapse-action"
            aria-label="Hide chats"
            onClick={onCollapse}
          >
            Hide
          </button>
        ) : null}
        <button
          type="button"
          className="mobile-session-close"
          aria-label="Close chats"
          onClick={() => setMobileOpen(false)}
        >
          Close
        </button>
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
      {showTools ? (
        <div className="session-tools" data-expanded={toolsExpanded ? "true" : "false"} data-testid="session-tools">
          {toolsExpanded ? (
            <label className="session-search">
              <span>Search chats</span>
              <input
                ref={searchInputRef}
                value={query}
                onChange={(event) => setQuery(event.target.value)}
                placeholder="Search chats"
                data-testid="session-search"
              />
              <small>{visibleRows.length}/{rows.length}</small>
            </label>
          ) : (
            <button type="button" className="session-find-toggle" aria-label="Search chats" onClick={() => setToolsOpen(true)}>
              <span>Search chats</span>
              <small>Filters</small>
            </button>
          )}
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
          ? visibleRows.map((row) => {
              const isSelected = selectedId === row.id;
              const visibleChips = isSelected ? row.chips.filter((chip) => chip !== "files") : [];
              const previewId = row.preview ? `session-preview-${row.id}` : undefined;
              const confirmingDelete = confirmDeleteId === row.id;
              const deleting = deletingId === row.id;
              return (
                <div key={row.id} className="session-row-shell" data-confirming={confirmingDelete ? "true" : "false"}>
                  <button
                    type="button"
                    className={`session-row${selectedId === row.id ? " is-selected" : ""}`}
                    data-tone={row.tone}
                    data-preview={shouldPinRowPreview(row.tone, isSelected) ? "pinned" : "hover"}
                    aria-describedby={previewId}
                    onClick={() => onSelect(row.id)}
                  >
                    <span className="session-row-top">
                      <span className="pulse-dot" data-status={dotStatus(row.tone)} aria-hidden="true" />
                      <span className="session-title" title={row.title}>
                        {row.title}
                      </span>
                      {shouldShowRowStatus(row.status) ? <span className="session-state">{row.status}</span> : null}
                    </span>
                    {row.detail ? <span className="session-detail">{row.detail}</span> : null}
                    {row.preview ? (
                      <span id={previewId} className="session-preview" data-testid="session-preview">
                        {row.preview}
                      </span>
                    ) : null}
                    {row.stats ? (
                      <span className="session-stats" data-testid="session-stats">
                        {row.stats}
                      </span>
                    ) : null}
                    {visibleChips.length > 0 ? (
                      <span className="session-chips" data-testid="session-chips">
                        {visibleChips.map(sessionChipLabel).join(" · ")}
                      </span>
                    ) : null}
                    {row.meta.length > 0 ? (
                      <span className="session-meta">
                        {row.meta.map((part) => (
                          <span key={part}>{part}</span>
                        ))}
                      </span>
                    ) : null}
                  </button>
                  {onDelete && !confirmingDelete ? (
                    <button
                      type="button"
                      className="session-delete-action"
                      aria-label="Delete chat"
                      title={`Delete ${row.title}`}
                      disabled={deleting}
                      onClick={() => setConfirmDeleteId(row.id)}
                    >
                      {deleting ? "Deleting" : "Delete"}
                    </button>
                  ) : null}
                  {onDelete && confirmingDelete ? (
                    <div className="session-delete-confirm" role="group" aria-label="Confirm delete chat">
                      <span>Delete this chat?</span>
                      <button type="button" onClick={() => setConfirmDeleteId(undefined)} disabled={deleting}>
                        Cancel
                      </button>
                      <button
                        type="button"
                        className="danger"
                        disabled={deleting}
                        onClick={() => {
                          void onDelete(row.id);
                        }}
                      >
                        Confirm
                      </button>
                    </div>
                  ) : null}
                </div>
              );
            })
          : null}
      </div>
      </aside>
    </>
  );
}

function dotStatus(tone: string): string {
  if (tone === "running") return "running";
  if (tone === "error") return "error";
  if (tone === "warning") return "warning";
  return "completed";
}

function shouldShowRowStatus(status: string): boolean {
  return !["Saved", "Ephemeral"].includes(status);
}

function shouldPinRowPreview(tone: string, selected: boolean): boolean {
  return selected || tone === "running" || tone === "error" || tone === "warning";
}

function chatListSummary(total: number, running: number): string {
  if (total === 0) return "No chats";
  if (running === 0) return chatCountLabel(total);
  return `${chatCountLabel(total)} · ${running} running`;
}

function chatCountLabel(total: number): string {
  return `${total} chat${total === 1 ? "" : "s"}`;
}

function sessionChipLabel(chip: string): string {
  switch (chip) {
    case "memory":
      return "Memory";
    case "plan":
      return "Plan";
    case "skills":
      return "Skills";
    case "unclassified":
      return "Unclassified";
    case "artifacts":
      return "Artifacts";
    default:
      return chip === "files" ? "Files" : chip;
  }
}
