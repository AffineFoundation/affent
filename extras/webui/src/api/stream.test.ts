import { describe, expect, it } from "vitest";
import { parseSSEBlock, rawEventFromSSE, readEventStream } from "./stream";

describe("SSE stream parsing", () => {
  it("parses affent SSE blocks into raw events", () => {
    const block = parseSSEBlock('event: tool.result\nid: 6\ndata: {"call_id":"c1","exit_code":0}\n');

    expect(block).toEqual({
      event: "tool.result",
      id: "6",
      data: '{"call_id":"c1","exit_code":0}',
    });
    expect(rawEventFromSSE(block!)).toEqual({
      id: 6,
      type: "tool.result",
      data: { call_id: "c1", exit_code: 0 },
    });
  });

  it("ignores keep-alive comments", () => {
    expect(parseSSEBlock(": ping")).toBeUndefined();
  });

  it("reads chunked streams and joins multiple data lines", async () => {
    const events: unknown[] = [];
    const resp = new Response(
      new ReadableStream<Uint8Array>({
        start(controller) {
          const enc = new TextEncoder();
          controller.enqueue(enc.encode("event: message.delta\nid: 7\ndata: {\"turn_id\":\"t1\","));
          controller.enqueue(enc.encode("\"delta\":\"hello\"}\n\n: ping\n\nevent: message.done\nid: 8\ndata: {\"turn_id\":\"t1\",\ndata: \"text\":\"hello\"}\n\n"));
          controller.close();
        },
      }),
    );

    await readEventStream(resp, { onEvent: (ev) => events.push(ev) });

    expect(events).toEqual([
      { id: 7, type: "message.delta", data: { turn_id: "t1", delta: "hello" } },
      { id: 8, type: "message.done", data: { turn_id: "t1", text: "hello" } },
    ]);
  });
});
