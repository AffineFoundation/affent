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
      label: "Current web info unavailable",
      detail: "This request needs live sources; results may be incomplete until search or page access is enabled.",
      tone: "warning",
    };
  }

  if (runtime.research === "limited") {
    return {
      label: "Current web access limited",
      detail: "This may need current sources; search or page browsing is only partially available.",
      tone: "warning",
    };
  }

  if (runtime.research === "unknown") {
    return {
      label: "Web access unknown",
      detail: "Send once to refresh this chat's web access before relying on current information.",
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
