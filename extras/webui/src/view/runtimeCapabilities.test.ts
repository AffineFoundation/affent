import { describe, expect, it } from "vitest";
import { buildRuntimeCapabilityView } from "./runtimeCapabilities";

describe("buildRuntimeCapabilityView", () => {
  it("stays hidden for saved sessions without an active capability snapshot", () => {
    expect(buildRuntimeCapabilityView(undefined, { selectedSessionId: "saved-1" })).toBeUndefined();
  });

  it("stays absent before any real session exists", () => {
    expect(buildRuntimeCapabilityView(undefined)).toBeUndefined();
  });

  it("summarizes a research-ready runtime without exposing protocol details", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: true,
      skill_install: false,
      plan: false,
      memory: true,
      session_search: false,
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
      headline: "Ready for web research",
      detail: "Can use live sources and project tools in this chat.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "Live sources", detail: "Search and browser are available.", tone: "ready" },
      { group: "Project", label: "Files and shell", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Workers", label: "Delegation on", detail: "Can hand off focused work (2 levels, 4 focused task types).", tone: "ready" },
      { group: "Recall", label: "Memory", detail: "Can use saved memory.", tone: "ready" },
    ]);
  });

  it("warns before a live research task when external tools are off", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: false,
      skill_install: false,
      plan: false,
      memory: true,
      session_search: false,
      browser: false,
      browser_screenshot: false,
      web: false,
      web_search: false,
      subagent: true,
      subagent_max_depth: 2,
      focused_tasks: true,
      focused_task_profiles: ["recall", "explore"],
    });

    expect(view?.headline).toBe("Local project mode");
    expect(view?.research).toBe("off");
    expect(view?.detail).toContain("current outside information may be incomplete");
    expect(view?.chips).toEqual(expect.arrayContaining([
      { group: "Research", label: "Offline", detail: "No live web tools for current outside information.", tone: "warning" },
      { group: "Project", label: "Read-only", detail: "Local file and shell tools are unavailable.", tone: "muted" },
      { group: "Workers", label: "Delegation on", detail: "Can hand off focused work (2 levels, 2 focused task types).", tone: "ready" },
      { group: "Recall", label: "Memory", detail: "Can use saved memory.", tone: "ready" },
    ]));
  });

  it("groups partial research access into one readable warning", () => {
    const view = buildRuntimeCapabilityView({
      eval_mode: false,
      builtins: true,
      skill_install: false,
      plan: false,
      memory: false,
      session_search: false,
      browser: false,
      browser_screenshot: true,
      web: true,
      web_search: false,
      subagent: false,
      subagent_max_depth: 1,
      focused_tasks: false,
    });

    expect(view).toMatchObject({
      headline: "Research is limited",
      detail: "Can fetch some web content, but live search or browser control may be unavailable.",
      tone: "warning",
      research: "limited",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "Fetch only", detail: "Can fetch pages or screenshots; search/browser may be missing.", tone: "warning" },
      { group: "Project", label: "Files and shell", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Workers", label: "Single agent", detail: "No delegated workers for parallel or focused work.", tone: "muted" },
      { group: "Recall", label: "Off", detail: "No memory or past chat search is available.", tone: "muted" },
    ]);
  });
});
