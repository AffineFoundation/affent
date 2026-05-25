import type { SessionCapabilities } from "../api/sessions";

export type RuntimeCapabilityTone = "ready" | "muted" | "warning" | "unknown";

export interface RuntimeCapabilityChip {
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
      ? { label: "Memory on", tone: "ready" }
      : { label: "Memory off", tone: "muted" },
  ];

  if (caps.eval_mode) chips.unshift({ label: "Evaluation run", tone: "warning" });

  if (externalReady) {
    return {
      headline: "Research ready",
      detail: "Live search and page browsing are available for current information.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Research limited",
      detail: "Some web tools are available, but live search or page browsing is missing.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: "Local work only",
    detail: "This chat can work locally, but cannot gather current web information.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function researchChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.web_search && caps.browser) return { label: "Research: search + browser", tone: "ready" };
  if (caps.web_search) return { label: "Research: search only", tone: "ready" };
  if (caps.browser) return { label: "Research: browser only", tone: "ready" };
  if (caps.web || caps.browser_screenshot) return { label: "Research: limited", tone: "warning" };
  return { label: "Research: off", tone: "warning" };
}

function workChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  return caps.builtins
    ? { label: "Files ready", tone: "ready" }
    : { label: "Files unavailable", tone: "muted" };
}

function delegationChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (!caps.subagent && !caps.focused_tasks) return { label: "Single worker", tone: "muted" };
  const parts: string[] = [];
  if (caps.subagent) parts.push(caps.subagent_max_depth > 1 ? `${caps.subagent_max_depth} levels` : "delegation");
  if (caps.focused_tasks) parts.push(focusedTaskLabel(caps.focused_task_profiles));
  return { label: `Delegation: ${parts.join(" + ")}`, tone: "ready" };
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "helpers";
  return `${count} helpers`;
}
