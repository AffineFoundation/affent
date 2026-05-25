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

export function buildComposerTaskHint(text: string, runtime?: RuntimeCapabilityView): ComposerTaskHint | undefined {
  if (!looksLikeLiveResearch(text)) return undefined;

  if (!runtime) {
    return {
      label: "Current sources unknown",
      detail: "This request may need current sources; web access has not loaded for this chat yet.",
      tone: "unknown",
    };
  }

  if (runtime.research === "off") {
    return {
      label: "Needs current sources",
      detail: "Web access is not available here; results may be incomplete unless you provide sources.",
      tone: "warning",
    };
  }

  if (runtime.research === "limited") {
    return {
      label: "Direct sources help",
      detail: "Discovery is limited; paste URLs or files if this task depends on current information.",
      tone: "warning",
    };
  }

  if (runtime.research === "unknown") {
    return {
      label: "Current sources unknown",
      detail: "Send once to refresh this chat's capabilities before relying on current information.",
      tone: "unknown",
    };
  }

  return undefined;
}

function looksLikeLiveResearch(text: string): boolean {
  const normalized = text.toLowerCase();
  if (normalized.trim().length < 8) return false;
  if (liveResearchPhrases.some((phrase) => normalized.includes(phrase))) return true;
  return liveResearchWords.some((term) => new RegExp(`\\b${term}\\b`, "i").test(normalized));
}
