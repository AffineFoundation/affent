import type { MemoryUpdateMeta } from "../api/events";
import type { SessionMemoryBucket, SessionMemoryResponse, SessionSummary } from "../api/sessions";
import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";

export interface SessionMemoryStats {
  bucketCount: number;
  entryCount: number;
  topicCount: number;
  charsUsed: number;
  charsLimit?: number;
  percent?: number;
  pressure: "empty" | "ok" | "watch" | "full";
}

export interface MemoryReviewFinding {
  kind: "sensitive" | "duplicate" | "capacity" | "large";
  bucketKey: string;
  bucketLabel: string;
  target: string;
  topic?: string;
  entryPreview?: string;
  detail: string;
}

export interface SessionMemoryCandidate {
  id: string;
  target: "memory" | "user";
  topic: string;
  content: string;
  source: string;
  reason: string;
}

export interface SessionMemoryCandidateInput {
  memory?: SessionMemoryResponse;
  session?: SessionSummary;
  changes?: SessionChangesView;
  files?: SessionFilesView;
}

export function memoryBuckets(memory?: SessionMemoryResponse): SessionMemoryBucket[] {
  if (!memory) return [];
  const out: SessionMemoryBucket[] = [];
  if (memory.user) out.push(memory.user);
  if (memory.core) out.push(memory.core);
  out.push(...(memory.topics ?? []));
  return out;
}

export function buildSessionMemoryCandidates(input: SessionMemoryCandidateInput): SessionMemoryCandidate[] {
  const existing = new Set(memoryBuckets(input.memory).flatMap((bucket) => bucket.entries ?? []).map(normalizeMemoryEntry).filter(Boolean));
  const candidates: SessionMemoryCandidate[] = [];
  const add = (candidate: SessionMemoryCandidate) => {
    const normalized = normalizeMemoryEntry(candidate.content);
    if (!normalized || existing.has(normalized) || candidates.some((item) => normalizeMemoryEntry(item.content) === normalized)) return;
    candidates.push(candidate);
  };
  const goal = durableGoal(input.session);
  if (goal) {
    add({
      id: "project-goal",
      target: "memory",
      topic: "project",
      content: `Project goal: ${goal}`,
      source: hasDurableLoopGoal(input.session) ? "Loop goal" : "Chat task",
      reason: "Useful for resuming this project without rereading the whole chat.",
    });
  }
  return candidates.slice(0, 4);
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

export function memoryBucketPreview(bucket: SessionMemoryBucket): string {
  const first = bucket.entries?.find((entry) => entry.trim());
  return first ? memoryEntrySafePreview(first) : "No entries in this bucket.";
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

export function memoryBucketKey(bucket: SessionMemoryBucket): string {
  return `${bucket.target}:${bucket.topic ?? ""}`;
}

export function memoryReviewFindings(memory?: SessionMemoryResponse): MemoryReviewFinding[] {
  const buckets = memoryBuckets(memory);
  const findings: MemoryReviewFinding[] = [];
  const duplicateEntries = duplicateMemoryEntries(buckets);
  buckets.forEach((bucket) => {
    const bucketKey = memoryBucketKey(bucket);
    const bucketLabel = memoryBucketLabel(bucket);
    if (bucket.percent !== undefined && bucket.percent >= 70) {
      findings.push({
        kind: "capacity",
        bucketKey,
        bucketLabel,
        target: bucket.target,
        topic: bucket.topic,
        detail: `${bucket.percent}% capacity used`,
      });
    }
    (bucket.entries ?? []).forEach((entry) => {
      const normalized = normalizeMemoryEntry(entry);
      if (!normalized) return;
      if (looksSensitive(entry)) {
        findings.push({
          kind: "sensitive",
          bucketKey,
          bucketLabel,
          target: bucket.target,
          topic: bucket.topic,
          entryPreview: redactSensitivePreview(entry),
          detail: "possible secret or credential",
        });
      }
      if (duplicateEntries.has(normalized)) {
        findings.push({
          kind: "duplicate",
          bucketKey,
          bucketLabel,
          target: bucket.target,
          topic: bucket.topic,
          entryPreview: entryPreview(entry),
          detail: "duplicate entry",
        });
      }
      if (entry.length >= 1000) {
        findings.push({
          kind: "large",
          bucketKey,
          bucketLabel,
          target: bucket.target,
          topic: bucket.topic,
          entryPreview: entryPreview(entry),
          detail: `${entry.length} chars; consider splitting`,
        });
      }
    });
  });
  return findings.slice(0, 20);
}

export function memoryBucketsNeedingReview(memory?: SessionMemoryResponse): Set<string> {
  return new Set(memoryReviewFindings(memory).map((finding) => finding.bucketKey));
}

export function memoryBucketMatchesQuery(bucket: SessionMemoryBucket, query: string): boolean {
  return memoryBucketSearchText(bucket).includes(query.trim().toLowerCase());
}

export function memoryBucketMatchingEntries(bucket: SessionMemoryBucket, query: string): string[] {
  const search = query.trim().toLowerCase();
  if (!search) return bucket.entries ?? [];
  return (bucket.entries ?? []).filter((entry) => entry.toLowerCase().includes(search));
}

export function memoryEntryIsSensitive(entry: string): boolean {
  return looksSensitive(entry);
}

export function memoryEntrySafePreview(entry: string): string {
  return looksSensitive(entry) ? redactSensitivePreview(entry) : entryPreview(entry);
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

export function memorySuggestionDraft(memory?: SessionMemoryResponse): string {
  const lines = [
    "Find durable memory candidates from the current chat.",
    "",
    "Only propose facts that are stable, useful in future sessions, and non-secret.",
    "For each candidate include target, topic, content, and why it should or should not be saved.",
    "Do not save memory yet unless I explicitly confirm.",
  ];
  if (memory) {
    const stats = memoryStats(memory);
    lines.push("");
    lines.push(`Current memory: ${stats.entryCount} ${stats.entryCount === 1 ? "entry" : "entries"} · ${memoryUsageLabel(stats)}`);
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

function memoryPressure(percent: number | undefined, bucketCount: number): SessionMemoryStats["pressure"] {
  if (bucketCount === 0) return "empty";
  if (percent === undefined) return "ok";
  if (percent >= 90) return "full";
  if (percent >= 70) return "watch";
  return "ok";
}

function durableGoal(session: SessionSummary | undefined): string | undefined {
  const raw = [
    session?.loop_state?.initial_goal_preview,
    session?.loop_protocol?.state?.initial_goal_preview,
  ].find((value) => value?.trim())?.trim();
  if (!raw) return undefined;
  const compact = raw.replace(/\s+/g, " ");
  if (compact.length < 12) return undefined;
  if (/^(?:push|done|ok|yes|no|continue|继续|可以|好的|推了吗|push了吗)[？?。!.]*$/i.test(compact)) return undefined;
  return compactMemoryCandidate(compact, 220);
}

function hasDurableLoopGoal(session: SessionSummary | undefined): boolean {
  return Boolean(session?.loop_state?.initial_goal_preview?.trim() || session?.loop_protocol?.state?.initial_goal_preview?.trim());
}

function compactMemoryCandidate(value: string, maxLength: number): string {
  const compact = value.replace(/\s+/g, " ").trim();
  if (compact.length <= maxLength) return compact;
  return `${compact.slice(0, maxLength - 3).trimEnd()}...`;
}

function memoryBucketSearchText(bucket: SessionMemoryBucket): string {
  return [memoryBucketLabel(bucket), bucket.target, bucket.topic, ...(bucket.entries ?? [])]
    .filter(Boolean)
    .join(" ")
    .toLowerCase();
}

function duplicateMemoryEntries(buckets: readonly SessionMemoryBucket[]): Set<string> {
  const counts = new Map<string, number>();
  buckets.forEach((bucket) => {
    (bucket.entries ?? []).forEach((entry) => {
      const normalized = normalizeMemoryEntry(entry);
      if (!normalized) return;
      counts.set(normalized, (counts.get(normalized) ?? 0) + 1);
    });
  });
  return new Set(Array.from(counts).filter(([, count]) => count > 1).map(([entry]) => entry));
}

function normalizeMemoryEntry(entry: string): string {
  return entry.trim().replace(/\s+/g, " ").toLowerCase();
}

function looksSensitive(entry: string): boolean {
  return /\b(api[_-]?key|access[_-]?token|auth[_-]?token|password|passwd|secret|private key|ssh-rsa|BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY)\b/i.test(entry);
}

function entryPreview(entry: string): string {
  const compact = entry.trim().replace(/\s+/g, " ");
  return compact.length > 120 ? `${compact.slice(0, 119).trimEnd()}...` : compact;
}

function redactSensitivePreview(entry: string): string {
  const compact = entryPreview(entry);
  return compact
    .replace(/((?:api[_-]?key|access[_-]?token|auth[_-]?token|password|passwd|secret)\s*[:=]\s*)\S+/gi, "$1[redacted]")
    .replace(/(BEGIN (?:OPENSSH |RSA |EC )?PRIVATE KEY)[\s\S]*/i, "$1 [redacted]");
}
