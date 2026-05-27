const ERROR_SUMMARY_LIMIT = 96;

export function panelErrorSummary(subject: string, error?: string): string {
  const message = normalizePanelError(error);
  if (!message) return `${subject} failed`;
  return `${subject} failed: ${truncatePanelError(message)}`;
}

function normalizePanelError(error?: string): string {
  return (error ?? "").replace(/\s+/g, " ").trim();
}

function truncatePanelError(error: string): string {
  if (error.length <= ERROR_SUMMARY_LIMIT) return error;
  return `${error.slice(0, ERROR_SUMMARY_LIMIT - 3).trimEnd()}...`;
}
