import { describe, expect, it } from "vitest";
import type { SessionSummary } from "../api/sessions";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionRows, countSessionsByFilter, filterSessionRows, mergeCurrentSessionRow } from "./sessionList";

describe("sessionList view model", () => {
  it("maps API session summaries into scannable rows", () => {
    const rows = buildSessionRows([
      session({
        id: "workspace-session-abcdef123456",
        active: true,
        last_used_at: "2026-05-23T18:30:00Z",
        latest_user_message: "review the webui timeline",
        has_events: true,
        has_artifacts: true,
        usage: { input_tokens: 1200, output_tokens: 450, turns: 3 },
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "webui timeline",
      meta: ["workspac...123456", "2026-05-23 18:30 UTC"],
      status: "Live",
      tone: "running",
      updated: "2026-05-23 18:30 UTC",
      chips: ["files"],
    });
    expect(rows[0].metrics).toEqual(["3 messages"]);
    expect(rows[0].searchText).toContain("workspace-session-abcdef123456");
    expect(rows[0].searchText).toContain("review the webui timeline");
    expect(rows[0].searchText).not.toContain("tokens");
  });

  it("filters rows by status, features, and search text", () => {
    const rows = buildSessionRows([
      session({ id: "live-a", active: true, has_events: true }),
      session({ id: "saved-b", durable: true, has_memory: true }),
      session({ id: "artifact-c", durable: true, has_artifacts: true }),
    ]);

    expect(countSessionsByFilter(rows)).toMatchObject({ all: 3, active: 1, saved: 2, artifacts: 1, memory: 1 });
    expect(filterSessionRows(rows, "active", "")).toHaveLength(1);
    expect(filterSessionRows(rows, "memory", "")[0].id).toBe("saved-b");
    expect(filterSessionRows(rows, "all", "artifact")[0].id).toBe("artifact-c");
  });

  it("orders live chats first, then recent chats by last activity", () => {
    const rows = buildSessionRows([
      session({
        id: "older-saved",
        durable: true,
        latest_user_message: "older saved task",
        last_used_at: "2026-05-23T18:30:00Z",
      }),
      session({
        id: "recent-saved",
        durable: true,
        latest_user_message: "recent saved task",
        last_used_at: "2026-05-24T17:37:00Z",
      }),
      session({
        id: "live-stale",
        active: true,
        latest_user_message: "live task",
        last_used_at: "2026-05-22T12:00:00Z",
      }),
    ]);

    expect(rows.map((row) => row.id)).toEqual(["live-stale", "recent-saved", "older-saved"]);
  });

  it("uses approachable empty chat copy without fake metrics", () => {
    const rows = buildSessionRows([session({ id: "new-session" })]);

    expect(rows[0].title).toBe("New chat");
    expect(rows[0].meta).toEqual(["new-session", "No messages yet"]);
    expect(rows[0].metrics).toEqual([]);
    expect(rows[0].searchText).toContain("no messages yet");
  });

  it("uses human titles for saved or live chats when the API summary has no latest task", () => {
    const rows = buildSessionRows([
      session({ id: "saved-session-abcdef123456", durable: true, has_events: true, last_used_at: "2026-05-23T18:30:00Z" }),
      session({ id: "live-session-abcdef123456", active: true, has_conversation: true }),
    ]);

    expect(rows.find((row) => row.id === "saved-session-abcdef123456")).toMatchObject({
      title: "Saved chat",
      meta: ["saved-se...123456", "2026-05-23 18:30 UTC"],
      status: "Saved",
    });
    expect(rows.find((row) => row.id === "live-session-abcdef123456")).toMatchObject({
      title: "Live chat",
      meta: ["live-ses...123456"],
      status: "Live",
    });
    expect(rows.find((row) => row.id === "saved-session-abcdef123456")?.searchText).toContain("saved-session-abcdef123456");
  });

  it("uses a stable topic title from the API while keeping the latest continuation searchable", () => {
    const rows = buildSessionRows([
      session({
        id: "affine-session",
        durable: true,
        latest_user_message: "请继续同一个任务。基于已有证据输出报告",
        topic_user_message: "affine 是 Bittensor 的一个子网，请收集信息",
        last_used_at: "2026-05-24T17:37:00Z",
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "affine 是 Bittensor 的一个子网",
      meta: ["affine-session", "2026-05-24 17:37 UTC"],
      status: "Saved",
    });
    expect(rows[0].searchText).toContain("请继续同一个任务");
  });

  it("turns long instruction-style tasks into topic-like titles", () => {
    const rows = buildSessionRows([
      session({
        id: "affine-research",
        durable: true,
        latest_user_message: "真实收集 Affine（Bittensor 子网）的相关信息并向我介绍",
      }),
      session({
        id: "webui-review",
        durable: true,
        latest_user_message: "please review the WebUI session list behavior",
      }),
    ]);

    expect(rows.find((row) => row.id === "affine-research")?.title).toBe("Affine（Bittensor 子网）");
    expect(rows.find((row) => row.id === "webui-review")?.title).toBe("WebUI session list behavior");
  });

  it("uses the selected timeline state when the API summary lacks recent task context", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents(completedTurn),
    );

    expect(rows[0]).toMatchObject({
      title: "list the files",
      meta: ["s1"],
      status: "Done",
      tone: "saved",
      updated: "",
      metrics: ["1 message", "1 action"],
      chips: [],
    });
    expect(rows[0].searchText).toContain("list the files");
  });

  it("keeps answered tool failures as tool issues instead of an error row", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
        {
          id: 3,
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
          id: 4,
          type: "tool.result",
          data: {
            turn_id: "t1",
            call_id: "c1",
            exit_code: 1,
            duration_ms: 42,
            result_summary: "DNS failed",
            result: "DNS failed",
            result_truncated: false,
            result_bytes: 10,
            result_omitted_bytes: 0,
            result_cap_bytes: 262144,
          },
        },
        { id: 5, type: "message.done", data: { turn_id: "t1", text: "I still found enough to answer." } },
        { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    );

    expect(rows[0]).toMatchObject({
      status: "Done",
      tone: "saved",
      metrics: ["1 message", "1 action", "1 tool issue"],
    });
  });

  it("keeps unresolved tool failures in the error state", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
        {
          id: 3,
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
          id: 4,
          type: "tool.result",
          data: {
            turn_id: "t1",
            call_id: "c1",
            exit_code: 1,
            duration_ms: 42,
            result_summary: "DNS failed",
            result: "DNS failed",
            result_truncated: false,
            result_bytes: 10,
            result_omitted_bytes: 0,
            result_cap_bytes: 262144,
          },
        },
        { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    );

    expect(rows[0]).toMatchObject({
      status: "Done",
      tone: "error",
      metrics: ["1 message", "1 action", "1 issue"],
    });
  });

  it("does not let a prior continuation limit color a later completed follow-up as failed", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
        {
          id: 3,
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
          id: 4,
          type: "tool.result",
          data: {
            turn_id: "t1",
            call_id: "c1",
            exit_code: 1,
            duration_ms: 42,
            result_summary: "DNS failed",
            result: "DNS failed",
            result_truncated: false,
            result_bytes: 10,
            result_omitted_bytes: 0,
            result_cap_bytes: 262144,
          },
        },
        { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
        { id: 6, type: "turn.start", data: { turn_id: "t2" } },
        { id: 7, type: "user.message", data: { turn_id: "t2", text: "continue and summarize" } },
        { id: 8, type: "message.done", data: { turn_id: "t2", text: "Here is the report." } },
        { id: 9, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
      ]),
    );

    expect(rows[0]).toMatchObject({
      title: "affine",
      status: "Done",
      tone: "saved",
      metrics: ["2 messages", "1 action", "1 continued"],
    });
    expect(rows[0].searchText).toContain("continue and summarize");
  });

  it("keeps continued attempts separate from answered tool issues in the session row", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
        {
          id: 3,
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
          id: 4,
          type: "tool.result",
          data: {
            turn_id: "t1",
            call_id: "c1",
            exit_code: 1,
            duration_ms: 42,
            result_summary: "DNS failed",
            result: "DNS failed",
            result_truncated: false,
            result_bytes: 10,
            result_omitted_bytes: 0,
            result_cap_bytes: 262144,
          },
        },
        { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
        { id: 6, type: "turn.start", data: { turn_id: "t2" } },
        { id: 7, type: "user.message", data: { turn_id: "t2", text: "continue with sources" } },
        {
          id: 8,
          type: "tool.request",
          data: {
            turn_id: "t2",
            call_id: "c2",
            tool: "web_fetch",
            args: { url: "https://blocked.example" },
            args_truncated: false,
            args_bytes: 32,
            args_omitted_bytes: 0,
            args_cap_bytes: 65536,
          },
        },
        {
          id: 9,
          type: "tool.result",
          data: {
            turn_id: "t2",
            call_id: "c2",
            exit_code: 1,
            duration_ms: 42,
            result_summary: "blocked",
            result: "blocked",
            result_truncated: false,
            result_bytes: 7,
            result_omitted_bytes: 0,
            result_cap_bytes: 262144,
          },
        },
        { id: 10, type: "message.done", data: { turn_id: "t2", text: "Here is the report." } },
        { id: 11, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
      ]),
    );

    expect(rows[0].metrics).toEqual(["2 messages", "2 actions", "1 continued", "1 tool issue"]);
  });

  it("keeps a Chinese continuation from replacing the original session topic", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "affine 是 Bittensor 的一个子网，请收集信息" } },
        { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
        { id: 4, type: "turn.start", data: { turn_id: "t2" } },
        { id: 5, type: "user.message", data: { turn_id: "t2", text: "请继续同一个任务。基于已有证据输出报告" } },
        { id: 6, type: "message.done", data: { turn_id: "t2", text: "阶段性报告如下。" } },
        { id: 7, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
      ]),
    );

    expect(rows[0]).toMatchObject({
      title: "affine 是 Bittensor 的一个子网",
      status: "Done",
      tone: "saved",
      metrics: ["2 messages", "1 continued"],
    });
    expect(rows[0].searchText).toContain("请继续同一个任务");
  });
});

function session(overrides: Partial<SessionSummary>): SessionSummary {
  return {
    id: "s1",
    active: false,
    durable: false,
    has_conversation: false,
    has_events: false,
    has_artifacts: false,
    has_memory: false,
    has_runtime_skills: false,
    ...overrides,
  };
}
