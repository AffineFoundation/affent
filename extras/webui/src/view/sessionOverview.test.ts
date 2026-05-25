import { describe, expect, it } from "vitest";
import { completedTurn } from "../fixtures/completedTurn";
import { completedSubagentTree, runningSubagent } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { deriveWorkflowStatus } from "../store/workflowStatus";
import { buildSessionOverview } from "./sessionOverview";

describe("buildSessionOverview", () => {
  it("keeps the no-session state task-first", () => {
    const session = reduceRawEvents([]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
    });

    expect(overview).toMatchObject({
      headline: "Start a chat",
      stateLabel: "Ready",
      tone: "ready",
    });
    expect(overview.detail).toContain("create the chat");
  });

  it("shows a submitted task while waiting for the first live event", () => {
    const session = reduceRawEvents([]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
      pendingTask: "summarize the repository architecture",
    });

    expect(overview).toMatchObject({
      headline: "summarize the repository architecture",
      stateLabel: "Sending",
      tone: "running",
      active: true,
    });
    expect(overview.detail).toContain("Creating a chat");
  });

  it("shows a submitted follow-up as the current context even when history exists", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      pendingTask: "explain main.go",
    });

    expect(overview).toMatchObject({
      headline: "explain main.go",
      stateLabel: "Sending",
      tone: "running",
      active: true,
    });
    expect(overview.detail).toContain("next update");
    expect(overview.metrics).not.toContainEqual({ label: "Actions", value: "1" });
  });

  it("labels pending live guidance as an intervention", () => {
    const session = reduceRawEvents(runningSubagent);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
      pendingGuidance: "Guidance for the current work: inspect tests first",
    });

    expect(overview).toMatchObject({
      headline: "use a subagent to inspect docs",
      stateLabel: "Guidance sending",
      detail: "Guidance is being added to the current turn.",
      tone: "running",
      active: true,
    });
  });

  it("uses the latest user task as the headline after a turn exists", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.headline).toBe("list the files");
    expect(overview.stateLabel).toBe("Result ready");
    expect(overview.detail).toBe("README.md main.go");
    expect(overview.metrics).toEqual([
      { label: "Actions", value: "1", tone: undefined },
      { label: "Tokens", value: "138" },
    ]);
  });

  it("adds evidence and usage metrics for completed delegated work", () => {
    const session = reduceRawEvents(completedSubagentTree);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Actions", value: "2", tone: undefined },
      { label: "Evidence", value: "4" },
    ]));
    expect(overview.detail).toBe("WebUI must render trace details as expandable runtime structure.");
  });

  it("keeps replayed turns task-first even without a selected live session", () => {
    const session = reduceRawEvents(completedTurn);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: false,
    });

    expect(overview.headline).toBe("list the files");
    expect(overview.stateLabel).toBe("Result ready");
  });

  it("shows prior work as task metrics after a no-tool finalization turn", () => {
    const session = reduceRawEvents([
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
      { id: 5, type: "usage", data: { turn_id: "t1", input_tokens: 400, output_tokens: 80 } },
      { id: 6, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 7, type: "turn.start", data: { turn_id: "t2" } },
      { id: 8, type: "user.message", data: { turn_id: "t2", text: "continue and summarize" } },
      { id: 9, type: "message.done", data: { turn_id: "t2", text: "Here is the report." } },
      { id: 10, type: "usage", data: { turn_id: "t2", input_tokens: 1000, output_tokens: 200 } },
      { id: 11, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.headline).toBe("research affine");
    expect(overview.stateLabel).toBe("Result ready");
    expect(overview.tone).toBe("success");
    expect(overview.metrics).toEqual([
      { label: "Handled", value: "1", tone: "warning" },
      { label: "Task actions", value: "1" },
      { label: "Turn tokens", value: "1.2k" },
      { label: "Chat tokens", value: "1.7k" },
    ]);
    expect(overview.metrics).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Actions" }),
    ]));
  });

  it("carries source counts into the header when a final report uses earlier tool evidence", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "真实收集 Affine 的相关信息" } },
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
          result_summary: "AFFINE subnet 120",
          result: "AFFINE subnet 120",
          result_truncated: false,
          result_bytes: 20,
          result_omitted_bytes: 0,
          result_cap_bytes: 262144,
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
          args_cap_bytes: 65536,
        },
      },
      {
        id: 6,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "c2",
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
      { id: 7, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 8, type: "turn.start", data: { turn_id: "t2" } },
      { id: 9, type: "user.message", data: { turn_id: "t2", text: "不要再调用任何工具。直接基于本 session 前面结果输出最终报告。" } },
      {
        id: 10,
        type: "message.done",
        data: { turn_id: "t2", text: "# Affine（Bittensor Subnet 120）调研报告\n\n基于已查阅来源整理。" },
      },
      { id: 11, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Handled", value: "1", tone: "warning" },
      { label: "Task actions", value: "2" },
      { label: "Task evidence", value: "1" },
    ]));
    expect(overview.metrics).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Evidence" }),
    ]));
  });

  it("uses explicit chat token wording when only aggregate usage is available", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "usage", data: { input_tokens: 1800, output_tokens: 200 } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual([{ label: "Chat tokens", value: "2.0k" }]);
  });

  it("keeps the original research topic after a finalization prompt", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "真实收集 Affine 的相关信息" } },
      { id: 3, type: "turn.end", data: { turn_id: "t1", reason: "max_turns" } },
      { id: 4, type: "turn.start", data: { turn_id: "t2" } },
      { id: 5, type: "user.message", data: { turn_id: "t2", text: "不要再调用任何工具。直接基于本 session 前两轮结果输出最终报告。" } },
      {
        id: 6,
        type: "message.done",
        data: { turn_id: "t2", text: "# Affine（Bittensor Subnet 120）调研报告\n\nAffine 是 SN120。" },
      },
      { id: 7, type: "turn.end", data: { turn_id: "t2", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.headline).toBe("真实收集 Affine 的相关信息");
    expect(overview.detail).toBe("Affine（Bittensor Subnet 120）调研报告: Affine 是 SN120。");
  });

  it("labels completed tool failures as handled work in the header metrics", () => {
    const session = reduceRawEvents([
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
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.metrics).toEqual(expect.arrayContaining([
      { label: "Handled", value: "1", tone: "warning" },
      { label: "Actions", value: "1", tone: undefined },
    ]));
    expect(overview.detail).toBe("I still found enough to answer.");
    expect(overview.metrics).not.toEqual(expect.arrayContaining([
      expect.objectContaining({ label: "Tool issue" }),
    ]));
  });

  it("uses plain text previews for markdown answers in the header", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize affine" } },
      {
        id: 3,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: "## Affine（Bittensor 子网）介绍\n\n**Reason Mining** uses [`TAOstats`](https://taostats.io/).\n\n---\n\n1. Registered as subnet 120.",
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.detail).toBe("Affine（Bittensor 子网）介绍: Reason Mining uses TAOstats.");
    expect(overview.detail).not.toContain("##");
    expect(overview.detail).not.toContain("**");
    expect(overview.detail).not.toContain("---");
  });

  it("keeps markdown tables out of the header preview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "summarize affine" } },
      {
        id: 3,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: [
            "现在我收集到了充分的数据。让我整理最终报告。",
            "",
            "# Affine（Bittensor Subnet 120）公开信息调查报告",
            "",
            "## 重要前提说明：两个 Affine",
            "",
            "经过查阅，存在两个同名项目需要区分：",
            "",
            "| 项目 | 域名 | 性质 |",
            "|------|------|------|",
            "| AFFiNE | affine.pro | 开源知识管理平台 |",
            "| Affine Subnet | affine.io | Bittensor 子网 #120 |",
            "",
            "本报告仅针对后者。",
          ].join("\n"),
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);

    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.detail).toBe("Affine（Bittensor Subnet 120）公开信息调查报告: 经过查阅，存在两个同名项目需要区分：");
    expect(overview.detail).not.toContain("|");
    expect(overview.detail).not.toContain("现在我收集到了充分的数据");
  });

  it("skips generic answer preambles in the header preview", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "introduce affine" } },
      {
        id: 3,
        type: "message.done",
        data: {
          turn_id: "t1",
          text: "我现在有了足够的信息来给你一个全面、诚实的回答。以下是基于我实际查阅的公开来源的整理：\n\n## Affine（Bittensor 子网）介绍\n\nAffine 是 Reason Mining 子网。",
        },
      },
      { id: 4, type: "turn.end", data: { turn_id: "t1", reason: "completed" } },
    ]);
    const overview = buildSessionOverview({
      session,
      workflow: deriveWorkflowStatus(session),
      hasSelectedSession: true,
    });

    expect(overview.detail).toBe("Affine（Bittensor 子网）介绍: Affine 是 Reason Mining 子网。");
    expect(overview.detail).not.toContain("我现在有了足够的信息");
    expect(overview.detail).not.toContain("以下是基于");
  });
});
