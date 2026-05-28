export type ChangeDiffLineKind = "meta" | "hunk" | "add" | "remove" | "context";

export interface ChangeDiffLine {
  kind: ChangeDiffLineKind;
  text: string;
}

export interface ChangeDiffEvidence {
  additions: number;
  deletions: number;
  preview: ChangeDiffLine[];
  truncated: boolean;
}

export function extractChangeDiff(source?: string): ChangeDiffEvidence | undefined {
  if (!source) return undefined;
  const rawLines = source.split(/\r?\n/);
  const start = rawLines.findIndex((line) => /^diff --git\s|^---\s/.test(line));
  if (start === -1) return undefined;
  const diffLines = rawLines.slice(start);
  if (!diffLines.some((line) => /^@@\s/.test(line))) return undefined;

  let additions = 0;
  let deletions = 0;
  const maxLines = 80;
  const preview: ChangeDiffLine[] = [];
  for (const line of diffLines) {
    if (line.startsWith("+") && !line.startsWith("+++")) additions += 1;
    if (line.startsWith("-") && !line.startsWith("---")) deletions += 1;
    if (preview.length < maxLines) preview.push({ kind: diffLineKind(line), text: line || " " });
  }
  return { additions, deletions, preview, truncated: diffLines.length > maxLines };
}

function diffLineKind(line: string): ChangeDiffLineKind {
  if (line.startsWith("@@")) return "hunk";
  if (line.startsWith("+") && !line.startsWith("+++")) return "add";
  if (line.startsWith("-") && !line.startsWith("---")) return "remove";
  if (/^(diff --git|index\s|---\s|\+\+\+\s)/.test(line)) return "meta";
  return "context";
}
