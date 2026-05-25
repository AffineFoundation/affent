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
      headline: "Research ready",
      detail: "Live search and page browsing are available for current information.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { label: "Research: search + browser", tone: "ready" },
      { label: "Files ready", tone: "ready" },
      { label: "Delegation: 2 levels + 4 helpers", tone: "ready" },
      { label: "Memory on", tone: "ready" },
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

    expect(view?.headline).toBe("Local work only");
    expect(view?.research).toBe("off");
    expect(view?.detail).toContain("cannot gather current web information");
    expect(view?.chips).toEqual(expect.arrayContaining([
      { label: "Research: off", tone: "warning" },
      { label: "Files unavailable", tone: "muted" },
      { label: "Delegation: 2 levels + 2 helpers", tone: "ready" },
      { label: "Memory on", tone: "ready" },
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
      headline: "Research limited",
      detail: "Some web tools are available, but live search or page browsing is missing.",
      tone: "warning",
      research: "limited",
    });
    expect(view?.chips).toEqual([
      { label: "Research: limited", tone: "warning" },
      { label: "Files ready", tone: "ready" },
      { label: "Single worker", tone: "muted" },
      { label: "Memory off", tone: "muted" },
    ]);
  });
});
