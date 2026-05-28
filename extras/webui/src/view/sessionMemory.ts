import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryBucket, SessionMemoryResponse } from "../api/sessions";

export function memoryBuckets(memory?: SessionMemoryResponse): SessionMemoryBucket[] {
  if (!memory) return [];
  const out: SessionMemoryBucket[] = [];
  if (memory.user) out.push(memory.user);
  if (memory.core) out.push(memory.core);
  out.push(...(memory.topics ?? []));
  return out;
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
