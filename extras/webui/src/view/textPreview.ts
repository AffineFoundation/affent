export function summarizePreview(text: string, limit: number): string {
  const singleLine = previewText(text);
  return limitText(singleLine, limit);
}

export function markdownToPlainText(text: string): string {
  const lines = text.replace(/\r\n/g, "\n").split("\n");
  const output: string[] = [];
  let inCodeBlock = false;
  let fenceMarker = "";

  for (const line of lines) {
    const fence = codeFenceMarker(line);
    if (!inCodeBlock && fence) {
      appendBlankLine(output);
      inCodeBlock = true;
      fenceMarker = fence;
      continue;
    }

    if (inCodeBlock) {
      if (isClosingFence(line, fenceMarker)) {
        inCodeBlock = false;
        fenceMarker = "";
      } else {
        output.push(line);
      }
      continue;
    }

    if (looksLikeTableSeparator(line)) continue;
    if (looksLikeTableLine(line)) {
      const row = tablePlainTextRow(line);
      if (row) output.push(row);
      continue;
    }

    const cleaned = cleanPlainTextLine(line);
    if (cleaned) {
      output.push(cleaned);
    } else {
      appendBlankLine(output);
    }
  }

  return output.join("\n").trim();
}

export function summarizeAnswerPreview(text: string, limit: number): string {
  const report = reportFactPreview(text);
  if (report) return limitText(report, limit);
  return summarizePreview(text, limit);
}

function limitText(text: string, limit: number): string {
  if (text.length <= limit) return text;
  return `${text.slice(0, Math.max(0, limit - 1))}...`;
}

export function previewText(text: string): string {
  const plain = stripMarkdownTables(text)
    .replace(/```[\s\S]*?```/g, " code block ")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/^[ \t]{0,3}#{1,6}[ \t]+/gm, "")
    .replace(/^[ \t]{0,3}>[ \t]?/gm, "")
    .replace(/^[ \t]*[-*_]{3,}[ \t]*$/gm, " ")
    .replace(/[*_~]{1,3}([^*_~]+)[*_~]{1,3}/g, "$1")
    .replace(/^[ \t]*[-*+][ \t]+/gm, "")
    .replace(/^[ \t]*\d+[.)][ \t]+/gm, "")
    .replace(/\s+/g, " ")
    .trim();
  return removeAnswerPreamble(plain);
}

function stripMarkdownTables(text: string): string {
  const lines = text.split(/\r?\n/);
  const output: string[] = [];
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    if (!looksLikeTableLine(line)) {
      output.push(line);
      continue;
    }
    const next = lines[index + 1];
    const previous = lines[index - 1];
    const isTableStart = looksLikeTableSeparator(next) || looksLikeTableSeparator(line) || looksLikeTableLine(previous);
    if (!isTableStart) {
      output.push(line);
      continue;
    }
    while (index + 1 < lines.length && (looksLikeTableLine(lines[index + 1]) || looksLikeTableSeparator(lines[index + 1]))) {
      index += 1;
    }
    output.push(" ");
  }
  return output.join("\n");
}

function appendBlankLine(output: string[]): void {
  if (output.length === 0) return;
  if (output[output.length - 1] === "") return;
  output.push("");
}

function codeFenceMarker(line: string): string | undefined {
  const match = line.match(/^[ \t]{0,3}(`{3,}|~{3,})/);
  return match?.[1];
}

function isClosingFence(line: string, fenceMarker: string): boolean {
  if (!fenceMarker) return false;
  const pattern = new RegExp(`^[ \\t]{0,3}${escapeRegExp(fenceMarker)}[ \\t]*$`);
  return pattern.test(line);
}

function looksLikeTableLine(line: string | undefined): boolean {
  if (!line) return false;
  const trimmed = line.trim();
  if (!trimmed.includes("|")) return false;
  return trimmed.startsWith("|") || trimmed.endsWith("|") || (trimmed.match(/\|/g)?.length ?? 0) >= 2;
}

function looksLikeTableSeparator(line: string | undefined): boolean {
  if (!line) return false;
  return /^[\s|:-]+$/.test(line.trim()) && line.includes("-") && line.includes("|");
}

function tablePlainTextRow(line: string): string | undefined {
  const cells = splitTableCells(line).map(cleanPlainTextLine);
  if (cells.length === 0) return undefined;
  return cells.join("\t").trim();
}

function splitTableCells(line: string): string[] {
  const trimmed = line.trim();
  const cells = trimmed.split("|");
  if (trimmed.startsWith("|")) cells.shift();
  if (trimmed.endsWith("|")) cells.pop();
  return cells;
}

function removeAnswerPreamble(text: string): string {
  return text
    .replace(/^现在我收集到了充分的数据[。.\s]*让我整理最终报告[。.\s]*/u, "")
    .replace(/^我现在有了足够的信息来给你一个(?:全面、?|完整、?)?诚实的回答[。.\s]*/u, "")
    .replace(/^以下是基于我实际查阅的公开来源的整理[:：]?\s*/u, "")
    .trim();
}

function reportFactPreview(text: string): string | undefined {
  const lines = text.split(/\r?\n/);
  const heading = firstHeading(lines);
  const title = heading?.title;
  if (!title || !looksLikeReportTitle(title)) return undefined;

  const reportLines = lines.slice(heading.index + 1);
  const facts = firstTableFacts(reportLines).slice(0, 2);
  if (facts.length > 0) return `${title}: ${facts.join(" · ")}`;

  const paragraph = firstMeaningfulParagraph(reportLines);
  return paragraph ? `${title}: ${paragraph}` : title;
}

function firstHeading(lines: readonly string[]): { title: string; index: number } | undefined {
  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index];
    const match = line.match(/^[ \t]{0,3}#{1,6}[ \t]+(.+?)\s*#*\s*$/);
    if (match) return { title: cleanInlineMarkdown(match[1]), index };
  }
  return undefined;
}

function looksLikeReportTitle(title: string): boolean {
  return /(报告|调研|研究|介绍|总结|分析|review|report|summary|analysis)/i.test(title);
}

function firstTableFacts(lines: readonly string[]): string[] {
  for (let index = 0; index < lines.length; index += 1) {
    if (!looksLikeTableLine(lines[index])) continue;
    const facts: string[] = [];
    while (index < lines.length && (looksLikeTableLine(lines[index]) || looksLikeTableSeparator(lines[index]))) {
      const fact = tableFact(lines[index]);
      if (fact) facts.push(fact);
      index += 1;
    }
    if (facts.length > 0) return facts;
  }
  return [];
}

function tableFact(line: string | undefined): string | undefined {
  if (!line || looksLikeTableSeparator(line)) return undefined;
  const cells = line.split("|").map((cell) => cleanInlineMarkdown(cell)).filter(Boolean);
  if (cells.length !== 2) return undefined;
  const [label, rawValue] = cells;
  const value = compactFactValue(rawValue);
  if (/^(字段|指标|来源|值|key|field|metric|value)$/i.test(label)) return undefined;
  if (!value || value === "—" || value === "-") return undefined;
  if (/^(官方|github|discord|来源)/i.test(label)) return undefined;
  return `${label} ${value}`;
}

function compactFactValue(value: string): string {
  const primary = value
    .split(/\s+[—–-]\s+/)[0]
    .replace(/^["“](.+)["”]$/, "$1")
    .trim();
  return limitText(primary || value, 48);
}

function firstMeaningfulParagraph(lines: readonly string[]): string | undefined {
  for (const line of lines) {
    const cleaned = cleanInlineMarkdown(line);
    if (!cleaned) continue;
    if (looksLikeReportTitle(cleaned)) continue;
    if (/^(说明|重要前提说明|来源)[:：]/.test(cleaned)) continue;
    if (/^[-*_]{3,}$/.test(cleaned)) continue;
    if (looksLikeTableLine(line) || looksLikeTableSeparator(line)) continue;
    return cleaned;
  }
  return undefined;
}

function cleanInlineMarkdown(value: string): string {
  return value
    .replace(/^[ \t]{0,3}>[ \t]?/, "")
    .replace(/^[ \t]{0,3}#{1,6}[ \t]+/, "")
    .replace(/`([^`]+)`/g, "$1")
    .replace(/\[([^\]]+)\]\([^)]+\)/g, "$1")
    .replace(/[*_~]{1,3}([^*_~]+)[*_~]{1,3}/g, "$1")
    .replace(/^[-*+]\s+/, "")
    .replace(/^\d+[.)]\s+/, "")
    .replace(/\s+/g, " ")
    .trim();
}

function cleanPlainTextLine(value: string): string {
  return cleanInlineMarkdown(value)
    .replace(/^[ \t]{0,3}#{1,6}[ \t]+/, "")
    .replace(/^[ \t]{0,3}>[ \t]?/, "")
    .replace(/^[ \t]*[-*+][ \t]+/, "")
    .replace(/^[ \t]*\d+[.)][ \t]+/, "")
    .trim();
}

function escapeRegExp(value: string): string {
  return value.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
}
