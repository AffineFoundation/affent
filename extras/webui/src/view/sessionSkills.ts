import type { SessionSkillInfo } from "../api/sessions";
import { formatByteCount } from "./byteFormat";

export function skillKindLabel(skill: SessionSkillInfo): string {
  return skill.runtime ? "Custom" : "Built in";
}

export function skillOriginLabel(skill: SessionSkillInfo): string | undefined {
  if (!skill.source) return undefined;
  if (skill.source.startsWith("embed:")) return "Built-in library";
  if (skill.runtime) return "Account skill";
  return skill.source;
}

export function activationSummary(skill: SessionSkillInfo): string {
  const triggers = skill.triggers ?? skill.auto_activation?.any ?? [];
  if (triggers.length > 0) return `Triggers: ${triggers.slice(0, 3).join(", ")}${triggers.length > 3 ? "..." : ""}`;
  if (skill.required_tools && skill.required_tools.length > 0) return `Needs: ${skill.required_tools.join(", ")}`;
  return "";
}

export function activationCoverage(skills: readonly SessionSkillInfo[]): string {
  const triggerable = skills.filter((skill) => (skill.triggers?.length ?? skill.auto_activation?.any?.length ?? 0) > 0).length;
  const toolBound = skills.filter((skill) => (skill.required_tools?.length ?? 0) > 0).length;
  const parts: string[] = [];
  if (triggerable > 0) parts.push(`${triggerable} triggerable`);
  if (toolBound > 0) parts.push(`${toolBound} tool-bound`);
  return parts.length > 0 ? ` · ${parts.join(" · ")}` : "";
}

export function skillSummaryTags(skill: SessionSkillInfo): string[] {
  const tags: string[] = [];
  const triggers = skillTriggers(skill);
  if (triggers.length > 0) tags.push(`${triggers.length} trigger${triggers.length === 1 ? "" : "s"}`);
  const requiredTools = skill.required_tools?.length ?? 0;
  if (requiredTools > 0) tags.push(`${requiredTools} tool${requiredTools === 1 ? "" : "s"}`);
  return tags;
}

export function skillMatchesQuery(skill: SessionSkillInfo, query: string): boolean {
  return skillSearchText(skill).includes(query.trim().toLowerCase());
}

export function skillSearchMatches(skill: SessionSkillInfo, query: string): string[] {
  const search = query.trim().toLowerCase();
  if (!search) return [];
  const matches = [
    skill.name.toLowerCase().includes(search) ? `Name: ${skill.name}` : undefined,
    skill.description?.toLowerCase().includes(search) ? `Summary: ${skill.description}` : undefined,
    skill.source?.toLowerCase().includes(search) ? `Source: ${skill.source}` : undefined,
    ...skillTriggers(skill)
      .filter((trigger) => trigger.toLowerCase().includes(search))
      .map((trigger) => `Trigger: ${trigger}`),
    ...(skill.required_tools ?? [])
      .filter((tool) => tool.toLowerCase().includes(search))
      .map((tool) => `Tool: ${tool}`),
    skill.body_preview?.toLowerCase().includes(search) ? `Preview: ${skill.body_preview}` : undefined,
  ].filter((match): match is string => Boolean(match));
  return [...new Set(matches)].slice(0, 3);
}

export function skillSizeLabel(skill: SessionSkillInfo): string {
  return formatByteCount(skill.body_bytes);
}

export function skillEvidenceText(skill: SessionSkillInfo, body?: string): string {
  const triggers = skillTriggers(skill);
  const lines = [
    `Skill evidence for ${skill.name}`,
    `Kind: ${skillKindLabel(skill)}`,
    skill.description ? `Summary: ${skill.description}` : undefined,
    skill.source ? `Source: ${skill.source}` : undefined,
    `Size: ${skillSizeLabel(skill)}`,
    triggers.length > 0 ? `Triggers: ${triggers.join(", ")}` : undefined,
    skill.required_tools && skill.required_tools.length > 0 ? `Required tools: ${skill.required_tools.join(", ")}` : undefined,
    skill.body_preview ? `Preview: ${skill.body_preview}` : undefined,
    body ? `Loaded content:\n${body}` : undefined,
  ].filter((line): line is string => !!line);
  return lines.join("\n");
}

export function skillDraft(skill: SessionSkillInfo, body?: string): string {
  return [
    "Use this skill evidence to decide whether to apply, update, or replace the reusable workflow:",
    "",
    skillEvidenceText(skill, body),
  ].join("\n");
}

export function skillUpdateDraft(skill: SessionSkillInfo, body?: string): string {
  return [
    "Review and update this reusable skill if it is stale, incomplete, duplicated, or unsafe. Preserve useful activation rules and explain any replacement:",
    "",
    skillEvidenceText(skill, body),
  ].join("\n");
}

function skillTriggers(skill: SessionSkillInfo): string[] {
  return skill.triggers ?? skill.auto_activation?.any ?? [];
}

function skillSearchText(skill: SessionSkillInfo): string {
  return [
    skill.name,
    skill.description,
    skill.source,
    skill.body_preview,
    ...skillTriggers(skill),
    ...(skill.required_tools ?? []),
  ].filter(Boolean).join(" ").toLowerCase();
}
