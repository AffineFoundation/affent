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
      detail: "This chat can search the web or open pages while answering.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "search + browser", tone: "ready" },
      { group: "Project tools", label: "files + shell", tone: "ready" },
      { group: "Workers", label: "subagents depth 2 + 4 focused tasks", tone: "ready" },
      { group: "Memory", label: "enabled", tone: "ready" },
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

    expect(view?.headline).toBe("Local project work");
    expect(view?.research).toBe("off");
    expect(view?.detail).toContain("cannot gather current web information");
    expect(view?.chips).toEqual(expect.arrayContaining([
      { group: "Research", label: "off", tone: "warning" },
      { group: "Project tools", label: "unavailable", tone: "muted" },
      { group: "Workers", label: "subagents depth 2 + 2 focused tasks", tone: "ready" },
      { group: "Memory", label: "enabled", tone: "ready" },
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
      headline: "Research tools limited",
      detail: "Some web access exists, but live search or page browsing is incomplete.",
      tone: "warning",
      research: "limited",
    });
    expect(view?.chips).toEqual([
      { group: "Research", label: "limited", tone: "warning" },
      { group: "Project tools", label: "files + shell", tone: "ready" },
      { group: "Workers", label: "single agent", tone: "muted" },
      { group: "Memory", label: "off", tone: "muted" },
    ]);
  });
});
