import { useMemo, useState, type FormEvent } from "react";
import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryResponse } from "../api/sessions";
import type { UseAsDraft } from "../view/draftSource";
import {
  memoryActionLabel,
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
  onUseAsDraft,
}: {
  memory?: SessionMemoryResponse;
  latestUpdate?: MemoryUpdateMeta;
  loading?: boolean;
  error?: string;
  noSession?: boolean;
  defaultOpen?: boolean;
  onUseAsDraft?: UseAsDraft;
}) {
  const [query, setQuery] = useState("");
  const [panelOpen, setPanelOpen] = useState(defaultOpen);
  const [memoryTarget, setMemoryTarget] = useState("memory");
  const [memoryTopic, setMemoryTopic] = useState("");
  const [memoryContent, setMemoryContent] = useState("");
  const buckets = useMemo(() => memoryBuckets(memory), [memory]);
  const filtered = useMemo(() => {
    const search = query.trim().toLowerCase();
    if (!search) return buckets;
    return buckets.filter((bucket) =>
      [memoryBucketLabel(bucket), bucket.target, bucket.topic, ...(bucket.entries ?? [])]
        .filter(Boolean)
        .join(" ")
        .toLowerCase()
        .includes(search),
    );
  }, [buckets, query]);
  const hasSearch = buckets.length > 0;
  const entryCount = buckets.reduce((sum, bucket) => sum + bucket.entry_count, 0);
  const topicCount = memory?.topics?.length ?? 0;
  const summary = noSession
    ? "Session memory unavailable"
    : loading
      ? "Loading memory"
      : error
        ? "Memory unavailable"
        : memory?.has_memory
          ? `${entryCount} ${entryCount === 1 ? "entry" : "entries"}`
          : "No durable memory";
  const summaryDetail = noSession
    ? "Open a saved chat before inspecting session memory."
    : loading
      ? "Reading durable buckets."
      : error
        ? panelErrorSummary("Memory API", error)
        : memory?.has_memory
          ? `${topicCount} ${topicCount === 1 ? "topic" : "topics"} · ${totalMemoryChars(buckets)} chars${memory.shared_user_memory ? " · shared user" : ""}`
          : "No user, core, or topic entries saved.";

  function handleManualMemorySubmit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const content = memoryContent.trim();
    if (!content || !onUseAsDraft) return;
    onUseAsDraft(manualMemoryDraft({ content, target: memoryTarget, topic: memoryTopic }), "memory");
  }

  return (
    <details
      className="session-skills-panel session-memory-panel"
      data-testid="session-memory-panel"
      open={panelOpen}
      onToggle={(event) => setPanelOpen(event.currentTarget.open)}
    >
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Memory</span>
        <strong>{summary}</strong>
        <span>{summaryDetail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? <div className="session-skills-empty">Loading session memory...</div> : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {!loading && !error && noSession ? <div className="session-skills-empty">Open a saved chat to inspect stored memory buckets.</div> : null}
        {!loading && !error && !noSession ? (
          <>
            {latestUpdate ? <LatestMemoryUpdate update={latestUpdate} onUseAsDraft={onUseAsDraft} /> : null}
            {hasSearch ? (
              <div className="session-skills-controls">
                <label className="session-skills-search">
                  <span>Search memory</span>
                  <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search entries or topics" />
                </label>
              </div>
            ) : null}
            <div className="session-skills-list" data-testid="session-memory-list">
              {filtered.length > 0 ? (
                filtered.map((bucket) => (
                  <details key={`${bucket.target}:${bucket.topic ?? ""}`} className="session-skill-item">
                    <summary>
                      <span className="session-skill-title">
                        <strong>{memoryBucketLabel(bucket)}</strong>
                        <span>{bucket.entry_count} entries</span>
                      </span>
                      <span className="session-skill-desc">{memoryBucketUsage(bucket)}</span>
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
                            <CopyButton label="Copy evidence" value={memoryBucketEvidenceText(bucket)} className="node-action" />
                            {onUseAsDraft ? (
                              <button type="button" className="node-action" onClick={() => onUseAsDraft(memoryBucketDraft(bucket), "memory")}>
                                Use memory as draft
                              </button>
                            ) : null}
                          </div>
                          <ul className="session-memory-entries">
                            {bucket.entries.map((entry, index) => (
                              <li key={`${index}:${entry}`}>{entry}</li>
                            ))}
                          </ul>
                        </>
                      ) : (
                        <p className="session-skill-preview">No entries in this bucket.</p>
                      )}
                    </div>
                  </details>
                ))
              ) : (
                <div className="session-skills-empty">{buckets.length > 0 ? "No matching memory." : "No memory buckets."}</div>
              )}
            </div>
            {onUseAsDraft ? (
              <form className="session-skill-form session-memory-form" data-testid="session-memory-form" onSubmit={handleManualMemorySubmit}>
                <label>
                  <span>Target</span>
                  <input value={memoryTarget} onChange={(event) => setMemoryTarget(event.target.value)} placeholder="memory" />
                </label>
                <label>
                  <span>Topic</span>
                  <input value={memoryTopic} onChange={(event) => setMemoryTopic(event.target.value)} placeholder="project, user, workflow" />
                </label>
                <label className="session-skill-form-body">
                  <span>Content</span>
                  <textarea value={memoryContent} onChange={(event) => setMemoryContent(event.target.value)} placeholder="Fact to remember" />
                </label>
                <button type="submit" className="session-skills-add-submit" disabled={!memoryContent.trim()}>
                  Use memory draft
                </button>
              </form>
            ) : null}
          </>
        ) : null}
      </div>
    </details>
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
            Use update as draft
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
