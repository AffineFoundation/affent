import type { ToolCallState } from "../store/sessionState";

export type MemoryUpdateAction = "add" | "replace" | "remove";

export interface MemoryUpdateSummary {
  action: MemoryUpdateAction;
  label: string;
  target: string;
  topic: string;
  location: string;
  preview: string;
}

export function describeMemoryUpdate(call: ToolCallState): MemoryUpdateSummary | undefined {
  if (call.tool !== "memory") return undefined;
  const action = stringArg(call, "action")?.toLowerCase();
  if (action !== "add" && action !== "replace" && action !== "remove") return undefined;

  const target = stringArg(call, "target") ?? "memory";
  const topic = normalizeMemoryTopic(target, stringArg(call, "topic"));
  const content = action === "remove"
    ? stringArg(call, "old_text")
    : stringArg(call, "content");
  const preview = summarize(content ?? "No content supplied", 180);

  return {
    action,
    label: memoryUpdateLabel(action),
    target,
    topic,
    location: `${target}:${topic}`,
    preview,
  };
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
