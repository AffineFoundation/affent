import { describe, expect, it } from "vitest";
import {
  activationCoverage,
  activationSummary,
  skillDraft,
  skillEvidenceText,
  skillKindLabel,
  skillMatchesQuery,
  skillSearchMatches,
  skillSizeLabel,
  skillSummaryTags,
  skillUpdateDraft,
} from "./sessionSkills";

describe("sessionSkills view helpers", () => {
  it("builds actionable skill evidence from metadata and loaded content", () => {
    const skill = {
      name: "coding_repair_workflow",
      description: "Repair code by reproducing failures first.",
      source: "embed:skill",
      runtime: false,
      triggers: ["fix", "repair"],
      required_tools: ["workspace"],
      body_preview: "AFFENT ACTIVE SKILL: coding_repair_workflow",
      body_bytes: 96,
    };

    expect(skillKindLabel(skill)).toBe("Built in");
    expect(skillSizeLabel(skill)).toBe("96 B");
    expect(activationSummary(skill)).toBe("Triggers: fix, repair");
    expect(skillSummaryTags(skill)).toEqual(["2 triggers", "1 tool"]);
    expect(skillMatchesQuery(skill, "workspace")).toBe(true);
    expect(skillSearchMatches(skill, "repair")).toEqual([
      "Name: coding_repair_workflow",
      "Summary: Repair code by reproducing failures first.",
      "Trigger: repair",
    ]);
    expect(skillSearchMatches(skill, "workspace")).toEqual(["Tool: workspace"]);
    expect(skillEvidenceText(skill, "AFFENT ACTIVE SKILL: coding_repair_workflow\nReproduce first.")).toBe([
      "Skill evidence for coding_repair_workflow",
      "Kind: Built in",
      "Summary: Repair code by reproducing failures first.",
      "Source: embed:skill",
      "Size: 96 B",
      "Triggers: fix, repair",
      "Required tools: workspace",
      "Preview: AFFENT ACTIVE SKILL: coding_repair_workflow",
      "Loaded content:\nAFFENT ACTIVE SKILL: coding_repair_workflow\nReproduce first.",
    ].join("\n"));
    expect(skillDraft(skill)).toContain("apply, update, or replace");
    expect(skillUpdateDraft(skill, "AFFENT ACTIVE SKILL: coding_repair_workflow\nReproduce first.")).toContain("Review and update this reusable skill");
  });

  it("summarizes activation coverage across skills", () => {
    expect(activationCoverage([
      { name: "one", runtime: true, body_bytes: 10, triggers: ["test"] },
      { name: "two", runtime: false, body_bytes: 20, required_tools: ["workspace"] },
    ])).toBe(" · 1 triggerable · 1 tool-bound");
  });
});
