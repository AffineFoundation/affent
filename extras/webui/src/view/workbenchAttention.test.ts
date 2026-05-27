import { describe, expect, it } from "vitest";
import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import type { SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import { buildWorkbenchAttention } from "./workbenchAttention";

describe("buildWorkbenchAttention", () => {
  it("prioritizes failed commands over recovery hints and changed files", () => {
    expect(buildWorkbenchAttention({
      overview: overview({ metrics: [{ label: "Recovery", value: "rerun tests", tone: "warning" }] }),
      files: files(),
      changes: changes({ changed: 2 }),
      run: run({ failed: 1 }),
    })).toEqual({ label: "1 failed command", detail: "View run", tone: "error", target: "run" });
  });

  it("uses recovery when there is no failed Workbench surface", () => {
    expect(buildWorkbenchAttention({
      overview: overview({ metrics: [{ label: "Recovery", value: "rerun tests", tone: "warning" }] }),
      files: files({ available: 1 }),
      changes: changes({ changed: 2 }),
      run: run(),
    })).toEqual({ label: "Recovery hint", detail: "rerun tests", tone: "warning", target: "context" });
  });

  it("opens workspace when the recorded command cwd is outside the session workspace", () => {
    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files(),
      changes: changes(),
      run: run(),
      workspace: {
        hasData: true,
        summary: "Workspace mismatch",
        detail: "/repo/affent · cwd /tmp",
        tone: "warning",
        issue: "Latest command cwd is outside the session workspace.",
      },
    })).toEqual({
      label: "Workspace mismatch",
      detail: "Latest command cwd is outside the session workspace.",
      tone: "warning",
      target: "workspace",
    });
  });

  it("uses changed files as the lowest attention badge and ignores read-only files", () => {
    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files({ available: 2 }),
      changes: changes({ changed: 3 }),
      run: run(),
    })).toEqual({ label: "3 changed files", detail: "Review changes", tone: "attention", target: "changes" });

    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files({ available: 2 }),
      changes: changes(),
      run: run(),
    })).toBeUndefined();
  });

  it("opens automation for pending loop or timer work without badging normal running automation", () => {
    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files(),
      changes: changes(),
      run: run(),
      automation: { title: "Loop waiting · 1 timer pending", detail: "Long-running protocol and timers share one automation surface" },
    })).toEqual({
      label: "Loop waiting · 1 timer pending",
      detail: "Long-running protocol and timers share one automation surface",
      tone: "warning",
      target: "automation",
    });

    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files(),
      changes: changes(),
      run: run(),
      automation: { title: "Loop running · 1 timer active", detail: "Long-running protocol and timers share one automation surface" },
    })).toBeUndefined();
  });
});

function overview(overrides: Partial<SessionOverview> = {}): SessionOverview {
  return {
    stateLabel: "Chat ready",
    headline: "checkout route",
    detail: "checkout route",
    tone: "ready",
    active: false,
    metrics: [],
    ...overrides,
  };
}

function files(counts: { available?: number; failed?: number; running?: number } = {}): SessionFilesView {
  return {
    items: [
      ...Array.from({ length: counts.available ?? 0 }, (_, index) => ({ path: `src/read-${index}.ts`, actions: ["read" as const], status: "available" as const, turnNumber: 1, actionCount: 1 })),
      ...Array.from({ length: counts.failed ?? 0 }, (_, index) => ({ path: `src/missing-${index}.ts`, actions: ["read" as const], status: "failed" as const, turnNumber: 1, actionCount: 1 })),
      ...Array.from({ length: counts.running ?? 0 }, (_, index) => ({ path: `src/running-${index}.ts`, actions: ["read" as const], status: "running" as const, turnNumber: 1, actionCount: 1 })),
    ],
    summary: "Files",
    detail: "Files",
  };
}

function changes(counts: { changed?: number; failed?: number; running?: number } = {}): SessionChangesView {
  return {
    files: [
      ...Array.from({ length: counts.changed ?? 0 }, (_, index) => ({ path: `src/changed-${index}.ts`, operation: "edit" as const, status: "changed" as const, turnNumber: 1, actionCount: 1 })),
      ...Array.from({ length: counts.failed ?? 0 }, (_, index) => ({ path: `src/failed-${index}.ts`, operation: "edit" as const, status: "failed" as const, turnNumber: 1, actionCount: 1 })),
      ...Array.from({ length: counts.running ?? 0 }, (_, index) => ({ path: `src/running-${index}.ts`, operation: "edit" as const, status: "running" as const, turnNumber: 1, actionCount: 1 })),
    ],
    summary: "Changes",
    detail: "Changes",
  };
}

function run(counts: { failed?: number; running?: number; passed?: number } = {}): SessionRunView {
  return {
    commands: [
      ...Array.from({ length: counts.failed ?? 0 }, (_, index) => ({ command: `npm test ${index}`, status: "failed" as const, turnNumber: 1 })),
      ...Array.from({ length: counts.running ?? 0 }, (_, index) => ({ command: `npm run build ${index}`, status: "running" as const, turnNumber: 1 })),
      ...Array.from({ length: counts.passed ?? 0 }, (_, index) => ({ command: `npm lint ${index}`, status: "passed" as const, turnNumber: 1 })),
    ],
    summary: "Run",
    detail: "Run",
  };
}
