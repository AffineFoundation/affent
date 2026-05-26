import type { ToolCallState, TurnState } from "../store/sessionState";

export type MemoryUpdateAction = "add" | "replace" | "remove";

export interface MemoryUpdateSummary {
  action: MemoryUpdateAction;
  label: string;
  target: string;
  topic: string;
  location: string;
  preview: string;
  previousPreview?: string;
  nextPreview?: string;
}

export function describeMemoryUpdate(call: ToolCallState): MemoryUpdateSummary | undefined {
  if (call.memoryUpdate) return summaryFromMeta(call.memoryUpdate);
  if (call.tool !== "memory") return undefined;
  if (call.status !== "success" || call.exitCode !== 0) return undefined;
  const response = parseMemoryResponse(call.result);
  if (!response?.ok) return undefined;

  const action = stringArg(call, "action")?.toLowerCase();
  if (action !== "add" && action !== "replace" && action !== "remove") return undefined;

  const target = response.target ?? stringArg(call, "target") ?? "memory";
  const topic = normalizeMemoryTopic(target, response.topic ?? stringArg(call, "topic"));
  const oldText = stringArg(call, "old_text");
  const newText = stringArg(call, "content");
  const previousPreview = oldText ? summarize(oldText, 120) : undefined;
  const nextPreview = newText ? summarize(newText, 120) : undefined;
  const preview = memoryUpdatePreview(action, previousPreview, nextPreview);

  const summary: MemoryUpdateSummary = {
    action,
    label: memoryUpdateLabel(action),
    target,
    topic,
    location: `${target}:${topic}`,
    preview,
  };
  if (previousPreview) summary.previousPreview = previousPreview;
  if (nextPreview) summary.nextPreview = nextPreview;
  return summary;
}

function summaryFromMeta(meta: NonNullable<ToolCallState["memoryUpdate"]>): MemoryUpdateSummary | undefined {
  if (meta.action !== "add" && meta.action !== "replace" && meta.action !== "remove") return undefined;
  const target = meta.target || "memory";
  const topic = normalizeMemoryTopic(target, meta.topic);
  const summary: MemoryUpdateSummary = {
    action: meta.action,
    label: memoryUpdateLabel(meta.action),
    target,
    topic,
    location: meta.location || `${target}:${topic}`,
    preview: meta.preview || "No content supplied",
  };
  if (meta.previous_preview) summary.previousPreview = meta.previous_preview;
  if (meta.next_preview) summary.nextPreview = meta.next_preview;
  return summary;
}

export function memoryUpdatesForTurn(turn: TurnState): MemoryUpdateSummary[] {
  return turn.toolCalls.flatMap((call) => {
    const summary = describeMemoryUpdate(call);
    return summary ? [summary] : [];
  });
}

function parseMemoryResponse(raw: string | undefined): { ok?: boolean; target?: string; topic?: string } | undefined {
  if (!raw) return undefined;
  try {
    const parsed = JSON.parse(raw) as unknown;
    if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) return undefined;
    const obj = parsed as Record<string, unknown>;
    return {
      ok: obj.ok === true,
      target: typeof obj.target === "string" && obj.target.trim() ? obj.target.trim() : undefined,
      topic: typeof obj.topic === "string" && obj.topic.trim() ? obj.topic.trim() : undefined,
    };
  } catch {
    return undefined;
  }
}

function memoryUpdateLabel(action: MemoryUpdateAction): string {
  switch (action) {
    case "add":
      return "Saved memory";
    case "replace":
      return "Updated memory";
    case "remove":
      return "Removed memory";
  }
}

function memoryUpdatePreview(action: MemoryUpdateAction, previousPreview: string | undefined, nextPreview: string | undefined): string {
  switch (action) {
    case "add":
      return nextPreview ?? "No content supplied";
    case "replace":
      if (previousPreview && nextPreview) return `${previousPreview} -> ${nextPreview}`;
      return nextPreview ?? previousPreview ?? "No content supplied";
    case "remove":
      return previousPreview ?? "No content supplied";
  }
}

function normalizeMemoryTopic(target: string, topic: string | undefined): string {
  if (target === "user") return "user";
  return topic?.trim() || "general";
}

function stringArg(call: ToolCallState, key: string): string | undefined {
  const value = call.args[key];
  return typeof value === "string" && value.trim() ? value.trim() : undefined;
}

function summarize(text: string, limit: number): string {
  const compact = text.replace(/\s+/g, " ").trim();
  if (compact.length <= limit) return compact;
  return `${compact.slice(0, Math.max(0, limit - 1))}...`;
}
