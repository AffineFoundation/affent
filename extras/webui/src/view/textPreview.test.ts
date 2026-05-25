import { describe, expect, it } from "vitest";
import { markdownToPlainText, previewText, summarizeAnswerPreview, summarizePreview } from "./textPreview";

describe("textPreview", () => {
  it("removes markdown table syntax from compact previews", () => {
    const preview = previewText([
      "## Affine（Bittensor Subnet 120）公开信息调查报告",
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
    ].join("\n"));

    expect(preview).toBe("Affine（Bittensor Subnet 120）公开信息调查报告 重要前提说明：两个 Affine 经过查阅，存在两个同名项目需要区分： 本报告仅针对后者。");
    expect(preview).not.toContain("|");
    expect(preview).not.toContain("------");
  });

  it("skips generic report preambles before summarizing", () => {
    const preview = summarizePreview(
      "现在我收集到了充分的数据。让我整理最终报告。\n\n## Affine\n\nAffine 是 Bittensor 子网。",
      120,
    );

    expect(preview).toBe("Affine Affine 是 Bittensor 子网。");
    expect(preview).not.toContain("现在我收集到了充分的数据");
  });

  it("summarizes report answers from the first fact table for header previews", () => {
    const preview = summarizeAnswerPreview([
      "# Affine（Bittensor Subnet 120）调研报告",
      "",
      "> **说明：** 以下报告完全基于本 session 两轮工具调用的实际输出结果整理。",
      "",
      "## 1. Affine 是什么 / 哪个 Subnet",
      "",
      "| 字段 | 值 |",
      "|---|---|",
      "| **Subnet ID (NetUID)** | **120** |",
      "| **定位** | \"Reason Mining\" — 首个激励式 RL Reasoning 子网 |",
      "| **官方站点** | <https://www.affine.io/> |",
    ].join("\n"), 160);

    expect(preview).toBe("Affine（Bittensor Subnet 120）调研报告: Subnet ID (NetUID) 120 · 定位 Reason Mining");
    expect(preview).not.toContain("说明");
    expect(preview).not.toContain("官方站点");
  });

  it("keeps code blocks and tables intact in plain text copies", () => {
    const plain = markdownToPlainText([
      "# Affine",
      "",
      "| Name | Value |",
      "| --- | --- |",
      "| Branch | feature/x |",
      "",
      "```ts",
      "const value = 1;",
      "```",
      "",
      "- item one",
    ].join("\n"));

    expect(plain).toContain("Affine");
    expect(plain).toContain("Name\tValue");
    expect(plain).toContain("Branch\tfeature/x");
    expect(plain).toContain("const value = 1;");
    expect(plain).toContain("item one");
    expect(plain).not.toContain("#");
    expect(plain).not.toContain("```");
    expect(plain).not.toContain("| --- |");
  });
});
