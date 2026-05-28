export type AutomationActionKind = "loop_setup" | "checkin" | "loop_tick" | "daily";

const loopSetupIntentPhrases = [
  "long running",
  "long-running",
  "keep improving",
  "keep working",
  "keep monitoring",
  "monitor continuously",
  "check in",
  "check-in",
  "schedule",
  "scheduled",
  "timer",
  "recurring",
  "repeat",
  "several days",
  "for days",
  "over days",
  "daily",
  "weekly",
  "hourly",
  "every hour",
  "every day",
  "every week",
  "loop",
  "automation",
  "automate",
  "持续",
  "长期",
  "长线",
  "定期",
  "定时",
  "自动",
  "循环",
  "监控",
  "跟踪",
  "每天",
  "每周",
  "每小时",
] as const;

export function automationActionLabel(kind: AutomationActionKind, busy = false): string {
  if (busy) return kind === "loop_setup" ? "Setting up" : "Scheduling";
  if (kind === "loop_setup") return "Set up long-running loop";
  if (kind === "checkin") return "Schedule 1h check-in";
  if (kind === "loop_tick") return "Schedule 30m loop tick";
  return "Schedule daily check-in";
}

export function shouldOfferLoopSetupAction(text: string): boolean {
  const normalized = text.toLowerCase().replace(/\s+/g, " ").trim();
  if (normalized.length < 8) return false;
  return loopSetupIntentPhrases.some((phrase) => normalized.includes(phrase));
}
