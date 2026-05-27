import { describe, expect, it } from "vitest";
import { buildComposerTaskHint } from "./composerTaskHint";
import type { RuntimeCapabilityView } from "./runtimeCapabilities";

describe("buildComposerTaskHint", () => {
  it("warns when a current research task is typed into a local runtime", () => {
    expect(buildComposerTaskHint("Analyze Affine recent market trends and Twitter reaction", runtime("off"))).toEqual({
      label: "Needs current sources",
      detail: "Web access is off here; paste URLs, docs, or files so the task can use current sources.",
      tone: "warning",
    });
  });

  it("surfaces unconfirmed research capability for saved chats", () => {
    expect(buildComposerTaskHint("分析 affine 最近的发展趋势和币价", runtime("unknown"))).toEqual({
      label: "Current sources not confirmed",
      detail: "Send once to confirm this chat's sources, or paste URLs, docs, or files now if you already have them.",
      tone: "unknown",
    });
  });

  it("warns when no capability snapshot exists for the first research task", () => {
    expect(buildComposerTaskHint("check latest market news")).toEqual({
      label: "Current sources not confirmed",
      detail: "Send once to confirm this chat's sources, or paste URLs, docs, or files now if you already have them.",
      tone: "unknown",
    });
  });

  it("warns for natural external information-gathering prompts", () => {
    expect(buildComposerTaskHint("Affine 是 Bittensor 的一个子网，请收集信息向我介绍", runtime("limited"))).toEqual({
      label: "Direct sources help",
      detail: "Discovery is limited; paste official URLs, docs, or files if this task depends on current information.",
      tone: "warning",
    });
    expect(buildComposerTaskHint("Gather information from official sources and tweets about Affine", runtime("off"))).toEqual({
      label: "Needs current sources",
      detail: "Web access is off here; paste URLs, docs, or files so the task can use current sources.",
      tone: "warning",
    });
  });

  it("stays quiet for code discovery when workspace tools are available", () => {
    expect(buildComposerTaskHint("find the implementation of repo_search in this workspace", runtimeWithRepoSearch())).toBeUndefined();
    expect(buildComposerTaskHint("find the implementation of symbol_context in this workspace", runtimeWithSymbolContext())).toBeUndefined();
  });

  it("warns when code discovery cannot use local project tools", () => {
    expect(buildComposerTaskHint("search the repo for the session capability wiring", runtimeWithFilesUnavailable())).toEqual({
      label: "Local project tools are off",
      detail: "Paste file paths, snippets, or a workspace snapshot so the task can still use direct evidence.",
      tone: "warning",
    });
  });

  it("guides skill installation through the review-and-confirm workflow", () => {
    expect(buildComposerTaskHint("help me install a skill from github", runtimeWithSkillInstall())).toEqual({
      label: "Skill install ready",
      detail: "Affent can inspect a skill source and ask for confirmation before installing it.",
      tone: "ready",
    });
  });

  it("explains skill installation when the runtime lacks that surface", () => {
    expect(buildComposerTaskHint("install a skill from github")).toEqual({
      label: "Skill install workflow",
      detail: "Send once to confirm the chat's tools, or paste the skill URL, repository, or exact SKILL.md body.",
      tone: "unknown",
    });
  });

  it("explains when code discovery needs workspace evidence", () => {
    expect(buildComposerTaskHint("search the repo for the session capability wiring")).toEqual({
      label: "Workspace tools not confirmed",
      detail: "Send once to confirm this chat's code tools, or paste file paths now if you already have them.",
      tone: "unknown",
    });
  });

  it("stays quiet for local project work and research-ready runtimes", () => {
    expect(buildComposerTaskHint("recap the project architecture", runtime("off"))).toBeUndefined();
    expect(buildComposerTaskHint("recap the project architecture")).toBeUndefined();
    expect(buildComposerTaskHint("check latest market news", runtime("ready"))).toBeUndefined();
  });
});

function runtime(research: RuntimeCapabilityView["research"]): RuntimeCapabilityView {
  return {
    headline: research,
    detail: research,
    tone: research === "ready" ? "ready" : research === "unknown" ? "unknown" : "warning",
    research,
    chips: [],
  };
}

function runtimeWithRepoSearch(): RuntimeCapabilityView {
  return {
    headline: "project",
    detail: "project",
    tone: "ready",
    research: "off",
    chips: [
      { group: "Research", label: "No live sources", detail: "Current outside information may be incomplete.", tone: "warning" },
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Discovery", label: "Repo search", detail: "Can search workspace text before broad file reads.", tone: "ready" },
    ],
  };
}

function runtimeWithSymbolContext(): RuntimeCapabilityView {
  return {
    headline: "project",
    detail: "project",
    tone: "ready",
    research: "off",
    chips: [
      { group: "Research", label: "No live sources", detail: "Current outside information may be incomplete.", tone: "warning" },
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Discovery", label: "Symbol index + repo search", detail: "Can locate declarations and search workspace text before broad file reads.", tone: "ready" },
    ],
  };
}

function runtimeWithFilesUnavailable(): RuntimeCapabilityView {
  return {
    headline: "project",
    detail: "project",
    tone: "warning",
    research: "off",
    chips: [
      { group: "Files", label: "Unavailable", detail: "Workspace files are unavailable.", tone: "warning" },
    ],
  };
}

function runtimeWithSkillInstall(): RuntimeCapabilityView {
  return {
    headline: "project",
    detail: "project",
    tone: "warning",
    research: "off",
    chips: [
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Skills", label: "Skill install", detail: "Can install and activate runtime skills without restarting.", tone: "ready" },
    ],
  };
}
