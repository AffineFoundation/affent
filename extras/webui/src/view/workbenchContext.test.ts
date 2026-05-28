import { describe, expect, it } from "vitest";
import {
  buildWorkbenchContextEvidence,
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
