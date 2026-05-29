import { useMemo, useState, type FormEvent } from "react";
import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryAddRequest, SessionMemoryBucket, SessionMemoryRemoveRequest, SessionMemoryReplaceRequest, SessionMemoryResponse } from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  memoryActionLabel,
  memoryBucketsNeedingReview,
  memoryBucketMatchesQuery,
  memoryBucketMatchingEntries,
  memoryBucketPreview,
  memoryBucketDraft,
  memoryBucketEvidenceText,
  memoryBucketKey,
  memoryBucketLabel,
  memoryBuckets,
  memoryBucketUsage,
  memoryEntryIsSensitive,
  memoryEntrySafePreview,
  memoryReviewFindings,
  memoryScopeLabel,
  memorySuggestionDraft,
  memoryStats,
  memorySnapshotDraft,
  memorySnapshotEvidenceText,
  memoryUsageLabel,
  memoryUpdateDraft,
  memoryUpdateEvidenceText,
  memoryUpdateLocation,
  memoryUpdatePreview,
  manualMemoryDraft,
  type SessionMemoryCandidate,
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
  candidates = [],
}: {
  memory?: SessionMemoryResponse;
  latestUpdate?: MemoryUpdateMeta;
  candidates?: readonly SessionMemoryCandidate[];
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
  const [selectedBucketKey, setSelectedBucketKey] = useState<string | undefined>();
  const [scopeFilter, setScopeFilter] = useState<MemoryScopeFilter>("all");
  const [writeOpen, setWriteOpen] = useState(false);
  const [savingCandidateId, setSavingCandidateId] = useState<string | undefined>();
  const [revealedEntryKeys, setRevealedEntryKeys] = useState<ReadonlySet<string>>(() => new Set());
  const buckets = useMemo(() => memoryBuckets(memory), [memory]);
  const reviewFindings = useMemo(() => memoryReviewFindings(memory), [memory]);
  const reviewBucketKeys = useMemo(() => memoryBucketsNeedingReview(memory), [memory]);
  const trimmedQuery = query.trim();
  const filtered = useMemo(() => {
    return buckets
      .filter((bucket) => memoryBucketMatchesScope(bucket, scopeFilter, reviewBucketKeys))
      .filter((bucket) => !trimmedQuery || memoryBucketMatchesQuery(bucket, trimmedQuery));
  }, [buckets, reviewBucketKeys, scopeFilter, trimmedQuery]);
  const focusedBucket = useMemo(() => {
    if (filtered.length === 0) return undefined;
    const selected = selectedBucketKey ? filtered.find((bucket) => memoryBucketKey(bucket) === selectedBucketKey) : undefined;
    if (selected) return selected;
    return filtered.find((bucket) => bucket.target === "memory" && bucket.topic && bucket.topic !== "core")
      ?? filtered.find((bucket) => bucket.target === "memory")
      ?? filtered[0];
  }, [filtered, selectedBucketKey]);
  const matchingEntryCount = useMemo(() => {
    if (!trimmedQuery) return 0;
    return filtered.reduce((sum, bucket) => sum + memoryBucketMatchingEntries(bucket, trimmedQuery).length, 0);
  }, [filtered, trimmedQuery]);
  const hasSearch = buckets.length > 0;
  const hasMemorySnapshot = !!memory;
  const stats = useMemo(() => memoryStats(memory), [memory]);
  const summary = noSession
    ? "Session memory unavailable"
    : loading
      ? "Loading memory"
      : error && !hasMemorySnapshot
        ? "Memory unavailable"
        : memory?.has_memory
          ? `${stats.entryCount} ${stats.entryCount === 1 ? "entry" : "entries"}`
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
          ? `${stats.topicCount} ${stats.topicCount === 1 ? "topic" : "topics"} · ${memoryUsageLabel(stats)}${memory.shared_user_memory ? " · shared user" : ""}`
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

  function handleUseCandidate(candidate: SessionMemoryCandidate) {
    setMemoryTarget(candidate.target);
    setMemoryTopic(candidate.topic);
    setMemoryContent(candidate.content);
    setWriteOpen(true);
    setMemorySaveState({ state: "saved", message: "Candidate loaded. Review before saving." });
  }

  async function handleSaveCandidate(candidate: SessionMemoryCandidate) {
    if (!onAddMemory) return;
    setSavingCandidateId(candidate.id);
    setMemorySaveState({ state: "saving" });
    try {
      await onAddMemory({ content: candidate.content, target: candidate.target, topic: candidate.topic });
      setMemorySaveState({ state: "saved", message: "Memory candidate saved." });
    } catch (err) {
      setMemorySaveState({ state: "error", message: formatPanelError(err) });
    } finally {
      setSavingCandidateId(undefined);
    }
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

  function toggleRevealedEntry(key: string) {
    setRevealedEntryKeys((current) => {
      const next = new Set(current);
      if (next.has(key)) {
        next.delete(key);
      } else {
        next.add(key);
      }
      return next;
    });
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
            <MemoryMaintenanceBoard
              reviewFindings={reviewFindings}
              candidateCount={candidates.length}
              latestUpdate={latestUpdate}
              canWrite={Boolean(onAddMemory)}
              onShowReview={() => setScopeFilter("review")}
              onUseAsDraft={onUseAsDraft}
            />
            {memory?.has_memory && reviewFindings.length > 0 ? (
              <MemoryReviewQueue
                findings={reviewFindings}
                onShowBuckets={() => setScopeFilter("review")}
              />
            ) : null}
            <MemoryPanelActions
              memory={memory}
              stats={stats}
              hasSearch={hasSearch}
              onRefresh={onRefresh}
              onUseAsDraft={onUseAsDraft}
            />
            {candidates.length > 0 && memorySaveState.message && !writeOpen ? (
              <span className="session-memory-form-status" data-tone={memorySaveState.state === "error" ? "error" : "success"} role="status" aria-live="polite">
                {memorySaveState.message}
              </span>
            ) : null}
            {candidates.length > 0 ? (
              <MemoryCandidateReview
                candidates={candidates}
                canSave={Boolean(onAddMemory)}
                savingCandidateId={savingCandidateId}
                onUseCandidate={handleUseCandidate}
                onSaveCandidate={(candidate) => void handleSaveCandidate(candidate)}
              />
            ) : null}
            {latestUpdate ? <LatestMemoryUpdate update={latestUpdate} onUseAsDraft={onUseAsDraft} /> : null}
            {focusedBucket ? (
              <MemoryBucketFocus
                bucket={focusedBucket}
                revealedEntryKeys={revealedEntryKeys}
                onToggleReveal={toggleRevealedEntry}
                onUseAsDraft={onUseAsDraft}
              />
            ) : null}
            {hasSearch ? (
              <div className="session-skills-controls">
                <MemoryScopeFilters buckets={buckets} reviewBucketKeys={reviewBucketKeys} value={scopeFilter} onChange={setScopeFilter} />
                <label className="session-skills-search">
                  <span>Search memory</span>
                  <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search entries or topics" />
                </label>
                {trimmedQuery || scopeFilter !== "all" ? (
                  <button type="button" className="ghost-action" onClick={() => {
                    setQuery("");
                    setScopeFilter("all");
                  }}>
                    Clear
                  </button>
                ) : null}
                {trimmedQuery || scopeFilter !== "all" ? (
                  <span className="session-search-count" data-testid="session-memory-search-count">
                    {filtered.length} {filtered.length === 1 ? "bucket" : "buckets"}
                    {matchingEntryCount > 0 ? ` · ${matchingEntryCount} ${matchingEntryCount === 1 ? "entry" : "entries"}` : ""}
                  </span>
                ) : null}
              </div>
            ) : null}
            <div className="session-skills-list" data-testid="session-memory-list">
              {filtered.length > 0 ? (
                filtered.map((bucket) => {
                  const matchingEntries = trimmedQuery ? memoryBucketMatchingEntries(bucket, trimmedQuery) : [];
                  const entriesToShow = matchingEntries.length > 0 ? matchingEntries : bucket.entries;
                  const bucketKey = memoryBucketKey(bucket);
                  return (
                    <details
                      key={bucketKey}
                      className="session-skill-item"
                      data-selected={focusedBucket && memoryBucketKey(focusedBucket) === bucketKey ? "true" : "false"}
                      open={trimmedQuery ? true : undefined}
                      onToggle={(event) => {
                        if (event.currentTarget.open) setSelectedBucketKey(bucketKey);
                      }}
                    >
                      <summary onClick={() => setSelectedBucketKey(bucketKey)}>
                        <span className="session-skill-title">
                          <strong>{memoryBucketLabel(bucket)}</strong>
                          <span>{bucket.entry_count} entries</span>
                        </span>
                        <span className="session-skill-desc">
                          <span data-testid={`memory-bucket-preview-${bucket.target}-${bucket.topic ?? "general"}`}>{memoryBucketPreview(bucket)}</span>
                          <small>{memoryBucketUsage(bucket)}</small>
                        </span>
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
                                      <MemoryEntryText
                                        entry={entry}
                                        entryKey={entryKey}
                                        revealed={revealedEntryKeys.has(entryKey)}
                                        onToggleReveal={toggleRevealedEntry}
                                      />
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
              ) : candidates.length > 0 ? null : (
                <div className="session-memory-empty-state">
                  <strong>{buckets.length > 0 ? "No matching memory" : "No durable memory saved"}</strong>
                  <span>{buckets.length > 0 ? "Clear the filters or search to inspect another bucket." : "Save only stable, non-secret facts that will help future turns."}</span>
                  {buckets.length === 0 && onUseAsDraft ? (
                    <button type="button" className="ghost-action" onClick={() => onUseAsDraft(memorySuggestionDraft(memory), "memory")}>
                      Find candidates
                    </button>
                  ) : null}
                </div>
              )}
            </div>
            {memorySaveState.message && !(onAddMemory || onUseAsDraft) ? (
              <span className="session-memory-form-status" data-tone={memorySaveState.state === "error" ? "error" : "success"}>{memorySaveState.message}</span>
            ) : null}
            {onAddMemory || onUseAsDraft ? (
              <details className="session-memory-write" open={writeOpen || (!memory?.has_memory && candidates.length === 0)} onToggle={(event) => setWriteOpen(event.currentTarget.open)}>
                <summary>
                  <strong>{onAddMemory ? "Add memory" : "Prepare memory draft"}</strong>
                  <span>{onAddMemory ? "Write a durable fact into this chat's memory." : "Prepare an agent instruction to write memory."}</span>
                </summary>
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
              </details>
            ) : null}
          </>
        ) : null}
      </div>
    </details>
  );
}

type MemoryScopeFilter = "all" | "session" | "user" | "review";

function MemoryScopeFilters({
  buckets,
  reviewBucketKeys,
  value,
  onChange,
}: {
  buckets: readonly SessionMemoryBucket[];
  reviewBucketKeys: ReadonlySet<string>;
  value: MemoryScopeFilter;
  onChange: (value: MemoryScopeFilter) => void;
}) {
  const counts = {
    all: buckets.length,
    session: buckets.filter((bucket) => bucket.target !== "user").length,
    user: buckets.filter((bucket) => bucket.target === "user").length,
    review: buckets.filter((bucket) => reviewBucketKeys.has(memoryBucketKey(bucket))).length,
  };
  const options: Array<{ value: MemoryScopeFilter; label: string; count: number }> = [
    { value: "all", label: "All", count: counts.all },
    { value: "session", label: "Session", count: counts.session },
    { value: "user", label: "User", count: counts.user },
    { value: "review", label: "Needs review", count: counts.review },
  ];
  return (
    <div className="session-filter-pills" role="group" aria-label="Filter memory buckets">
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          disabled={option.count === 0 && value !== option.value}
          onClick={() => onChange(option.value)}
        >
          <span>{option.label}</span>
          <strong>{option.count}</strong>
        </button>
      ))}
    </div>
  );
}

function memoryBucketMatchesScope(bucket: SessionMemoryBucket, scope: MemoryScopeFilter, reviewBucketKeys: ReadonlySet<string>): boolean {
  if (scope === "user") return bucket.target === "user";
  if (scope === "session") return bucket.target !== "user";
  if (scope === "review") return reviewBucketKeys.has(memoryBucketKey(bucket));
  return true;
}

function MemoryReviewQueue({
  findings,
  onShowBuckets,
}: {
  findings: ReturnType<typeof memoryReviewFindings>;
  onShowBuckets: () => void;
}) {
  const counts = findings.reduce<Record<string, number>>((acc, finding) => {
    acc[finding.kind] = (acc[finding.kind] ?? 0) + 1;
    return acc;
  }, {});
  const buckets = new Set(findings.map((finding) => finding.bucketKey)).size;
  return (
    <section className="session-memory-review" data-testid="session-memory-review" aria-label="Memory review queue">
      <div className="session-memory-review-head">
        <span>Review queue</span>
        <strong>{findings.length} {findings.length === 1 ? "finding" : "findings"} · {buckets} {buckets === 1 ? "bucket" : "buckets"}</strong>
        <button type="button" className="ghost-action" onClick={onShowBuckets}>Show buckets</button>
      </div>
      <div className="session-memory-review-kinds">
        {Object.entries(counts).map(([kind, count]) => (
          <span key={kind} data-kind={kind}>
            {memoryFindingKindLabel(kind)}
            <strong>{count}</strong>
          </span>
        ))}
      </div>
      <ul className="session-memory-review-list">
        {findings.slice(0, 5).map((finding, index) => (
          <li key={`${finding.kind}:${finding.bucketKey}:${index}`}>
            <strong>{finding.bucketLabel}</strong>
            <span>{finding.detail}</span>
            {finding.entryPreview ? <small title={finding.entryPreview}>{finding.entryPreview}</small> : null}
          </li>
        ))}
      </ul>
    </section>
  );
}

function memoryFindingKindLabel(kind: string): string {
  if (kind === "sensitive") return "Sensitive";
  if (kind === "duplicate") return "Duplicate";
  if (kind === "capacity") return "Capacity";
  if (kind === "large") return "Large";
  return kind;
}

function MemoryMaintenanceBoard({
  reviewFindings,
  candidateCount,
  latestUpdate,
  canWrite,
  onShowReview,
  onUseAsDraft,
}: {
  reviewFindings: ReturnType<typeof memoryReviewFindings>;
  candidateCount: number;
  latestUpdate?: MemoryUpdateMeta;
  canWrite: boolean;
  onShowReview: () => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const counts = reviewFindings.reduce<Record<string, number>>((acc, finding) => {
    acc[finding.kind] = (acc[finding.kind] ?? 0) + 1;
    return acc;
  }, {});
  const actions: Array<{
    id: string;
    label: string;
    detail: string;
    meta: string;
    tone?: "danger" | "action" | "muted";
    onClick?: () => void;
    button?: string;
  }> = [];
  if ((counts.sensitive ?? 0) > 0) {
    actions.push({
      id: "sensitive",
      label: "Remove secrets",
      detail: "Sensitive-looking memory is redacted in previews. Inspect and remove or replace it before it is reused.",
      meta: `${counts.sensitive} ${counts.sensitive === 1 ? "entry" : "entries"}`,
      tone: "danger",
      onClick: onShowReview,
      button: "Review",
    });
  }
  if ((counts.duplicate ?? 0) > 0) {
    actions.push({
      id: "duplicate",
      label: "Deduplicate",
      detail: "Duplicate facts increase prompt noise and can hide the authoritative wording.",
      meta: `${counts.duplicate} duplicate ${counts.duplicate === 1 ? "entry" : "entries"}`,
      tone: "action",
      onClick: onShowReview,
      button: "Review",
    });
  }
  const pressureCount = (counts.capacity ?? 0) + (counts.large ?? 0);
  if (pressureCount > 0) {
    actions.push({
      id: "pressure",
      label: "Reduce pressure",
      detail: "Large or nearly full buckets should be split, shortened, or moved out of durable memory.",
      meta: `${pressureCount} ${pressureCount === 1 ? "bucket" : "items"}`,
      tone: "action",
      onClick: onShowReview,
      button: "Review",
    });
  }
  if (candidateCount > 0) {
    actions.push({
      id: "candidates",
      label: canWrite ? "Save candidates" : "Prepare candidates",
      detail: "Stable facts were derived from this session. Review the candidate card before saving durable memory.",
      meta: `${candidateCount} candidate ${candidateCount === 1 ? "fact" : "facts"}`,
      tone: "action",
    });
  }
  if (latestUpdate) {
    actions.push({
      id: "latest",
      label: "Verify latest write",
      detail: "A memory write happened in this chat. Use the latest-update card below for the exact preview and evidence.",
      meta: [memoryActionLabel(latestUpdate.action), memoryUpdateLocation(latestUpdate)].filter(Boolean).join(" · "),
      tone: "muted",
      onClick: onUseAsDraft ? () => onUseAsDraft(memoryUpdateDraft(latestUpdate), "memory") : undefined,
      button: onUseAsDraft ? "Review" : undefined,
    });
  }
  if (actions.length === 0) {
    actions.push({
      id: "clean",
      label: "No maintenance queued",
      detail: "No duplicate, sensitive, oversized, or candidate memory items are visible for this session.",
      meta: "Clean",
      tone: "muted",
    });
  }
  return (
    <section className="session-memory-maintenance" data-testid="session-memory-maintenance" aria-label="Memory maintenance queue">
      <div className="session-memory-maintenance-head">
        <span>Maintenance</span>
        <strong>{actions.length === 1 && actions[0].id === "clean" ? "Nothing to fix" : `${actions.length} active ${actions.length === 1 ? "item" : "items"}`}</strong>
      </div>
      <div className="session-memory-maintenance-list">
        {actions.map((action) => (
          <article key={action.id} data-tone={action.tone}>
            <div>
              <span>{action.meta}</span>
              <strong>{action.label}</strong>
              <p>{action.detail}</p>
            </div>
            {action.onClick && action.button ? (
              <button type="button" className="ghost-action" onClick={action.onClick}>
                {action.button}
              </button>
            ) : null}
          </article>
        ))}
      </div>
    </section>
  );
}

function MemoryBucketFocus({
  bucket,
  revealedEntryKeys,
  onToggleReveal,
  onUseAsDraft,
}: {
  bucket: SessionMemoryBucket;
  revealedEntryKeys: ReadonlySet<string>;
  onToggleReveal: (key: string) => void;
  onUseAsDraft?: UseAsDraft;
}) {
  const entries = bucket.entries ?? [];
  const previewEntries = entries.slice(0, 4);
  return (
    <section className="session-memory-focus" data-testid="session-memory-focus" aria-label={`Memory bucket ${memoryBucketLabel(bucket)}`}>
      <div className="session-memory-focus-head">
        <span>{bucket.target === "user" ? "User memory" : bucket.topic === "core" ? "Core memory" : "Topic memory"}</span>
        <strong>{memoryBucketLabel(bucket)}</strong>
        <small>{memoryBucketPreview(bucket)}</small>
      </div>
      <div className="session-memory-focus-grid">
        <MemoryFocusFact label="Target" value={bucket.target} />
        <MemoryFocusFact label="Entries" value={String(bucket.entry_count)} />
        <MemoryFocusFact label="Usage" value={memoryBucketUsage(bucket)} />
        <MemoryFocusFact label="Updated" value={bucket.newest_at ? formatTimestamp(bucket.newest_at) : "Unknown"} />
      </div>
      <div className="session-memory-focus-entries">
        <span>Entries</span>
        {previewEntries.length > 0 ? (
          <ul>
            {previewEntries.map((entry, index) => {
              const entryKey = memoryEntryKey(bucket.target, bucket.topic, entry);
              return (
                <li key={`${index}:${entry}`}>
                  <MemoryEntryText
                    entry={entry}
                    entryKey={entryKey}
                    revealed={revealedEntryKeys.has(entryKey)}
                    onToggleReveal={onToggleReveal}
                  />
                </li>
              );
            })}
          </ul>
        ) : (
          <p>No entries in this bucket.</p>
        )}
        {entries.length > previewEntries.length ? <small>{entries.length - previewEntries.length} more entries in the bucket list.</small> : null}
      </div>
      <div className="session-memory-actions">
        <CopyButton label="Copy details" value={memoryBucketEvidenceText(bucket)} className="node-action" />
        <CopyButton label="Copy entries" value={entries.join("\n\n")} className="node-action" />
        {onUseAsDraft ? (
          <button type="button" className="node-action" onClick={() => onUseAsDraft(memoryBucketDraft(bucket), "memory")}>
            Start from memory
          </button>
        ) : null}
      </div>
    </section>
  );
}

function MemoryEntryText({
  entry,
  entryKey,
  revealed,
  onToggleReveal,
}: {
  entry: string;
  entryKey: string;
  revealed: boolean;
  onToggleReveal: (key: string) => void;
}) {
  const sensitive = memoryEntryIsSensitive(entry);
  const text = sensitive && !revealed ? memoryEntrySafePreview(entry) : entry;
  return (
    <span className="session-memory-entry-text" data-sensitive={sensitive ? "true" : undefined} data-revealed={revealed ? "true" : undefined}>
      <span>{text}</span>
      {sensitive ? (
        <button type="button" className="ghost-action" onClick={() => onToggleReveal(entryKey)}>
          {revealed ? "Hide" : "Reveal"}
        </button>
      ) : null}
    </span>
  );
}

function MemoryFocusFact({ label, value }: { label: string; value: string }) {
  return (
    <div className="session-memory-focus-fact">
      <span>{label}</span>
      <strong title={value}>{value}</strong>
    </div>
  );
}

function MemoryPanelActions({
  memory,
  stats,
  hasSearch,
  onRefresh,
  onUseAsDraft,
}: {
  memory?: SessionMemoryResponse;
  stats: ReturnType<typeof memoryStats>;
  hasSearch: boolean;
  onRefresh?: () => Promise<void> | void;
  onUseAsDraft?: UseAsDraft;
}) {
  if (!memory && !onRefresh) return null;
  const hasSavedMemory = Boolean(memory?.has_memory);
  const minimal = !hasSavedMemory && !hasSearch;
  const candidateDraftHandler = !minimal ? onUseAsDraft : undefined;
  const status = memory && hasSavedMemory
    ? `${stats.entryCount} ${stats.entryCount === 1 ? "entry" : "entries"} · ${memoryUsageLabel(stats)} · ${memoryScopeLabel(memory)}`
    : undefined;
  return (
    <div className="session-memory-toolbar" data-mode={minimal ? "minimal" : undefined} data-testid="session-memory-toolbar">
      {memory && hasSavedMemory ? <CopyButton label="Copy snapshot" value={memorySnapshotEvidenceText(memory)} className="ghost-action" /> : null}
      {candidateDraftHandler ? (
        <button type="button" className="ghost-action" onClick={() => candidateDraftHandler(memorySuggestionDraft(memory), "memory")}>
          Find candidates
        </button>
      ) : null}
      {memory && hasSavedMemory && onUseAsDraft ? (
        <button type="button" className="ghost-action" onClick={() => onUseAsDraft(memorySnapshotDraft(memory), "memory")}>
          Review snapshot
        </button>
      ) : null}
      {status ? <span>{status}</span> : hasSearch ? <span>Searchable durable memory</span> : null}
      {onRefresh ? (
        <button type="button" className="ghost-action" onClick={() => void onRefresh()}>
          Refresh
        </button>
      ) : null}
    </div>
  );
}

function MemoryCandidateReview({
  candidates,
  canSave,
  savingCandidateId,
  onUseCandidate,
  onSaveCandidate,
}: {
  candidates: readonly SessionMemoryCandidate[];
  canSave: boolean;
  savingCandidateId?: string;
  onUseCandidate: (candidate: SessionMemoryCandidate) => void;
  onSaveCandidate: (candidate: SessionMemoryCandidate) => void;
}) {
  return (
    <section className="session-memory-candidates" data-testid="session-memory-candidates" aria-label="Memory candidates">
      <div className="session-memory-candidates-head">
        <span>Candidate facts</span>
        <strong>{candidates.length} from current session evidence</strong>
      </div>
      <ul>
        {candidates.map((candidate) => (
          <li key={candidate.id}>
            <div>
              <span>{candidate.source}</span>
              <strong>{candidate.topic}</strong>
              <p>{candidate.content}</p>
              <small>{candidate.reason}</small>
            </div>
            <div className="session-memory-actions">
              <button type="button" className="ghost-action" onClick={() => onUseCandidate(candidate)}>
                Edit before save
              </button>
              {canSave ? (
                <button
                  type="button"
                  className="ghost-action primary-run-action"
                  disabled={savingCandidateId === candidate.id}
                  onClick={() => onSaveCandidate(candidate)}
                >
                  {savingCandidateId === candidate.id ? "Saving" : "Save"}
                </button>
              ) : null}
            </div>
          </li>
        ))}
      </ul>
    </section>
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
  const targetOptions = [
    { value: "memory", label: "Session", description: "Project or task fact" },
    { value: "user", label: "User", description: "Stable preference" },
  ];
  return (
    <form className="session-skill-form session-memory-form" data-testid="session-memory-form" onSubmit={onSubmit}>
      <fieldset className="session-memory-targets">
        <legend>Target</legend>
        <div>
          {targetOptions.map((option) => (
            <button
              key={option.value}
              type="button"
              disabled={busy}
              aria-pressed={memoryTarget === option.value}
              onClick={() => setMemoryTarget(option.value)}
            >
              <strong>{option.label}</strong>
              <span>{option.description}</span>
            </button>
          ))}
        </div>
      </fieldset>
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
