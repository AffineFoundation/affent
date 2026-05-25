import type { SessionCapabilities } from "../api/sessions";

export type RuntimeCapabilityTone = "ready" | "muted" | "warning" | "unknown";

export interface RuntimeCapabilityChip {
  group: string;
  label: string;
  detail: string;
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

  if (caps.eval_mode) {
    chips.unshift({
      group: "Mode",
      label: "Evaluation run",
      detail: "Behavior may be constrained for repeatable evals.",
      tone: "warning",
    });
  }

  if (externalReady) {
    return {
      headline: "Ready for web research",
      detail: "Can use live sources and project tools in this chat.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Research is limited",
      detail: "Can fetch some web content, but live search or browser control may be unavailable.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: "Local project mode",
    detail: "Best for code and saved context; current outside information may be incomplete.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function researchChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.web_search && caps.browser) {
    return { group: "Research", label: "Live sources", detail: "Search and browser are available.", tone: "ready" };
  }
  if (caps.web_search) {
    return { group: "Research", label: "Search available", detail: "Live search is available; browser control is off.", tone: "ready" };
  }
  if (caps.browser) {
    return { group: "Research", label: "Browser available", detail: "Browser control is available; live search is off.", tone: "ready" };
  }
  if (caps.web || caps.browser_screenshot) {
    return { group: "Research", label: "Fetch only", detail: "Can fetch pages or screenshots; search/browser may be missing.", tone: "warning" };
  }
  return { group: "Research", label: "Offline", detail: "No live web tools for current outside information.", tone: "warning" };
}

function workChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  return caps.builtins
    ? { group: "Project", label: "Files and shell", detail: "Can inspect files and run local commands.", tone: "ready" }
    : { group: "Project", label: "Read-only", detail: "Local file and shell tools are unavailable.", tone: "muted" };
}

function delegationChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (!caps.subagent && !caps.focused_tasks) {
    return { group: "Workers", label: "Single agent", detail: "No delegated workers for parallel or focused work.", tone: "muted" };
  }
  const parts: string[] = [];
  if (caps.subagent) parts.push(caps.subagent_max_depth > 1 ? `${caps.subagent_max_depth} levels` : "1 level");
  if (caps.focused_tasks) parts.push(focusedTaskLabel(caps.focused_task_profiles));
  return {
    group: "Workers",
    label: "Delegation on",
    detail: `Can hand off focused work (${parts.join(", ")}).`,
    tone: "ready",
  };
}

function recallChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.memory && caps.session_search) {
    return { group: "Recall", label: "Memory and chats", detail: "Can use saved memory and past chat search.", tone: "ready" };
  }
  if (caps.memory) return { group: "Recall", label: "Memory", detail: "Can use saved memory.", tone: "ready" };
  if (caps.session_search) return { group: "Recall", label: "Past chats", detail: "Can search previous chats.", tone: "ready" };
  return { group: "Recall", label: "Off", detail: "No memory or past chat search is available.", tone: "muted" };
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "focused task types";
  return `${count} focused task types`;
}
