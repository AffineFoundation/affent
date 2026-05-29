import { describe, expect, it } from "vitest";
import {
  activationCoverage,
  activationSummary,
  skillKindLabel,
  matchingSkillsForPrompt,
  skillMatchesQuery,
  skillSearchMatches,
  skillSizeLabel,
  skillSummaryTags,
} from "./sessionSkills";

describe("sessionSkills view helpers", () => {
  it("builds scannable skill metadata and search matches", () => {
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
  });

  it("summarizes activation coverage across skills", () => {
    expect(activationCoverage([
      { name: "one", runtime: true, body_bytes: 10, triggers: ["test"] },
      { name: "two", runtime: false, body_bytes: 20, required_tools: ["workspace"] },
    ])).toBe(" · 1 triggerable · 1 tool-bound");
  });

  it("matches skills against a task without sending anything to chat", () => {
    const matches = matchingSkillsForPrompt([
      { name: "browser_source_workflow", runtime: false, body_bytes: 20, auto_activation: { any: ["browser evidence"] } },
      { name: "release_gate", runtime: true, body_bytes: 20, auto_activation: { all_any: [["release", "ship"], ["test", "gate"]] } },
      { name: "manual", runtime: true, body_bytes: 20 },
    ], "Run the release test gate before shipping");

    expect(matches.map((match) => [match.skill.name, match.reason])).toEqual([
      ["release_gate", "auto: release + test"],
    ]);
  });
});
