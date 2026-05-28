import { describe, expect, it } from "vitest";
import { resultTruncated } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { artifactAggregateLabel, artifactSizeLabel, buildTurnArtifacts } from "./turnArtifacts";

describe("buildTurnArtifacts", () => {
  it("summarizes full-output artifacts from tool calls", () => {
    const turn = reduceRawEvents(resultTruncated).turns[0];

    expect(buildTurnArtifacts(turn)).toEqual([
      {
        path: ".affent/artifacts/tool-results/000001-c1.txt",
        name: "000001-c1.txt",
        source: "cat big.log",
        tool: "shell",
        callIndex: 1,
        summary: "line 1\nline 2\n…(truncated)",
        truncated: true,
        status: "success",
        exitCode: 0,
        durationMs: 88,
        failureKind: undefined,
        failureKinds: undefined,
        turnNumber: undefined,
        bytes: 8192,
        omittedBytes: 1048576,
        capBytes: 8192,
      },
    ]);
  });

  it("formats artifact byte sizes compactly", () => {
    const turn = reduceRawEvents(resultTruncated).turns[0];
    const artifact = buildTurnArtifacts(turn)[0];
    expect(artifactSizeLabel(artifact)).toBe("(8 KiB, 1 MiB omitted)");
  });

  it("summarizes artifact groups compactly", () => {
    const turn = reduceRawEvents(resultTruncated).turns[0];
    const artifact = buildTurnArtifacts(turn)[0];
    expect(artifactAggregateLabel([artifact])).toBe("000001-c1.txt (8 KiB, 1 MiB omitted)");
  });
});
