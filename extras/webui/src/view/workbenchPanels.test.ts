import { describe, expect, it } from "vitest";
import {
  shouldShowWorkbenchAccessPanel,
  shouldShowWorkbenchMemoryPanel,
  shouldShowWorkbenchRuntimePanel,
  shouldShowWorkbenchSkillsPanel,
} from "./workbenchPanels";

describe("workbench secondary panel visibility", () => {
  it("keeps empty secondary panels folded", () => {
    expect(shouldShowWorkbenchRuntimePanel({ state: "ready", stats: { model: "qwen-small", active_sessions: 0, running_turns: 0 } })).toBe(false);
    expect(shouldShowWorkbenchAccessPanel({ state: "ready", settings: { env: [], ssh: { exists: false } } })).toBe(false);
    expect(shouldShowWorkbenchMemoryPanel({ state: "ready", memory: { session_id: "s1", has_memory: false, topics: [] } })).toBe(false);
    expect(shouldShowWorkbenchSkillsPanel({ state: "ready", skills: [] })).toBe(false);
  });

  it("shows runtime only when it has diagnostic signal", () => {
    expect(shouldShowWorkbenchRuntimePanel({
      state: "ready",
      stats: {
        model: "qwen-small",
        running_turns: 1,
        aggregate: {
          blocked_by_type: 0,
          blocked_by_domain: 0,
          cache_hit: 0,
          cache_miss: 0,
          network_fetch: 0,
          input_tokens: 0,
          output_tokens: 0,
          turns: 0,
          tools: { tool_requests: 0, tool_errors: 0, source_access_results: 1 },
          runtime: { runtime_errors: 0, context_compactions: 1 },
        },
      },
    })).toBe(true);
  });

  it("shows second-level surfaces for configured access, memory updates, skills, loading, or errors", () => {
    expect(shouldShowWorkbenchAccessPanel({ state: "ready", settings: { env: [{ name: "GITHUB_TOKEN", configured: true }], ssh: { exists: false } } })).toBe(true);
    expect(shouldShowWorkbenchAccessPanel({ state: "loading" })).toBe(true);
    expect(shouldShowWorkbenchMemoryPanel(
      { state: "ready", memory: { session_id: "s1", has_memory: false, topics: [] } },
      { action: "add", target: "memory", topic: "repo", location: "memory:repo", preview: "Project fact" },
    )).toBe(true);
    expect(shouldShowWorkbenchSkillsPanel({ state: "error", error: "skills unavailable" })).toBe(true);
    expect(shouldShowWorkbenchSkillsPanel({ state: "ready", skills: [{ name: "repair", runtime: true, body_bytes: 32 }] })).toBe(true);
  });
});
