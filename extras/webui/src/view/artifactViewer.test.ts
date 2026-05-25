import { describe, expect, it } from "vitest";
import { buildArtifactMatchPreviews, buildArtifactStats } from "./artifactViewer";

describe("buildArtifactStats", () => {
  it("reports loaded progress and query matches for a chunk", () => {
    expect(buildArtifactStats({
      path: "out.txt",
      bytes: 100,
      offset: 20,
      text: "needle x NEEDLE",
      hasMore: true,
    }, "needle")).toEqual({
      loadedBytes: 35,
      totalBytes: 100,
      loadedPercent: 35,
      matchCount: 2,
      complete: false,
    });
  });

  it("treats zero-sized artifacts as complete progress", () => {
    expect(buildArtifactStats({
      path: "empty.txt",
      bytes: 0,
      offset: 0,
      text: "",
      hasMore: false,
    }, "")).toMatchObject({
      loadedPercent: 100,
      matchCount: 0,
      complete: true,
    });
  });

  it("builds compact line previews for artifact search matches", () => {
    expect(buildArtifactMatchPreviews("alpha\nneedle here\nskip\nNEEDLE again", "needle")).toEqual([
      { lineNumber: 2, text: "needle here" },
      { lineNumber: 4, text: "NEEDLE again" },
    ]);
  });
});
