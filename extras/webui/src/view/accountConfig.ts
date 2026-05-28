import type { AccountEnvSummary, AccountSettingsResponse } from "../api/settings";
import type { RunCommandExecutionRequest } from "./sessionRun";

export type AccountConfigReview = {
  tone: "ready" | "attention" | "missing";
  headline: string;
  detail: string;
  privateGit: string;
  publicKey: string;
  keyPath: string;
  keyPathDetail: string;
  envCount: string;
  latestEnvUpdate?: string;
  nextAction: string;
};

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
  if (path) return "custom path";
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

export function accountGitAccessVerifyRequest(host: string): RunCommandExecutionRequest {
  const normalizedHost = normalizeGitHost(host);
  const quotedHost = shellSingleQuote(normalizedHost);
  return {
    command: [
      `host=${quotedHost}`,
      `out="$(ssh -T -o BatchMode=yes -o ConnectTimeout=12 -o StrictHostKeyChecking=accept-new git@$host 2>&1)"`,
      "code=$?",
      `printf '%s\\n' "$out"`,
      `case "$out" in *"successfully authenticated"*|*"Welcome to GitLab"*|*"authenticated via ssh key"*) exit 0 ;; *) exit "$code" ;; esac`,
    ].join("; "),
  };
}

export function accountEnvMatchesQuery(entry: AccountEnvSummary, query: string): boolean {
  return [
    entry.name,
    entry.configured ? "configured" : "empty",
    entry.updated_at,
  ].filter(Boolean).join(" ").toLowerCase().includes(query.trim().toLowerCase());
}

export function accountConfigReview(settings: AccountSettingsResponse): AccountConfigReview {
  const latestEnvUpdate = settings.env
    .map((entry) => entry.updated_at)
    .filter((value): value is string => Boolean(value))
    .sort()
    .at(-1);
  const envCount = settings.env.length;
  const envLabel = `${envCount} env${envCount === 1 ? "" : "s"}`;
  const keyPath = sshPathDisplay(settings.ssh.public_key_path) || (settings.ssh.exists ? "Not reported" : "Not generated");
  const keyPathDetail = sshPathState(settings.ssh.public_key_path, settings.ssh.exists);

  if (settings.ssh.public_key) {
    return {
      tone: "ready",
      headline: "Private Git ready",
      detail: "SSH public key is available. Add it to the Git provider account that owns private repositories.",
      privateGit: "Ready",
      publicKey: "Available",
      keyPath,
      keyPathDetail,
      envCount: envLabel,
      latestEnvUpdate,
      nextAction: envCount > 0 ? "Run tasks normally; add or rotate secrets only when the next job needs them." : "Add only the secrets required by the next private workflow.",
    };
  }

  if (settings.ssh.exists) {
    return {
      tone: "attention",
      headline: "SSH key needs review",
      detail: settings.ssh.public_key_error || "A private key exists, but the public key cannot be read.",
      privateGit: "Blocked",
      publicKey: "Unavailable",
      keyPath,
      keyPathDetail,
      envCount: envLabel,
      latestEnvUpdate,
      nextAction: "Fix or derive the public key in ~/.ssh, then refresh config before cloning private repositories.",
    };
  }

  return {
    tone: "missing",
    headline: "SSH key missing",
    detail: "This runtime cannot clone private Git repositories until an SSH key exists and its public key is registered.",
    privateGit: "Not ready",
    publicKey: "Missing",
    keyPath,
    keyPathDetail,
    envCount: envLabel,
    latestEnvUpdate,
    nextAction: "Generate an SSH key only when this runtime needs private repo access.",
  };
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

function normalizeGitHost(value: string): string {
  const trimmed = value.trim().replace(/^ssh:\/\//i, "").replace(/^git@/i, "").replace(/[:/].*$/, "");
  const safe = trimmed.toLowerCase().replace(/[^a-z0-9.-]/g, "");
  return safe || "github.com";
}

function shellSingleQuote(value: string): string {
  return `'${value.replace(/'/g, `'\\''`)}'`;
}
