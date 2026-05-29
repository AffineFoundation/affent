import { describe, expect, it } from "vitest";
import { ApiClient } from "./client";
import { checkAccountGitAccess, deleteAccountEnv, ensureAccountSSHKey, getAccountSettings, setAccountEnv } from "./settings";

describe("account settings API helpers", () => {
  it("uses documented settings endpoints and methods", async () => {
    const calls: Array<[RequestInfo | URL, RequestInit | undefined]> = [];
    const fetchImpl: typeof fetch = async (input, init) => {
      calls.push([input, init]);
      return jsonResponse({ env: [], ssh: { exists: false } });
    };
    const client = new ApiClient({ fetchImpl });

    await getAccountSettings(client);
    await setAccountEnv(client, { name: "GITHUB_TOKEN", value: "secret" });
    await deleteAccountEnv(client, "GITHUB/TOKEN");
    await ensureAccountSSHKey(client);
    await checkAccountGitAccess(client, { kind: "host", target: "github.com" });

    expect(calls[0]?.[0]).toBe("/v1/settings");
    expect(calls[1]?.[0]).toBe("/v1/settings/env");
    expect(calls[1]?.[1]?.method).toBe("POST");
    expect(calls[1]?.[1]?.body).toBe(JSON.stringify({ name: "GITHUB_TOKEN", value: "secret" }));
    expect(calls[2]?.[0]).toBe("/v1/settings/env/GITHUB%2FTOKEN");
    expect(calls[2]?.[1]?.method).toBe("DELETE");
    expect(calls[3]?.[0]).toBe("/v1/settings/ssh-key");
    expect(calls[3]?.[1]?.method).toBe("POST");
    expect(calls[4]?.[0]).toBe("/v1/settings/git-check");
    expect(calls[4]?.[1]?.method).toBe("POST");
    expect(calls[4]?.[1]?.body).toBe(JSON.stringify({ kind: "host", target: "github.com" }));
  });
});

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
