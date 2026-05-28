import { describe, expect, it } from "vitest";
import {
  memoryBucketMatchesQuery,
  memoryBucketMatchingEntries,
  memoryBucketDraft,
  memoryBucketEvidenceText,
  memoryBucketLabel,
  memoryBucketUsage,
  manualMemoryDraft,
  memoryUpdateDraft,
  memoryUpdateEvidenceText,
} from "./sessionMemory";

describe("sessionMemory view helpers", () => {
  it("builds evidence for memory updates", () => {
    const update = {
      action: "replace",
      target: "memory",
      topic: "research",
      location: "memory:research",
      preview: "taostats pages require browser network evidence",
    } as const;

    expect(memoryUpdateEvidenceText(update)).toBe([
      "Memory update evidence",
      "Action: Replaced",
      "Location: memory:research",
      "Target: memory",
      "Topic: research",
      "Preview: taostats pages require browser network evidence",
    ].join("\n"));
    expect(memoryUpdateDraft(update)).toContain("kept, corrected, or used in the next step");
  });

  it("builds evidence for memory buckets", () => {
    const bucket = {
      target: "memory",
      topic: "research",
      entries: ["taostats pages are dynamic"],
      entry_count: 1,
      chars_used: 27,
      chars_limit: 4400,
      percent: 1,
      newest_at: "2026-05-26T10:00:00Z",
    };

    expect(memoryBucketLabel(bucket)).toBe("research");
    expect(memoryBucketUsage(bucket)).toBe("27/4400 chars · 1%");
    expect(memoryBucketEvidenceText(bucket)).toBe([
      "Memory bucket evidence for research",
      "Target: memory",
      "Topic: research",
      "Entries: 1",
      "Usage: 27/4400 chars · 1%",
      "Updated: 2026-05-26T10:00:00Z",
      "Content:",
      "- taostats pages are dynamic",
    ].join("\n"));
    expect(memoryBucketDraft(bucket)).toContain("relevant, stale, or needs correction");
    expect(memoryBucketMatchesQuery(bucket, "TAOSTATS")).toBe(true);
    expect(memoryBucketMatchingEntries(bucket, "dynamic")).toEqual(["taostats pages are dynamic"]);
    expect(memoryBucketMatchingEntries(bucket, "research")).toEqual([]);
  });

  it("builds a manual memory draft", () => {
    expect(manualMemoryDraft({
      target: " memory ",
      topic: " research ",
      content: " CoinGecko pages require a browser fallback. ",
    })).toBe([
      "Add or update durable memory if this is useful, accurate, and non-secret:",
      "Target: memory",
      "Topic: research",
      "Content:",
      "CoinGecko pages require a browser fallback.",
    ].join("\n"));
  });
});
