import type { ApiClient } from "./client";

export interface AccountEnvSummary {
  name: string;
  configured: boolean;
  updated_at?: string;
}

export interface AccountSSHKeyInfo {
  exists: boolean;
  public_key?: string;
  public_key_path?: string;
  private_key_path?: string;
  created?: boolean;
}

export interface AccountSettingsResponse {
  env: AccountEnvSummary[];
  ssh: AccountSSHKeyInfo;
}

export interface AccountEnvSetRequest {
  name: string;
  value: string;
}

export function getAccountSettings(client: ApiClient, signal?: AbortSignal): Promise<AccountSettingsResponse> {
  return client.json<AccountSettingsResponse>("/v1/settings", { signal });
}

export function setAccountEnv(
  client: ApiClient,
  body: AccountEnvSetRequest,
  signal?: AbortSignal,
): Promise<AccountSettingsResponse> {
  return client.json<AccountSettingsResponse>("/v1/settings/env", { method: "POST", body, signal });
}

export function deleteAccountEnv(client: ApiClient, name: string, signal?: AbortSignal): Promise<AccountSettingsResponse> {
  return client.json<AccountSettingsResponse>(`/v1/settings/env/${encodeURIComponent(name)}`, {
    method: "DELETE",
    signal,
  });
}

export function ensureAccountSSHKey(client: ApiClient, signal?: AbortSignal): Promise<AccountSettingsResponse> {
  return client.json<AccountSettingsResponse>("/v1/settings/ssh-key", { method: "POST", signal });
}
