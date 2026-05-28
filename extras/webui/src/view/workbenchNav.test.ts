import { describe, expect, it } from "vitest";
import type { SessionOverview } from "./sessionOverview";
import { buildWorkbenchNavItems, workbenchTabFromAttention } from "./workbenchNav";

const overview = {
  headline: "Checkout test failed",
  detail: "Open run evidence.",
  stateLabel: "Review needed",
  tone: "warning",
  active: false,
  metrics: [],
} as SessionOverview;

describe("buildWorkbenchNavItems", () => {
  it("keeps stable Workbench sections with short factual empty states", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: { summary: "No changed files", detail: "No changes", files: [] },
      run: { summary: "No commands", detail: "No commands", commands: [] },
      files: { summary: "No files", detail: "No files", items: [] },
      workspace: { hasData: false, summary: "No workspace evidence", shortStatus: "No workspace evidence", detail: "No workspace binding or command cwd recorded." },
      runtimeState: { state: "idle" },
      configState: { state: "idle" },
      memoryState: { state: "idle" },
      skillsState: { state: "idle" },
    });

    expect(items.map((item) => item.key)).toEqual([
      "context",
      "changes",
      "run",
      "files",
      "workspace",
      "automation",
      "memory",
      "skills",
      "config",
      "trace",
    ]);
    expect(items.find((item) => item.key === "context")).toMatchObject({ detail: "Review needed" });
    expect(items.find((item) => item.key === "changes")).toMatchObject({ detail: "Changed file review" });
    expect(items.find((item) => item.key === "run")).toMatchObject({ detail: "Command history" });
    expect(items.find((item) => item.key === "files")).toMatchObject({ detail: "Task file evidence" });
    expect(items.find((item) => item.key === "workspace")).toMatchObject({ detail: "No binding evidence" });
    expect(items.find((item) => item.key === "automation")).toMatchObject({ detail: "Loop and timers" });
    expect(items.find((item) => item.key === "trace")).toMatchObject({ detail: "Runtime diagnostics" });
  });

  it("surfaces only actionable counts and attention tones", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: {
        summary: "1 changed file",
        detail: "1 changed",
        files: [{ path: "src/payments.ts", operation: "edit", status: "changed", turnNumber: 1, actionCount: 1 }],
      },
      run: {
        summary: "1 failed command",
        detail: "1 failed",
        tone: "error",
        commands: [{ command: "npm test -- checkout.spec.ts", status: "failed", turnNumber: 1, exitCode: 1 }],
      },
      files: {
        summary: "1 file reference",
        detail: "1 read",
        items: [{ path: "src/payments.ts", actions: ["read"], status: "available", turnNumber: 1, actionCount: 1 }],
      },
      workspace: {
        hasData: true,
        summary: "affent",
        shortStatus: "affent",
        detail: "/repo/affent · branch main · dirty",
        label: "affent",
        path: "/repo/affent",
        issue: "Latest command cwd is outside the session workspace.",
        tone: "warning",
      },
      automation: { title: "Loop waiting" },
      attention: { target: "run", label: "1 failed command · View run", detail: "npm test failed", tone: "error" },
      runtimeState: {
        state: "ready",
        stats: {
          model: "qwen-small",
          running_turns: 0,
          aggregate: {
            blocked_by_type: 0,
            blocked_by_domain: 0,
            cache_hit: 0,
            cache_miss: 0,
            network_fetch: 0,
            input_tokens: 0,
            output_tokens: 0,
            turns: 1,
            tools: { tool_requests: 1, tool_errors: 1 },
            runtime: { runtime_errors: 0 },
          },
        },
      },
      configState: { state: "ready", settings: { env: [{ name: "GITHUB_TOKEN", configured: true }], ssh: { exists: false } } },
      memoryState: { state: "ready", memory: { session_id: "s1", has_memory: true, topics: [{ target: "memory", topic: "checkout", entries: [], entry_count: 0, chars_used: 0 }] } },
      skillsState: { state: "ready", skills: [{ name: "checkout_repair", runtime: true, body_bytes: 128 }] },
      latestMemoryUpdate: { action: "add", target: "memory", topic: "checkout", location: "memory:checkout", preview: "payment fixture" },
    });

    expect(items.find((item) => item.key === "changes")).toMatchObject({ badge: "1" });
    expect(items.find((item) => item.key === "run")).toMatchObject({ badge: "1", tone: "error" });
    expect(items.find((item) => item.key === "workspace")).toMatchObject({ badge: "!", tone: "warning" });
    expect(items.find((item) => item.key === "automation")).toMatchObject({ badge: "active", detail: "Loop waiting" });
    expect(items.find((item) => item.key === "memory")).toMatchObject({ badge: "updated", detail: "1 topics" });
    expect(items.find((item) => item.key === "skills")).toMatchObject({ badge: "1", detail: "1 reusable workflows" });
    expect(items.find((item) => item.key === "config")).toMatchObject({ badge: "1", detail: "1 env configured" });
    expect(items.find((item) => item.key === "trace")).toMatchObject({ badge: "1", detail: "qwen-small" });
  });

  it("maps attention targets to Workbench tabs", () => {
    expect(workbenchTabFromAttention("workspace")).toBe("workspace");
    expect(workbenchTabFromAttention("automation")).toBe("automation");
  });
});
