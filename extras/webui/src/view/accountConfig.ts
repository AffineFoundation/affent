import type { AccountEnvSummary, AccountSettingsResponse } from "../api/settings";

export function accountConfigSummary(settings?: AccountSettingsResponse): string {
  if (!settings) return "No config";
  const env = settings.env.length > 0 ? `${settings.env.length} env${settings.env.length === 1 ? "" : "s"}` : undefined;
  const ssh = settings.ssh;
  if (ssh.public_key) return env ? `${env} · SSH key` : "SSH key";
  if (ssh.exists) return env ? `${env} · SSH key issue` : "SSH key issue";
  return env ?? "No config";
}

export function accountConfigDetail(settings?: AccountSettingsResponse): string {
  if (!settings) return "No env vars or SSH key configured";
  const envCount = settings.env.length;
  const ssh = settings.ssh;
  if (ssh.public_key) return envCount > 0 ? `SSH ready · ${envCount} env${envCount === 1 ? "" : "s"}` : "SSH ready";
  if (ssh.exists) return "SSH key found; public key unavailable";
  if (envCount > 0) return "No SSH key configured";
  return "No env vars or SSH key configured";
}

export function sshAccessDescription(ssh?: AccountSettingsResponse["ssh"]): string {
  if (ssh?.public_key) return "Public key is ready for private Git remotes. Existing keys are never overwritten.";
  if (ssh?.exists) return "A private key exists, but its public key is unavailable.";
  return "Generate a key when this runtime needs private Git access.";
}

export function sshStorageDescription(ssh?: AccountSettingsResponse["ssh"]): string | undefined {
  if (ssh?.public_key_path) return ssh.public_key_path;
  if (ssh?.public_key || ssh?.exists) return "Storage path not reported by this server build.";
  return undefined;
}

export function sshPathDisplay(path?: string): string {
  if (!path) return "";
  const marker = "/.ssh/";
  const index = path.lastIndexOf(marker);
  if (index >= 0) return `~${path.slice(index)}`;
  if (path.startsWith(".ssh/")) return `~/${path}`;
  return path;
}

export function sshPathState(path?: string, exists = false): string {
  if (path?.includes("/.ssh/") || path?.startsWith(".ssh/")) return "standard ~/.ssh";
  if (exists) return "path not reported";
  return "not configured";
}

export function accountConfigEvidenceText(settings: AccountSettingsResponse): string {
  return [
    "Runtime config evidence",
    `Environment variables: ${settings.env.length}`,
    ...settings.env.map(envEvidenceLine),
    `SSH: ${sshEvidence(settings.ssh)}`,
    settings.ssh.public_key_path ? `SSH public key path: ${settings.ssh.public_key_path}` : undefined,
    settings.ssh.public_key_error ? `SSH issue: ${settings.ssh.public_key_error}` : undefined,
  ].filter((line): line is string => !!line).join("\n");
}

export function accountEnvMatchesQuery(entry: AccountEnvSummary, query: string): boolean {
  return [
    entry.name,
    entry.configured ? "configured" : "empty",
    entry.updated_at,
  ].filter(Boolean).join(" ").toLowerCase().includes(query.trim().toLowerCase());
}

function envEvidenceLine(entry: AccountEnvSummary): string {
  const parts = [
    `- ${entry.name}: ${entry.configured ? "configured" : "empty"}`,
    entry.updated_at ? `updated ${entry.updated_at}` : undefined,
  ].filter(Boolean);
  return parts.join(" · ");
}

function sshEvidence(ssh: AccountSettingsResponse["ssh"]): string {
  if (ssh.public_key) return "public key ready";
  if (ssh.exists) return "private key exists, public key unavailable";
  return "not configured";
}
