import { describe, expect, it } from "vitest";
import type { SessionChangesView } from "./sessionChanges";
import type { SessionFilesView } from "./sessionFiles";
import type { SessionOverview } from "./sessionOverview";
import type { SessionRunView } from "./sessionRun";
import { buildWorkbenchAttention } from "./workbenchAttention";

describe("buildWorkbenchAttention", () => {
  it("uses the current issue detail as the Workbench badge fact", () => {
    expect(buildWorkbenchAttention({
      overview: overview({
        tone: "error",
        detail: "shell command failed: Next: retry after fixing checkout route",
        metrics: [{ label: "Issue", value: "1", tone: "error" }],
      }),
      files: files(),
      changes: changes(),
      run: run({ failed: 1, failedDetail: "checkout spec failed", failedNext: "update payment route then rerun" }),
    })).toEqual({
      label: "Issue: checkout spec failed · View context",
      detail: "checkout spec failed · Next: update payment route then rerun",
      tone: "error",
      target: "context",
    });
  });

  it("falls back to the issue count when the overview detail is generic", () => {
    expect(buildWorkbenchAttention({
      overview: overview({
        tone: "error",
        detail: "Open current chat context and recovery evidence.",
        metrics: [{ label: "Issues", value: "2", tone: "error" }],
      }),
      files: files(),
      changes: changes(),
      run: run(),
    })).toEqual({
      label: "2 issues · View context",
      detail: "Open current chat context and recovery evidence.",
      tone: "error",
      target: "context",
    });
  });

  it("prioritizes failed commands over recovery hints and changed files", () => {
    expect(buildWorkbenchAttention({
      overview: overview({ metrics: [{ label: "Recovery", value: "rerun tests", tone: "warning" }] }),
      files: files(),
      changes: changes({ changed: 2 }),
      run: run({ failed: 1, failedDetail: "checkout spec failed", failedNext: "update payment route then rerun" }),
    })).toEqual({
      label: "1 failed command · View run",
      detail: "npm test 0 · checkout spec failed · Next: update payment route then rerun",
      tone: "error",
      target: "run",
    });
  });

  it("uses recovery when there is no failed Workbench surface", () => {
    expect(buildWorkbenchAttention({
      overview: overview({ metrics: [{ label: "Recovery", value: "rerun tests", tone: "warning" }] }),
      files: files({ available: 1 }),
      changes: changes({ changed: 2 }),
      run: run(),
    })).toEqual({ label: "Recovery hint · View context", detail: "rerun tests", tone: "warning", target: "context" });
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
      label: "Workspace mismatch · View workspace",
      detail: "Latest command cwd is outside the session workspace.",
      tone: "warning",
      target: "workspace",
    });
  });

  it("uses changed files as the lowest attention badge and ignores read-only files", () => {
    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files({ available: 2 }),
      changes: changes({ changed: 3, changedDetail: "Updated payment route" }),
      run: run(),
    })).toEqual({
      label: "3 changed files · Review diff",
      detail: "src/changed-0.ts · Updated payment route",
      tone: "attention",
      target: "changes",
    });

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
      automation: { title: "Loop waiting · 1 timer pending", detail: "Answer setup question before LOOP.md can run." },
    })).toEqual({
      label: "Loop waiting · 1 timer pending · Open automation",
      detail: "Answer setup question before LOOP.md can run.",
      tone: "warning",
      target: "automation",
    });

    expect(buildWorkbenchAttention({
      overview: overview(),
      files: files(),
      changes: changes(),
      run: run(),
      automation: { title: "Loop running · 1 timer active", detail: "Next timer May 27, 02:00 PM" },
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

function files(counts: { available?: number; failed?: number; running?: number; failedDetail?: string; runningDetail?: string; next?: string } = {}): SessionFilesView {
  return {
    items: [
      ...Array.from({ length: counts.available ?? 0 }, (_, index) => ({ path: `src/read-${index}.ts`, actions: ["read" as const], status: "available" as const, turnNumber: 1, actionCount: 1 })),
      ...Array.from({ length: counts.failed ?? 0 }, (_, index) => ({
        path: `src/missing-${index}.ts`,
        actions: ["read" as const],
        status: "failed" as const,
        turnNumber: 1,
        actionCount: 1,
        detail: counts.failedDetail,
        next: counts.next,
      })),
      ...Array.from({ length: counts.running ?? 0 }, (_, index) => ({
        path: `src/running-${index}.ts`,
        actions: ["read" as const],
        status: "running" as const,
        turnNumber: 1,
        actionCount: 1,
        detail: counts.runningDetail,
      })),
    ],
    summary: "Files",
    detail: "Files",
  };
}

function changes(counts: { changed?: number; failed?: number; running?: number; changedDetail?: string; failedDetail?: string; runningDetail?: string } = {}): SessionChangesView {
  return {
    files: [
      ...Array.from({ length: counts.changed ?? 0 }, (_, index) => ({
        path: `src/changed-${index}.ts`,
        operation: "edit" as const,
        status: "changed" as const,
        turnNumber: 1,
        actionCount: 1,
        detail: counts.changedDetail,
      })),
      ...Array.from({ length: counts.failed ?? 0 }, (_, index) => ({
        path: `src/failed-${index}.ts`,
        operation: "edit" as const,
        status: "failed" as const,
        turnNumber: 1,
        actionCount: 1,
        detail: counts.failedDetail,
      })),
      ...Array.from({ length: counts.running ?? 0 }, (_, index) => ({
        path: `src/running-${index}.ts`,
        operation: "edit" as const,
        status: "running" as const,
        turnNumber: 1,
        actionCount: 1,
        detail: counts.runningDetail,
      })),
    ],
    summary: "Changes",
    detail: "Changes",
  };
}

function run(counts: { failed?: number; running?: number; passed?: number; failedDetail?: string; failedNext?: string } = {}): SessionRunView {
  return {
    commands: [
      ...Array.from({ length: counts.failed ?? 0 }, (_, index) => ({
        command: `npm test ${index}`,
        status: "failed" as const,
        turnNumber: 1,
        detail: counts.failedDetail,
        next: counts.failedNext,
      })),
      ...Array.from({ length: counts.running ?? 0 }, (_, index) => ({ command: `npm run build ${index}`, status: "running" as const, turnNumber: 1 })),
      ...Array.from({ length: counts.passed ?? 0 }, (_, index) => ({ command: `npm lint ${index}`, status: "passed" as const, turnNumber: 1 })),
    ],
    summary: "Run",
    detail: "Run",
  };
}
