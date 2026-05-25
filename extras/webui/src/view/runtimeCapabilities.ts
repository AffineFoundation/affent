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
    caps.memory
      ? { group: "Memory", label: "enabled", tone: "ready" }
      : { group: "Memory", label: "off", tone: "muted" },
  ];

  if (caps.eval_mode) chips.unshift({ group: "Mode", label: "evaluation", tone: "warning" });

  if (externalReady) {
    return {
      headline: "Ready for web research",
      detail: "This chat can search the web or open pages while answering.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Research tools limited",
      detail: "Some web access exists, but live search or page browsing is incomplete.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: "Local project work",
    detail: "This chat can use local tools, but cannot gather current web information.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function researchChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.web_search && caps.browser) return { group: "Research", label: "search + browser", tone: "ready" };
  if (caps.web_search) return { group: "Research", label: "search only", tone: "ready" };
  if (caps.browser) return { group: "Research", label: "browser only", tone: "ready" };
  if (caps.web || caps.browser_screenshot) return { group: "Research", label: "limited", tone: "warning" };
  return { group: "Research", label: "off", tone: "warning" };
}

function workChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  return caps.builtins
    ? { group: "Project tools", label: "files + shell", tone: "ready" }
    : { group: "Project tools", label: "unavailable", tone: "muted" };
}

function delegationChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (!caps.subagent && !caps.focused_tasks) return { group: "Workers", label: "single agent", tone: "muted" };
  const parts: string[] = [];
  if (caps.subagent) parts.push(caps.subagent_max_depth > 1 ? `subagents depth ${caps.subagent_max_depth}` : "subagents");
  if (caps.focused_tasks) parts.push(focusedTaskLabel(caps.focused_task_profiles));
  return { group: "Workers", label: parts.join(" + "), tone: "ready" };
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "focused tasks";
  return `${count} focused tasks`;
}
