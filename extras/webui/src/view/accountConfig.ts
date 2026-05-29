import type { AccountEnvSummary, AccountGitCheckRequest, AccountSettingsResponse } from "../api/settings";

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

export type AccountEnvReviewFinding = {
  kind: "empty" | "incomplete";
  name: string;
  detail: string;
  related?: string[];
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

export function accountGitAccessVerifyRequest(host: string): AccountGitCheckRequest {
  const normalizedHost = normalizeGitHost(host);
  return {
    kind: "host",
    target: normalizedHost,
  };
}

export function accountGitRemoteVerifyRequest(remote: string): AccountGitCheckRequest {
  const normalizedRemote = normalizeGitRemote(remote);
  return {
    kind: "remote",
    target: normalizedRemote,
  };
}

export function accountEnvMatchesQuery(entry: AccountEnvSummary, query: string): boolean {
  return [
    entry.name,
    entry.configured ? "configured" : "empty",
    entry.updated_at,
  ].filter(Boolean).join(" ").toLowerCase().includes(query.trim().toLowerCase());
}

export function accountEnvMatchesFilter(
  entry: AccountEnvSummary,
  filter: AccountEnvFilter,
  reviewNames: ReadonlySet<string>,
): boolean {
  if (filter === "configured") return entry.configured;
  if (filter === "empty") return !entry.configured;
  if (filter === "review") return reviewNames.has(entry.name);
  return true;
}

export type AccountEnvFilter = "all" | "configured" | "empty" | "review";

export function accountEnvReviewFindings(settings?: AccountSettingsResponse): AccountEnvReviewFinding[] {
  if (!settings) return [];
  const findings: AccountEnvReviewFinding[] = [];
  const configured = new Set(settings.env.filter((entry) => entry.configured).map((entry) => entry.name));
  settings.env.forEach((entry) => {
    if (!entry.configured) {
      findings.push({
        kind: "empty",
        name: entry.name,
        detail: "saved with an empty value",
      });
    }
  });
  const hasGoogleApi = configured.has("GOOGLE_API_KEY") || configured.has("GOOGLE_CSE_API_KEY");
  const hasGoogleCx = configured.has("GOOGLE_CSE_ID") || configured.has("GOOGLE_SEARCH_ENGINE_ID");
  if (hasGoogleApi && !hasGoogleCx) {
    const name = configured.has("GOOGLE_API_KEY") ? "GOOGLE_API_KEY" : "GOOGLE_CSE_API_KEY";
    findings.push({
      kind: "incomplete",
      name,
      detail: "Google search also needs GOOGLE_CSE_ID or GOOGLE_SEARCH_ENGINE_ID",
      related: ["GOOGLE_CSE_ID", "GOOGLE_SEARCH_ENGINE_ID"],
    });
  }
  if (hasGoogleCx && !hasGoogleApi) {
    const name = configured.has("GOOGLE_CSE_ID") ? "GOOGLE_CSE_ID" : "GOOGLE_SEARCH_ENGINE_ID";
    findings.push({
      kind: "incomplete",
      name,
      detail: "Google search also needs GOOGLE_API_KEY or GOOGLE_CSE_API_KEY",
      related: ["GOOGLE_API_KEY", "GOOGLE_CSE_API_KEY"],
    });
  }
  return findings;
}

export function accountEnvReviewNames(settings?: AccountSettingsResponse): Set<string> {
  return new Set(accountEnvReviewFindings(settings).flatMap((finding) => [finding.name, ...(finding.related ?? [])]));
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

function normalizeGitRemote(value: string): string {
  const trimmed = value.trim();
  try {
    const parsed = new URL(trimmed);
    if (parsed.protocol !== "https:") return trimmed;
    const host = parsed.hostname.toLowerCase();
    const path = parsed.pathname.replace(/^\/+|\/+$/g, "").replace(/\.git$/i, "");
    if (!host || !path || !path.includes("/")) return trimmed;
    return `git@${host}:${path}.git`;
  } catch {
    return trimmed;
  }
}
