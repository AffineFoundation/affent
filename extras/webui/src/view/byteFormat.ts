export function formatBytes(bytes?: number, omitted?: number, cap?: number, truncated?: boolean): string {
  const parts: string[] = [];
  if (bytes != null) parts.push(formatByteCount(bytes));
  if (cap != null && cap > 0 && cap !== bytes) parts.push(`cap ${formatByteCount(cap)}`);
  if (truncated && omitted != null && omitted > 0) parts.push(`${formatByteCount(omitted)} omitted`);
  return parts.length ? `(${parts.join(", ")})` : "";
}

export function formatByteCount(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "0 B";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KiB", "MiB", "GiB", "TiB"];
  let value = bytes / 1024;
  let unit = units[0];
  for (let i = 0; i < units.length; i += 1) {
    unit = units[i];
    if (value < 1024 || i === units.length - 1) break;
    value /= 1024;
  }
  const text = value >= 10 || Number.isInteger(value) ? value.toFixed(0) : value.toFixed(1);
  return `${trimTrailingZeros(text)} ${unit}`;
}

function trimTrailingZeros(text: string): string {
  return text.includes(".") ? text.replace(/\.0+$/, "").replace(/(\.\d*[1-9])0+$/, "$1") : text;
}
