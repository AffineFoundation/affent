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
      label: "Eval constraints",
      detail: "Some choices may be fixed for repeatable runs.",
      tone: "warning",
    });
  }

  if (externalReady) {
    return {
      headline: "Ready for current research",
      detail: "Live sources and project tools are available for this chat.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Current research needs direct sources",
      detail: "Project work is available; outside info may need URLs or files from you.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: caps.builtins ? "Project work ready" : "Chat-only mode",
    detail: caps.builtins
      ? "Good for code and saved context; current outside information may be incomplete."
      : "Current outside information and local project tools may be unavailable.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function researchChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.web_search && caps.browser) {
    return { group: "Web", label: "Search + browser", detail: "Can discover and inspect current sources.", tone: "ready" };
  }
  if (caps.web_search) {
    return { group: "Web", label: "Search available", detail: "Can discover current sources; browser control is off.", tone: "ready" };
  }
  if (caps.browser) {
    return { group: "Web", label: "Browser available", detail: "Can inspect pages; live search is off.", tone: "ready" };
  }
  if (caps.web || caps.browser_screenshot) {
    return { group: "Web", label: "Direct sources", detail: "Can inspect provided URLs; discovery may be limited.", tone: "warning" };
  }
  return { group: "Web", label: "Not available", detail: "Current outside information may be incomplete.", tone: "warning" };
}

function workChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  return caps.builtins
    ? { group: "Project", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" }
    : { group: "Project", label: "Unavailable", detail: "Local file and command tools are off.", tone: "muted" };
}

function delegationChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (!caps.subagent && !caps.focused_tasks) {
    return { group: "Agents", label: "Single thread", detail: "No delegated workers for parallel or focused work.", tone: "muted" };
  }
  const parts: string[] = [];
  if (caps.subagent) parts.push(caps.subagent_max_depth > 1 ? `${caps.subagent_max_depth} levels` : "1 level");
  if (caps.focused_tasks) parts.push(focusedTaskLabel(caps.focused_task_profiles));
  return {
    group: "Agents",
    label: "Subtasks available",
    detail: `Can delegate focused work (${parts.join(", ")}).`,
    tone: "ready",
  };
}

function recallChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.memory && caps.session_search) {
    return { group: "Context", label: "Memory + chats", detail: "Can use saved memory and past chat search.", tone: "ready" };
  }
  if (caps.memory) return { group: "Context", label: "Memory", detail: "Can use saved memory.", tone: "ready" };
  if (caps.session_search) return { group: "Context", label: "Past chats", detail: "Can search previous chats.", tone: "ready" };
  return { group: "Context", label: "No recall", detail: "No memory or past chat search is available.", tone: "muted" };
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "focused task types";
  return `${count} focused task types`;
}
