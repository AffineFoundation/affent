export type ContextCompactionSummaryState = "present" | "missing" | "empty" | "unknown";

type ContextCompactionSummaryLike = {
  summary_present?: unknown;
  summary_bytes?: unknown;
  summary_preview?: unknown;
};

export function contextCompactionSummaryState(compaction: unknown): ContextCompactionSummaryState {
  if (!isContextCompactionSummaryLike(compaction)) return "unknown";
  const hasBytes = typeof compaction.summary_bytes === "number" && compaction.summary_bytes > 0;
  const hasPreview = typeof compaction.summary_preview === "string" && Boolean(compaction.summary_preview.trim());
  if (hasBytes || hasPreview) return "present";
  if (compaction.summary_present === false) return "missing";
  if (compaction.summary_present === true) return "empty";
  return "unknown";
}

export function contextCompactionSummaryLabel(compaction: unknown): string | undefined {
  const state = contextCompactionSummaryState(compaction);
  if (state === "missing") return "summary missing";
  if (state === "empty") return "summary empty";
  return undefined;
}

function isContextCompactionSummaryLike(value: unknown): value is ContextCompactionSummaryLike {
  return typeof value === "object" && value !== null;
}
