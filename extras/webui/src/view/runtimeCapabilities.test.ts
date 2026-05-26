import { describe, expect, it } from "vitest";
import { buildRuntimeCapabilityView } from "./runtimeCapabilities";

describe("buildRuntimeCapabilityView", () => {
  it("shows an unknown runtime for saved sessions without an active capability snapshot", () => {
    expect(buildRuntimeCapabilityView(undefined, { selectedSessionId: "saved-1" })).toMatchObject({
      headline: "Capabilities not confirmed",
      detail: "This saved chat has no capability snapshot yet.",
      tone: "unknown",
      research: "unknown",
    });
  });

  it("stays absent before any real session exists", () => {
    expect(buildRuntimeCapabilityView(undefined)).toBeUndefined();
  });

  it("summarizes a research-ready runtime without exposing protocol details", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: true,
      skill_install: true,
      plan: false,
      memory: true,
      session_search: false,
      symbol_context: true,
      repo_search: true,
      browser: true,
      browser_screenshot: false,
      web: true,
      web_search: true,
      subagent: true,
      subagent_max_depth: 2,
      focused_tasks: true,
      focused_task_profiles: ["recall", "explore", "verify", "review"],
    });

    expect(view).toMatchObject({
      headline: "Ready for current research",
      detail: "Live sources and project tools are available for this chat.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "Web search + browser", detail: "Can find and inspect current sources.", tone: "ready" },
      { group: "Skills", label: "Skill install", detail: "Can install and activate runtime skills without restarting.", tone: "ready" },
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Discovery", label: "Symbol index + repo search", detail: "Can locate declarations and search workspace text before broad file reads.", tone: "ready" },
      { group: "Subtasks", label: "Nested work", detail: "Can delegate focused work (2 levels, 4 focused task types).", tone: "ready" },
      { group: "Context", label: "Saved memory", detail: "Can use saved memory.", tone: "ready" },
    ]);
  });

  it("omits the skills chip when skill install is unavailable", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: true,
      skill_install: false,
      plan: false,
      memory: true,
      session_search: false,
      symbol_context: false,
      repo_search: true,
      browser: true,
      browser_screenshot: false,
      web: true,
      web_search: true,
      subagent: false,
      subagent_max_depth: 1,
      focused_tasks: false,
    });

    expect(view?.chips.some((chip) => chip.group === "Skills")).toBe(false);
  });

  it("warns before a live research task when external tools are off", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: false,
      skill_install: false,
      plan: false,
      memory: true,
      session_search: false,
      symbol_context: false,
      repo_search: false,
      browser: false,
      browser_screenshot: false,
      web: false,
      web_search: false,
      subagent: true,
      subagent_max_depth: 2,
      focused_tasks: true,
      focused_task_profiles: ["recall", "explore"],
    });

    expect(view?.headline).toBe("Chat-only mode");
    expect(view?.research).toBe("off");
    expect(view?.detail).toContain("Local project tools may be unavailable");
    expect(view?.chips).toEqual(expect.arrayContaining([
      { group: "Research", label: "No live sources", detail: "Current outside information may be incomplete.", tone: "warning" },
      { group: "Files", label: "Unavailable", detail: "Local file and command tools are off.", tone: "muted" },
      { group: "Subtasks", label: "Nested work", detail: "Can delegate focused work (2 levels, 2 focused task types).", tone: "ready" },
      { group: "Context", label: "Saved memory", detail: "Can use saved memory.", tone: "ready" },
    ]));
  });

  it("keeps project-ready sessions task-oriented when web research is unavailable", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: true,
      skill_install: false,
      plan: false,
      memory: true,
      session_search: false,
      symbol_context: false,
      repo_search: false,
      browser: false,
      browser_screenshot: false,
      web: false,
      web_search: false,
      subagent: false,
      subagent_max_depth: 1,
      focused_tasks: false,
    });

    expect(view).toMatchObject({
      headline: "Project work ready",
      detail: "Good for code and saved context; current outside information may be incomplete.",
      research: "off",
      tone: "warning",
    });
  });

  it("groups partial research access into one readable warning", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: true,
      skill_install: false,
      plan: false,
      memory: false,
      session_search: false,
      symbol_context: false,
      repo_search: false,
      browser: false,
      browser_screenshot: true,
      web: true,
      web_search: false,
      subagent: false,
      subagent_max_depth: 1,
      focused_tasks: false,
    });

    expect(view).toMatchObject({
      headline: "Current research needs direct sources",
      detail: "Project work is available; outside info may need URLs or files from you.",
      tone: "warning",
      research: "limited",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "Direct URLs", detail: "Can inspect provided URLs; discovery may be limited.", tone: "warning" },
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Subtasks", label: "Single thread", detail: "No delegated workers for parallel or focused work.", tone: "muted" },
      { group: "Context", label: "No saved context", detail: "No memory or past chat search is available.", tone: "muted" },
    ]);
  });

  it("keeps eval constraints as a secondary capability instead of front-loading them", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: true,
      builtins: true,
      skill_install: false,
      plan: false,
      memory: true,
      session_search: true,
      symbol_context: false,
      repo_search: false,
      browser: true,
      browser_screenshot: false,
      web: true,
      web_search: true,
      subagent: false,
      subagent_max_depth: 1,
      focused_tasks: false,
    });

    expect(view?.chips[view!.chips.length - 1]).toEqual({
      group: "Mode",
      label: "Eval constraints",
      detail: "Some choices may be fixed for repeatable runs.",
      tone: "warning",
    });
    expect(view?.chips[0]).toEqual({
      group: "Research",
      label: "Web search + browser",
      detail: "Can find and inspect current sources.",
      tone: "ready",
    });
  });
});
