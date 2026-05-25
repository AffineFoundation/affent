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
  "twitter",
  "tweet",
] as const;

const liveResearchPhrases = [
  "market cap",
  "x.com",
  "taostats",
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
  "评价",
] as const;

export function buildComposerTaskHint(text: string, runtime?: RuntimeCapabilityView): ComposerTaskHint | undefined {
  if (!runtime || !looksLikeLiveResearch(text)) return undefined;

  if (runtime.research === "off") {
    return {
      label: "Research tools off",
      detail: "This asks for current external information; enable web search or browser for reliable results.",
      tone: "warning",
    };
  }

  if (runtime.research === "limited") {
    return {
      label: "Research limited",
      detail: "Some web tools are available, but live search or browsing is not fully enabled.",
      tone: "warning",
    };
  }

  if (runtime.research === "unknown") {
    return {
      label: "Research tools unknown",
      detail: "Send the message to refresh this chat's tool status before relying on live research.",
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
