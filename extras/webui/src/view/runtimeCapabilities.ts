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
    caps.web_search
      ? { label: "Web search", tone: "ready" }
      : { label: "Web search off", tone: "warning" },
    caps.browser
      ? { label: "Browser", tone: "ready" }
      : { label: "Browser off", tone: externalReady ? "muted" : "warning" },
    caps.subagent
      ? { label: `Subagents depth ${caps.subagent_max_depth || 1}`, tone: "ready" }
      : { label: "Subagents off", tone: "muted" },
    caps.focused_tasks
      ? { label: focusedTaskLabel(caps.focused_task_profiles), tone: "ready" }
      : { label: "Focused tasks off", tone: "muted" },
    caps.builtins
      ? { label: "Files + shell", tone: "ready" }
      : { label: "Files off", tone: "muted" },
    caps.memory
      ? { label: "Memory", tone: "ready" }
      : { label: "Memory off", tone: "muted" },
  ];

  if (caps.eval_mode) chips.unshift({ label: "Eval mode", tone: "warning" });

  if (externalReady) {
    return {
      headline: "Research ready",
      detail: "External research tools are available for live tasks.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Limited research",
      detail: "Some web tools are available, but live search or browsing is not fully enabled.",
      tone: "warning",
      research: "limited",
      chips,
    };
  }

  return {
    headline: "Local runtime",
    detail: "Web search and browser are off; research tasks may need runtime configuration.",
    tone: "warning",
    research: "off",
    chips,
  };
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "Focused tasks";
  return `${count} focused tasks`;
}
