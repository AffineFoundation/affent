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
      headline: "Ready for current research",
      detail: "Live sources and project tools are available for this chat.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { group: "Web", label: "Search + browser", detail: "Can discover and inspect current sources.", tone: "ready" },
      { group: "Project", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Agents", label: "Subtasks available", detail: "Can delegate focused work (2 levels, 4 focused task types).", tone: "ready" },
      { group: "Context", label: "Memory", detail: "Can use saved memory.", tone: "ready" },
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

    expect(view?.headline).toBe("Chat-only mode");
    expect(view?.research).toBe("off");
    expect(view?.detail).toContain("local project tools may be unavailable");
    expect(view?.chips).toEqual(expect.arrayContaining([
      { group: "Web", label: "Not available", detail: "Current outside information may be incomplete.", tone: "warning" },
      { group: "Project", label: "Unavailable", detail: "Local file and command tools are off.", tone: "muted" },
      { group: "Agents", label: "Subtasks available", detail: "Can delegate focused work (2 levels, 2 focused task types).", tone: "ready" },
      { group: "Context", label: "Memory", detail: "Can use saved memory.", tone: "ready" },
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
      { group: "Web", label: "Direct sources", detail: "Can inspect provided URLs; discovery may be limited.", tone: "warning" },
      { group: "Project", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Agents", label: "Single thread", detail: "No delegated workers for parallel or focused work.", tone: "muted" },
      { group: "Context", label: "No recall", detail: "No memory or past chat search is available.", tone: "muted" },
    ]);
  });
});
