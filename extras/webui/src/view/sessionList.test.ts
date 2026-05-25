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
      title: "WebUI timeline",
      meta: ["May 23 18:30 UTC"],
      status: "Live",
      tone: "running",
      updated: "May 23 18:30 UTC",
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
    expect(rows[0].meta).toEqual(["No messages yet"]);
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
      meta: ["saved-se...123456", "May 23 18:30 UTC"],
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
      title: "Affine（Bittensor 子网）",
      detail: "Latest · 基于已有证据输出报告",
      preview: "Latest · 基于已有证据输出报告",
      meta: ["May 24 17:37 UTC"],
      status: "Saved",
    });
    expect(rows[0].searchText).toContain("请继续同一个任务");
    expect(rows[0].searchText).toContain("基于已有证据输出报告");
  });

  it("prefers a runtime summarized title over the first user message", () => {
    const rows = buildSessionRows([
      session({
        id: "affine-generated-title",
        durable: true,
        title: "Affine market research",
        latest_user_message: "affine 是 Bittensor 的一个子网，请收集信息并向我介绍",
        last_used_at: "2026-05-24T17:37:00Z",
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "Affine market research",
      titleSource: "provided",
      meta: ["May 24 17:37 UTC"],
      status: "Saved",
    });
    expect(rows[0].searchText).toContain("affine 是 bittensor");
    expect(rows[0].searchText).toContain("affine market research");
  });

  it("prefers explicit summary title fields over a rough runtime title", () => {
    const rows = buildSessionRows([
      session({
        id: "summary-title-first",
        durable: true,
        title: "请你收集 Affine 信息",
        summary_title: "Affine（Bittensor 子网）",
        latest_user_message: "请你收集 Affine（Bittensor 子网）的相关信息并向我介绍",
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "Affine（Bittensor 子网）",
      titleSource: "provided",
    });
    expect(rows[0].searchText).toContain("请你收集 affine 信息");
  });

  it("re-summarizes provided titles that are only the raw first prompt", () => {
    const rows = buildSessionRows([
      session({
        id: "raw-provided-title",
        durable: true,
        title: "请你收集 Affine（Bittensor 子网）的相关信息并向我介绍",
        latest_user_message: "请你收集 Affine（Bittensor 子网）的相关信息并向我介绍",
        topic_user_message: "请你收集 Affine（Bittensor 子网）的相关信息并向我介绍",
      }),
      session({
        id: "raw-question-title",
        durable: true,
        summary_title: "bittensor是什么",
        latest_user_message: "bittensor是什么",
      }),
    ]);

    expect(rows.find((row) => row.id === "raw-provided-title")).toMatchObject({
      title: "Affine（Bittensor 子网）",
      titleSource: "topic",
    });
    expect(rows.find((row) => row.id === "raw-question-title")).toMatchObject({
      title: "Bittensor",
      titleSource: "topic",
    });
  });

  it("skips truncated raw runtime titles before accepting generated summaries", () => {
    const rows = buildSessionRows([
      session({
        id: "truncated-runtime-title",
        durable: true,
        title: "请你收集 Affine（Bittensor 子网）的相关信息...",
        summary_title: "Affine subnet research",
        latest_user_message: "请你收集 Affine（Bittensor 子网）的相关信息并向我介绍",
      }),
      session({
        id: "raw-runtime-no-summary",
        durable: true,
        title: "会话的标题最好是经过总结的，而不是把第一句话...",
        latest_user_message: "会话的标题最好是经过总结的，而不是把第一句话的输入当做标题",
      }),
    ]);

    expect(rows.find((row) => row.id === "truncated-runtime-title")).toMatchObject({
      title: "Affine subnet research",
      titleSource: "provided",
    });
    expect(rows.find((row) => row.id === "raw-runtime-no-summary")).toMatchObject({
      title: "会话标题摘要",
      titleSource: "topic",
    });
  });

  it("keeps a provided title when merging the selected live timeline", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([
        session({
          id: "s1",
          durable: true,
          has_events: true,
          summary_title: "Repository file listing",
          latest_user_message: "list the files",
        }),
      ]),
      "s1",
      reduceRawEvents(completedTurn),
    );

    expect(rows[0].title).toBe("Repository file listing");
    expect(rows[0].meta).not.toContain("s1");
  });

  it("uses a pending first task as the selected chat title before events arrive", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "new-1", active: true, durable: true })]),
      "new-1",
      reduceRawEvents([]),
      "summarize the repo",
    );

    expect(rows[0]).toMatchObject({
      title: "repo",
      detail: "Sending · summarize the repo",
      preview: "Waiting for the next update.",
      status: "Live",
      tone: "running",
      meta: [],
    });
    expect(rows[0].searchText).toContain("summarize the repo");
    expect(rows[0].searchText).toContain("waiting for the next update");
  });

  it("surfaces a pending follow-up without replacing the original chat topic", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", active: true, durable: true, latest_user_message: "list the files" })]),
      "s1",
      reduceRawEvents(completedTurn),
      "explain main.go",
    );

    expect(rows[0]).toMatchObject({
      title: "list the files",
      detail: "Sending · main.go",
      preview: "Waiting for the next update.",
      status: "Live",
      tone: "running",
    });
    expect(rows[0].searchText).toContain("explain main.go");
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

  it("keeps dotted filenames intact when summarizing instruction titles", () => {
    const rows = buildSessionRows([
      session({
        id: "main-go",
        durable: true,
        latest_user_message: "explain main.go",
      }),
    ]);

    expect(rows[0].title).toBe("main.go");
  });

  it("summarizes question-style session titles into topics", () => {
    const rows = buildSessionRows([
      session({
        id: "bittensor-question",
        durable: true,
        latest_user_message: "bittensor是什么",
      }),
      session({
        id: "english-subnet",
        durable: true,
        latest_user_message: "Affine is a Bittensor subnet, collect recent information",
      }),
    ]);

    expect(rows.find((row) => row.id === "bittensor-question")?.title).toBe("Bittensor");
    expect(rows.find((row) => row.id === "english-subnet")?.title).toBe("Affine (Bittensor subnet)");
  });

  it("summarizes fixed-reply smoke prompts as task checks", () => {
    const rows = buildSessionRows([
      session({
        id: "embedded-webui",
        durable: true,
        latest_user_message: "只回复：OK embedded WebUI",
      }),
      session({
        id: "dashscope-container",
        durable: true,
        latest_user_message: "reply with: OK DashScope full-stack container",
      }),
    ]);

    expect(rows.find((row) => row.id === "embedded-webui")?.title).toBe("Embedded WebUI check");
    expect(rows.find((row) => row.id === "dashscope-container")?.title).toBe("DashScope full-stack container check");
  });

  it("uses the stated focus as the title instead of the first instruction clause", () => {
    const rows = buildSessionRows([
      session({
        id: "webui-focus",
        durable: true,
        latest_user_message: "理解当前项目，重点关注webui的设计",
      }),
      session({
        id: "english-focus",
        durable: true,
        latest_user_message: "understand the current project, focus on webui session titles",
      }),
    ]);

    expect(rows.find((row) => row.id === "webui-focus")?.title).toBe("WebUI 设计");
    expect(rows.find((row) => row.id === "english-focus")?.title).toBe("WebUI session titles");
  });

  it("summarizes title feedback instead of showing the full request as a title", () => {
    const rows = buildSessionRows([
      session({
        id: "title-feedback",
        durable: true,
        latest_user_message: "会话的标题最好是经过总结的，而不是把第一句话的输入当做标题",
      }),
    ]);

    expect(rows[0].title).toBe("会话标题摘要");
  });

  it("extracts the task subject from long instruction-style first messages", () => {
    const rows = buildSessionRows([
      session({
        id: "csv-export",
        durable: true,
        latest_user_message: "帮我新增一个导出 CSV 的功能，要求支持筛选条件和权限控制",
      }),
      session({
        id: "blank-login",
        durable: true,
        latest_user_message: "请你修复登录页面的空白问题，顺便补充回归测试",
      }),
    ]);

    expect(rows.find((row) => row.id === "csv-export")?.title).toBe("导出 CSV 功能");
    expect(rows.find((row) => row.id === "blank-login")?.title).toBe("登录页面 空白问题");
  });

  it("does not repeat the topic when a continuation prompt embeds the original task", () => {
    const rows = buildSessionRows([
      session({
        id: "affine-repeat",
        durable: true,
        topic_user_message: "真实收集 Affine（Bittensor 子网）的相关信息并向我介绍",
        latest_user_message: "continue this task from where it stopped: 请真实收集 Affine（Bittensor 子网）的相关信息并向我介绍",
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "Affine（Bittensor 子网）",
      detail: undefined,
    });
    expect(rows[0].searchText).toContain("continue this task from where it stopped");
  });

  it("ignores raw continuation prompts when they are saved as runtime titles", () => {
    const rows = buildSessionRows([
      session({
        id: "raw-continuation-title",
        durable: true,
        title: "请继续同一个任务。基于已有证据输出报告",
        topic_user_message: "真实收集 Affine（Bittensor 子网）的相关信息并向我介绍",
        latest_user_message: "请继续同一个任务。基于已有证据输出报告",
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "Affine（Bittensor 子网）",
      titleSource: "topic",
      detail: "Latest · 基于已有证据输出报告",
    });
    expect(rows[0].searchText).toContain("请继续同一个任务");
  });

  it("keeps runtime recovery prompts out of chat titles", () => {
    const recoveryPrompt = "Tools are disabled for the rest of this turn, but the previous assistant step still requested another tool. Do not call tools again. Use only existing tool results.";
    const rows = buildSessionRows([
      session({
        id: "runtime-recovery-session",
        active: true,
        durable: true,
        summary_title: "Tools are disabled for the rest of this turn",
        latest_user_message: recoveryPrompt,
        topic_user_message: recoveryPrompt,
        has_conversation: true,
        has_events: true,
      }),
    ]);

    expect(rows[0]).toMatchObject({
      title: "Live chat",
      titleSource: "fallback",
      preview: undefined,
      status: "Live",
    });
    expect(rows[0].meta).toContain("runtime-...ession");
    expect(rows[0].searchText).toContain("tools are disabled");
  });

  it("uses the selected timeline state when the API summary lacks recent task context", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents(completedTurn),
    );

    expect(rows[0]).toMatchObject({
      title: "list the files",
      preview: "Answer · There are two files.",
      meta: [],
      status: "Done",
      tone: "saved",
      updated: "",
      metrics: ["1 message", "1 action"],
      chips: [],
    });
    expect(rows[0].searchText).toContain("list the files");
    expect(rows[0].searchText).toContain("there are two files");
  });

  it("surfaces unknown events as a notes chip in the chat list", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "future.event", data: { turn_id: "t1", payload: "kept" } },
        { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    );

    expect(rows[0].chips).toContain("notes");
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
      preview: "Issue · DNS failed",
      metrics: ["1 message", "1 action", "1 issue"],
    });
    expect(rows[0].searchText).toContain("dns failed");
  });

  it("summarizes action-limit chats as needing a final answer", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
        { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      ]),
    );

    expect(rows[0]).toMatchObject({
      status: "No final answer",
      tone: "warning",
      preview: "Needs final answer · Action limit reached before a final reply.",
      metrics: ["1 message", "1 issue"],
    });
    expect(rows[0].searchText).toContain("needs final answer");
  });

  it("summarizes provider errors with user-readable issue labels", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "ask provider" } },
        { id: 3, type: "error", data: { turn_id: "t1", code: "upstream_5xx", message: "provider returned 503", recoverable: false } },
        { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "error" } },
      ]),
    );

    expect(rows[0]).toMatchObject({
      status: "Blocked",
      tone: "error",
      preview: "Issue · Provider returned an error",
      metrics: ["1 message", "1 issue"],
    });
    expect(rows[0].searchText).toContain("provider returned an error");
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
      title: "Affine",
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
      title: "Affine（Bittensor 子网）",
      detail: "Latest · 基于已有证据输出报告",
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
