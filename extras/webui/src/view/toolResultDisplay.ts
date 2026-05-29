export interface ToolResultSource {
  tool?: string;
  originalTool?: string;
}

export function showsChatArtifact(source: ToolResultSource): boolean {
  return !isRawSourceCapture(source);
}

export function showsWorkbenchArtifact(source: ToolResultSource): boolean {
  return !isRawSourceCapture(source);
}

export function showsResultStorageChrome(source: ToolResultSource): boolean {
  return !isRawSourceCapture(source);
}

export function showsToolContextChrome(source: ToolResultSource): boolean {
  return !isRawSourceCapture(source);
}

function isRawSourceCapture(source: ToolResultSource): boolean {
  return toolNames(source).some((tool) => rawSourceCaptureTools.has(tool));
}

function toolNames(source: ToolResultSource): string[] {
  return [source.tool, source.originalTool].filter((tool): tool is string => typeof tool === "string" && tool.length > 0);
}

const rawSourceCaptureTools = new Set([
  "web_fetch",
]);
