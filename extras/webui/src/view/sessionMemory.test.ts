import { describe, expect, it } from "vitest";
import {
  memoryBucketMatchesQuery,
  memoryBucketMatchingEntries,
  memoryBucketDraft,
  memoryBucketEvidenceText,
  memoryBucketKey,
  memoryBucketLabel,
  memoryBucketPreview,
  memoryBucketUsage,
  memoryBucketsNeedingReview,
  memoryEntryIsSensitive,
  memoryEntrySafePreview,
  buildSessionMemoryCandidates,
  memoryPressureLabel,
  memoryReviewFindings,
  memoryScopeLabel,
  memoryStats,
  memorySnapshotDraft,
  memorySnapshotEvidenceText,
  memorySuggestionDraft,
  memoryUsageLabel,
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

  it("redacts sensitive memory in preview helpers", () => {
    const secret = "access_token=ghp_example should not be stored";
    expect(memoryEntryIsSensitive(secret)).toBe(true);
    expect(memoryEntrySafePreview(secret)).toBe("access_token=[redacted] should not be stored");
    expect(memoryBucketPreview({
      target: "user",
      topic: "user",
      entries: [secret],
      entry_count: 1,
      chars_used: secret.length,
    })).toBe("access_token=[redacted] should not be stored");
  });

  it("builds a complete memory snapshot", () => {
    const snapshot = {
      session_id: "s1",
      has_memory: true,
      shared_user_memory: true,
      user: {
        target: "user",
        topic: "user",
        entries: ["prefers concise reports"],
        entry_count: 1,
        chars_used: 23,
        chars_limit: 1375,
        percent: 1,
      },
      topics: [
        {
          target: "memory",
          topic: "research",
          entries: ["taostats pages are dynamic"],
          entry_count: 1,
          chars_used: 27,
          chars_limit: 4400,
          percent: 1,
        },
      ],
    };

    expect(memorySnapshotEvidenceText(snapshot)).toContain("Memory snapshot evidence");
    expect(memorySnapshotEvidenceText(snapshot)).toContain("Session: s1");
    expect(memorySnapshotEvidenceText(snapshot)).toContain("Scope: Shared user + session");
    expect(memorySnapshotEvidenceText(snapshot)).toContain("Memory bucket evidence for User");
    expect(memorySnapshotEvidenceText(snapshot)).toContain("Memory bucket evidence for research");
    expect(memorySnapshotDraft(snapshot)).toContain("durable memory snapshot");
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

  it("builds a current-chat memory suggestion prompt without saving directly", () => {
    const draft = memorySuggestionDraft({
      session_id: "s1",
      has_memory: true,
      topics: [{
        target: "memory",
        topic: "project",
        entries: ["Use Vite for WebUI development."],
        entry_count: 1,
        chars_used: 31,
        chars_limit: 4400,
        percent: 1,
      }],
    });

    expect(draft).toContain("Find durable memory candidates");
    expect(draft).toContain("non-secret");
    expect(draft).toContain("Do not save memory yet");
    expect(draft).toContain("Current memory: 1 entry");
  });

  it("keeps transient task file evidence out of durable memory candidates", () => {
    const candidates = buildSessionMemoryCandidates({
      memory: {
        session_id: "s1",
        has_memory: true,
        topics: [
          {
            target: "memory",
            topic: "project",
            entries: ["Project goal: Build a Python CLI 2048 game."],
            entry_count: 1,
            chars_used: 41,
          },
        ],
      },
      session: {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: true,
        has_runtime_skills: false,
        loop_state: { version: 1, initial_goal_preview: "Build a Python CLI 2048 game." },
      },
      changes: {
        files: [{ path: "game2048.py", operation: "write", status: "changed", turnNumber: 2, actionCount: 1 }],
        summary: "1 changed file",
        detail: "1 changed",
      },
      files: {
        items: [{ path: "game2048.py", actions: ["read"], status: "available", turnNumber: 1, actionCount: 1, contentPreview: "class Game:" }],
        summary: "1 file",
        detail: "1 read",
      },
    });

    expect(candidates).toEqual([]);
  });

  it("proposes durable loop goals without deriving candidates from ordinary chat text", () => {
    const fromLoop = buildSessionMemoryCandidates({
      memory: { session_id: "s1", has_memory: false, topics: [] },
      session: {
        id: "s1",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        latest_user_message: "just fixed current bug",
        topic_user_message: "temporary chat task",
        loop_state: { version: 1, initial_goal_preview: "Build a Python CLI 2048 game." },
      },
    });
    expect(fromLoop).toEqual([
      expect.objectContaining({
        id: "project-goal",
        target: "memory",
        topic: "project",
        content: "Project goal: Build a Python CLI 2048 game.",
        source: "Loop goal",
      }),
    ]);

    const fromOrdinaryChat = buildSessionMemoryCandidates({
      memory: { session_id: "s2", has_memory: false, topics: [] },
      session: {
        id: "s2",
        active: true,
        durable: true,
        has_conversation: true,
        has_events: true,
        has_artifacts: false,
        has_memory: false,
        has_runtime_skills: false,
        latest_user_message: "fix this failing test",
        topic_user_message: "fix this failing test",
      },
    });
    expect(fromOrdinaryChat).toEqual([]);
  });

  it("summarizes memory scope and capacity pressure", () => {
    const stats = memoryStats({
      session_id: "s1",
      has_memory: true,
      shared_user_memory: true,
      core: {
        target: "memory",
        topic: "core",
        entries: ["current repository facts"],
        entry_count: 1,
        chars_used: 90,
        chars_limit: 100,
        percent: 90,
      },
      topics: [],
    });

    expect(stats).toMatchObject({
      bucketCount: 1,
      entryCount: 1,
      topicCount: 0,
      charsUsed: 90,
      charsLimit: 100,
      percent: 90,
      pressure: "full",
    });
    expect(memoryUsageLabel(stats)).toBe("90/100 chars");
    expect(memoryPressureLabel(stats)).toBe("90% used");
    expect(memoryScopeLabel({ session_id: "s1", has_memory: false, shared_user_memory: true, topics: [] })).toBe("Shared user + session");
  });

  it("finds memory entries that need maintenance", () => {
    const memory = {
      session_id: "s1",
      has_memory: true,
      user: {
        target: "user",
        topic: "user",
        entries: ["access_token=ghp_example should not be stored"],
        entry_count: 1,
        chars_used: 43,
      },
      topics: [
        {
          target: "memory",
          topic: "project",
          entries: ["Use Vite for WebUI development.", "Use Vite for WebUI development."],
          entry_count: 2,
          chars_used: 62,
          chars_limit: 70,
          percent: 89,
        },
      ],
    };

    expect(memoryBucketKey(memory.topics[0])).toBe("memory:project");
    expect(memoryReviewFindings(memory).map((finding) => finding.kind)).toEqual([
      "sensitive",
      "capacity",
      "duplicate",
      "duplicate",
    ]);
    expect(memoryReviewFindings(memory)[0].entryPreview).toBe("access_token=[redacted] should not be stored");
    expect(memoryBucketsNeedingReview(memory)).toEqual(new Set(["user:user", "memory:project"]));
  });
});
