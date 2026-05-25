import { describe, expect, it } from "vitest";
import { reduceRawEvents } from "../store/reduce";
import { detectConstraintDeviations } from "./constraintDeviation";

describe("detectConstraintDeviations", () => {
  it("flags tool use after a no-tool finalization prompt", () => {
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
    ]).turns[0];

    expect(detectConstraintDeviations(turn)).toEqual([
      {
        id: "no-tools",
        summary: "Tools were used after the user asked for a no-tool reply.",
        detail: "Used web_fetch after the message asked not to call tools.",
        toolNames: ["web_fetch"],
      },
    ]);
  });

  it("flags shell use after a no-shell instruction", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "user.message", data: { turn_id: "t1", text: "不要为了搜索而用 shell，只读取这些来源。" } },
      {
        id: 3,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "shell",
          args: { command: "curl https://example.com" },
          args_truncated: false,
          args_bytes: 32,
          args_omitted_bytes: 0,
          args_cap_bytes: 8192,
        },
      },
    ]).turns[0];

    expect(detectConstraintDeviations(turn)[0]).toMatchObject({
      id: "no-shell",
      detail: "Used shell 1 time after the message asked not to use shell.",
      toolNames: ["shell"],
    });
  });
});
