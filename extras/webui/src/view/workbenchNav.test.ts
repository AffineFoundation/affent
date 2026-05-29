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
  it("keeps empty current-work sections out of the primary nav while preserving platform access", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: { summary: "No changed files", detail: "No changes", files: [] },
      run: { summary: "No commands", detail: "No commands", commands: [] },
      files: { summary: "No files", detail: "No files", items: [] },
      workspace: { hasData: false, summary: "No workspace evidence", shortStatus: "No workspace evidence", detail: "No workspace binding or command cwd recorded.", verification: "unknown" },
      runtimeState: { state: "idle" },
      configState: { state: "idle" },
      memoryState: { state: "idle" },
      skillsState: { state: "idle" },
    });

    expect(items.map((item) => item.key)).toEqual([
      "context",
      "loop",
      "memory",
      "skills",
      "config",
      "trace",
    ]);
    expect(items.find((item) => item.key === "context")).toMatchObject({ detail: "Current chat" });
    expect(items.find((item) => item.key === "loop")).toMatchObject({
      label: "Automation",
      detail: "Loop and timers",
      scope: "current",
    });
    expect(items.find((item) => item.key === "trace")).toMatchObject({ detail: "Runtime diagnostics" });
    expect(items.filter((item) => item.scope === "current").map((item) => item.key)).toEqual([
      "context",
      "loop",
    ]);
    expect(items.filter((item) => item.scope === "platform").map((item) => item.key)).toEqual([
      "memory",
      "skills",
      "config",
      "trace",
    ]);
  });

  it("surfaces only actionable counts and attention tones", () => {
    const items = buildWorkbenchNavItems({
      overview,
      usage: {
        totalTokens: 1540,
        trend: [{ label: "Turn 1", value: 1540, valueLabel: "0.0015M tokens", detail: "t1" }],
        items: [{ label: "Session tokens", value: "0.0015M tokens (0.0012M in / 0.0003M out)", detail: "1 turn from loaded trace" }],
      },
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
      artifacts: [{ path: ".affent/artifacts/test.log", name: "test.log", source: "npm test", summary: "checkout failure log", truncated: true, bytes: 4096 }],
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
        verification: "mismatch",
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

    expect(items.find((item) => item.key === "context")).toMatchObject({ detail: "0.0015M tokens" });
    expect(items.find((item) => item.key === "changes")).toMatchObject({ badge: "1" });
    expect(items.find((item) => item.key === "run")).toMatchObject({ badge: "1", tone: "error" });
    expect(items.find((item) => item.key === "artifacts")).toMatchObject({ badge: "1", detail: "1 artifact file · 1 full output · 4 KiB" });
    expect(items.find((item) => item.key === "workspace")).toMatchObject({ badge: "!" });
    expect(items.find((item) => item.key === "workspace")?.tone).toBeUndefined();
    expect(items.find((item) => item.key === "loop")).toMatchObject({
      label: "Automation",
      badge: "active",
      detail: "Loop waiting",
      scope: "current",
    });
    expect(items.find((item) => item.key === "memory")).toMatchObject({ badge: "updated", detail: "1 topics" });
    expect(items.find((item) => item.key === "skills")).toMatchObject({ badge: "1", detail: "1 reusable workflows" });
    expect(items.find((item) => item.key === "config")).toMatchObject({ badge: "1", detail: "1 env configured" });
    expect(items.find((item) => item.key === "trace")).toMatchObject({ badge: "1", detail: "qwen-small" });
  });

  it("uses current session trace evidence before runtime diagnostics when trace is loaded", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: { summary: "No changed files", detail: "No changes", files: [] },
      run: { summary: "No commands", detail: "No commands", commands: [] },
      files: { summary: "No files", detail: "No files", items: [] },
      workspace: { hasData: false, summary: "No workspace evidence", shortStatus: "No workspace evidence", detail: "No workspace binding or command cwd recorded.", verification: "unknown" },
      trace: {
        summary: "12 trace entries",
        detail: "5 grouped records · schema v1",
        eventCount: 12,
        toolRequests: { total: 0 },
        toolIssueCount: 0,
        toolIssues: [],
        recordCount: 5,
        metadataCount: 1,
        unknownCount: 0,
        schemaVersion: 1,
      },
      runtimeState: { state: "ready", stats: { model: "qwen-small", active_sessions: 1, running_turns: 0 } },
      configState: { state: "idle" },
      memoryState: { state: "idle" },
      skillsState: { state: "idle" },
    });

    expect(items.find((item) => item.key === "trace")).toMatchObject({
      scope: "current",
      badge: "12",
      detail: "5 records · schema v1",
    });
    expect(items.filter((item) => item.key === "trace")).toHaveLength(1);
  });

  it("keeps Files reachable while a workspace browser is open even without file evidence", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: { summary: "No changed files", detail: "No changes", files: [] },
      run: { summary: "No commands", detail: "No commands", commands: [] },
      files: { summary: "No files", detail: "No files", items: [] },
      workspaceBrowserActive: true,
      workspace: {
        hasData: true,
        summary: "affent bound",
        shortStatus: "affent",
        detail: "/repo/affent",
        verification: "bound",
        path: "/repo/affent",
      },
      runtimeState: { state: "idle" },
      configState: { state: "idle" },
      memoryState: { state: "idle" },
      skillsState: { state: "idle" },
    });

    expect(items.find((item) => item.key === "files")).toMatchObject({
      label: "Files",
      detail: "Workspace browser",
      scope: "current",
    });
  });

  it("keeps Files reachable for bound workspaces before the browser opens", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: { summary: "No changed files", detail: "No changes", files: [] },
      run: { summary: "No commands", detail: "No commands", commands: [] },
      files: { summary: "No files", detail: "No files", items: [] },
      workspaceBrowserActive: false,
      workspace: {
        hasData: true,
        summary: "affent bound",
        shortStatus: "affent",
        detail: "/repo/affent",
        verification: "bound",
        path: "/repo/affent",
      },
      runtimeState: { state: "idle" },
      configState: { state: "idle" },
      memoryState: { state: "idle" },
      skillsState: { state: "idle" },
    });

    expect(items.find((item) => item.key === "files")).toMatchObject({
      label: "Files",
      detail: "Workspace browser",
      scope: "current",
    });
  });

  it("keeps Workspace reachable when current work exists but no binding was recorded", () => {
    const items = buildWorkbenchNavItems({
      overview,
      changes: { summary: "No changed files", detail: "No changes", files: [] },
      run: {
        summary: "1 command",
        detail: "1 command",
        commands: [{ command: "git ls-remote git@github.com:team/repo.git HEAD", status: "failed", turnNumber: 1, exitCode: 128 }],
      },
      files: { summary: "No files", detail: "No files", items: [] },
      workspace: {
        hasData: false,
        summary: "No workspace evidence",
        shortStatus: "No workspace evidence",
        detail: "No workspace binding or command cwd recorded.",
        verification: "unknown",
      },
      runtimeState: { state: "idle" },
      configState: { state: "idle" },
      memoryState: { state: "idle" },
      skillsState: { state: "idle" },
    });

    expect(items.find((item) => item.key === "workspace")).toMatchObject({
      label: "Workspace",
      detail: "No binding evidence",
      badge: "?",
      scope: "current",
    });
  });

  it("maps attention targets to Workbench tabs", () => {
    expect(workbenchTabFromAttention("workspace")).toBe("workspace");
    expect(workbenchTabFromAttention("automation")).toBe("loop");
  });
});
