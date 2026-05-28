import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { buildSessionPlanFromToolResults } from "./sessionPlan";

describe("buildSessionPlanFromToolResults", () => {
  it("derives the latest visible plan from successful plan tool results", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "plan-1",
          tool: "plan",
          args: { action: "set", steps: [] },
          args_truncated: false,
        },
      },
      {
        id: 3,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "plan-1",
          exit_code: 0,
          result_summary: JSON.stringify({
            version: 1,
            updated_at: "2026-05-28T10:42:23.219Z",
            steps: [
              { text: "查询北京天气", status: "completed", evidence: ["weather ok"] },
              { text: "分析 Bittensor 趋势", status: "in_progress" },
              { text: "研究 Affine 子网现状", status: "pending" },
            ],
          }),
          result_truncated: false,
        },
      },
      {
        id: 4,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "plan-bad",
          tool: "plan",
          args: { index: 3, status: "in_progress" },
          args_truncated: false,
        },
      },
      {
        id: 5,
        type: "tool.result",
        data: {
          turn_id: "t1",
          call_id: "plan-bad",
          exit_code: 1,
          result_summary: "Error: action is required",
          result_truncated: false,
        },
      },
    ]);

    expect(buildSessionPlanFromToolResults(session)).toEqual({
      plan: {
        version: 1,
        updated_at: "2026-05-28T10:42:23.219Z",
        source: "tool_result",
        steps: [
          { text: "查询北京天气", status: "completed", evidence: ["weather ok"] },
          { text: "分析 Bittensor 趋势", status: "in_progress" },
          { text: "研究 Affine 子网现状", status: "pending" },
        ],
      },
      summary: {
        label: "plan:1/3:active",
        total_steps: 3,
        completed_steps: 1,
        active: true,
        blocked: false,
        done: false,
        current_step: "分析 Bittensor 趋势",
        current_step_index: 2,
        current_step_status: "in_progress",
        last_completed_step: "查询北京天气",
        last_completed_step_index: 1,
        blocked_step: undefined,
        blocked_step_index: undefined,
        error: false,
      },
    });
  });

  it("ignores non-json outputs and treats clear results as no active plan", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "plan-1", tool: "plan", args: { action: "set" }, args_truncated: false } },
      { id: 3, type: "tool.result", data: { turn_id: "t1", call_id: "plan-1", exit_code: 0, result_summary: "{\"steps\":[{\"text\":\"first\",\"status\":\"in_progress\"}]}", result_truncated: false } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "plan-bad", tool: "plan", args: { action: "update" }, args_truncated: false } },
      { id: 5, type: "tool.result", data: { turn_id: "t1", call_id: "plan-bad", exit_code: 0, result_summary: "plan set", result_truncated: false } },
      { id: 6, type: "tool.request", data: { turn_id: "t1", call_id: "plan-2", tool: "plan", args: { action: "clear" }, args_truncated: false } },
      { id: 7, type: "tool.result", data: { turn_id: "t1", call_id: "plan-2", exit_code: 0, result_summary: "{\"steps\":[]}", result_truncated: false } },
    ]);

    expect(buildSessionPlanFromToolResults(session)).toBeUndefined();
  });

  it("treats an empty step snapshot as a cleared plan", () => {
    const session = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "plan-1", tool: "plan", args: { action: "set" }, args_truncated: false } },
      { id: 3, type: "tool.result", data: { turn_id: "t1", call_id: "plan-1", exit_code: 0, result_summary: "{\"steps\":[{\"text\":\"first\",\"status\":\"in_progress\"}]}", result_truncated: false } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "plan-2", tool: "plan", args: { action: "clear" }, args_truncated: false } },
      { id: 5, type: "tool.result", data: { turn_id: "t1", call_id: "plan-2", exit_code: 0, result_summary: "{\"steps\":[]}", result_truncated: false } },
    ]);

    expect(buildSessionPlanFromToolResults(session)).toBeUndefined();
  });
});
