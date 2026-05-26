import { describe, expect, it } from "vitest";
import type { RawEvent } from "../api/events";
import { completedSubagentTree, resultTruncated } from "../fixtures/scenarios";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { hasIssueContext } from "./issueContext";

describe("hasIssueContext", () => {
  it("keeps simple one-message answers low-noise", () => {
    expect(hasIssueContext(reduceRawEvents(completedTurn))).toBe(false);
  });

  it("does not show issue controls for a simple in-flight message", () => {
    expect(hasIssueContext(reduceRawEvents([
      { id: 30, type: "turn.start", data: { turn_id: "t3" } },
      { id: 31, type: "user.message", data: { turn_id: "t3", text: "summarize the repo" } },
    ]))).toBe(false);
  });

  it("shows issue controls for complex, delegated, or artifact-heavy chats", () => {
    expect(hasIssueContext(reduceRawEvents(completedSubagentTree))).toBe(true);
    expect(hasIssueContext(reduceRawEvents(resultTruncated))).toBe(true);
    expect(hasIssueContext(reduceRawEvents([...completedTurn, ...namespaceTurn("second")]))).toBe(true);
  });

  it("shows issue controls when source evidence is partial or discovery-only", () => {
    expect(hasIssueContext(reduceRawEvents(sourceEvidenceTurn({
      source_access_results: 2,
      source_access_verified: 1,
      source_access_dynamic_partial: 1,
    })))).toBe(true);
    expect(hasIssueContext(reduceRawEvents(sourceEvidenceTurn({
      source_access_results: 1,
      source_access_discovery_only: 1,
    })))).toBe(true);
    expect(hasIssueContext(reduceRawEvents(sourceEvidenceTurn({
      source_access_results: 1,
      source_access_verified: 1,
    })))).toBe(false);
  });
});

function sourceEvidenceTurn(toolStats: Record<string, number>): RawEvent[] {
  return [
    { id: 1, type: "turn.start", data: { turn_id: "t1" } },
    { id: 2, type: "user.message", data: { turn_id: "t1", text: "read a source" } },
    { id: 3, type: "message.done", data: { turn_id: "t1", text: "source read", finish_reason: "stop" } },
    { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed", tool_stats: toolStats } },
  ];
}

function namespaceTurn(suffix: string): RawEvent[] {
  return [
    { id: 100, type: "turn.start", data: { turn_id: `t-${suffix}` } },
    { id: 101, type: "user.message", data: { turn_id: `t-${suffix}`, text: "second message" } },
    { id: 102, type: "message.done", data: { turn_id: `t-${suffix}`, text: "second answer", finish_reason: "stop" } },
    { id: 103, type: "turn.end", data: { turn_id: `t-${suffix}`, reason: "completed" } },
  ];
}
