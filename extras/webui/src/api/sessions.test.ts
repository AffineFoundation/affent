import { describe, expect, it, vi } from "vitest";
import { ApiClient } from "./client";
import {
  addSessionMemory,
  cancelSessionTurn,
  createSession,
  deleteSkill,
  deleteSessionLoopProtocol,
  deleteSession,
  getSessionHistory,
  getSessionLoopProtocol,
  getSessionPlan,
  installSkill,
  listSessions,
  listSkills,
  readSessionArtifact,
  readSkill,
  removeSessionMemory,
  replaceSessionMemory,
  runSessionCommand,
  sendSessionMessage,
  sessionArtifactPath,
  streamSessionEvents,
  updateSessionLoopProtocol,
  updateSessionSchedule,
} from "./sessions";

describe("session API helpers", () => {
  it("builds session list and history URLs with encoded cursors", async () => {
    const fetchImpl = mockFetch(async () => jsonResponse({ sessions: [], has_more: false }));
    const client = new ApiClient({ fetchImpl });

    await listSessions(client, { after: "s1", limit: 20 });

    expect(fetchImpl.mock.calls[0][0]).toBe("/v1/sessions?after=s1&limit=20");

    fetchImpl.mockResolvedValueOnce(
      jsonResponse({ session_id: "s/1", events: [], next_after: -1, has_more: false, trace_schema_detected: false }),
    );
    await getSessionHistory(client, "s/1", { after: -1, limit: 500 });

    expect(fetchImpl.mock.calls[1][0]).toBe("/v1/sessions/s%2F1/history?after=-1&limit=500");

    fetchImpl.mockResolvedValueOnce(jsonResponse({ session_id: "s/1", plan: {}, summary: undefined }));
    await getSessionPlan(client, "s/1");

    expect(fetchImpl.mock.calls[2][0]).toBe("/v1/sessions/s%2F1/plan");

    fetchImpl.mockResolvedValueOnce(jsonResponse({ session_id: "s/1", protocol: "# Loop" }));
    await getSessionLoopProtocol(client, "s/1");

    expect(fetchImpl.mock.calls[3][0]).toBe("/v1/sessions/s%2F1/loop-protocol");
  });

  it("uses the documented HTTP methods for controls", async () => {
    const fetchImpl = mockFetch(async () => jsonResponse({ session: { id: "s1" } }));
    const client = new ApiClient({ fetchImpl });

    await createSession(client, { session_id: "s1" });
    await sendSessionMessage(client, "s1", { content: "hello" });
    await runSessionCommand(client, "s1", { command: "npm test", cwd: "extras/webui" });
    await cancelSessionTurn(client, "s1");
    await deleteSession(client, "s/1");
    await updateSessionLoopProtocol(client, "s/1", { protocol: "# Loop" });
    await updateSessionLoopProtocol(client, "s/1", { activate: true, goal: "long run" });
    await deleteSessionLoopProtocol(client, "s/1");
    await updateSessionSchedule(client, "s/1", "sched/1", { enabled: false });
    await addSessionMemory(client, "s/1", { target: "memory", topic: "research", content: "remember this" });
    await removeSessionMemory(client, "s/1", { action: "remove", target: "memory", topic: "research", old_text: "remember this" });
    await replaceSessionMemory(client, "s/1", { action: "replace", target: "memory", topic: "research", old_text: "remember this", new_content: "remember this updated" });
    await listSkills(client);
    await readSkill(client, "skill/1");
    await installSkill(client, { name: "skill_1", body: "AFFENT ACTIVE SKILL: skill_1" });
    await deleteSkill(client, "skill/1");

    expect((fetchImpl.mock.calls[0][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[1][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[1][1] as RequestInit).body).toBe(JSON.stringify({ content: "hello" }));
    expect(fetchImpl.mock.calls[2][0]).toBe("/v1/sessions/s1/commands");
    expect((fetchImpl.mock.calls[2][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[2][1] as RequestInit).body).toBe(JSON.stringify({ command: "npm test", cwd: "extras/webui" }));
    expect((fetchImpl.mock.calls[3][1] as RequestInit).method).toBe("POST");
    expect(fetchImpl.mock.calls[4][0]).toBe("/v1/sessions/s%2F1");
    expect((fetchImpl.mock.calls[4][1] as RequestInit).method).toBe("DELETE");
    expect(fetchImpl.mock.calls[5][0]).toBe("/v1/sessions/s%2F1/loop-protocol");
    expect((fetchImpl.mock.calls[5][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[5][1] as RequestInit).body).toBe(JSON.stringify({ protocol: "# Loop" }));
    expect(fetchImpl.mock.calls[6][0]).toBe("/v1/sessions/s%2F1/loop-protocol");
    expect((fetchImpl.mock.calls[6][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[6][1] as RequestInit).body).toBe(JSON.stringify({ activate: true, goal: "long run" }));
    expect(fetchImpl.mock.calls[7][0]).toBe("/v1/sessions/s%2F1/loop-protocol");
    expect((fetchImpl.mock.calls[7][1] as RequestInit).method).toBe("DELETE");
    expect(fetchImpl.mock.calls[8][0]).toBe("/v1/sessions/s%2F1/schedules/sched%2F1");
    expect((fetchImpl.mock.calls[8][1] as RequestInit).method).toBe("PATCH");
    expect((fetchImpl.mock.calls[8][1] as RequestInit).body).toBe(JSON.stringify({ enabled: false }));
    expect(fetchImpl.mock.calls[9][0]).toBe("/v1/sessions/s%2F1/memory");
    expect((fetchImpl.mock.calls[9][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[9][1] as RequestInit).body).toBe(JSON.stringify({ target: "memory", topic: "research", content: "remember this" }));
    expect(fetchImpl.mock.calls[10][0]).toBe("/v1/sessions/s%2F1/memory");
    expect((fetchImpl.mock.calls[10][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[10][1] as RequestInit).body).toBe(JSON.stringify({ action: "remove", target: "memory", topic: "research", old_text: "remember this" }));
    expect(fetchImpl.mock.calls[11][0]).toBe("/v1/sessions/s%2F1/memory");
    expect((fetchImpl.mock.calls[11][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[11][1] as RequestInit).body).toBe(JSON.stringify({
      action: "replace",
      target: "memory",
      topic: "research",
      old_text: "remember this",
      new_content: "remember this updated",
    }));
    expect(fetchImpl.mock.calls[12][0]).toBe("/v1/skills");
    expect(fetchImpl.mock.calls[13][0]).toBe("/v1/skills/skill%2F1");
    expect(fetchImpl.mock.calls[14][0]).toBe("/v1/skills");
    expect((fetchImpl.mock.calls[14][1] as RequestInit).method).toBe("POST");
    expect((fetchImpl.mock.calls[14][1] as RequestInit).body).toBe(JSON.stringify({ name: "skill_1", body: "AFFENT ACTIVE SKILL: skill_1" }));
    expect(fetchImpl.mock.calls[15][0]).toBe("/v1/skills/skill%2F1");
    expect((fetchImpl.mock.calls[15][1] as RequestInit).method).toBe("DELETE");
  });

  it("streams native affent session events", async () => {
    const fetchImpl = mockFetch(async () =>
      new Response('event: turn.start\nid: 1\ndata: {"turn_id":"t1"}\n\n', {
        headers: { "Content-Type": "text/event-stream" },
      }),
    );
    const client = new ApiClient({ fetchImpl });
    const events: unknown[] = [];

    await streamSessionEvents(client, "s1", { onEvent: (ev) => events.push(ev) });

    expect(fetchImpl.mock.calls[0][0]).toBe("/v1/sessions/s1/events");
    expect(events).toEqual([{ id: 1, type: "turn.start", data: { turn_id: "t1" } }]);
  });

  it("passes the replay cursor when streaming session events", async () => {
    const fetchImpl = mockFetch(async () => new Response(""));
    const client = new ApiClient({ fetchImpl });

    await streamSessionEvents(client, "s1", { lastEventId: 41, onEvent: vi.fn() });

    const init = fetchImpl.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get("Last-Event-ID")).toBe("41");
  });

  it("reads artifact chunks with path and byte metadata", async () => {
    const fetchImpl = mockFetch(async () =>
      new Response("456789", {
        headers: {
          "X-Affent-Artifact-Path": ".affent/artifacts/tool-results/000001-c1.txt",
          "X-Affent-Artifact-Bytes": "16",
          "X-Affent-Artifact-Offset": "4",
        },
      }),
    );
    const client = new ApiClient({ fetchImpl });

    const chunk = await readSessionArtifact(client, "s/1", ".affent/artifacts/tool-results/000001-c1.txt", {
      offset: 4,
      limit: 6,
    });

    expect(fetchImpl.mock.calls[0][0]).toBe(
      "/v1/sessions/s%2F1/artifacts/.affent/artifacts/tool-results/000001-c1.txt?offset=4&limit=6",
    );
    expect(chunk).toEqual({
      path: ".affent/artifacts/tool-results/000001-c1.txt",
      bytes: 16,
      offset: 4,
      text: "456789",
      hasMore: true,
    });
    expect(sessionArtifactPath("s/1", ".affent/artifacts/tool-results/000001-c1.txt")).toBe(
      "/v1/sessions/s%2F1/artifacts/.affent/artifacts/tool-results/000001-c1.txt",
    );
  });
});

function mockFetch(fn: typeof fetch): ReturnType<typeof vi.fn<typeof fetch>> {
  return vi.fn(fn);
}

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
