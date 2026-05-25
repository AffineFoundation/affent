export function isContinuationPrompt(text: string): boolean {
  const normalized = text.replace(/\s+/g, " ").trim().toLowerCase();
  if (!normalized) return false;
  return [
    /^continue\b/,
    /^resume\b/,
    /^please continue\b/,
    /^continue from\b/,
    /^continue after\b/,
    /^continue with\b/,
    /^continue the same\b/,
    /^same task\b/,
    /^use this\b/,
    /^use the already\b/,
    /^based on (the )?(previous|already collected|existing)\b/,
    /^go on\b/,
    /^pick up\b/,
    /^继续/,
    /^请继续/,
    /^继续完成/,
    /^从这里继续/,
    /^接着/,
    /^同一个任务/,
    /^上一轮/,
    /^基于(本|已有|前面|上面)/,
    /^不要再调用/,
    /^不要使用工具/,
    /^直接基于/,
  ].some((pattern) => pattern.test(normalized));
}

export function conversationTopicFromTurns<T extends { userText?: string }>(
  turns: readonly T[],
): string | undefined {
  const topicTurn = [...turns].reverse().find((turn) => turn.userText && !isContinuationPrompt(turn.userText));
  const fallbackTurn = turns.find((turn) => turn.userText);
  return topicTurn?.userText || fallbackTurn?.userText || undefined;
}
