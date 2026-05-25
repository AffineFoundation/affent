import { describe, expect, it } from "vitest";
import { resultTruncated } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { buildTurnArtifacts } from "./turnArtifacts";

describe("buildTurnArtifacts", () => {
  it("summarizes full-output artifacts from tool calls", () => {
    const turn = reduceRawEvents(resultTruncated).turns[0];

    expect(buildTurnArtifacts(turn)).toEqual([
      {
        path: ".affent/artifacts/tool-results/000001-c1.txt",
        name: "000001-c1.txt",
        source: "cat big.log",
        summary: "line 1\nline 2\n…(truncated)",
        truncated: true,
      },
    ]);
  });
});
