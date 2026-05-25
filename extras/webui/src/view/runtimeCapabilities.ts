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
    if (!opts.selectedSessionId) return undefined;
    return {
      headline: "Capability snapshot unknown",
      detail: "This saved chat has not loaded a capability snapshot yet.",
      tone: "unknown",
      research: "unknown",
      chips: unknownChips(),
    };
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
      headline: "Research and project tools ready",
      detail: "Web search, browser, files, and subtasks are available in this chat.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Research needs direct sources",
      detail: "Project tools are available; live discovery may need URLs or files from you.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: caps.builtins ? "Project tools ready" : "Chat-only mode",
    detail: caps.builtins
      ? "Files and commands are available; current outside information may be incomplete."
      : "Files, commands, and live sources are unavailable here.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function researchChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.web_search && caps.browser) {
    return { group: "Research", label: "Web search + browser", detail: "Can find and inspect current sources.", tone: "ready" };
  }
  if (caps.web_search) {
    return { group: "Research", label: "Web search only", detail: "Can find current sources; browser control is off.", tone: "ready" };
  }
  if (caps.browser) {
    return { group: "Research", label: "Browser only", detail: "Can inspect pages; live search is off.", tone: "ready" };
  }
  if (caps.web || caps.browser_screenshot) {
    return { group: "Research", label: "Direct URLs", detail: "Can inspect provided URLs; discovery may be limited.", tone: "warning" };
  }
  return { group: "Research", label: "No live sources", detail: "Current outside information may be incomplete.", tone: "warning" };
}

function workChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  return caps.builtins
    ? { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" }
    : { group: "Files", label: "Unavailable", detail: "Local file and command tools are off.", tone: "muted" };
}

function delegationChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (!caps.subagent && !caps.focused_tasks) {
    return { group: "Subtasks", label: "Single thread", detail: "No delegated workers for parallel or focused work.", tone: "muted" };
  }
  const parts: string[] = [];
  if (caps.subagent) parts.push(caps.subagent_max_depth > 1 ? `${caps.subagent_max_depth} levels` : "1 level");
  if (caps.focused_tasks) parts.push(focusedTaskLabel(caps.focused_task_profiles));
  return {
    group: "Subtasks",
    label: "Nested work",
    detail: `Can delegate focused work (${parts.join(", ")}).`,
    tone: "ready",
  };
}

function recallChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.memory && caps.session_search) {
    return { group: "Context", label: "Memory + chats", detail: "Can use saved memory and past chat search.", tone: "ready" };
  }
  if (caps.memory) return { group: "Context", label: "Saved memory", detail: "Can use saved memory.", tone: "ready" };
  if (caps.session_search) return { group: "Context", label: "Past chats", detail: "Can search previous chats.", tone: "ready" };
  return { group: "Context", label: "No saved context", detail: "No memory or past chat search is available.", tone: "muted" };
}

function unknownChips(): RuntimeCapabilityChip[] {
  return [
    { group: "Research", label: "Unknown", detail: "Current research capability is not confirmed yet.", tone: "unknown" },
    { group: "Files", label: "Unknown", detail: "File and command access is not confirmed yet.", tone: "unknown" },
    { group: "Subtasks", label: "Unknown", detail: "Delegation capability is not confirmed yet.", tone: "unknown" },
    { group: "Context", label: "Unknown", detail: "Memory and chat search are not confirmed yet.", tone: "unknown" },
  ];
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "focused task types";
  return `${count} focused task types`;
}
