import type { RawEvent } from "./events";

export interface ParsedSSEBlock {
  id?: string;
  event?: string;
  data: string;
}

export interface StreamEventsOptions {
  signal?: AbortSignal;
  lastEventId?: number;
  onEvent: (event: RawEvent) => void;
}

export type StreamEvents = (path: string, options: StreamEventsOptions) => Promise<void>;

/**
 * Parses one SSE event block. Comments and keep-alives return undefined.
 * Data lines are joined exactly as the EventSource algorithm specifies.
 */
export function parseSSEBlock(block: string): ParsedSSEBlock | undefined {
  let id: string | undefined;
  let event: string | undefined;
  const data: string[] = [];

  for (const line of block.split(/\r?\n/)) {
    if (line === "" || line.startsWith(":")) continue;
    const sep = line.indexOf(":");
    const field = sep === -1 ? line : line.slice(0, sep);
    const value = sep === -1 ? "" : line.slice(sep + (line[sep + 1] === " " ? 2 : 1));
    switch (field) {
      case "id":
        id = value;
        break;
      case "event":
        event = value;
        break;
      case "data":
        data.push(value);
        break;
      default:
        break;
    }
  }

  if (!id && !event && data.length === 0) return undefined;
  return { id, event, data: data.join("\n") };
}

export function rawEventFromSSE(block: ParsedSSEBlock): RawEvent {
  const id = Number.parseInt(block.id ?? "0", 10);
  if (!Number.isFinite(id)) {
    throw new Error(`invalid SSE id ${block.id}`);
  }
  if (!block.event) {
    throw new Error("SSE event type is required");
  }
  return {
    id,
    type: block.event,
    data: block.data === "" ? null : JSON.parse(block.data),
  };
}

export async function readEventStream(
  resp: Response,
  options: StreamEventsOptions,
): Promise<void> {
  if (!resp.body) throw new Error("event stream response has no body");

  const reader = resp.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";

  try {
    for (;;) {
      const { value, done } = await reader.read();
      if (done) break;
      buffer += decoder.decode(value, { stream: true }).replace(/\r\n/g, "\n");
      buffer = drainSSEBuffer(buffer, options.onEvent);
    }
    buffer += decoder.decode();
    drainSSEBuffer(buffer, options.onEvent);
  } finally {
    reader.releaseLock();
  }
}

function drainSSEBuffer(buffer: string, onEvent: (event: RawEvent) => void): string {
  for (;;) {
    const idx = buffer.indexOf("\n\n");
    if (idx === -1) return buffer;
    const rawBlock = buffer.slice(0, idx);
    buffer = buffer.slice(idx + 2);
    const block = parseSSEBlock(rawBlock);
    if (!block) continue;
    onEvent(rawEventFromSSE(block));
  }
}
