import { describe, expect, it } from "vitest";
import { resultTruncated } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { artifactAggregateLabel, artifactSizeLabel, buildTurnArtifacts, chatVisibleTurnArtifacts } from "./turnArtifacts";

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

  it("keeps web fetch raw artifacts out of the default chat artifact strip", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "web", tool: "web_fetch", args: { url: "https://example.test" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "web",
          exit_code: 0,
          result_summary: "Fetched https://example.test",
          result: "Fetched https://example.test",
          result_artifact_path: ".affent/artifacts/tool-results/000001-web.txt",
          result_bytes: 8300,
          result_cap_bytes: 262144,
        },
      },
    ]).turns[0];

    expect(buildTurnArtifacts(turn)).toHaveLength(1);
    expect(chatVisibleTurnArtifacts(turn)).toEqual([]);
  });

  it("keeps browser navigation capture files out of the default chat artifact strip", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "browser", tool: "browser_navigate", args: { url: "https://example.test" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "browser",
          exit_code: 0,
          result_summary: "PAGE TEXT: Example",
          result: "PAGE TEXT: Example",
          result_artifact_path: ".affent/artifacts/tool-results/000001-browser.txt",
          result_bytes: 22000,
          result_cap_bytes: 262144,
        },
      },
    ]).turns[0];

    expect(buildTurnArtifacts(turn)).toHaveLength(1);
    expect(chatVisibleTurnArtifacts(turn)).toEqual([]);
  });
});
