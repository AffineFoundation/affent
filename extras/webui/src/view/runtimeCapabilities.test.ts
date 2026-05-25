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
      headline: "Web research available",
      detail: "Good for current sources, pages, prices, and news.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "Search and browser", tone: "ready" },
      { group: "Project", label: "Files and commands", tone: "ready" },
      { group: "Workers", label: "Can delegate 2 levels + 4 task types", tone: "ready" },
      { group: "Recall", label: "Memory", tone: "ready" },
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

    expect(view?.headline).toBe("No live web access");
    expect(view?.research).toBe("off");
    expect(view?.detail).toContain("current outside information may be incomplete");
    expect(view?.chips).toEqual(expect.arrayContaining([
      { group: "Research", label: "No live web", tone: "warning" },
      { group: "Project", label: "No local tools", tone: "muted" },
      { group: "Workers", label: "Can delegate 2 levels + 2 task types", tone: "ready" },
      { group: "Recall", label: "Memory", tone: "ready" },
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
      headline: "Limited research access",
      detail: "Some external fetching is available, but search or browsing may be missing.",
      tone: "warning",
      research: "limited",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "Fetch/screenshots only", tone: "warning" },
      { group: "Project", label: "Files and commands", tone: "ready" },
      { group: "Workers", label: "Single agent", tone: "muted" },
      { group: "Recall", label: "Off", tone: "muted" },
    ]);
  });
});
