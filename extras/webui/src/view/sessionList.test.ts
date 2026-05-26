import { describe, expect, it } from "vitest";
import type { SessionSummary } from "../api/sessions";
import { completedTurn } from "../fixtures/completedTurn";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionRows, countSessionsByFilter, filterSessionRows, formatLoadingChatTitle, mergeCurrentSessionRow } from "./sessionList";

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

  it("surfaces tool work in row stats when the summary includes tool counters", () => {
    const rows = buildSessionRows([
      session({
        id: "tools-session",
        durable: true,
        latest_user_message: "review the webui timeline",
        usage: { input_tokens: 1200, output_tokens: 450, turns: 3 },
        tools: { tool_requests: 5, tool_errors: 1, tool_repair_succeeded: 2, tool_repair_failed: 0 },
      }),
    ]);

    expect(rows[0].metrics).toEqual(["3 messages", "5 actions", "1 issue"]);
    expect(rows[0].stats).toBe("3 messages · 5 actions · 1 issue");
    expect(rows[0].searchText).toContain("5 actions");
    expect(rows[0].searchText).toContain("1 issue");
  });

  it("surfaces source evidence quality in row stats and search", () => {
    const rows = buildSessionRows([
      session({
        id: "evidence-session",
        durable: true,
        latest_user_message: "research taostats subnet metrics",
        tools: {
          tool_requests: 4,
          tool_errors: 0,
          tool_repair_succeeded: 0,
          tool_repair_failed: 0,
          source_access_results: 3,
          source_access_verified: 2,
          source_access_discovery_only: 1,
          source_access_network: 1,
          source_access_dynamic_partial: 1,
        },
      }),
    ]);

    expect(rows[0].metrics).toContain("Evidence 2/3 verified, 1 network, 1 partial, 1 discovery");
    expect(rows[0].stats).toBe("4 actions · Evidence 2/3 verified, 1 network, 1 partial, 1 discovery");
    expect(rows[0].searchText).toContain("evidence 2/3 verified");
    expect(rows[0].searchText).toContain("1 network");
  });

  it("surfaces session search recall quality in row stats and search", () => {
    const rows = buildSessionRows([
      session({
        id: "recall-session",
        durable: true,
        latest_user_message: "resume alpha coast analysis",
        tools: {
          tool_requests: 1,
          tool_errors: 0,
          tool_repair_succeeded: 0,
          tool_repair_failed: 0,
          session_search_calls: 1,
          session_search_results: 2,
          session_search_context_hits: 1,
          session_search_matched_terms: 3,
        },
      }),
    ]);

    expect(rows[0].metrics).toContain("Recall 2 hits, 1 context, 3 terms");
    expect(rows[0].stats).toBe("1 action · Recall 2 hits, 1 context, 3 terms");
    expect(rows[0].searchText).toContain("recall 2 hits, 1 context, 3 terms");
  });

  it("surfaces loop guard interventions in row stats and search", () => {
    const rows = buildSessionRows([
      session({
        id: "guard-session",
        durable: true,
        latest_user_message: "recover from repeated browser failures",
        tools: {
          tool_requests: 5,
          tool_errors: 2,
          tool_repair_succeeded: 0,
          tool_repair_failed: 0,
          loop_guard_interventions: 2,
          forced_no_tools: 1,
        },
      }),
    ]);

    expect(rows[0].metrics).toContain("Guard 2, 1 no-tools");
    expect(rows[0].stats).toBe("5 actions · 2 issues · Guard 2, 1 no-tools");
    expect(rows[0].searchText).toContain("guard 2, 1 no-tools");
  });

  it("surfaces persisted plan progress in row stats and search", () => {
    const rows = buildSessionRows([
      session({
        id: "planned-session",
        durable: true,
        has_plan: true,
        latest_user_message: "continue market analysis",
        plan_summary: {
          label: "plan:1/3:active",
          total_steps: 3,
          completed_steps: 1,
          active: true,
          blocked: false,
          done: false,
          current_step: "verify browser evidence",
          current_step_index: 2,
          current_step_status: "in_progress",
          last_completed_step: "inspect plan state",
          last_completed_step_index: 1,
          error: false,
        },
      }),
    ]);

    expect(rows[0].metrics).toContain("Plan 1/3, step 2 active");
    expect(rows[0].stats).toBe("Plan 1/3, step 2 active");
    expect(rows[0].searchText).toContain("plan 1/3, step 2 active");
  });

  it("surfaces high context pressure in row stats and search", () => {
    const rows = buildSessionRows([
      session({
        id: "long-run-session",
        durable: true,
        latest_user_message: "continue long market run",
        context: {
          message_count: 192,
          compact_trigger: 240,
          compact_percent: 80,
          messages_until_compact: 48,
        },
      }),
      session({
        id: "near-compact-session",
        durable: true,
        latest_user_message: "continue bittensor subnet audit",
        context: {
          message_count: 34,
          compact_trigger: 80,
          compact_percent: 43,
          messages_until_compact: 4,
        },
      }),
    ]);

    expect(rows.find((row) => row.id === "long-run-session")?.metrics).toContain("Context 80%");
    expect(rows.find((row) => row.id === "long-run-session")?.searchText).toContain("context 80%");
    expect(rows.find((row) => row.id === "near-compact-session")?.metrics).toContain("Context 43%, 4 msgs left");
    expect(rows.find((row) => row.id === "near-compact-session")?.searchText).toContain("4 msgs left");
  });

  it("keeps low context pressure out of row stats", () => {
    const rows = buildSessionRows([
      session({
        id: "short-session",
        durable: true,
        latest_user_message: "short task",
        context: {
          message_count: 12,
          compact_trigger: 120,
          compact_percent: 10,
          messages_until_compact: 108,
        },
      }),
    ]);

    expect(rows[0].metrics).not.toEqual(expect.arrayContaining([expect.stringContaining("Context")]));
    expect(rows[0].searchText).not.toContain("context 10%");
  });

  it("surfaces durable context compaction summaries in row stats and search", () => {
    const rows = buildSessionRows([
      session({
        id: "compacted-session",
        durable: true,
        latest_user_message: "continue long run after compaction",
        context_compactions: {
          count: 2,
          reactive: 1,
          removed_messages: 96,
          summary_bytes: 4096,
          latest_reason: "context_overflow",
          latest_reactive: true,
        },
      }),
      session({
        id: "large-compacted-session",
        durable: true,
        latest_user_message: "continue old long run",
        context_compactions: {
          count: 3,
          reactive: 2,
          removed_messages: 144,
          latest_reactive: false,
          tail_only: true,
        },
      }),
    ]);

    expect(rows.find((row) => row.id === "compacted-session")?.metrics).toContain("2 compactions, reactive, -96 msgs");
    expect(rows.find((row) => row.id === "compacted-session")?.searchText).toContain("2 compactions, reactive, -96 msgs");
    expect(rows.find((row) => row.id === "large-compacted-session")?.metrics).toContain("recent 3 compactions, -144 msgs");
    expect(rows.find((row) => row.id === "large-compacted-session")?.searchText).toContain("recent 3 compactions");
  });

  it("filters rows by status, features, and search text", () => {
    const rows = buildSessionRows([
      session({ id: "live-a", active: true, has_events: true }),
      session({ id: "saved-b", durable: true, has_memory: true }),
      session({ id: "artifact-c", durable: true, has_artifacts: true }),
      session({ id: "planned-d", durable: true, has_plan: true }),
      session({
        id: "evidence-e",
        durable: true,
        tools: {
          tool_requests: 2,
          tool_errors: 0,
          tool_repair_succeeded: 0,
          tool_repair_failed: 0,
          source_access_results: 2,
          source_access_verified: 1,
        },
      }),
      session({
        id: "issue-f",
        durable: true,
        latest_user_message: "debug broken browser extraction",
        tools: { tool_requests: 2, tool_errors: 1, tool_repair_succeeded: 0, tool_repair_failed: 0 },
      }),
      session({
        id: "guard-g",
        durable: true,
        tools: {
          tool_requests: 3,
          tool_errors: 1,
          tool_repair_succeeded: 0,
          tool_repair_failed: 0,
          loop_guard_interventions: 1,
        },
      }),
    ]);

    expect(countSessionsByFilter(rows)).toMatchObject({ all: 7, active: 1, saved: 6, artifacts: 1, memory: 1, plan: 1, evidence: 1, guard: 1, issues: 2 });
    expect(filterSessionRows(rows, "active", "")).toHaveLength(1);
    expect(filterSessionRows(rows, "memory", "")[0].id).toBe("saved-b");
    expect(filterSessionRows(rows, "plan", "")[0].id).toBe("planned-d");
    expect(filterSessionRows(rows, "evidence", "")[0].id).toBe("evidence-e");
    expect(filterSessionRows(rows, "guard", "")[0].id).toBe("guard-g");
    expect(filterSessionRows(rows, "issues", "").map((row) => row.id).sort()).toEqual(["guard-g", "issue-f"]);
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
    expect(rows[0].stats).toBeUndefined();
    expect(rows[0].searchText).toContain("no messages yet");
  });

  it("exposes a compact stats line only when it carries real work context", () => {
    const baseRows = buildSessionRows([
      session({
        id: "simple-chat",
        durable: true,
        latest_user_message: "plain follow-up",
        usage: { input_tokens: 1200, output_tokens: 450, turns: 3 },
      }),
    ]);

    expect(baseRows[0].stats).toBeUndefined();

    const workRows = mergeCurrentSessionRow(baseRows, "simple-chat", reduceRawEvents(completedTurn));
    expect(workRows[0].stats).toBe("1 message · 1 action");
    expect(workRows[0].searchText).toContain("1 message · 1 action");
  });

  it("uses human titles for saved or live chats when the API summary has no latest task", () => {
    const rows = buildSessionRows([
      session({ id: "saved-session-abcdef123456", durable: true, has_events: true, last_used_at: "2026-05-23T18:30:00Z" }),
      session({ id: "live-session-abcdef123456", active: true, has_conversation: true }),
    ]);

    expect(rows.find((row) => row.id === "saved-session-abcdef123456")).toMatchObject({
      title: "Saved chat",
      meta: ["May 23 18:30 UTC"],
      status: "Saved",
    });
    expect(rows.find((row) => row.id === "live-session-abcdef123456")).toMatchObject({
      title: "Live chat",
      meta: [],
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
    expect(rows[0].meta).toEqual([]);
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
      metrics: ["1 message · 1 action"],
      chips: [],
    });
    expect(rows[0].searchText).toContain("list the files");
    expect(rows[0].searchText).toContain("there are two files");
  });

  it("surfaces artifact output in the selected chat row stats", () => {
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
            exit_code: 0,
            duration_ms: 42,
            result_summary: "saved output",
            result: "saved output",
            result_truncated: true,
            result_bytes: 8192,
            result_omitted_bytes: 1048576,
            result_cap_bytes: 262144,
            result_artifact_path: ".affent/artifacts/tool-results/000001-c1.txt",
          },
        },
        { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    );

    expect(rows[0].stats).toBe("1 message · 1 action · 1 file (8 KiB, 1 MiB omitted)");
    expect(rows[0].searchText).toContain("1 file (8 kib, 1 mib omitted)");
  });

  it("surfaces context compactions in the selected chat row stats", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "continue long run" } },
        {
          id: 3,
          type: "context.compacted",
          data: {
            turn_id: "t1",
            before_messages: 90,
            after_messages: 18,
            removed_messages: 72,
            reactive: true,
            reason: "context_overflow",
            summary_present: true,
            summary_bytes: 4096,
          },
        },
        { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    );

    expect(rows[0].stats).toBe("1 message · 1 compaction, reactive, -72 msgs");
    expect(rows[0].metrics).toContain("1 compaction, reactive, -72 msgs");
    expect(rows[0].searchText).toContain("1 compaction, reactive, -72 msgs");
  });

  it("surfaces live source evidence quality in the selected chat row stats", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "research subnet metrics" } },
        {
          id: 3,
          type: "turn.end",
          data: {
            turn_id: "t1",
            reason: "completed",
            tool_stats: {
              tool_requests: 2,
              source_access_results: 2,
              source_access_verified: 1,
              source_access_discovery_only: 1,
              source_access_network: 1,
              source_access_dynamic_partial: 1,
            },
          },
        },
      ]),
    );

    expect(rows[0].stats).toBe("1 message · Evidence 1/2 verified, 1 network, 1 partial, 1 discovery");
    expect(rows[0].searchText).toContain("evidence 1/2 verified");
  });

  it("surfaces live session search recall in the selected chat row stats", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "resume alpha coast analysis" } },
        {
          id: 3,
          type: "turn.end",
          data: {
            turn_id: "t1",
            reason: "completed",
            tool_stats: {
              session_search_calls: 1,
              session_search_results: 2,
              session_search_context_hits: 1,
              session_search_matched_terms: 3,
            },
          },
        },
      ]),
    );

    expect(rows[0].stats).toBe("1 message · Recall 2 hits, 1 context, 3 terms");
    expect(rows[0].searchText).toContain("recall 2 hits, 1 context, 3 terms");
  });

  it("surfaces live loop guard interventions in the selected chat row stats", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "user.message", data: { turn_id: "t1", text: "recover repeated failed calls" } },
        {
          id: 3,
          type: "turn.end",
          data: {
            turn_id: "t1",
            reason: "max_turns",
            tool_stats: {
              tool_requests: 3,
              tool_errors: 1,
              loop_guard_interventions: 2,
              forced_no_tools: 1,
            },
          },
        },
      ]),
    );

    expect(rows[0].stats).toBe("1 message · 1 issue · Guard 2, 1 no-tools");
    expect(rows[0].searchText).toContain("guard 2, 1 no-tools");
  });

  it("surfaces unknown events as an unclassified chip in the chat list", () => {
    const rows = mergeCurrentSessionRow(
      buildSessionRows([session({ id: "s1", durable: true, has_events: true })]),
      "s1",
      reduceRawEvents([
        { id: 1, type: "turn.start", data: { turn_id: "t1" } },
        { id: 2, type: "future.event", data: { turn_id: "t1", payload: "kept" } },
        { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
      ]),
    );

    expect(rows[0].chips).toContain("unclassified");
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
      metrics: ["1 message · 1 action · 1 issue"],
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
      status: "Blocked",
      tone: "error",
      preview: "Issue · DNS failed",
      metrics: ["1 message · 1 action · 1 issue"],
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
      status: "Needs final answer",
      tone: "warning",
      preview: "Needs final answer · Action limit reached before a final reply.",
      metrics: ["1 message · 1 issue"],
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
      metrics: ["1 message · 1 issue"],
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
      metrics: ["2 messages · 1 action · 1 continued"],
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

    expect(rows[0].metrics).toEqual(["2 messages · 2 actions · 1 continued"]);
    expect(rows[0].searchText).toContain("1 tool issue");
  });

  it("sums separate prior and tool issues in the visible row metrics", () => {
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

    expect(rows[0].metrics).toEqual(["2 messages · 2 actions · 2 issues"]);
    expect(rows[0].searchText).toContain("1 prior issue");
    expect(rows[0].searchText).toContain("1 tool issue");
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
      metrics: ["2 messages · 1 continued"],
    });
    expect(rows[0].searchText).toContain("请继续同一个任务");
  });

  it("formats loading titles without exposing generic chat labels", () => {
    expect(formatLoadingChatTitle(undefined)).toBe("Loading chat");
    expect(formatLoadingChatTitle("Live chat")).toBe("Loading chat");
    expect(formatLoadingChatTitle("saved chat")).toBe("Loading chat");
    expect(formatLoadingChatTitle("Affine research")).toBe("Loading Affine research");
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
