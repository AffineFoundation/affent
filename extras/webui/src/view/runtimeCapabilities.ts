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
      headline: "Capabilities not confirmed",
      detail: "This saved chat has no capability snapshot yet.",
      tone: "unknown",
      research: "unknown",
      chips: [],
    };
  }

  const externalReady = caps.web_search || caps.browser;
  const externalPartial = caps.web || caps.browser_screenshot;
  const chips: RuntimeCapabilityChip[] = [
    researchChip(caps),
  ];
  const skills = skillsChip(caps);
  if (skills) chips.push(skills);
  chips.push(scheduleChip(caps));
  chips.push(workChip(caps));
  const discovery = discoveryChip(caps);
  if (discovery) chips.push(discovery);
  chips.push(delegationChip(caps));
  chips.push(recallChip(caps));

  if (caps.eval_mode) {
    chips.push({
      group: "Mode",
      label: caps.eval_all_tools ? "Eval · all tools" : "Eval constraints",
      detail: evalModeDetail(caps),
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
      : "Local project tools may be unavailable, and live sources are off.",
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
  if (caps.builtins) {
    return { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" };
  }
  const workspaceTools = caps.workspace_tools?.filter(Boolean) ?? [];
  if (workspaceTools.length > 0) {
    return {
      group: "Files",
      label: "Partial workspace",
      detail: `Available: ${workspaceTools.join(", ")}.`,
      tone: "warning",
    };
  }
  return { group: "Files", label: "Unavailable", detail: "Local file and command tools are off.", tone: "muted" };
}

function skillsChip(caps: SessionCapabilities): RuntimeCapabilityChip | undefined {
  return caps.skill_install
    ? { group: "Skills", label: "Skill install", detail: "Can install and activate runtime skills without restarting.", tone: "ready" }
    : undefined;
}

function scheduleChip(caps: SessionCapabilities): RuntimeCapabilityChip {
  if (caps.session_schedule_runner) {
    return { group: "Automation", label: "Background schedules", detail: "Server-owned scheduled turns keep running without an open Workbench.", tone: "ready" };
  }
  return caps.session_schedule
    ? { group: "Automation", label: "Session schedules", detail: "Can create future and recurring turns without requiring LOOP.md.", tone: "ready" }
    : { group: "Automation", label: "Schedules unavailable", detail: "Future and recurring turns cannot be created from this tool surface.", tone: "muted" };
}

function discoveryChip(caps: SessionCapabilities): RuntimeCapabilityChip | undefined {
  if (caps.symbol_context && caps.repo_search) {
    return {
      group: "Discovery",
      label: "Symbol index + repo search",
      detail: "Can locate declarations and search workspace text before broad file reads.",
      tone: "ready",
    };
  }
  if (caps.symbol_context) {
    return {
      group: "Discovery",
      label: "Symbol index",
      detail: "Can locate Go declarations and signatures before broad file reads.",
      tone: "ready",
    };
  }
  return caps.repo_search
    ? { group: "Discovery", label: "Repo search", detail: "Can search workspace text before broad file reads.", tone: "ready" }
    : undefined;
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

function focusedTaskLabel(profiles?: readonly string[]): string {
  const count = profiles?.length ?? 0;
  if (count === 0) return "focused task types";
  return `${count} focused task types`;
}

function evalModeDetail(caps: SessionCapabilities): string {
  if (caps.eval_all_tools) return "Full tool surface is enabled for this repeatable run.";
  const tools = caps.eval_tools?.trim();
  if (!tools) return "Tools are disabled by default unless explicitly enabled.";
  return `Allowed tools: ${tools}.`;
}
