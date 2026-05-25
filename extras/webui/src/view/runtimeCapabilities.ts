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
      ? { label: "Can search web", tone: "ready" }
      : { label: "No live search", tone: "warning" },
    caps.browser
      ? { label: "Can open pages", tone: "ready" }
      : { label: "No browser", tone: externalReady ? "muted" : "warning" },
    caps.subagent
      ? { label: splitWorkLabel(caps.subagent_max_depth), tone: "ready" }
      : { label: "Single worker", tone: "muted" },
    caps.focused_tasks
      ? { label: focusedTaskLabel(caps.focused_task_profiles), tone: "ready" }
      : { label: "No task helpers", tone: "muted" },
    caps.builtins
      ? { label: "Can use files", tone: "ready" }
      : { label: "Files unavailable", tone: "muted" },
    caps.memory
      ? { label: "Memory available", tone: "ready" }
      : { label: "No memory", tone: "muted" },
  ];

  if (caps.eval_mode) chips.unshift({ label: "Evaluation run", tone: "warning" });

  if (externalReady) {
    return {
      headline: "Research ready",
      detail: "Current web information can be gathered in this chat.",
      tone: "ready",
      research: "ready",
      chips,
    };
  }

  if (externalPartial) {
    return {
      headline: "Current web access limited",
      detail: "Some web access exists, but live search or page browsing is unavailable.",
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

function splitWorkLabel(depth = 1): string {
  return depth > 1 ? `Can delegate ${depth} levels` : "Can delegate";
}

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "Task helpers";
  return `${count} task helpers`;
}
