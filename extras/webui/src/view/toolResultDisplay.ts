export interface ToolResultSource {
  path?: string;
  tool?: string;
  originalTool?: string;
  source?: string;
}

export function showsChatArtifact(source: ToolResultSource): boolean {
  if (isToolResultStoragePath(source.path)) return false;
  return !isRawSourceCapture(source);
}

export function showsWorkbenchArtifact(source: ToolResultSource): boolean {
  return !isRawSourceCapture(source);
}

export function showsResultStorageChrome(source: ToolResultSource): boolean {
  if (isToolResultStoragePath(source.path)) return false;
  return !isRawSourceCapture(source);
}

export function showsToolContextChrome(source: ToolResultSource): boolean {
  return !isRawSourceCapture(source);
}

function isRawSourceCapture(source: ToolResultSource): boolean {
  return toolNames(source).some((tool) => rawSourceCaptureTools.has(tool));
}

function isToolResultStoragePath(path: string | undefined): boolean {
  if (typeof path !== "string") return false;
  const normalized = path.replace(/\\/g, "/");
  if (/(?:^|\/)\.affent\/artifacts\/tool-results\//.test(normalized)) return true;
  return /(?:^|\/)\d{6}-call_[A-Za-z0-9_-]+\.txt$/.test(normalized);
}

function toolNames(source: ToolResultSource): string[] {
  return [source.tool, source.originalTool, source.source].filter((tool): tool is string => typeof tool === "string" && tool.length > 0);
}

const rawSourceCaptureTools = new Set([
  "browser_find",
  "browser_navigate",
  "browser_network",
  "browser_network_read",
  "browser_snapshot",
  "web_fetch",
]);
