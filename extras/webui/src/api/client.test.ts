import { describe, expect, it, vi } from "vitest";
import { ApiClient, ApiError } from "./client";

describe("ApiClient", () => {
  it("sends JSON requests with auth when configured", async () => {
    const fetchImpl = mockFetch(async () => jsonResponse({ ok: true }));
    const client = new ApiClient({ basePath: "/api", authToken: "tok", fetchImpl });

    await client.json("/v1/sessions", { method: "POST", body: { session_id: "s1" } });

    expect(fetchImpl).toHaveBeenCalledWith(
      "/api/v1/sessions",
      expect.objectContaining({
        method: "POST",
        body: JSON.stringify({ session_id: "s1" }),
      }),
    );
    const init = fetchImpl.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get("Accept")).toBe("application/json");
    expect(headers.get("Content-Type")).toBe("application/json");
    expect(headers.get("Authorization")).toBe("Bearer tok");
  });

  it("turns affentserve JSON errors into ApiError", async () => {
    const fetchImpl = mockFetch(async () =>
      jsonResponse({ error: { message: "session busy", type: "affentserve_error" } }, 409),
    );
    const client = new ApiClient({ fetchImpl });

    await expect(client.json("/v1/sessions/s1/messages")).rejects.toMatchObject({
      name: "ApiError",
      status: 409,
      message: "session busy",
      type: "affentserve_error",
    } satisfies Partial<ApiError>);
  });

  it("reports HTML API fallbacks as a connection/configuration problem", async () => {
    const fetchImpl = mockFetch(async () =>
      new Response("<!doctype html><div id=\"root\"></div>", {
        status: 200,
        headers: { "Content-Type": "text/html" },
      }),
    );
    const client = new ApiClient({ fetchImpl });

    await expect(client.json("/v1/sessions")).rejects.toMatchObject({
      name: "ApiError",
      status: 200,
      message: "API returned HTML for /v1/sessions. Start affentserve or configure the WebUI API proxy.",
      type: "invalid_api_response",
    } satisfies Partial<ApiError>);
  });

  it("fetches raw responses with auth for artifact chunks", async () => {
    const fetchImpl = mockFetch(async () => new Response("chunk"));
    const client = new ApiClient({ basePath: "/api", authToken: "tok", fetchImpl });

    const resp = await client.raw("/v1/sessions/s1/artifacts/a.txt", { accept: "application/octet-stream" });

    expect(await resp.text()).toBe("chunk");
    const init = fetchImpl.mock.calls[0][1] as RequestInit;
    const headers = init.headers as Headers;
    expect(headers.get("Accept")).toBe("application/octet-stream");
    expect(headers.get("Authorization")).toBe("Bearer tok");
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
