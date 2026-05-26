export type SourceEvidenceStatus = "verified" | "dynamic_partial" | "discovery_only" | "network";

export interface SourceAccessInfo {
  urlField: string;
  accessedUrl: string;
  requestedUrl?: string;
  pageTextBelow?: string;
  renderedBrowserSourceStatus?: string;
  sourceMethod?: string;
  jsonPath?: string;
  status: SourceEvidenceStatus;
}

export function describeSourceAccess(result: string | undefined): SourceAccessInfo | undefined {
  const line = firstSourceAccessLine(result);
  if (!line) return undefined;
  const fields = sourceAccessFields(line);
  const urlField = ["fetched_url", "browser_rendered_url", "browser_network_url"].find((field) => fields[field]);
  if (!urlField) return undefined;
  const info: SourceAccessInfo = {
    urlField,
    accessedUrl: fields[urlField],
    requestedUrl: fields.requested_url,
    pageTextBelow: fields.page_text_below,
    renderedBrowserSourceStatus: fields.rendered_browser_source_status,
    sourceMethod: fields.source_method,
    jsonPath: firstJSONPathLine(result),
    status: "verified",
  };
  info.status = sourceEvidenceStatus(info, result ?? "");
  return info;
}

export function sourceEvidenceLabel(info: SourceAccessInfo): string {
  switch (info.status) {
    case "network":
      return "network source";
    case "dynamic_partial":
      return "partial source";
    case "discovery_only":
      return "discovery source";
    case "verified":
      return "verified source";
  }
}

function sourceEvidenceStatus(info: SourceAccessInfo, result: string): SourceEvidenceStatus {
  if (info.urlField === "browser_network_url" || info.sourceMethod === "network_xhr_fetch") return "network";
  if (info.pageTextBelow === "partial_dynamic_page_evidence" || info.renderedBrowserSourceStatus === "partial_dynamic_page_evidence" || result.includes("empty_dynamic_metric_widgets:")) {
    return "dynamic_partial";
  }
  if (info.pageTextBelow === "search_results_discovery_only" || info.pageTextBelow === "not_found_page_discovery_only" || info.renderedBrowserSourceStatus === "search_results_discovery_only" || info.renderedBrowserSourceStatus === "not_found_page_discovery_only") {
    return "discovery_only";
  }
  return "verified";
}

function firstSourceAccessLine(result: string | undefined): string | undefined {
  if (!result) return undefined;
  return result.split(/\r?\n/).find((line) => line.trimStart().startsWith("SourceAccess:"))?.trim();
}

function firstJSONPathLine(result: string | undefined): string | undefined {
  if (!result) return undefined;
  const value = result.split(/\r?\n/).find((line) => line.trimStart().startsWith("JSON_PATH:"))?.replace(/^\s*JSON_PATH:\s*/, "").trim();
  return value || undefined;
}

function sourceAccessFields(line: string): Record<string, string> {
  const fields: Record<string, string> = {};
  const body = line.trim().replace(/^SourceAccess:\s*/, "");
  for (const part of body.split(";")) {
    const [rawKey, ...rawValue] = part.trim().split("=");
    const key = rawKey?.trim();
    const value = rawValue.join("=").trim();
    if (!key || !value) continue;
    fields[key] = value;
  }
  return fields;
}
