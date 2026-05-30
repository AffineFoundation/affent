import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryAddRequest, SessionMemoryBucket, SessionMemoryRemoveRequest, SessionMemoryReplaceRequest, SessionMemoryResponse } from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  memoryActionLabel,
  memoryBucketsNeedingReview,
  memoryBucketMatchesQuery,
  memoryBucketMatchingEntries,
  memoryBucketPreview,
  memoryBucketKey,
  memoryBucketLabel,
  memoryBuckets,
  memoryBucketUsage,
  memoryEntryIsSensitive,
  memoryEntrySafePreview,
  memoryReviewFindings,
  memoryStats,
  memorySnapshotEvidenceText,
  memoryUsageLabel,
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
  const latestUpdateBucketKey = latestUpdate ? memoryUpdateBucketKey(latestUpdate) : undefined;
  const focusedBucket = useMemo(() => {
    if (filtered.length === 0) return undefined;
    const selected = selectedBucketKey ? filtered.find((bucket) => memoryBucketKey(bucket) === selectedBucketKey) : undefined;
    if (selected) return selected;
    const review = filtered.find((bucket) => reviewBucketKeys.has(memoryBucketKey(bucket)));
    if (review) return review;
    const latest = latestUpdateBucketKey ? filtered.find((bucket) => memoryBucketKey(bucket) === latestUpdateBucketKey) : undefined;
    if (latest) return latest;
    return filtered.find((bucket) => bucket.target === "memory" && bucket.topic && bucket.topic !== "core")
      ?? filtered.find((bucket) => bucket.target === "memory")
      ?? filtered[0];
  }, [filtered, latestUpdateBucketKey, reviewBucketKeys, selectedBucketKey]);
  const matchingEntryCount = useMemo(() => {
    if (!trimmedQuery) return 0;
    return filtered.reduce((sum, bucket) => sum + memoryBucketMatchingEntries(bucket, trimmedQuery).length, 0);
  }, [filtered, trimmedQuery]);
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
          : "";

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

  function prepareMemoryDraftForBucket(bucket?: SessionMemoryBucket) {
    if (!bucket) return;
    setMemoryTarget(bucket.target);
    setMemoryTopic(bucket.topic ?? (bucket.target === "user" ? "user" : ""));
    setMemorySaveState({ state: "idle" });
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

  function openReviewFinding(finding: ReturnType<typeof memoryReviewFindings>[number]) {
    setScopeFilter("review");
    setSelectedBucketKey(finding.bucketKey);
    setConfirmRemoveKey(undefined);
    setEditingEntry(undefined);
    setWriteOpen(false);
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
        {summaryDetail ? <span>{summaryDetail}</span> : null}
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
            <MemoryWorkbenchToolbar
              memory={memory}
              stats={stats}
              buckets={buckets}
              reviewBucketKeys={reviewBucketKeys}
              filter={scopeFilter}
              query={query}
              filteredCount={filtered.length}
              matchingEntryCount={matchingEntryCount}
              reviewFindings={reviewFindings}
              candidateCount={candidates.length}
              canWrite={Boolean(onAddMemory)}
              onFilterChange={setScopeFilter}
              onQueryChange={setQuery}
              onClear={() => {
                setQuery("");
                setScopeFilter("all");
              }}
              onRefresh={onRefresh}
              onOpenFinding={openReviewFinding}
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
            {buckets.length > 0 ? (
              <div className="session-memory-manager">
                <aside className="session-memory-sidebar" aria-label="Memory buckets">
                  <MemoryBucketList
                    buckets={filtered}
                    allBucketCount={buckets.length}
                    reviewFindings={reviewFindings}
                    selectedBucketKey={focusedBucket ? memoryBucketKey(focusedBucket) : undefined}
                    query={trimmedQuery}
                    onSelect={setSelectedBucketKey}
                  />
                </aside>
                <section className="session-memory-main">
                  {focusedBucket ? (
                    <MemoryBucketFocus
                      bucket={focusedBucket}
                      query={trimmedQuery}
                      reviewFindings={reviewFindings.filter((finding) => finding.bucketKey === memoryBucketKey(focusedBucket))}
                      autoScrollReview={scopeFilter === "review" || Boolean(selectedBucketKey)}
                      latestUpdate={latestUpdateBucketKey === memoryBucketKey(focusedBucket) ? latestUpdate : undefined}
                      editingEntry={editingEntry}
                      saving={memorySaveState.state === "saving"}
                      confirmRemoveKey={confirmRemoveKey}
                      canRemove={Boolean(onRemoveMemory)}
                      canReplace={Boolean(onReplaceMemory)}
                      revealedEntryKeys={revealedEntryKeys}
                      onToggleReveal={toggleRevealedEntry}
                      onStartEdit={(key, value) => {
                        setConfirmRemoveKey(undefined);
                        setEditingEntry({ key, value });
                      }}
                      onCancelEdit={() => setEditingEntry(undefined)}
                      onEditChange={(key, value) => setEditingEntry({ key, value })}
                      onSaveEdit={(entry) => void handleReplaceMemory(focusedBucket, entry)}
                      onAskRemove={(key) => {
                        setEditingEntry(undefined);
                        setConfirmRemoveKey(key);
                      }}
                      onCancelRemove={() => setConfirmRemoveKey(undefined)}
                      onConfirmRemove={(entry) => void handleRemoveMemory(focusedBucket, entry)}
                    />
                  ) : candidates.length > 0 ? null : (
                    <div className="session-memory-empty-state">
                      <strong>No matching memory</strong>
                      <span>Clear the filters or search to inspect another bucket.</span>
                    </div>
                  )}
                  {memorySaveState.message && !(onAddMemory || onUseAsDraft) ? (
                    <span className="session-memory-form-status" data-tone={memorySaveState.state === "error" ? "error" : "success"}>{memorySaveState.message}</span>
                  ) : null}
                  {onAddMemory || onUseAsDraft ? (
                    <MemoryWritePanel
                      open={writeOpen}
                      forceOpen={!memory?.has_memory && candidates.length === 0}
                      canSave={Boolean(onAddMemory)}
                      memoryTarget={memoryTarget}
                      memoryTopic={memoryTopic}
                      memoryContent={memoryContent}
                      bucketContext={focusedBucket}
                      busy={memorySaveState.state === "saving"}
                      status={memorySaveState}
                      setOpen={setWriteOpen}
                      onOpen={() => prepareMemoryDraftForBucket(focusedBucket)}
                      setMemoryTarget={setMemoryTarget}
                      setMemoryTopic={setMemoryTopic}
                      setMemoryContent={setMemoryContent}
                      onSubmit={handleManualMemorySubmit}
                    />
                  ) : null}
                </section>
              </div>
            ) : (
              <section className="session-memory-empty-workflow">
                {memorySaveState.message && !(onAddMemory || onUseAsDraft) ? (
                  <span className="session-memory-form-status" data-tone={memorySaveState.state === "error" ? "error" : "success"}>{memorySaveState.message}</span>
                ) : null}
                {onAddMemory || onUseAsDraft ? (
                  <MemoryWritePanel
                    open={writeOpen}
                    forceOpen={candidates.length === 0}
                    canSave={Boolean(onAddMemory)}
                    memoryTarget={memoryTarget}
                    memoryTopic={memoryTopic}
                    memoryContent={memoryContent}
                    bucketContext={undefined}
                    busy={memorySaveState.state === "saving"}
                    status={memorySaveState}
                    setOpen={setWriteOpen}
                    onOpen={() => undefined}
                    setMemoryTarget={setMemoryTarget}
                    setMemoryTopic={setMemoryTopic}
                    setMemoryContent={setMemoryContent}
                    onSubmit={handleManualMemorySubmit}
                  />
                ) : (
                  <div className="session-memory-empty-state" data-testid="session-memory-list">
                    <strong>No durable memory saved</strong>
                    <span>Save only stable, non-secret facts that will help future turns.</span>
                  </div>
                )}
              </section>
            )}
          </>
        ) : null}
      </div>
    </details>
  );
}

type MemoryScopeFilter = "all" | "session" | "user" | "review";

function MemoryWorkbenchToolbar({
  memory,
  stats,
  buckets,
  reviewBucketKeys,
  filter,
  query,
  filteredCount,
  matchingEntryCount,
  reviewFindings,
  candidateCount,
  canWrite,
  onFilterChange,
  onQueryChange,
  onClear,
  onRefresh,
  onOpenFinding,
}: {
  memory?: SessionMemoryResponse;
  stats: ReturnType<typeof memoryStats>;
  buckets: readonly SessionMemoryBucket[];
  reviewBucketKeys: ReadonlySet<string>;
  filter: MemoryScopeFilter;
  query: string;
  filteredCount: number;
  matchingEntryCount: number;
  reviewFindings: ReturnType<typeof memoryReviewFindings>;
  candidateCount: number;
  canWrite: boolean;
  onFilterChange: (filter: MemoryScopeFilter) => void;
  onQueryChange: (query: string) => void;
  onClear: () => void;
  onRefresh?: () => Promise<void> | void;
  onOpenFinding: (finding: ReturnType<typeof memoryReviewFindings>[number]) => void;
}) {
  const trimmedQuery = query.trim();
  const hasSavedMemory = Boolean(memory?.has_memory && buckets.length > 0);
  const hasActions = hasSavedMemory || reviewFindings.length > 0 || candidateCount > 0 || Boolean(onRefresh);
  if (!hasActions) return null;
  const status = hasSavedMemory ? memoryToolbarStatus(stats, memory, reviewFindings) : undefined;
  return (
    <section className="session-memory-workbench-toolbar" data-testid="session-memory-toolbar" aria-label="Memory commands">
      {buckets.length > 0 ? <MemoryScopeFilters buckets={buckets} reviewBucketKeys={reviewBucketKeys} value={filter} onChange={onFilterChange} /> : null}
      {buckets.length > 0 ? (
        <label className="session-skills-search session-memory-toolbar-search">
          <span className="visually-hidden">Search memory</span>
          <input value={query} onChange={(event) => onQueryChange(event.target.value)} placeholder="Search entries or topics" />
        </label>
      ) : null}
      <div className="session-memory-toolbar-actions">
        {trimmedQuery || filter !== "all" ? (
          <button type="button" className="ghost-action" onClick={onClear}>
            Clear
          </button>
        ) : null}
        {memory && hasSavedMemory ? <CopyButton label="Copy snapshot" value={memorySnapshotEvidenceText(memory)} className="session-workspace-icon-action" icon="copy" title="Copy snapshot" /> : null}
        {onRefresh ? (
          <button type="button" className="session-workspace-icon-action" data-icon="refresh" aria-label="Refresh memory" title="Refresh memory" onClick={() => void onRefresh()}>
            <span className="visually-hidden">Refresh memory</span>
          </button>
        ) : null}
      </div>
      {status ? <span className="session-memory-toolbar-status">{status}</span> : null}
      <MemoryReviewActions
        reviewFindings={reviewFindings}
        candidateCount={candidateCount}
        canWrite={canWrite}
        onOpenFinding={onOpenFinding}
      />
      {trimmedQuery || filter !== "all" ? (
        <span className="session-search-count" data-testid="session-memory-search-count">
          {filteredCount} {filteredCount === 1 ? "bucket" : "buckets"}
          {matchingEntryCount > 0 ? ` · ${matchingEntryCount} ${matchingEntryCount === 1 ? "entry" : "entries"}` : ""}
        </span>
      ) : null}
    </section>
  );
}

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
      {options.filter((option) => option.value === "all" || option.count > 0 || value === option.value).map((option) => (
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

function MemoryReviewActions({
  reviewFindings,
  candidateCount,
  canWrite,
  onOpenFinding,
}: {
  reviewFindings: ReturnType<typeof memoryReviewFindings>;
  candidateCount: number;
  canWrite: boolean;
  onOpenFinding: (finding: ReturnType<typeof memoryReviewFindings>[number]) => void;
}) {
  const actions = memoryReviewActions(reviewFindings, candidateCount, canWrite, onOpenFinding);
  if (actions.length === 0) return null;
  return (
    <div className="session-memory-review-actions" data-testid="session-memory-maintenance" aria-label="Memory items needing review">
      {actions.map((action) => (
        <button key={action.id} type="button" data-tone={action.tone} onClick={action.onClick} disabled={!action.onClick}>
          <strong>{action.label}</strong>
          <small>{action.meta}</small>
        </button>
      ))}
    </div>
  );
}

function memoryToolbarStatus(
  stats: ReturnType<typeof memoryStats>,
  memory: SessionMemoryResponse | undefined,
  reviewFindings: ReturnType<typeof memoryReviewFindings>,
): string {
  if (reviewFindings.length === 0) {
    return `Ready · ${stats.entryCount} ${stats.entryCount === 1 ? "entry" : "entries"} in ${stats.bucketCount} ${stats.bucketCount === 1 ? "bucket" : "buckets"} · ${memoryUsageLabel(stats)}`;
  }
  const counts = reviewFindings.reduce<Record<string, number>>((acc, finding) => {
    acc[finding.kind] = (acc[finding.kind] ?? 0) + 1;
    return acc;
  }, {});
  const parts = [
    counts.sensitive ? `${counts.sensitive} secret ${counts.sensitive === 1 ? "entry" : "entries"}` : undefined,
    counts.duplicate ? `${counts.duplicate} duplicate ${counts.duplicate === 1 ? "entry" : "entries"}` : undefined,
    (counts.capacity ?? 0) + (counts.large ?? 0) > 0 ? `${(counts.capacity ?? 0) + (counts.large ?? 0)} pressure ${(counts.capacity ?? 0) + (counts.large ?? 0) === 1 ? "issue" : "issues"}` : undefined,
  ].filter((part): part is string => Boolean(part));
  const scope = memory?.shared_user_memory ? "shared + session" : "session";
  return `Review · ${parts.join(" · ")} · ${scope}`;
}

function memoryReviewActions(
  reviewFindings: ReturnType<typeof memoryReviewFindings>,
  candidateCount: number,
  canWrite: boolean,
  onOpenFinding: (finding: ReturnType<typeof memoryReviewFindings>[number]) => void,
): Array<{
  id: string;
  label: string;
  meta: string;
  tone?: "danger" | "action" | "muted";
  onClick?: () => void;
}> {
  const counts = reviewFindings.reduce<Record<string, number>>((acc, finding) => {
    acc[finding.kind] = (acc[finding.kind] ?? 0) + 1;
    return acc;
  }, {});
  const actions: Array<{
    id: string;
    label: string;
    meta: string;
    tone?: "danger" | "action" | "muted";
    onClick?: () => void;
  }> = [];
  const firstSensitive = reviewFindings.find((finding) => finding.kind === "sensitive");
  if ((counts.sensitive ?? 0) > 0) {
    actions.push({
      id: "sensitive",
      label: "Remove secrets",
      meta: `${counts.sensitive} ${counts.sensitive === 1 ? "entry" : "entries"} · ${firstSensitive?.bucketLabel ?? "review"}`,
      tone: "danger",
      onClick: firstSensitive ? () => onOpenFinding(firstSensitive) : undefined,
    });
  }
  const firstDuplicate = reviewFindings.find((finding) => finding.kind === "duplicate");
  if ((counts.duplicate ?? 0) > 0) {
    actions.push({
      id: "duplicate",
      label: "Deduplicate",
      meta: `${counts.duplicate} duplicate ${counts.duplicate === 1 ? "entry" : "entries"} · ${firstDuplicate?.bucketLabel ?? "review"}`,
      tone: "action",
      onClick: firstDuplicate ? () => onOpenFinding(firstDuplicate) : undefined,
    });
  }
  const pressureCount = (counts.capacity ?? 0) + (counts.large ?? 0);
  const firstPressure = reviewFindings.find((finding) => finding.kind === "capacity" || finding.kind === "large");
  if (pressureCount > 0) {
    actions.push({
      id: "pressure",
      label: "Reduce pressure",
      meta: `${pressureCount} ${pressureCount === 1 ? "issue" : "issues"} · ${firstPressure?.bucketLabel ?? "review"}`,
      tone: "action",
      onClick: firstPressure ? () => onOpenFinding(firstPressure) : undefined,
    });
  }
  if (candidateCount > 0) {
    actions.push({
      id: "candidates",
      label: canWrite ? "Save candidates" : "Prepare candidates",
      meta: `${candidateCount} candidate ${candidateCount === 1 ? "fact" : "facts"}`,
      tone: "action",
    });
  }
  return actions;
}

function MemoryBucketList({
  buckets,
  allBucketCount,
  reviewFindings,
  selectedBucketKey,
  query,
  onSelect,
}: {
  buckets: readonly SessionMemoryBucket[];
  allBucketCount: number;
  reviewFindings: ReturnType<typeof memoryReviewFindings>;
  selectedBucketKey?: string;
  query: string;
  onSelect: (bucketKey: string) => void;
}) {
  const bucketRisk = useMemo(() => memoryBucketRiskMap(reviewFindings), [reviewFindings]);
  const displayBuckets = useMemo(() => prioritizeMemoryBuckets(buckets, bucketRisk), [bucketRisk, buckets]);
  return (
    <div className="session-memory-bucket-list" data-testid="session-memory-list">
      {displayBuckets.length > 0 ? (
        displayBuckets.map((bucket) => {
          const bucketKey = memoryBucketKey(bucket);
          const matchingEntries = query ? memoryBucketMatchingEntries(bucket, query) : [];
          const bucketType = bucket.target === "user" ? "user" : bucket.topic === "core" ? "core" : "topic";
          const bucketGlyph = bucket.target === "user" ? "U" : bucket.topic === "core" ? "C" : "T";
          const usage = bucket.percent !== undefined ? `${bucket.percent}%` : `${bucket.chars_used}c`;
          const risk = bucketRisk.get(bucketKey);
          return (
            <button
              key={bucketKey}
              type="button"
              className="session-memory-bucket-button"
              data-kind={bucketType}
              data-risk={risk?.tone}
              data-selected={selectedBucketKey === bucketKey ? "true" : "false"}
              onClick={() => onSelect(bucketKey)}
            >
              <span className="session-memory-bucket-kind" aria-hidden="true">{bucketGlyph}</span>
              <span className="session-memory-bucket-content">
                <span className="session-memory-bucket-main">
                  <strong>{memoryBucketLabel(bucket)}</strong>
                  <small>{bucketType}</small>
                </span>
                <span className="session-memory-bucket-preview" data-testid={`memory-bucket-preview-${bucket.target}-${bucket.topic ?? "general"}`}>
                  {memoryBucketPreview(bucket)}
                </span>
              </span>
              <span className="session-memory-bucket-stats">
                {risk ? <strong className="session-memory-bucket-risk">{risk.label}</strong> : null}
                <strong>{bucket.entry_count} {bucket.entry_count === 1 ? "entry" : "entries"}</strong>
                <small>{usage}</small>
                {query && matchingEntries.length > 0 ? <strong className="session-memory-bucket-match">{matchingEntries.length} matched</strong> : null}
              </span>
            </button>
          );
        })
      ) : (
        <div className="session-memory-empty-state">
          <strong>{allBucketCount > 0 ? "No matching memory" : "No durable memory saved"}</strong>
          <span>{allBucketCount > 0 ? "Clear the filters or search to inspect another bucket." : "Save only stable, non-secret facts that will help future turns."}</span>
        </div>
      )}
    </div>
  );
}

function memoryBucketRiskMap(reviewFindings: ReturnType<typeof memoryReviewFindings>): Map<string, { label: string; tone: "danger" | "warning" | "attention"; rank: number }> {
  const findingsByBucket = new Map<string, Set<ReturnType<typeof memoryReviewFindings>[number]["kind"]>>();
  for (const finding of reviewFindings) {
    const current = findingsByBucket.get(finding.bucketKey) ?? new Set();
    current.add(finding.kind);
    findingsByBucket.set(finding.bucketKey, current);
  }
  const out = new Map<string, { label: string; tone: "danger" | "warning" | "attention"; rank: number }>();
  for (const [bucketKey, kinds] of findingsByBucket) {
    const sensitive = kinds.has("sensitive");
    const duplicate = kinds.has("duplicate");
    const pressure = kinds.has("capacity") || kinds.has("large");
    const labels = [
      sensitive ? "Secret" : undefined,
      duplicate ? "Duplicate" : undefined,
      pressure ? "Pressure" : undefined,
    ].filter((label): label is string => Boolean(label));
    out.set(bucketKey, {
      label: labels.join(" + "),
      tone: sensitive ? "danger" : pressure ? "warning" : "attention",
      rank: sensitive ? 3 : duplicate ? 2 : 1,
    });
  }
  return out;
}

function prioritizeMemoryBuckets(
  buckets: readonly SessionMemoryBucket[],
  bucketRisk: Map<string, { rank: number }>,
): SessionMemoryBucket[] {
  return [...buckets].sort((a, b) => {
    const riskDelta = (bucketRisk.get(memoryBucketKey(b))?.rank ?? 0) - (bucketRisk.get(memoryBucketKey(a))?.rank ?? 0);
    if (riskDelta !== 0) return riskDelta;
    return 0;
  });
}

function MemoryBucketFocus({
  bucket,
  query,
  reviewFindings,
  autoScrollReview,
  latestUpdate,
  editingEntry,
  saving,
  confirmRemoveKey,
  canRemove,
  canReplace,
  revealedEntryKeys,
  onToggleReveal,
  onStartEdit,
  onCancelEdit,
  onEditChange,
  onSaveEdit,
  onAskRemove,
  onCancelRemove,
  onConfirmRemove,
}: {
  bucket: SessionMemoryBucket;
  query: string;
  reviewFindings: ReturnType<typeof memoryReviewFindings>;
  autoScrollReview: boolean;
  latestUpdate?: MemoryUpdateMeta;
  editingEntry?: { key: string; value: string };
  saving: boolean;
  confirmRemoveKey?: string;
  canRemove: boolean;
  canReplace: boolean;
  revealedEntryKeys: ReadonlySet<string>;
  onToggleReveal: (key: string) => void;
  onStartEdit: (key: string, value: string) => void;
  onCancelEdit: () => void;
  onEditChange: (key: string, value: string) => void;
  onSaveEdit: (entry: string) => void;
  onAskRemove: (key: string) => void;
  onCancelRemove: () => void;
  onConfirmRemove: (entry: string) => void;
}) {
  const entries = bucket.entries ?? [];
  const matchingEntries = query ? memoryBucketMatchingEntries(bucket, query) : [];
  const entriesToShow = matchingEntries.length > 0 ? matchingEntries : entries;
  const latestPreview = latestUpdate ? memoryUpdatePreview(latestUpdate) : "";
  const firstReviewedEntryRef = useRef<HTMLLIElement | null>(null);
  const reviewedEntryKinds = useMemo(() => {
    const byPreview = new Map<string, string[]>();
    reviewFindings.forEach((finding) => {
      if (!finding.entryPreview) return;
      const current = byPreview.get(finding.entryPreview) ?? [];
      current.push(finding.kind);
      byPreview.set(finding.entryPreview, current);
    });
    return byPreview;
  }, [reviewFindings]);
  const firstReviewedEntryIndex = entriesToShow.findIndex((entry) => reviewedEntryKinds.has(memoryEntrySafePreview(entry)));

  useEffect(() => {
    if (!autoScrollReview) return;
    if (firstReviewedEntryIndex < 0) return;
    firstReviewedEntryRef.current?.scrollIntoView?.({ block: "nearest" });
  }, [autoScrollReview, bucket.target, bucket.topic, firstReviewedEntryIndex, reviewedEntryKinds]);

  return (
    <section className="session-memory-focus" data-testid="session-memory-focus" aria-label={`Memory bucket ${memoryBucketLabel(bucket)}`}>
      <div className="session-memory-focus-head">
        <span>{bucket.target === "user" ? "User memory" : bucket.topic === "core" ? "Core memory" : "Topic memory"}</span>
        <strong>{memoryBucketLabel(bucket)}</strong>
        <small>{memoryBucketPreview(bucket)}</small>
      </div>
      {latestUpdate ? (
        <div className="session-memory-focus-update" data-testid="session-memory-latest">
          <span>Latest write</span>
          <strong>{memoryActionLabel(latestUpdate.action)}</strong>
          {latestPreview ? <small>{latestPreview}</small> : null}
        </div>
      ) : null}
      <div className="session-memory-focus-grid">
        <MemoryFocusFact label="Scope" value={memoryBucketScopeValue(bucket)} />
        <MemoryFocusFact label="Entries" value={String(bucket.entry_count)} />
        <MemoryFocusFact label="Capacity" value={memoryBucketUsage(bucket)} />
        <MemoryFocusFact label="Updated" value={bucket.newest_at ? formatTimestamp(bucket.newest_at) : "Unknown"} />
      </div>
      {reviewFindings.length > 0 ? <MemoryFocusReview findings={reviewFindings} /> : null}
      <div className="session-memory-focus-entries">
        <div className="session-memory-focus-entries-head">
          <span>Entries</span>
          <CopyButton label="Copy entries" value={entries.join("\n\n")} className="node-action" />
        </div>
        {entriesToShow.length > 0 ? (
          <ul className="session-memory-entries" data-filtered={matchingEntries.length > 0 ? "true" : "false"}>
            {entriesToShow.map((entry, index) => {
              const entryKey = memoryEntryKey(bucket.target, bucket.topic, entry);
              const isEditing = editingEntry?.key === entryKey;
              const reviewKinds = reviewedEntryKinds.get(memoryEntrySafePreview(entry)) ?? [];
              const isReviewedEntry = reviewKinds.length > 0;
              return (
                <li
                  key={`${index}:${entry}`}
                  ref={isReviewedEntry && index === firstReviewedEntryIndex ? firstReviewedEntryRef : undefined}
                  className="session-memory-entry-row"
                  data-review={isReviewedEntry ? "true" : undefined}
                  data-review-kind={reviewKinds[0]}
                >
                  {isEditing ? (
                    <form className="session-memory-entry-edit" onSubmit={(event) => {
                      event.preventDefault();
                      onSaveEdit(entry);
                    }}>
                      <label>
                        <span>Edit memory {index + 1}</span>
                        <textarea
                          value={editingEntry.value}
                          disabled={saving}
                          onChange={(event) => onEditChange(entryKey, event.target.value)}
                        />
                      </label>
                      <div className="session-memory-entry-actions">
                        <button
                          type="button"
                          className="ghost-action"
                          disabled={saving}
                          onClick={onCancelEdit}
                        >
                          Cancel
                        </button>
                        <button
                          type="submit"
                          className="ghost-action"
                          disabled={saving || !editingEntry.value.trim() || editingEntry.value.trim() === entry.trim()}
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
                      onToggleReveal={onToggleReveal}
                    />
                  )}
                  {!isEditing && (canRemove || canReplace) ? (
                    confirmRemoveKey === entryKey ? (
                      <span className="session-memory-entry-actions" role="group" aria-label={`Confirm remove memory ${index + 1}`}>
                        <button type="button" className="ghost-action" disabled={saving} onClick={onCancelRemove}>
                          Cancel
                        </button>
                        <button
                          type="button"
                          className="ghost-action danger-action"
                          disabled={saving}
                          onClick={() => onConfirmRemove(entry)}
                        >
                          Confirm remove
                        </button>
                      </span>
                    ) : (
                      <span className="session-memory-entry-actions">
                        {canReplace ? (
                          <button
                            type="button"
                            className="ghost-action"
                            disabled={saving}
                            onClick={() => onStartEdit(entryKey, entry)}
                          >
                            Edit
                          </button>
                        ) : null}
                        {canRemove ? (
                          <button
                            type="button"
                            className="ghost-action danger-action"
                            disabled={saving}
                            onClick={() => onAskRemove(entryKey)}
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
        ) : (
          <p>No entries in this bucket.</p>
        )}
        {query && matchingEntries.length > 0 ? <small>{matchingEntries.length} matched {matchingEntries.length === 1 ? "entry" : "entries"} in this bucket.</small> : null}
      </div>
    </section>
  );
}

function MemoryFocusReview({ findings }: { findings: ReturnType<typeof memoryReviewFindings> }) {
  return (
    <div className="session-memory-focus-review" data-testid="session-memory-focus-review">
      <span>Review</span>
      <ul>
        {findings.map((finding, index) => (
          <li key={`${finding.kind}:${index}:${finding.entryPreview ?? finding.detail}`}>
            <strong data-kind={finding.kind}>{memoryFindingLabel(finding.kind)}</strong>
            <span>{finding.detail}</span>
            {finding.entryPreview ? <small>{finding.entryPreview}</small> : null}
          </li>
        ))}
      </ul>
    </div>
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

function MemoryWritePanel({
  open,
  forceOpen,
  canSave,
  memoryTarget,
  memoryTopic,
  memoryContent,
  bucketContext,
  busy,
  status,
  setOpen,
  onOpen,
  setMemoryTarget,
  setMemoryTopic,
  setMemoryContent,
  onSubmit,
}: {
  open: boolean;
  forceOpen: boolean;
  canSave: boolean;
  memoryTarget: string;
  memoryTopic: string;
  memoryContent: string;
  bucketContext?: SessionMemoryBucket;
  busy: boolean;
  status: { state: "idle" | "saving" | "saved" | "error"; message?: string };
  setOpen: (open: boolean) => void;
  onOpen: () => void;
  setMemoryTarget: (value: string) => void;
  setMemoryTopic: (value: string) => void;
  setMemoryContent: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
}) {
  if (forceOpen) {
    return (
      <section className="session-memory-write" data-forced="true">
        <MemoryDraftForm
          memoryTarget={memoryTarget}
          memoryTopic={memoryTopic}
          memoryContent={memoryContent}
          busy={busy}
          status={status}
          submitLabel={canSave ? "Save memory" : "Prepare memory draft"}
          setMemoryTarget={setMemoryTarget}
          setMemoryTopic={setMemoryTopic}
          setMemoryContent={setMemoryContent}
          onSubmit={onSubmit}
        />
      </section>
    );
  }
  const bucketLabel = bucketContext ? memoryBucketLabel(bucketContext) : "";
  const summaryLabel = bucketContext
    ? canSave ? `Add to ${bucketLabel}` : `Draft for ${bucketLabel}`
    : canSave ? "Add memory" : "Prepare memory draft";
  return (
    <details className="session-memory-write" open={open} onToggle={(event) => {
      setOpen(event.currentTarget.open);
      if (event.currentTarget.open) onOpen();
    }}>
      <summary>
        <strong>{summaryLabel}</strong>
        {bucketContext ? <span>{bucketContext.target}:{bucketContext.topic ?? ""}</span> : null}
      </summary>
      <MemoryDraftForm
        memoryTarget={memoryTarget}
        memoryTopic={memoryTopic}
        memoryContent={memoryContent}
        busy={busy}
        status={status}
        submitLabel={canSave ? "Save memory" : "Prepare memory draft"}
        setMemoryTarget={setMemoryTarget}
        setMemoryTopic={setMemoryTopic}
        setMemoryContent={setMemoryContent}
        onSubmit={onSubmit}
      />
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
  const targetOptions = [
    { value: "memory", label: "Session", description: "Project or task fact" },
    { value: "user", label: "User", description: "Stable preference" },
  ];
  const editorLocation = memoryFormLocation(memoryTarget, memoryTopic);
  const editorStats = memoryBodyStats(memoryContent);
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
      <div className="session-skill-form-body session-memory-form-body">
        <div className="session-skill-editor-head session-memory-editor-head" data-testid="session-memory-editor-meta">
          <span>Content</span>
          <code title={editorLocation}>{editorLocation}</code>
          <small>{editorStats}</small>
        </div>
        <textarea
          aria-label="Content"
          value={memoryContent}
          onChange={(event) => setMemoryContent(event.target.value)}
          placeholder="Fact to remember"
          disabled={busy}
        />
      </div>
      <button type="submit" className="session-skills-add-submit" disabled={!memoryContent.trim() || busy}>
        {busy ? "Saving" : submitLabel}
      </button>
      {status?.message ? (
        <span className="session-memory-form-status" data-tone={status.state === "error" ? "error" : "success"} role="status" aria-live="polite">{status.message}</span>
      ) : null}
    </form>
  );
}

function memoryFormLocation(target: string, topic: string): string {
  const cleanTarget = target.trim() || "memory";
  const cleanTopic = topic.trim() || (cleanTarget === "user" ? "user" : "general");
  return `${cleanTarget}:${cleanTopic}`;
}

function memoryBodyStats(body: string): string {
  const lineCount = body.length > 0 ? body.split("\n").length : 0;
  return `${lineCount} ${lineCount === 1 ? "line" : "lines"} · ${body.length} chars`;
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, { month: "short", day: "numeric", hour: "numeric", minute: "2-digit" });
}

function memoryBucketScopeValue(bucket: SessionMemoryBucket): string {
  if (bucket.target === "user") return "Shared user";
  if (bucket.topic === "core") return "Session core";
  return "Session topic";
}

function memoryFindingLabel(kind: ReturnType<typeof memoryReviewFindings>[number]["kind"]): string {
  if (kind === "sensitive") return "Secret";
  if (kind === "duplicate") return "Duplicate";
  if (kind === "capacity") return "Capacity";
  if (kind === "large") return "Large entry";
  return kind;
}

function memoryEntryKey(target: string, topic: string | undefined, entry: string): string {
  return `${target}:${topic ?? ""}:${entry}`;
}

function memoryUpdateBucketKey(update: MemoryUpdateMeta): string {
  if (update.target || update.topic) return `${update.target || "memory"}:${update.topic ?? ""}`;
  const location = memoryUpdateLocation(update);
  const [target, ...topicParts] = location.split(":");
  return `${target || "memory"}:${topicParts.join(":")}`;
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
