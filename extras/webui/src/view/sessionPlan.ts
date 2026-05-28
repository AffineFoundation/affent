import type { SessionPlanSummary } from "../api/sessions";
import type { SessionState, ToolCallState } from "../store/sessionState";

export interface DerivedSessionPlan {
  plan: {
    version?: number;
    updated_at?: string;
    steps: DerivedPlanStep[];
    source: "tool_result";
  };
  summary: SessionPlanSummary;
}

export interface DerivedPlanStep {
  text: string;
  status: "completed" | "in_progress" | "blocked" | "pending";
  evidence?: string[];
  note?: string;
}

export function buildSessionPlanFromToolResults(session: SessionState): DerivedSessionPlan | undefined {
  let latest: DerivedSessionPlan | undefined;
  for (const turn of session.turns) {
    for (const call of turn.toolCalls) {
      const plan = planFromToolCall(call);
      if (plan === "cleared") latest = undefined;
      else if (plan) latest = plan;
    }
  }
  return latest;
}

function planFromToolCall(call: ToolCallState): DerivedSessionPlan | "cleared" | undefined {
  if (call.tool !== "plan" || call.status !== "success" || call.exitCode !== 0) return undefined;
  if (readCompactString(call.args.action) === "clear") return "cleared";
  const snapshot = parsePlanSnapshot(call.result) ?? parsePlanSnapshot(call.resultSummary);
  if (!snapshot) return undefined;
  if (snapshot.steps.length === 0) return "cleared";
  return {
    plan: {
      version: snapshot.version,
      updated_at: snapshot.updated_at,
      steps: snapshot.steps,
      source: "tool_result",
    },
    summary: summarizePlan(snapshot.steps),
  };
}

function parsePlanSnapshot(text?: string): { version?: number; updated_at?: string; steps: DerivedPlanStep[] } | undefined {
  if (!text?.trim()) return undefined;
  let parsed: unknown;
  try {
    parsed = JSON.parse(text);
  } catch {
    return undefined;
  }
  if (!parsed || typeof parsed !== "object") return undefined;
  const record = parsed as Record<string, unknown>;
  if (!Array.isArray(record.steps)) return undefined;
  const steps = record.steps.map(normalizeStep).filter((step): step is DerivedPlanStep => !!step);
  return {
    version: typeof record.version === "number" ? record.version : undefined,
    updated_at: typeof record.updated_at === "string" ? record.updated_at : undefined,
    steps,
  };
}

function normalizeStep(value: unknown): DerivedPlanStep | undefined {
  if (!value || typeof value !== "object") return undefined;
  const record = value as Record<string, unknown>;
  const text = readCompactString(record.text) ?? readCompactString(record.step);
  if (!text) return undefined;
  const evidence = Array.isArray(record.evidence)
    ? record.evidence.map(readCompactString).filter((item): item is string => !!item)
    : undefined;
  const note = readCompactString(record.note);
  return {
    text,
    status: normalizeStatus(readCompactString(record.status)),
    ...(evidence && evidence.length > 0 ? { evidence } : {}),
    ...(note ? { note } : {}),
  };
}

function normalizeStatus(status?: string): DerivedPlanStep["status"] {
  const value = status?.toLowerCase();
  if (value === "completed" || value === "in_progress" || value === "blocked" || value === "pending") return value;
  if (value === "done") return "completed";
  if (value === "active") return "in_progress";
  return "pending";
}

function summarizePlan(steps: readonly DerivedPlanStep[]): SessionPlanSummary {
  const total = steps.length;
  const completed = steps.filter((step) => step.status === "completed").length;
  const blockedIndex = steps.findIndex((step) => step.status === "blocked");
  const activeIndex = steps.findIndex((step) => step.status === "in_progress");
  const nextIndex = steps.findIndex((step) => step.status !== "completed");
  const currentIndex = activeIndex >= 0 ? activeIndex : blockedIndex >= 0 ? blockedIndex : nextIndex;
  const done = total > 0 && completed === total;
  const blocked = blockedIndex >= 0;
  const active = activeIndex >= 0;
  const current = currentIndex >= 0 ? steps[currentIndex] : undefined;
  const lastCompletedIndex = findLastIndex(steps, (step) => step.status === "completed");
  const labelState = done ? "done" : blocked ? "blocked" : active ? "active" : "pending";
  return {
    label: `plan:${completed}/${total}:${labelState}`,
    total_steps: total,
    completed_steps: completed,
    active,
    blocked,
    done,
    current_step: current?.text,
    current_step_index: current ? currentIndex + 1 : undefined,
    current_step_status: current?.status,
    last_completed_step: lastCompletedIndex >= 0 ? steps[lastCompletedIndex].text : undefined,
    last_completed_step_index: lastCompletedIndex >= 0 ? lastCompletedIndex + 1 : undefined,
    blocked_step: blockedIndex >= 0 ? steps[blockedIndex].text : undefined,
    blocked_step_index: blockedIndex >= 0 ? blockedIndex + 1 : undefined,
    error: false,
  };
}

function findLastIndex<T>(items: readonly T[], predicate: (item: T) => boolean): number {
  for (let index = items.length - 1; index >= 0; index -= 1) {
    if (predicate(items[index])) return index;
  }
  return -1;
}

function readCompactString(value: unknown): string | undefined {
  return typeof value === "string" ? value.replace(/\s+/g, " ").trim() || undefined : undefined;
}
