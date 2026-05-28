import { useMemo, useState, type FormEvent } from "react";
import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryAddRequest, SessionMemoryBucket, SessionMemoryRemoveRequest, SessionMemoryReplaceRequest, SessionMemoryResponse } from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  memoryActionLabel,
  memoryBucketMatchesQuery,
  memoryBucketMatchingEntries,
  memoryBucketDraft,
  memoryBucketEvidenceText,
  memoryBucketLabel,
  memoryBuckets,
  memoryBucketUsage,
  memoryUpdateDraft,
  memoryUpdateEvidenceText,
  memoryUpdateLocation,
  memoryUpdatePreview,
  manualMemoryDraft,
  totalMemoryChars,
} from "../view/sessionMemory";
import { CopyButton } from "./CopyButton";
import { panelErrorSummary } from "./panelErrorSummary";

export function SessionMemoryPanel({
  memory,
  latestUpdate,
  loading = false,
  error,
  noSession = false,
  defaultOpen = false,
  surface = false,
  onRefresh,
  onAddMemory,
  onRemoveMemory,
  onReplaceMemory,
  onUseAsDraft,
}: {
  memory?: SessionMemoryResponse;
  latestUpdate?: MemoryUpdateMeta;
  loading?: boolean;
  error?: string;
  noSession?: boolean;
  defaultOpen?: boolean;
  surface?: boolean;
  onRefresh?: () => Promise<void> | void;
  onAddMemory?: (request: SessionMemoryAddRequest) => Promise<SessionMemoryResponse> | SessionMemoryResponse;
  onRemoveMemory?: (request: SessionMemoryRemoveRequest) => Promise<SessionMemoryResponse> | SessionMemoryResponse;
  onReplaceMemory?: (request: SessionMemoryReplaceRequest) => Promise<SessionMemoryResponse> | SessionMemoryResponse;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [panelOpen, setPanelOpen] = useState(defaultOpen);
  const [memoryTarget, setMemoryTarget] = useState("memory");
  const [memoryTopic, setMemoryTopic] = useState("");
  const [memoryContent, setMemoryContent] = useState("");
  const [memorySaveState, setMemorySaveState] = useState<{ state: "idle" | "saving" | "saved" | "error"; message?: string }>({ state: "idle" });
  const [confirmRemoveKey, setConfirmRemoveKey] = useState<string | undefined>();
  const [editingEntry, setEditingEntry] = useState<{ key: string; value: string } | undefined>();
  const buckets = useMemo(() => memoryBuckets(memory), [memory]);
  const trimmedQuery = query.trim();
  const filtered = useMemo(() => {
    if (!trimmedQuery) return buckets;
    return buckets.filter((bucket) => memoryBucketMatchesQuery(bucket, trimmedQuery));
  }, [buckets, trimmedQuery]);
  const matchingEntryCount = useMemo(() => {
    if (!trimmedQuery) return 0;
    return filtered.reduce((sum, bucket) => sum + memoryBucketMatchingEntries(bucket, trimmedQuery).length, 0);
  }, [filtered, trimmedQuery]);
  const hasSearch = buckets.length > 0;
  const hasMemorySnapshot = !!memory;
  const entryCount = buckets.reduce((sum, bucket) => sum + bucket.entry_count, 0);
  const topicCount = memory?.topics?.length ?? 0;
  const summary = noSession
    ? "Session memory unavailable"
    : loading
      ? "Loading memory"
      : error && !hasMemorySnapshot
        ? "Memory unavailable"
        : memory?.has_memory
          ? `${entryCount} ${entryCount === 1 ? "entry" : "entries"}`
          : "No durable memory";
  const summaryDetail = noSession
    ? "Open a saved chat before inspecting session memory."
    : loading
      ? "Reading durable buckets."
      : error
        ? hasMemorySnapshot
          ? `${panelErrorSummary("Memory API", error)} · showing last loaded memory`
          : panelErrorSummary("Memory API", error)
        : memory?.has_memory
          ? `${topicCount} ${topicCount === 1 ? "topic" : "topics"} · ${totalMemoryChars(buckets)} chars${memory.shared_user_memory ? " · shared user" : ""}`
          : "No user, core, or topic entries saved.";

  async function handleManualMemorySubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const content = memoryContent.trim();
    if (!content) return;
    if (onAddMemory) {
      setMemorySaveState({ state: "saving" });
      try {
        await onAddMemory({ content, target: memoryTarget, topic: memoryTopic });
        setMemoryContent("");
        setMemorySaveState({ state: "saved", message: "Memory saved." });
      } catch (err) {
        setMemorySaveState({ state: "error", message: formatPanelError(err) });
      }
      return;
    }
    if (!onUseAsDraft) return;
    onUseAsDraft(manualMemoryDraft({ content, target: memoryTarget, topic: memoryTopic }), "memory");
    setMemorySaveState({ state: "saved", message: "Memory draft prepared." });
  }

  async function handleRemoveMemory(bucket: SessionMemoryBucket, entry: string) {
    if (!onRemoveMemory) return;
    setMemorySaveState({ state: "saving" });
    try {
      await onRemoveMemory({ action: "remove", target: bucket.target, topic: bucket.topic, old_text: entry });
      setConfirmRemoveKey(undefined);
      setMemorySaveState({ state: "saved", message: "Memory removed." });
    } catch (err) {
      setMemorySaveState({ state: "error", message: formatPanelError(err) });
    }
  }

  async function handleReplaceMemory(bucket: SessionMemoryBucket, entry: string) {
    if (!onReplaceMemory || !editingEntry) return;
    const next = editingEntry.value.trim();
    if (!next || next === entry.trim()) return;
    setMemorySaveState({ state: "saving" });
    try {
      await onReplaceMemory({ action: "replace", target: bucket.target, topic: bucket.topic, old_text: entry, new_content: next });
      setEditingEntry(undefined);
      setMemorySaveState({ state: "saved", message: "Memory updated." });
    } catch (err) {
      setMemorySaveState({ state: "error", message: formatPanelError(err) });
    }
  }

  return (
    <details
      className="session-skills-panel session-memory-panel"
      data-testid="session-memory-panel"
      data-surface={surface ? "true" : undefined}
      open={surface || panelOpen}
      onToggle={(event) => {
        if (surface) {
          event.currentTarget.open = true;
          return;
        }
        setPanelOpen(event.currentTarget.open);
      }}
    >
      <summary className="session-skills-summary" onClick={surface ? (event) => event.preventDefault() : undefined}>
        <span className="session-skills-kicker">Memory</span>
        <strong>{summary}</strong>
        <span>{summaryDetail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? <div className="session-skills-empty">Loading session memory...</div> : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
            {onRefresh ? (
              <button type="button" className="ghost-action" onClick={() => void onRefresh()}>
                Retry
              </button>
            ) : null}
          </div>
        ) : null}
        {!loading && error && !hasMemorySnapshot && onUseAsDraft ? (
          <>
            <div className="session-runtime-fallback" data-testid="session-memory-fallback">
              <strong>Memory can still be prepared</strong>
              <span>Use a draft when the API is unavailable; Affent can save or inspect memory after the runtime route is fixed.</span>
            </div>
            <MemoryDraftForm
              memoryTarget={memoryTarget}
              memoryTopic={memoryTopic}
              memoryContent={memoryContent}
              busy={memorySaveState.state === "saving"}
              status={memorySaveState}
              submitLabel={onAddMemory ? "Save memory" : "Prepare memory draft"}
              setMemoryTarget={setMemoryTarget}
              setMemoryTopic={setMemoryTopic}
              setMemoryContent={setMemoryContent}
              onSubmit={handleManualMemorySubmit}
            />
          </>
        ) : null}
        {!loading && !error && noSession ? <div className="session-skills-empty">Open a saved chat to inspect stored memory buckets.</div> : null}
        {!loading && !noSession && (!error || hasMemorySnapshot) ? (
          <>
            {latestUpdate ? <LatestMemoryUpdate update={latestUpdate} onUseAsDraft={onUseAsDraft} /> : null}
            {hasSearch ? (
              <div className="session-skills-controls">
                <label className="session-skills-search">
                  <span>Search memory</span>
                  <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search entries or topics" />
                </label>
                {trimmedQuery ? (
                  <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                    Clear
                  </button>
                ) : null}
                {trimmedQuery ? (
                  <span className="session-search-count" data-testid="session-memory-search-count">
                    {filtered.length} {filtered.length === 1 ? "bucket" : "buckets"}
                    {matchingEntryCount > 0 ? ` · ${matchingEntryCount} ${matchingEntryCount === 1 ? "entry" : "entries"}` : ""}
                  </span>
                ) : null}
                {onRefresh ? (
                  <button type="button" className="ghost-action" onClick={() => void onRefresh()}>
                    Refresh
                  </button>
                ) : null}
              </div>
            ) : onRefresh ? (
              <div className="session-skills-controls">
                <button type="button" className="ghost-action" onClick={() => void onRefresh()}>
                  Refresh
                </button>
              </div>
            ) : null}
            <div className="session-skills-list" data-testid="session-memory-list">
              {filtered.length > 0 ? (
                filtered.map((bucket) => {
                  const matchingEntries = trimmedQuery ? memoryBucketMatchingEntries(bucket, trimmedQuery) : [];
                  const entriesToShow = matchingEntries.length > 0 ? matchingEntries : bucket.entries;
                  return (
                    <details key={`${bucket.target}:${bucket.topic ?? ""}`} className="session-skill-item" open={trimmedQuery ? true : undefined}>
                      <summary>
                        <span className="session-skill-title">
                          <strong>{memoryBucketLabel(bucket)}</strong>
                          <span>{bucket.entry_count} entries</span>
                        </span>
                        <span className="session-skill-desc">{memoryBucketUsage(bucket)}</span>
                        {trimmedQuery && matchingEntries.length > 0 ? (
                          <span className="session-skill-status">
                            <span>{matchingEntries.length} matched</span>
                          </span>
                        ) : null}
                      </summary>
                      <div className="session-skill-detail">
                        <div className="session-skill-meta">
                          <span>{bucket.target}</span>
                          {bucket.newest_at ? <span>Updated {formatTimestamp(bucket.newest_at)}</span> : null}
                        </div>
                        {bucket.entries && bucket.entries.length > 0 ? (
                          <>
                            <div className="session-memory-actions">
                              <CopyButton label="Copy entries" value={bucket.entries.join("\n\n")} className="node-action" />
                              <CopyButton label="Copy details" value={memoryBucketEvidenceText(bucket)} className="node-action" />
                              {onUseAsDraft ? (
                                <button type="button" className="node-action" onClick={() => onUseAsDraft(memoryBucketDraft(bucket), "memory")}>
                                  Start from memory
                                </button>
                              ) : null}
                            </div>
                            <ul className="session-memory-entries" data-filtered={matchingEntries.length > 0 ? "true" : "false"}>
                              {(entriesToShow ?? []).map((entry, index) => {
                                const entryKey = memoryEntryKey(bucket.target, bucket.topic, entry);
                                const isEditing = editingEntry?.key === entryKey;
                                return (
                                  <li key={`${index}:${entry}`} className="session-memory-entry-row">
                                    {isEditing ? (
                                      <form className="session-memory-entry-edit" onSubmit={(event) => {
                                        event.preventDefault();
                                        void handleReplaceMemory(bucket, entry);
                                      }}>
                                        <label>
                                          <span>Edit memory {index + 1}</span>
                                          <textarea
                                            value={editingEntry.value}
                                            disabled={memorySaveState.state === "saving"}
                                            onChange={(event) => setEditingEntry({ key: entryKey, value: event.target.value })}
                                          />
                                        </label>
                                        <div className="session-memory-entry-actions">
                                          <button
                                            type="button"
                                            className="ghost-action"
                                            disabled={memorySaveState.state === "saving"}
                                            onClick={() => setEditingEntry(undefined)}
                                          >
                                            Cancel
                                          </button>
                                          <button
                                            type="submit"
                                            className="ghost-action"
                                            disabled={memorySaveState.state === "saving" || !editingEntry.value.trim() || editingEntry.value.trim() === entry.trim()}
                                          >
                                            Save edit
                                          </button>
                                        </div>
                                      </form>
                                    ) : (
                                      <span>{entry}</span>
                                    )}
                                    {!isEditing && (onRemoveMemory || onReplaceMemory) ? (
                                      confirmRemoveKey === entryKey ? (
                                        <span className="session-memory-entry-actions" role="group" aria-label={`Confirm remove memory ${index + 1}`}>
                                          <button type="button" className="ghost-action" disabled={memorySaveState.state === "saving"} onClick={() => setConfirmRemoveKey(undefined)}>
                                            Cancel
                                          </button>
                                          <button
                                            type="button"
                                            className="ghost-action danger-action"
                                            disabled={memorySaveState.state === "saving"}
                                            onClick={() => void handleRemoveMemory(bucket, entry)}
                                          >
                                            Confirm remove
                                          </button>
                                        </span>
                                      ) : (
                                        <span className="session-memory-entry-actions">
                                          {onReplaceMemory ? (
                                            <button
                                              type="button"
                                              className="ghost-action"
                                              disabled={memorySaveState.state === "saving"}
                                              onClick={() => {
                                                setConfirmRemoveKey(undefined);
                                                setEditingEntry({ key: entryKey, value: entry });
                                              }}
                                            >
                                              Edit
                                            </button>
                                          ) : null}
                                          {onRemoveMemory ? (
                                            <button
                                              type="button"
                                              className="ghost-action danger-action"
                                              disabled={memorySaveState.state === "saving"}
                                              onClick={() => {
                                                setEditingEntry(undefined);
                                                setConfirmRemoveKey(entryKey);
                                              }}
                                            >
                                              Remove
                                            </button>
                                          ) : null}
                                        </span>
                                      )
                                    ) : null}
                                  </li>
                                );
                              })}
                            </ul>
                          </>
                        ) : (
                          <p className="session-skill-preview">No entries in this bucket.</p>
                        )}
                      </div>
                    </details>
                  );
                })
              ) : (
                <div className="session-skills-empty">{buckets.length > 0 ? "No matching memory." : "No memory buckets."}</div>
              )}
            </div>
            {memorySaveState.message && !(onAddMemory || onUseAsDraft) ? (
              <span className="session-memory-form-status" data-tone={memorySaveState.state === "error" ? "error" : "success"}>{memorySaveState.message}</span>
            ) : null}
            {onAddMemory || onUseAsDraft ? (
              <MemoryDraftForm
                memoryTarget={memoryTarget}
                memoryTopic={memoryTopic}
                memoryContent={memoryContent}
                busy={memorySaveState.state === "saving"}
                status={memorySaveState}
                submitLabel={onAddMemory ? "Save memory" : "Prepare memory draft"}
                setMemoryTarget={setMemoryTarget}
                setMemoryTopic={setMemoryTopic}
                setMemoryContent={setMemoryContent}
                onSubmit={handleManualMemorySubmit}
              />
            ) : null}
          </>
        ) : null}
      </div>
    </details>
  );
}

function MemoryDraftForm({
  memoryTarget,
  memoryTopic,
  memoryContent,
  busy = false,
  status,
  submitLabel,
  setMemoryTarget,
  setMemoryTopic,
  setMemoryContent,
  onSubmit,
}: {
  memoryTarget: string;
  memoryTopic: string;
  memoryContent: string;
  busy?: boolean;
  status?: { state: "idle" | "saving" | "saved" | "error"; message?: string };
  submitLabel: string;
  setMemoryTarget: (value: string) => void;
  setMemoryTopic: (value: string) => void;
  setMemoryContent: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  return (
    <form className="session-skill-form session-memory-form" data-testid="session-memory-form" onSubmit={onSubmit}>
      <label>
        <span>Target</span>
        <input value={memoryTarget} onChange={(event) => setMemoryTarget(event.target.value)} placeholder="memory" disabled={busy} />
      </label>
      <label>
        <span>Topic</span>
        <input value={memoryTopic} onChange={(event) => setMemoryTopic(event.target.value)} placeholder="project, user, workflow" disabled={busy} />
      </label>
      <label className="session-skill-form-body">
        <span>Content</span>
        <textarea value={memoryContent} onChange={(event) => setMemoryContent(event.target.value)} placeholder="Fact to remember" disabled={busy} />
      </label>
      <button type="submit" className="session-skills-add-submit" disabled={!memoryContent.trim() || busy}>
        {busy ? "Saving" : submitLabel}
      </button>
      {status?.message ? (
        <span className="session-memory-form-status" data-tone={status.state === "error" ? "error" : "success"} role="status" aria-live="polite">{status.message}</span>
      ) : null}
    </form>
  );
}

function LatestMemoryUpdate({ update, onUseAsDraft }: { update: MemoryUpdateMeta; onUseAsDraft?: UseAsDraft }) {
  const location = memoryUpdateLocation(update);
  const preview = memoryUpdatePreview(update);
  return (
    <div className="session-memory-latest" data-testid="session-memory-latest">
      <div>
        <strong>Latest update</strong>
        <span>{memoryActionLabel(update.action)}</span>
        {location ? <code>{location}</code> : null}
      </div>
      {preview ? <p>{preview}</p> : null}
      <div className="session-memory-actions">
        <CopyButton label="Copy update evidence" value={memoryUpdateEvidenceText(update)} className="node-action" />
        {onUseAsDraft ? (
          <button type="button" className="node-action" onClick={() => onUseAsDraft(memoryUpdateDraft(update), "memory")}>
            Review update
          </button>
        ) : null}
      </div>
    </div>
  );
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}

function memoryEntryKey(target: string, topic: string | undefined, entry: string): string {
  return `${target}:${topic ?? ""}:${entry}`;
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
