import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { artifactEvidenceDraft, artifactEvidenceText, buildSessionArtifacts, buildWorkbenchArtifacts, sessionArtifactLabel } from "./sessionArtifacts";

describe("sessionArtifacts", () => {
  it("deduplicates artifacts across turns and summarizes their size", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          duration_ms: 10,
          result_summary: "saved output",
          result: "saved output",
          result_truncated: true,
          result_bytes: 8192,
          result_omitted_bytes: 1048576,
          result_cap_bytes: 262144,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      { id: 5, type: "turn.start", data: { turn_id: "t2" } },
      {
        id: 6,
        type: "tool.request",
        data: {
          turn_id: "t2",
          call_id: "c2",
          tool: "web_fetch",
          args: { url: "https://example.invalid" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 65536,
        },
      },
      {
        id: 7,
        type: "tool.result",
        data: {
          turn_id: "t2",
          call_id: "c2",
          exit_code: 0,
          duration_ms: 12,
          result_summary: "same output",
          result: "same output",
          result_truncated: true,
          result_bytes: 8192,
          result_omitted_bytes: 1048576,
          result_cap_bytes: 262144,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
        },
      },
      { id: 8, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);

    const artifacts = buildSessionArtifacts(session);
    expect(artifacts).toHaveLength(1);
    expect(buildWorkbenchArtifacts(session)).toHaveLength(0);
    expect(sessionArtifactLabel(session)).toBe("1 file (8 KiB, 1 MiB omitted)");
    expect(artifactEvidenceText(artifacts[0])).toBe(
      [
        "Artifact evidence for .affent/artifacts/tool-results/000001-c1.txt",
        "Source: web_fetch",
        "Size: (8 KiB, cap 256 KiB, 1 MiB omitted)",
        "Full output available as artifact",
        "Summary: saved output",
      ].join("\n"),
    );
    expect(artifactEvidenceDraft(artifacts[0])).toContain("Reference this artifact in the next step:\nArtifact evidence");
  });

  it("keeps deliverable artifacts in Workbench while excluding raw tool result files", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "write_file", args: { path: "report.md" } } },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c1",
          exit_code: 0,
          result_summary: "report generated",
          result_artifact_path: ".affent/artifacts/reports/report.md",
          result_bytes: 2048,
        },
      },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "c2", tool: "read_file", args: { path: "report.md" } } },
      {
        id: 5,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c2",
          exit_code: 0,
          result_summary: "read snapshot",
          result_artifact_path: ".affent/artifacts/tool-results/000002-c2.txt",
          result_bytes: 2048,
        },
      },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    expect(buildSessionArtifacts(session).map((artifact) => artifact.path)).toEqual([
      ".affent/artifacts/reports/report.md",
      ".affent/artifacts/tool-results/000002-c2.txt",
    ]);
    expect(buildWorkbenchArtifacts(session).map((artifact) => artifact.path)).toEqual([
      ".affent/artifacts/reports/report.md",
    ]);
  });
});
