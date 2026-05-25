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
      detail: "Current web information can be gathered in this chat.",
      tone: "ready",
      research: "ready",
    });
    expect(view?.chips).toEqual([
      { label: "Can search web", tone: "ready" },
      { label: "Can open pages", tone: "ready" },
      { label: "Can delegate 2 levels", tone: "ready" },
      { label: "4 task helpers", tone: "ready" },
      { label: "Can use files", tone: "ready" },
      { label: "Memory available", tone: "ready" },
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
      { label: "No live search", tone: "warning" },
      { label: "No browser", tone: "warning" },
      { label: "Can delegate 2 levels", tone: "ready" },
      { label: "2 task helpers", tone: "ready" },
    ]));
  });
});
