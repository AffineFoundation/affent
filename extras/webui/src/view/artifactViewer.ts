import type { ArtifactChunk } from "../api/sessions";

export interface ArtifactStatsView {
  loadedBytes: number;
  totalBytes: number;
  loadedPercent: number;
  matchCount: number;
  complete: boolean;
}

export interface ArtifactMatchPreview {
  lineNumber: number;
  text: string;
}

export function buildArtifactStats(chunk: ArtifactChunk, query: string): ArtifactStatsView {
  const loadedBytes = chunk.offset + chunk.text.length;
  return {
    loadedBytes,
    totalBytes: chunk.bytes,
    loadedPercent: percent(loadedBytes, chunk.bytes),
    matchCount: countMatches(chunk.text, query),
    complete: !chunk.hasMore,
  };
}

export function buildArtifactMatchPreviews(text: string, query: string, limit = 5): ArtifactMatchPreview[] {
  const needle = query.trim().toLowerCase();
  if (!needle) return [];
  const previews: ArtifactMatchPreview[] = [];
  const lines = text.split(/\r?\n/);
  for (let index = 0; index < lines.length; index += 1) {
    if (!lines[index].toLowerCase().includes(needle)) continue;
    previews.push({ lineNumber: index + 1, text: compactLine(lines[index]) });
    if (previews.length >= limit) break;
  }
  return previews;
}

function percent(loaded: number, total: number): number {
  if (total <= 0) return 100;
  return Math.max(0, Math.min(100, Math.round((loaded / total) * 100)));
}

function compactLine(line: string): string {
  const compact = line.replace(/\s+/g, " ").trim();
  if (compact.length <= 160) return compact || "(blank line)";
  return `${compact.slice(0, 157).trimEnd()}...`;
}

function countMatches(text: string, query: string): number {
  const needle = query.trim().toLowerCase();
  if (!needle) return 0;
  let count = 0;
  let cursor = 0;
  const haystack = text.toLowerCase();
  while (cursor <= haystack.length) {
    const next = haystack.indexOf(needle, cursor);
    if (next === -1) break;
    count += 1;
    cursor = next + needle.length;
  }
  return count;
}
