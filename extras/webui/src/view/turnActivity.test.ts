import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { completedSubagentTree, resultTruncated, runningSubagent, toolError, turnError } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import type { TurnState } from "../store/sessionState";
import { buildTurnActivity } from "./turnActivity";

describe("buildTurnActivity", () => {
  it("summarizes a turn as processed activity without repeating reasoning", () => {
    const turn = reduceRawEvents(completedTurn).turns[0];
    const activity = buildTurnActivity(turn);

    expect(activity).toMatchObject({
      title: "What Affent did",
      statusLabel: "Done",
      live: false,
      tone: "success",
      digest: {
        label: "Result",
        summary: "README.md main.go",
        meta: ["1 step", "1 action", "138 tokens"],
        tone: "success",
      },
    });
    expect(activity?.brief.rows).toEqual([]);
    expect(activity?.items).toEqual([]);
    expect(activity?.nodes).toEqual([
      expect.objectContaining({
        label: "Action",
        title: "List current directory",
        detail: "README.md main.go",
        meta: "done · 12ms",
        autoOpen: false,
      }),
    ]);
  });

  it("surfaces visible loop decisions on the owning turn", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "extract hidden dashboard metrics" } },
      {
        id: 3,
        type: "loop.decision",
        data: {
          turn_id: "t1",
          kind: "evidence_quality",
          trigger: "source_access_dynamic_partial",
          decision: "defer",
          confidence: "high",
          reason: "Rendered page evidence had empty metric widgets.",
          required_action: "Read browser network responses before citing metrics.",
          visible_in_ui: true,
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.digest).toEqual({
      label: "Decision",
      summary: "Evidence quality: defer: Rendered page evidence had empty metric widgets. Next: Read browser network responses before citing metrics.",
      meta: ["1 decision"],
      tone: "warning",
    });
    expect(activity?.brief.rows).toEqual([
      { id: "goal", label: "Goal", value: "extract hidden dashboard metrics" },
      {
        id: "decision:3",
        label: "Decision",
        value: "defer · Rendered page evidence had empty metric widgets. · Next: Read browser network responses before citing metrics.",
        tone: "warning",
        action: {
          label: "Use action",
          draft: "Continue: Read browser network responses before citing metrics.",
          source: "tool_guidance",
        },
      },
    ]);
  });

  it("surfaces context compactions on the owning turn", () => {
    const turn = reduceRawEvents([
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
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.digest).toEqual({
      label: "Context",
      summary: "Context compacted reactively: 90->18 messages · removed 72 · 4 KiB summary",
      meta: ["1 compaction"],
      tone: "warning",
    });
    expect(activity?.brief.rows).toEqual([
      { id: "goal", label: "Goal", value: "continue long run" },
      {
        id: "compaction:3",
        label: "Context",
        value: "reactive · 90->18 messages · removed 72 · 4 KiB summary · context_overflow",
        tone: "warning",
      },
    ]);
  });

  it("keeps completed delegated work folded as a summary", () => {
    const turn = reduceRawEvents(completedSubagentTree).turns[0];
    const activity = buildTurnActivity(turn);

    expect(activity?.nodes[0]).toMatchObject({
      label: "Delegate",
      title: "Find the WebUI trace requirements",
      autoOpen: false,
      children: expect.arrayContaining([
        expect.objectContaining({ label: "MCP", title: "Search" }),
        expect.objectContaining({ label: "Delegate", title: "Check focused task docs" }),
      ]),
    });
    expect(activity?.digest).toEqual({
      label: "Result",
      summary: "WebUI must render trace details as expandable agent structure.",
      meta: ["2 delegated tasks", "2 actions", "4 evidence"],
      tone: "success",
    });
    expect(activity?.evidencePreview).toEqual([
      { label: "Read", value: "docs/webui-product-design.md" },
      { label: "Read", value: "docs/focused-tasks.md" },
      { label: "MCP", value: "webui trace" },
    ]);
    expect(activity?.evidenceAction).toEqual({
      label: "Use sources",
      draft: [
        "Use this evidence in the next step:",
        "- Read docs/webui-product-design.md",
        "- Read docs/focused-tasks.md",
        "- MCP webui trace",
        "- Listed docs",
      ].join("\n"),
      source: "evidence",
    });
    expect(activity?.brief.rows).toEqual([
      { id: "goal", label: "Goal", value: "delegate docs inspection" },
      {
        id: "evidence",
        label: "Sources",
        evidence: [
          { label: "Read", value: "docs/webui-product-design.md" },
          { label: "Read", value: "docs/focused-tasks.md" },
          { label: "MCP", value: "webui trace" },
          { label: "Listed", value: "docs" },
        ],
        action: {
          label: "Use sources",
          draft: [
            "Use this evidence in the next step:",
            "- Read docs/webui-product-design.md",
            "- Read docs/focused-tasks.md",
            "- MCP webui trace",
            "- Listed docs",
          ].join("\n"),
          source: "evidence",
        },
      },
      {
        id: "next",
        label: "Next",
        value: "Replace result parsing with explicit child trace events when backend exposes them.",
        tone: "muted",
        action: {
          label: "Use next step",
          draft: "Continue: Replace result parsing with explicit child trace events when backend exposes them.",
          source: "tool_guidance",
        },
      },
    ]);
    expect(activity?.nodes[0].detail).toBe("WebUI must render trace details as expandable agent structure.");
    expect(activity?.nodes[0].meta).toBe("done · 1.48s · 392 tokens");
    expect(activity?.nodes[0].evidence).toEqual([
      { label: "Listed", value: "docs" },
      { label: "Read", value: "docs/webui-product-design.md" },
      { label: "MCP", value: "webui trace" },
    ]);
    expect(activity?.nodes[1]).toMatchObject({
      label: "Focused task",
      title: "Verify trace tree requirements",
      detail: "Trace UI needs hierarchical detail for focused tasks and subagents.",
      meta: "done · 920ms · 278 tokens",
      evidence: [{ label: "Read", value: "docs/focused-tasks.md" }],
      autoOpen: false,
      children: expect.arrayContaining([
        expect.objectContaining({ label: "Action", title: "docs/focused-tasks.md" }),
      ]),
    });
  });

  it("auto-opens only the currently running agent path", () => {
    const turn = reduceRawEvents(runningSubagent).turns[0];
    const activity = buildTurnActivity(turn);

    expect(activity).toMatchObject({
      statusLabel: "Live",
      live: true,
      tone: "running",
      digest: {
        label: "Now",
        summary: "Inspect docs for WebUI trace requirements",
        meta: ["1 delegated task", "1 action"],
        tone: "running",
      },
    });
    const thinkingActivity = buildTurnActivity({
      id: "thinking",
      status: "running",
      thinkingText: "I should list files.",
      thinkingStreaming: true,
      assistantText: "",
      messageStreaming: false,
      toolCalls: [],
      userText: "",
    } as TurnState);

    expect(thinkingActivity?.items[0]).toMatchObject({
      label: "Thinking",
      title: "Thinking through the next step",
    });
    expect(activity?.brief.rows).toEqual([
      { id: "goal", label: "Goal", value: "use a subagent to inspect docs" },
      {
        id: "next",
        label: "Next",
        value: "You can still guide this run while it is working.",
        tone: "running",
        action: {
          label: "Guide run",
          draft: "Guidance for current run: ",
          source: "tool_guidance",
        },
      },
    ]);
    expect(activity?.nodes).toEqual([
      expect.objectContaining({
        label: "Delegate",
        title: "Inspect docs for WebUI trace requirements",
        detail: "Running",
        meta: "running",
        autoOpen: true,
      }),
    ]);
  });

  it("summarizes runtime errors without making the digest a raw log line", () => {
    const turn = reduceRawEvents(turnError).turns[0];
    const activity = buildTurnActivity(turn);

    expect(activity?.digest).toMatchObject({
      label: "Issue",
      summary: "Provider returned an error: The model provider returned HTTP 503.",
      tone: "error",
    });
    expect(activity?.brief.rows).not.toContainEqual(expect.objectContaining({ id: "focus" }));
  });

  it("does not turn a completed answer into an issue digest just because one tool failed", () => {
    const turn = reduceRawEvents(toolError).turns[0];
    const activity = buildTurnActivity(turn);

    expect(activity?.digest.label).toBe("Process");
    expect(activity?.digest.summary).toBe("Answered after working around 1 issue.");
    expect(activity?.digest.tone).toBe("warning");
  });

  it("surfaces fetched web sources in the folded evidence preview", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://www.affine.io/" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 40,
          result_summary: "AFFINE subnet 120",
          result: "AFFINE subnet 120",
          result_truncated: false,
          result_bytes: 20,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      {
        id: 5,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c2",
          tool: "web_fetch",
          args: { url: "https://affine.io" },
          args_truncated: false,
          args_bytes: 28,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 6,
        type: "tool.result",
        data: {
          call_id: "c2",
          exit_code: 0,
          duration_ms: 42,
          result_summary: "AFFINE subnet dashboard",
          result: "AFFINE subnet dashboard",
          result_truncated: false,
          result_bytes: 24,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 7, type: "message.done", data: { turn_id: "t1", text: "Affine is subnet 120." } },
      { id: 8, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.evidenceCount).toBe(1);
    expect(activity?.evidencePreview).toEqual([{ label: "Fetched", value: "https://www.affine.io/", displayValue: "affine.io" }]);
    expect(activity?.brief.rows).toContainEqual({
      id: "evidence",
      label: "Sources",
      evidence: [{ label: "Fetched", value: "https://www.affine.io/", displayValue: "affine.io" }],
      action: {
        label: "Use sources",
        draft: "Use this evidence in the next step:\n- Fetched affine.io",
        source: "evidence",
      },
    });
  });

  it("surfaces source evidence quality in the activity evidence preview", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research taostats" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "browser_navigate",
          args: { url: "https://taostats.io/subnets/120", wait_until: "networkidle" },
          args_truncated: false,
          args_bytes: 64,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 80,
          result_summary: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence",
          result: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence\nPAGE TEXT:\nMarket Cap",
          result_truncated: false,
          result_bytes: 128,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      {
        id: 5,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c2",
          tool: "browser_network_read",
          args: { ref: "n1" },
          args_truncated: false,
          args_bytes: 16,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 6,
        type: "tool.result",
        data: {
          call_id: "c2",
          exit_code: 0,
          duration_ms: 30,
          result_summary: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; source_method=network_xhr_fetch",
          result: "SourceAccess: browser_network_url=https://taostats.io/api/subnets/120; source_method=network_xhr_fetch\n{\"price\":\"0.06342 T\"}",
          result_truncated: false,
          result_bytes: 140,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 7, type: "message.done", data: { turn_id: "t1", text: "Used network evidence." } },
      { id: 8, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.evidencePreview).toEqual([
      { label: "Network Source", value: "https://taostats.io/api/subnets/120", displayValue: "taostats.io/api/subnets" },
      { label: "Partial Source", value: "https://taostats.io/subnets/120", displayValue: "taostats.io/subnets/120" },
    ]);
    expect(activity?.evidenceAction?.draft).toContain("- Network Source taostats.io/api/subnets");
    expect(activity?.evidenceAction?.draft).toContain("- Partial Source taostats.io/subnets/120");
  });

  it("adds artifact summaries to the activity digest meta for file-bearing turns", () => {
    const turn = reduceRawEvents(resultTruncated).turns[0];
    const activity = buildTurnActivity(turn);

    expect(activity?.digest.meta).toContain("1 file (8 KiB, 1 MiB omitted)");
    expect(activity?.items.map((item) => item.label)).not.toContain("Artifact");
  });

  it("softens a historical max-turn attempt after the chat continues", () => {
    const turn = reduceRawEvents([
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
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 1,
          duration_ms: 40,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 5, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn, { continuedAfterLimit: true, continuedIntoTurnNumber: 2 });

    expect(activity?.statusLabel).toBe("Continued");
    expect(activity?.tone).toBe("muted");
    expect(activity?.digest).toMatchObject({
      label: "Handoff",
      summary: "Ran 1 action; 1 issue carried forward; message 2 continued the task.",
      tone: "muted",
    });
    expect(activity?.brief.rows).not.toContainEqual(expect.objectContaining({ id: "next" }));
  });

  it("keeps historical research progress visible after a later message continues the task", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://www.affine.io/" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 40,
          result_summary: "AFFINE subnet 120",
          result: "AFFINE subnet 120",
          result_truncated: false,
          result_bytes: 20,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      {
        id: 5,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c2",
          tool: "web_fetch",
          args: { url: "https://missing.invalid/" },
          args_truncated: false,
          args_bytes: 34,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 6,
        type: "tool.result",
        data: {
          call_id: "c2",
          exit_code: 1,
          duration_ms: 30,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 7, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn, { continuedAfterLimit: true, continuedIntoTurnNumber: 3 });

    expect(activity?.digest).toMatchObject({
      label: "Handoff",
      summary: "Checked 1 evidence source across 2 actions; 1 issue carried forward; message 3 continued the task.",
      meta: ["2 steps", "2 actions", "1 evidence"],
      tone: "muted",
    });
    expect(activity?.evidencePreview).toEqual([
      { label: "Fetched", value: "https://www.affine.io/", displayValue: "affine.io" },
    ]);
  });

  it("uses a process summary for completed turns with handled tool failures", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "introduce affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_search",
          args: { query: "affine bittensor" },
          args_truncated: false,
          args_bytes: 29,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 1,
          duration_ms: 40,
          result_summary: "temporary network issue",
          result: "temporary network issue",
          result_truncated: false,
          result_bytes: 23,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      {
        id: 5,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: "我现在有了足够的信息来给你一个全面、诚实的回答。以下是基于我实际查阅的公开来源的整理：\n\n## Affine（Bittensor 子网）介绍\n\n**Affine** 是 Reason Mining 子网。",
        },
      },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.digest).toMatchObject({
      label: "Process",
      summary: "Answered after working around 1 issue.",
      tone: "warning",
    });
    expect(activity?.brief.rows).not.toContainEqual(expect.objectContaining({ id: "focus" }));
    expect(activity?.brief.rows).toContainEqual({
      id: "handled",
      label: "Tool issues",
      evidence: [{ label: "Failed", value: "affine bittensor", displayValue: "affine bittensor" }],
      tone: "warning",
      action: {
        label: "Use issue context",
        draft: "Use these issue targets in the next step:\n- Failed affine bittensor",
        source: "error",
      },
    });
  });

  it("keeps failed fetch attempts out of evidence while naming the failed target", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "research affine" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://affine.invalid/missing" },
          args_truncated: false,
          args_bytes: 40,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 1,
          duration_ms: 40,
          result_summary: "DNS failed",
          result: "DNS failed",
          result_truncated: false,
          result_bytes: 10,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      {
        id: 5,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c2",
          tool: "web_fetch",
          args: { url: "https://www.affine.io/" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 6,
        type: "tool.result",
        data: {
          call_id: "c2",
          exit_code: 0,
          duration_ms: 42,
          result_summary: "AFFINE subnet 120",
          result: "AFFINE subnet 120",
          result_truncated: false,
          result_bytes: 20,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 7, type: "message.done", data: { turn_id: "t1", text: "Affine is subnet 120." } },
      { id: 8, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.evidenceCount).toBe(1);
    expect(activity?.evidencePreview).toEqual([{ label: "Fetched", value: "https://www.affine.io/", displayValue: "affine.io" }]);
    expect(activity?.brief.rows).toContainEqual({
      id: "evidence",
      label: "Sources",
      evidence: [{ label: "Fetched", value: "https://www.affine.io/", displayValue: "affine.io" }],
      action: {
        label: "Use sources",
        draft: "Use this evidence in the next step:\n- Fetched affine.io",
        source: "evidence",
      },
    });
    expect(activity?.brief.rows).toContainEqual({
      id: "handled",
      label: "Tool issues",
      evidence: [{ label: "Failed", value: "https://affine.invalid/missing", displayValue: "affine.invalid/missing" }],
      tone: "warning",
      action: {
        label: "Use issue context",
        draft: "Use these issue targets in the next step:\n- Failed https://affine.invalid/missing",
        source: "error",
      },
    });
    expect(activity?.nodes[0]).toMatchObject({
      title: "Fetch affine.invalid/missing",
      evidence: [],
    });
    expect(activity?.nodes[1]).toMatchObject({
      title: "Fetch affine.io",
      evidence: [{ label: "Fetched", value: "https://www.affine.io/", displayValue: "affine.io" }],
    });
  });

  it("adds a brief warning when tools contradict the user's instruction", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "不要再调用任何工具。直接基于已有结果输出最终报告。" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "web_fetch",
          args: { url: "https://example.com" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
      {
        id: 4,
        type: "tool.result",
        data: {
          call_id: "c1",
          exit_code: 0,
          duration_ms: 40,
          result_summary: "Fetched page",
          result: "Fetched page",
          result_truncated: false,
          result_bytes: 12,
          result_omitted_bytes: 0,
          result_cap_bytes: 8192,
        },
      },
      { id: 5, type: "message.done", data: { turn_id: "t1", text: "Final report." } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]).turns[0];

    const activity = buildTurnActivity(turn);

    expect(activity?.brief.rows).toContainEqual({
      id: "constraint:no-tools",
      label: "Constraint",
      value: "Used web_fetch after the message asked not to call tools.",
      tone: "warning",
    });
    expect(activity?.digest.summary).toBe("Fetched page");
  });
});
