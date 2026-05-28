import type { RuntimeCapabilityView } from "./runtimeCapabilities";

export type ComposerTaskHintTone = "ready" | "warning" | "unknown";

export interface ComposerTaskHint {
  label: string;
  detail: string;
  tone: ComposerTaskHintTone;
}

const liveResearchWords = [
  "latest",
  "recent",
  "today",
  "yesterday",
  "current",
  "now",
  "trend",
  "trends",
  "market",
  "price",
  "prices",
  "valuation",
  "news",
  "source",
  "sources",
  "twitter",
  "tweet",
  "tweets",
  "whitepaper",
] as const;

const liveResearchPhrases = [
  "collect information",
  "gather information",
  "collect info",
  "gather info",
  "official site",
  "official website",
  "online source",
  "online sources",
  "market cap",
  "social sentiment",
  "x.com",
  "taostats",
  "收集信息",
  "收集资料",
  "检索信息",
  "检索资料",
  "查询信息",
  "查询资料",
  "查资料",
  "相关信息",
  "官方网站",
  "官方资料",
  "官网",
  "最近",
  "当前",
  "今天",
  "昨天",
  "趋势",
  "市场",
  "价格",
  "币价",
  "市值",
  "新闻",
  "推特",
  "推文",
  "社媒",
  "评价",
] as const;

const codeDiscoveryWords = [
  "repo",
  "repository",
  "codebase",
  "source",
  "symbol",
  "function",
  "class",
  "module",
  "package",
  "file",
  "files",
  "implementation",
  "grep",
  "search",
  "inspect",
] as const;

const codeDiscoveryPhrases = [
  "find the file",
  "find the files",
  "find file",
  "find files",
  "where is",
  "where are",
  "search the repo",
  "search repository",
  "search codebase",
  "look in the repo",
  "look in repository",
  "local project",
  "代码",
  "源码",
  "仓库",
  "实现",
  "函数",
  "类",
  "模块",
  "包",
  "文件",
] as const;

const skillInstallWords = [
  "skill",
  "skills",
  "github",
  "gitee",
  "workflow",
  "workflow.md",
  "skill.md",
  "skill.json",
] as const;

const skillInstallPhrases = [
  "install skill",
  "install a skill",
  "install the skill",
  "add a skill",
  "add skill",
  "find a skill",
  "find the skill",
  "search for a skill",
  "search skill",
  "skill install",
  "skill installer",
  "skill workflow",
  "技能",
  "安装 skill",
  "安装一个 skill",
  "安装技能",
  "找 skill",
  "找一个 skill",
  "查找 skill",
  "帮我找一个 skill",
] as const;

export function buildComposerTaskHint(text: string, runtime?: RuntimeCapabilityView): ComposerTaskHint | undefined {
  if (looksLikeLiveResearch(text)) {
    return buildLiveResearchHint(runtime);
  }
  if (looksLikeSkillInstall(text)) {
    return buildSkillInstallHint(runtime);
  }
  if (looksLikeCodeDiscovery(text)) {
    return buildCodeDiscoveryHint(runtime);
  }
  return undefined;
}

function buildLiveResearchHint(runtime?: RuntimeCapabilityView): ComposerTaskHint | undefined {
  if (!runtime) {
    return undefined;
  }

  if (runtime.research === "off") {
    return {
      label: "Needs current sources",
      detail: "Web access is off here; paste URLs, docs, or files so the task can use current sources.",
      tone: "warning",
    };
  }

  if (runtime.research === "limited") {
    return {
      label: "Direct sources help",
      detail: "Discovery is limited; paste official URLs, docs, or files if this task depends on current information.",
      tone: "warning",
    };
  }

  if (runtime.research === "unknown") {
    return undefined;
  }

  return undefined;
}

function buildCodeDiscoveryHint(runtime?: RuntimeCapabilityView): ComposerTaskHint | undefined {
  if (!runtime) {
    return undefined;
  }

  if (runtime.chips.some((chip) => chip.group === "Files" && chip.label === "Unavailable")) {
    return {
      label: "Local project tools are off",
      detail: "Paste file paths, snippets, or a workspace snapshot so the task can still use direct evidence.",
      tone: "warning",
    };
  }

  return undefined;
}

function buildSkillInstallHint(runtime?: RuntimeCapabilityView): ComposerTaskHint | undefined {
  if (!runtime) {
    return undefined;
  }

  if (runtime.chips.some((chip) => chip.group === "Skills" && chip.label === "Skill install")) {
    return {
      label: "Skill install ready",
      detail: "Affent can inspect a skill source and ask for confirmation before installing it.",
      tone: "ready",
    };
  }

  return {
    label: "Skill install unavailable",
    detail: "Paste the exact SKILL.md body or ask for a runtime with skill install enabled before trying to install it.",
    tone: "warning",
  };
}

function looksLikeLiveResearch(text: string): boolean {
  const normalized = text.toLowerCase();
  if (normalized.trim().length < 8) return false;
  if (liveResearchPhrases.some((phrase) => normalized.includes(phrase))) return true;
  return liveResearchWords.some((term) => new RegExp(`\\b${term}\\b`, "i").test(normalized));
}

function looksLikeCodeDiscovery(text: string): boolean {
  const normalized = text.toLowerCase();
  if (normalized.trim().length < 8) return false;
  if (codeDiscoveryPhrases.some((phrase) => normalized.includes(phrase))) return true;
  return codeDiscoveryWords.some((term) => new RegExp(`\\b${term}\\b`, "i").test(normalized));
}

function looksLikeSkillInstall(text: string): boolean {
  const normalized = text.toLowerCase();
  if (normalized.trim().length < 8) return false;
  if (skillInstallPhrases.some((phrase) => normalized.includes(phrase))) return true;
  return skillInstallWords.some((term) => new RegExp(`\\b${term}\\b`, "i").test(normalized))
    && (normalized.includes("install") || normalized.includes("add") || normalized.includes("find") || normalized.includes("search") || normalized.includes("获取") || normalized.includes("安装") || normalized.includes("找"));
}
