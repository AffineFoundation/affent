export interface UserErrorSummary {
  title: string;
  detail: string;
}

export function summarizeUserError(code: string, message: string): UserErrorSummary {
  const text = message.trim();
  const refused = text.match(/dial tcp ([^"\s]+): connect: connection refused/i);
  if (refused?.[1]) {
    return {
      title: "Runtime provider is unreachable",
      detail: `The model endpoint at ${refused[1]} refused the connection.`,
    };
  }

  const upstreamStatus = text.match(/provider (?:returned|responded with) (\d{3})/i);
  if (upstreamStatus?.[1]) {
    return {
      title: "Provider returned an error",
      detail: `The model provider returned HTTP ${upstreamStatus[1]}.`,
    };
  }

  if (/timeout|deadline exceeded/i.test(text)) {
    return {
      title: "Provider request timed out",
      detail: "The model provider did not respond before the request timed out.",
    };
  }

  return {
    title: humanizeCode(code),
    detail: summarize(text || "The request ended with an error.", 140),
  };
}

function humanizeCode(code: string): string {
  const normalized = code.replace(/[_-]+/g, " ").trim();
  if (!normalized) return "Request failed";
  return normalized[0].toUpperCase() + normalized.slice(1);
}

function summarize(text: string, limit: number): string {
  const singleLine = text.replace(/\s+/g, " ").trim();
  if (singleLine.length <= limit) return singleLine;
  return `${singleLine.slice(0, Math.max(0, limit - 1)).trimEnd()}...`;
}
