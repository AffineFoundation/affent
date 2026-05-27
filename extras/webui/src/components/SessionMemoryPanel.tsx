import { useMemo, useState } from "react";
import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryBucket, SessionMemoryResponse } from "../api/sessions";
import { CopyButton } from "./CopyButton";
import { panelErrorSummary } from "./panelErrorSummary";

export function SessionMemoryPanel({
  memory,
  latestUpdate,
  loading = false,
  error,
  noSession = false,
  defaultOpen = false,
}: {
  memory?: SessionMemoryResponse;
  latestUpdate?: MemoryUpdateMeta;
  loading?: boolean;
  error?: string;
  noSession?: boolean;
  defaultOpen?: boolean;
}) {
  const [query, setQuery] = useState("");
  const [panelOpen, setPanelOpen] = useState(defaultOpen);
  const buckets = useMemo(() => memoryBuckets(memory), [memory]);
  const filtered = useMemo(() => {
    const search = query.trim().toLowerCase();
    if (!search) return buckets;
    return buckets.filter((bucket) =>
      [bucketLabel(bucket), bucket.target, bucket.topic, ...(bucket.entries ?? [])]
        .filter(Boolean)
        .join(" ")
        .toLowerCase()
        .includes(search),
    );
  }, [buckets, query]);
  const entryCount = buckets.reduce((sum, bucket) => sum + bucket.entry_count, 0);
  const topicCount = memory?.topics?.length ?? 0;
  const summary = noSession
    ? "No chat selected"
    : loading
      ? "Loading memory"
      : error
        ? "Memory unavailable"
        : memory?.has_memory
          ? `${entryCount} ${entryCount === 1 ? "entry" : "entries"}`
          : "No durable memory";
  const summaryDetail = noSession
    ? "Select a chat to inspect its stored memory."
    : loading
      ? "Reading durable buckets."
      : error
        ? panelErrorSummary("Memory API", error)
        : memory?.has_memory
          ? `${topicCount} ${topicCount === 1 ? "topic" : "topics"} · ${totalChars(buckets)} chars${memory.shared_user_memory ? " · shared user" : ""}`
          : "No user, core, or topic entries saved.";

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
        {!loading && !error && noSession ? <div className="session-skills-empty">No selected chat.</div> : null}
        {!loading && !error && !noSession ? (
          <>
            {latestUpdate ? <LatestMemoryUpdate update={latestUpdate} /> : null}
            <div className="session-skills-controls">
              <label className="session-skills-search">
                <span>Search memory</span>
                <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="Search entries or topics" />
              </label>
            </div>
            <div className="session-skills-list" data-testid="session-memory-list">
              {filtered.length > 0 ? (
                filtered.map((bucket) => (
                  <details key={`${bucket.target}:${bucket.topic ?? ""}`} className="session-skill-item">
                    <summary>
                      <span className="session-skill-title">
                        <strong>{bucketLabel(bucket)}</strong>
                        <span>{bucket.entry_count} entries</span>
                      </span>
                      <span className="session-skill-desc">{bucketUsage(bucket)}</span>
                    </summary>
                    <div className="session-skill-detail">
                      <div className="session-skill-meta">
                        <span>{bucket.target}</span>
                        {bucket.newest_at ? <span>Updated {formatTimestamp(bucket.newest_at)}</span> : null}
                      </div>
                      {bucket.entries && bucket.entries.length > 0 ? (
                        <>
                          <CopyButton label="Copy" value={bucket.entries.join("\n\n")} className="node-action" />
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
          </>
        ) : null}
      </div>
    </details>
  );
}

function LatestMemoryUpdate({ update }: { update: MemoryUpdateMeta }) {
  const location = update.location || [update.target, update.topic].filter(Boolean).join(":");
  const preview = update.preview || update.next_preview || update.previous_preview || "";
  return (
    <div className="session-memory-latest" data-testid="session-memory-latest">
      <div>
        <strong>Latest update</strong>
        <span>{memoryActionLabel(update.action)}</span>
        {location ? <code>{location}</code> : null}
      </div>
      {preview ? <p>{preview}</p> : null}
    </div>
  );
}

function memoryBuckets(memory?: SessionMemoryResponse): SessionMemoryBucket[] {
  if (!memory) return [];
  const out: SessionMemoryBucket[] = [];
  if (memory.user) out.push(memory.user);
  if (memory.core) out.push(memory.core);
  out.push(...(memory.topics ?? []));
  return out;
}

function bucketLabel(bucket: SessionMemoryBucket): string {
  if (bucket.target === "user") return "User";
  if (bucket.topic === "core") return "Core";
  return bucket.topic || "General";
}

function bucketUsage(bucket: SessionMemoryBucket): string {
  const base = bucket.chars_limit ? `${bucket.chars_used}/${bucket.chars_limit} chars` : `${bucket.chars_used} chars`;
  return bucket.percent ? `${base} · ${bucket.percent}%` : base;
}

function memoryActionLabel(action: string): string {
  if (action === "add") return "Added";
  if (action === "replace") return "Replaced";
  if (action === "remove") return "Removed";
  return action;
}

function totalChars(buckets: readonly SessionMemoryBucket[]): number {
  return buckets.reduce((sum, bucket) => sum + bucket.chars_used, 0);
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
