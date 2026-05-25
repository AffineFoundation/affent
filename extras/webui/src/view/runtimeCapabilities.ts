import type { SessionCapabilities } from "../api/sessions";

export type RuntimeCapabilityTone = "ready" | "muted" | "warning" | "unknown";

export interface RuntimeCapabilityChip {
  group: string;
  label: string;
  tone: RuntimeCapabilityTone;
}

export interface RuntimeCapabilityView {
  headline: string;
  detail: string;
  tone: RuntimeCapabilityTone;
  research: "ready" | "limited" | "off" | "unknown";
  chips: RuntimeCapabilityChip[];
}

export function buildRuntimeCapabilityView(caps?: SessionCapabilities, opts: { selectedSessionId?: string } = {}): RuntimeCapabilityView | undefined {
  if (!caps) {
    void opts;
    return undefined;
  }

  const externalReady = caps.web_search || caps.browser;
  const externalPartial = caps.web || caps.browser_screenshot;
  const chips: RuntimeCapabilityChip[] = [
    researchChip(caps),
    workChip(caps),
    delegationChip(caps),
    recallChip(caps),
  ];

  if (caps.eval_mode) chips.unshift({ group: "Mode", label: "evaluation", tone: "warning" });

  if (externalReady) {
    return {
      headline: "Web research available",
      detail: "Good for current sources, pages, prices, and news.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Limited research access",
      detail: "Some external fetching is available, but search or browsing may be missing.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: "No live web access",
    detail: "Best for local code and saved context; current outside information may be incomplete.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function researchChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.web_search && caps.browser) return { group: "Research", label: "Search and browser", tone: "ready" };
  if (caps.web_search) return { group: "Research", label: "Search only", tone: "ready" };
  if (caps.browser) return { group: "Research", label: "Browser only", tone: "ready" };
  if (caps.web || caps.browser_screenshot) return { group: "Research", label: "Fetch/screenshots only", tone: "warning" };
  return { group: "Research", label: "No live web", tone: "warning" };
}

function workChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  return caps.builtins
    ? { group: "Project", label: "Files and commands", tone: "ready" }
    : { group: "Project", label: "No local tools", tone: "muted" };
}

function delegationChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (!caps.subagent && !caps.focused_tasks) return { group: "Workers", label: "Single agent", tone: "muted" };
  const parts: string[] = [];
  if (caps.subagent) parts.push(caps.subagent_max_depth > 1 ? `Can delegate ${caps.subagent_max_depth} levels` : "Can delegate");
  if (caps.focused_tasks) parts.push(focusedTaskLabel(caps.focused_task_profiles));
  return { group: "Workers", label: parts.join(" + "), tone: "ready" };
}

function recallChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.memory && caps.session_search) return { group: "Recall", label: "Memory and past chats", tone: "ready" };
  if (caps.memory) return { group: "Recall", label: "Memory", tone: "ready" };
  if (caps.session_search) return { group: "Recall", label: "Past chats", tone: "ready" };
  return { group: "Recall", label: "Off", tone: "muted" };
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "Focused task types";
  return `${count} task types`;
}
