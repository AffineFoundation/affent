import { describe, expect, it } from "vitest";
import { completedSubagentTree } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import {
  buildWorkbenchContextEvidence,
  buildWorkbenchContextUsage,
  workbenchContextEvidenceDraft,
  workbenchContextEvidenceText,
  workbenchContextStatusDetail,
  workbenchContextSummary,
} from "./workbenchContext";
import type { SessionOverview } from "./sessionOverview";

describe("workbenchContext", () => {
  it("builds actionable context evidence without promoting token-only metrics", () => {
    const overview = sessionOverview({
      headline: "Fix checkout tests",
      detail: "Tests failed after the route changed.",
      stateLabel: "Review needed",
      tone: "warning",
      metrics: [
        { label: "Recovery", value: "update payment route" },
        { label: "Tokens", value: "12k" },
      ],
    });
    const input = {
      overview,
      hasSelectedSession: true,
      changes: { summary: "2 changed files", detail: "2 changed", files: [{ path: "src/payments.ts", operation: "edit" as const, status: "changed" as const, turnNumber: 1, actionCount: 1 }] },
      run: { summary: "1 failed command", detail: "1 failed", tone: "error" as const, commands: [{ command: "npm test", status: "failed" as const, turnNumber: 1, exitCode: 1 }] },
      artifacts: [{ path: ".affent/artifacts/test.log", name: "test.log", source: "npm test", summary: "checkout failure log", truncated: true, bytes: 4096 }],
    };

    expect(workbenchContextSummary(overview, true)).toBe("Review needed");
    expect(buildWorkbenchContextEvidence(input).map((item) => item.label)).toEqual(["Changes", "Run", "Artifacts"]);
    expect(workbenchContextEvidenceText(input)).toContain("Recovery: update payment route");
    expect(workbenchContextEvidenceText(input)).toContain("Artifacts: 1 artifact · checkout failure log");
    expect(workbenchContextEvidenceText(input)).not.toContain("Tokens: 12k");
    expect(workbenchContextEvidenceDraft(input)).toContain("Use this current chat context in the next step:");
  });

  it("builds explicit workspace and token evidence for Workbench context", () => {
    const session = reduceRawEvents([
      ...completedSubagentTree,
      { id: 9, type: "usage", data: { turn_id: "t1", input_tokens: 1200, output_tokens: 340 } },
    ]);
    const usage = buildWorkbenchContextUsage(session);
    const input = {
      overview: sessionOverview({ headline: "Inspect delegated work" }),
      hasSelectedSession: true,
      workspace: {
        hasData: true,
        summary: "affent",
        shortStatus: "affent · main",
        detail: "/home/claudeuser/work/affent · branch main",
        label: "affent",
        path: "/home/claudeuser/work/affent",
        branch: "main",
      },
      usage,
    };

    expect(usage.items).toEqual(expect.arrayContaining([
      { label: "Session tokens", value: "1,540 tokens (1,200 in / 340 out)", detail: "1 turn from loaded trace" },
      { label: "Latest turn tokens", value: "1,540 tokens (1,200 in / 340 out)", detail: "t1" },
      expect.objectContaining({ label: "Focused task tokens", value: "278 tokens (220 in / 58 out)" }),
      expect.objectContaining({ label: "Subagent tokens", value: "392 tokens (310 in / 82 out)" }),
    ]));
    expect(workbenchContextEvidenceText(input)).toContain("Workspace path: /home/claudeuser/work/affent");
    expect(workbenchContextEvidenceText(input)).toContain("Session tokens: 1,540 tokens (1,200 in / 340 out)");
    expect(workbenchContextEvidenceText(input)).toContain("Subagent tokens: 392 tokens (310 in / 82 out)");
  });

  it("uses session index usage when the trace has not loaded token events yet", () => {
    const usage = buildWorkbenchContextUsage(reduceRawEvents([]), {
      id: "s1",
      active: false,
      durable: true,
      has_conversation: true,
      has_events: true,
      has_artifacts: false,
      has_memory: false,
      has_runtime_skills: false,
      usage: { input_tokens: 2000, output_tokens: 500, turns: 4 },
    });

    expect(usage.items).toEqual([
      { label: "Session tokens", value: "2,500 tokens (2,000 in / 500 out)", detail: "4 turns from session index" },
    ]);
  });

  it("uses context attention detail as the status detail", () => {
    expect(workbenchContextStatusDetail({
      overview: sessionOverview({ detail: "generic issue" }),
      attention: { label: "Issue · View context", detail: "checkout spec failed", tone: "error", target: "context" },
    })).toBe("checkout spec failed");
  });
});

function sessionOverview(overrides: Partial<SessionOverview>): SessionOverview {
  return {
    headline: "Ready",
    detail: "Describe the outcome.",
    stateLabel: "Ready",
    tone: "ready",
    active: false,
    metrics: [],
    ...overrides,
  };
}
