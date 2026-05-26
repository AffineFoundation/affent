import { describe, expect, it } from "vitest";
import type { TurnState } from "../store/sessionState";
import { buildTurnBoundaryView } from "./turnBoundary";

describe("turnBoundary view model", () => {
  it("summarizes completed turns with action, artifact, duration, and tokens", () => {
    const view = buildTurnBoundaryView({
      turn: turn({
        usage: { inputTokens: 120, outputTokens: 18 },
        toolStats: { tool_requests: 1, tool_duration_ms: 12 },
        toolCalls: [
          {
            callId: "c1",
            tool: "list_files",
            args: {},
            argsTruncated: false,
            argsRepaired: false,
            canonicalized: false,
            status: "success",
            resultTruncated: false,
          },
        ],
      }),
      turnNumber: 2,
      artifactCount: 1,
      artifactLabel: "1 file (8 KiB, 1 MiB omitted)",
    });

    expect(view).toEqual({
      title: "list the files",
      statusLabel: "Done",
      tone: "success",
      meta: ["1 action", "1 file (8 KiB, 1 MiB omitted)", "12ms", "138 tokens"],
      ariaLabel: "Message 2: Done. list the files. 1 action. 1 file (8 KiB, 1 MiB omitted). 12ms. 138 tokens",
    });
  });

  it("uses warning and error tones for interrupted turns", () => {
    expect(buildTurnBoundaryView({ turn: turn({ status: "max_turns" }), turnNumber: 1 }).tone).toBe("warning");
    expect(buildTurnBoundaryView({ turn: turn({ status: "error" }), turnNumber: 1 }).tone).toBe("error");
  });

  it("includes verified source and network evidence in completed turn metadata", () => {
    const view = buildTurnBoundaryView({
      turn: turn({
        toolStats: {
          source_access_verified: 2,
          source_access_network: 1,
        },
      }),
      turnNumber: 3,
    });

    expect(view.meta).toEqual(["2 sources", "1 network"]);
    expect(view.ariaLabel).toContain("2 sources. 1 network");
  });

  it("marks a max-turn boundary as continued when a later message has taken over", () => {
    const view = buildTurnBoundaryView({
      turn: turn({ status: "max_turns" }),
      turnNumber: 1,
      continuedAfterLimit: true,
    });

    expect(view.statusLabel).toBe("Continued");
    expect(view.tone).toBe("muted");
    expect(view.ariaLabel).toContain("Message 1: Continued");
  });
});

function turn(overrides: Partial<TurnState> = {}): TurnState {
  return {
    id: "t1",
    status: "completed",
    userText: "list the files",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [],
    ...overrides,
  };
}
