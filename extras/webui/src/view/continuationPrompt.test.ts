import { describe, expect, it } from "vitest";
import { conversationTopicFromTurns, isContinuationPrompt } from "./continuationPrompt";

describe("continuationPrompt", () => {
  it("recognizes continuation and finalization prompts", () => {
    expect(isContinuationPrompt("continue and summarize")).toBe(true);
    expect(isContinuationPrompt("请继续同一个任务。基于已有证据输出报告")).toBe(true);
    expect(isContinuationPrompt("不要再调用任何工具。直接基于本 session 前两轮结果输出最终报告。")).toBe(true);
    expect(isContinuationPrompt("真实收集 Affine 的相关信息")).toBe(false);
  });

  it("uses the latest non-continuation user task as the conversation topic", () => {
    expect(conversationTopicFromTurns([
      { userText: "真实收集 Affine 的相关信息" },
      { userText: "继续完成同一个 Affine 调研任务" },
      { userText: "不要再调用任何工具。直接基于本 session 前两轮结果输出最终报告。" },
    ])).toBe("真实收集 Affine 的相关信息");
  });

  it("falls back to the first user message when a chat only contains continuation text", () => {
    expect(conversationTopicFromTurns([
      { userText: "继续完成同一个任务" },
      { userText: "不要再调用任何工具。直接输出最终报告。" },
    ])).toBe("继续完成同一个任务");
  });
});
