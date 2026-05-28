import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryBucket, SessionMemoryResponse } from "../api/sessions";

export interface SessionMemoryStats {
  bucketCount: number;
  entryCount: number;
  topicCount: number;
  charsUsed: number;
  charsLimit?: number;
  percent?: number;
  pressure: "empty" | "ok" | "watch" | "full";
}

export function memoryBuckets(memory?: SessionMemoryResponse): SessionMemoryBucket[] {
  if (!memory) return [];
  const out: SessionMemoryBucket[] = [];
  if (memory.user) out.push(memory.user);
  if (memory.core) out.push(memory.core);
  out.push(...(memory.topics ?? []));
  return out;
}

export function memoryStats(memory?: SessionMemoryResponse): SessionMemoryStats {
  const buckets = memoryBuckets(memory);
  const charsUsed = totalMemoryChars(buckets);
  const limits = buckets.map((bucket) => bucket.chars_limit ?? 0).filter((limit) => limit > 0);
  const charsLimit = limits.length > 0 ? limits.reduce((sum, limit) => sum + limit, 0) : undefined;
  const explicitPercent = buckets.map((bucket) => bucket.percent ?? 0).filter((percent) => percent > 0);
  const percent = charsLimit
    ? Math.min(100, Math.round((charsUsed / charsLimit) * 100))
    : explicitPercent.length > 0
      ? Math.max(...explicitPercent)
      : undefined;
  return {
    bucketCount: buckets.length,
    entryCount: buckets.reduce((sum, bucket) => sum + bucket.entry_count, 0),
    topicCount: memory?.topics?.length ?? 0,
    charsUsed,
    charsLimit,
    percent,
    pressure: memoryPressure(percent, buckets.length),
  };
}

export function memoryBucketLabel(bucket: SessionMemoryBucket): string {
  if (bucket.target === "user") return "User";
  if (bucket.topic === "core") return "Core";
  return bucket.topic || "General";
}

export function memoryBucketUsage(bucket: SessionMemoryBucket): string {
  const base = bucket.chars_limit ? `${bucket.chars_used}/${bucket.chars_limit} chars` : `${bucket.chars_used} chars`;
  return bucket.percent ? `${base} · ${bucket.percent}%` : base;
}

export function memoryUsageLabel(stats: SessionMemoryStats): string {
  if (stats.charsLimit) return `${stats.charsUsed}/${stats.charsLimit} chars`;
  return `${stats.charsUsed} chars`;
}

export function memoryPressureLabel(stats: SessionMemoryStats): string {
  if (stats.pressure === "empty") return "No pressure";
  if (stats.percent === undefined) return "Capacity unknown";
  if (stats.pressure === "full") return `${stats.percent}% used`;
  if (stats.pressure === "watch") return `${stats.percent}% used`;
  return `${stats.percent}% used`;
}

export function memoryScopeLabel(memory?: SessionMemoryResponse): string {
  if (!memory) return "No snapshot";
  return memory.shared_user_memory ? "Shared user + session" : "Session scoped";
}

export function memoryBucketMatchesQuery(bucket: SessionMemoryBucket, query: string): boolean {
  return memoryBucketSearchText(bucket).includes(query.trim().toLowerCase());
}

export function memoryBucketMatchingEntries(bucket: SessionMemoryBucket, query: string): string[] {
  const search = query.trim().toLowerCase();
  if (!search) return bucket.entries ?? [];
  return (bucket.entries ?? []).filter((entry) => entry.toLowerCase().includes(search));
}

export function memoryActionLabel(action: string): string {
  if (action === "add") return "Added";
  if (action === "replace") return "Replaced";
  if (action === "remove") return "Removed";
  return action;
}

export function memoryUpdateLocation(update: MemoryUpdateMeta): string {
  return update.location || [update.target, update.topic].filter(Boolean).join(":");
}

export function memoryUpdatePreview(update: MemoryUpdateMeta): string {
  return update.preview || update.next_preview || update.previous_preview || "";
}

export function memoryUpdateEvidenceText(update: MemoryUpdateMeta): string {
  const location = memoryUpdateLocation(update);
  const preview = memoryUpdatePreview(update);
  const lines = [
    "Memory update evidence",
    `Action: ${memoryActionLabel(update.action)}`,
  ];
  if (location) lines.push(`Location: ${location}`);
  if (update.target) lines.push(`Target: ${update.target}`);
  if (update.topic) lines.push(`Topic: ${update.topic}`);
  if (preview) lines.push(`Preview: ${preview}`);
  return lines.join("\n");
}

export function memoryUpdateDraft(update: MemoryUpdateMeta): string {
  return [
    "Review this memory update and decide whether it should be kept, corrected, or used in the next step:",
    "",
    memoryUpdateEvidenceText(update),
  ].join("\n");
}

export function memoryBucketEvidenceText(bucket: SessionMemoryBucket): string {
  const lines = [
    `Memory bucket evidence for ${memoryBucketLabel(bucket)}`,
    `Target: ${bucket.target}`,
  ];
  if (bucket.topic) lines.push(`Topic: ${bucket.topic}`);
  lines.push(`Entries: ${bucket.entry_count}`);
  lines.push(`Usage: ${memoryBucketUsage(bucket)}`);
  if (bucket.newest_at) lines.push(`Updated: ${bucket.newest_at}`);
  if (bucket.entries && bucket.entries.length > 0) {
    lines.push("Content:");
    lines.push(...bucket.entries.map((entry) => `- ${entry}`));
  }
  return lines.join("\n");
}

export function memorySnapshotEvidenceText(memory: SessionMemoryResponse): string {
  const stats = memoryStats(memory);
  const buckets = memoryBuckets(memory);
  const lines = [
    "Memory snapshot evidence",
    `Session: ${memory.session_id}`,
    `Scope: ${memoryScopeLabel(memory)}`,
    `Entries: ${stats.entryCount}`,
    `Buckets: ${stats.bucketCount}`,
    `Topics: ${stats.topicCount}`,
    `Usage: ${memoryUsageLabel(stats)}`,
  ];
  if (stats.percent !== undefined) lines.push(`Capacity: ${stats.percent}% used`);
  if (buckets.length === 0) {
    lines.push("No durable memory entries are saved.");
    return lines.join("\n");
  }
  lines.push("");
  lines.push(...buckets.map(memoryBucketEvidenceText).join("\n\n").split("\n"));
  return lines.join("\n");
}

export function memorySnapshotDraft(memory: SessionMemoryResponse): string {
  return [
    "Use this durable memory snapshot as context for the next step. Treat stale or irrelevant entries as candidates to correct:",
    "",
    memorySnapshotEvidenceText(memory),
  ].join("\n");
}

export function memoryBucketDraft(bucket: SessionMemoryBucket): string {
  return [
    "Use this memory evidence to continue the chat. Verify whether it is relevant, stale, or needs correction:",
    "",
    memoryBucketEvidenceText(bucket),
  ].join("\n");
}

export function manualMemoryDraft({
  content,
  target = "memory",
  topic,
}: {
  content: string;
  target?: string;
  topic?: string;
}): string {
  const lines = [
    "Add or update durable memory if this is useful, accurate, and non-secret:",
    "",
    `Target: ${target.trim() || "memory"}`,
    topic?.trim() ? `Topic: ${topic.trim()}` : undefined,
    "Content:",
    content.trim(),
  ];
  return lines.filter((line): line is string => Boolean(line)).join("\n");
}

export function totalMemoryChars(buckets: readonly SessionMemoryBucket[]): number {
  return buckets.reduce((sum, bucket) => sum + bucket.chars_used, 0);
}

function memoryPressure(percent: number | undefined, bucketCount: number): SessionMemoryStats["pressure"] {
  if (bucketCount === 0) return "empty";
  if (percent === undefined) return "ok";
  if (percent >= 90) return "full";
  if (percent >= 70) return "watch";
  return "ok";
}

function memoryBucketSearchText(bucket: SessionMemoryBucket): string {
  return [memoryBucketLabel(bucket), bucket.target, bucket.topic, ...(bucket.entries ?? [])]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}
