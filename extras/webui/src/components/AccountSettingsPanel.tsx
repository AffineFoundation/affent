import { useMemo, useState, type FormEvent } from "react";
import type { AccountSettingsResponse } from "../api/settings";
import {
  accountGitAccessVerifyRequest,
  accountConfigDetail,
  accountConfigReview,
  accountConfigSummary,
  accountEnvMatchesFilter,
  accountEnvMatchesQuery,
  accountEnvReviewFindings,
  accountEnvReviewNames,
  sshAccessDescription,
  sshPathDisplay,
  sshPathState,
  sshStorageDescription,
  type AccountEnvFilter,
} from "../view/accountConfig";
import type { RunCommandExecutionRequest } from "../view/sessionRun";
import { CopyButton } from "./CopyButton";
import { panelErrorSummary } from "./panelErrorSummary";

export function AccountSettingsPanel({
  settings,
  loading = false,
  error,
  busy,
  defaultOpen = false,
  surface = false,
  onRefresh,
  onSetEnv,
  onDeleteEnv,
  onEnsureSSHKey,
  onVerifyGitAccess,
}: {
  settings?: AccountSettingsResponse;
  loading?: boolean;
  error?: string;
  busy?: "env" | "ssh" | string;
  defaultOpen?: boolean;
  surface?: boolean;
  onRefresh?: () => Promise<void> | void;
  onSetEnv?: (name: string, value: string) => Promise<void> | void;
  onDeleteEnv?: (name: string) => Promise<void> | void;
  onEnsureSSHKey?: () => Promise<void> | void;
  onVerifyGitAccess?: (request: RunCommandExecutionRequest) => Promise<void> | void;
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [query, setQuery] = useState("");
  const [gitHost, setGitHost] = useState("github.com");
  const [confirmDeleteEnv, setConfirmDeleteEnv] = useState<string | undefined>();
  const [mutationStatus, setMutationStatus] = useState<{ tone: "success" | "error"; message: string } | undefined>();
  const [envFilter, setEnvFilter] = useState<AccountEnvFilter>("all");
  const ssh = settings?.ssh;
  const envReviewFindings = useMemo(() => accountEnvReviewFindings(settings), [settings]);
  const envReviewNames = useMemo(() => accountEnvReviewNames(settings), [settings]);
  const trimmedQuery = query.trim();
  const visibleEnv = useMemo(() => {
    const env = settings?.env ?? [];
    return env
      .filter((entry) => accountEnvMatchesFilter(entry, envFilter, envReviewNames))
      .filter((entry) => !trimmedQuery || accountEnvMatchesQuery(entry, trimmedQuery));
  }, [envFilter, envReviewNames, settings?.env, trimmedQuery]);
  const title = loading ? "Loading" : error ? "Unavailable" : accountConfigSummary(settings);
  const detail = error
    ? panelErrorSummary("Config API", error)
    : accountConfigDetail(settings);
  const sshDescription = sshAccessDescription(ssh);
  const sshStorage = sshStorageDescription(ssh);
  const canSubmit = !!name.trim() && !!onSetEnv && !busy;

  async function submitEnv(event: FormEvent) {
    event.preventDefault();
    if (!canSubmit) return;
    const envName = name.trim();
    setMutationStatus(undefined);
    try {
      await onSetEnv?.(envName, value);
      setName("");
      setValue("");
      setMutationStatus({ tone: "success", message: `${envName} saved.` });
    } catch (err) {
      setMutationStatus({ tone: "error", message: formatPanelError(err) });
    }
  }

  async function deleteEnv(envName: string) {
    setMutationStatus(undefined);
    try {
      await onDeleteEnv?.(envName);
      setConfirmDeleteEnv(undefined);
      setMutationStatus({ tone: "success", message: `${envName} deleted.` });
    } catch (err) {
      setMutationStatus({ tone: "error", message: formatPanelError(err) });
    }
  }

  async function ensureSSHKey() {
    if (!onEnsureSSHKey) return;
    setMutationStatus(undefined);
    try {
      await onEnsureSSHKey();
      setMutationStatus({ tone: "success", message: "SSH key ready." });
    } catch (err) {
      setMutationStatus({ tone: "error", message: formatPanelError(err) });
    }
  }

  return (
    <details
      className="session-skills-panel account-settings-panel"
      data-testid="account-settings-panel"
      data-surface={surface ? "true" : undefined}
      {...(defaultOpen || surface ? { open: true } : {})}
      onToggle={(event) => {
        if (surface) event.currentTarget.open = true;
      }}
    >
      <summary className="session-skills-summary" onClick={surface ? (event) => event.preventDefault() : undefined}>
        <span className="session-skills-kicker">Config</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? <div className="session-skills-empty">Loading config...</div> : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
            {onRefresh ? (
              <button type="button" className="ghost-action" disabled={!!busy} onClick={() => void onRefresh()}>
                Retry
              </button>
            ) : null}
          </div>
        ) : null}
        {!loading && error && (onSetEnv || onEnsureSSHKey || settings) ? (
          <div className="session-runtime-fallback" data-testid="account-settings-fallback">
            <strong>Config actions remain available</strong>
            <span>Try saving env or generating SSH again; the server will report the exact failure if the API route is still unavailable.</span>
          </div>
        ) : null}
        {!loading && (!error || settings || onSetEnv || onEnsureSSHKey) ? (
          <>
            {settings ? (
              <>
                <ConfigDashboard settings={settings} />
                <AccountConfigFocus
                  settings={settings}
                  busy={busy}
                  onRefresh={onRefresh}
                  onEnsureSSHKey={onEnsureSSHKey ? ensureSSHKey : undefined}
                  gitHost={gitHost}
                  setGitHost={setGitHost}
                  onVerifyGitAccess={onVerifyGitAccess}
                />
                <div className="account-settings-actions">
                  {onRefresh ? (
                    <button type="button" className="node-action" disabled={!!busy} onClick={() => void onRefresh()}>
                      Refresh
                    </button>
                  ) : null}
                </div>
              </>
            ) : null}
            <div className="account-settings-section">
              <div className="account-settings-section-heading">
                <strong>Private repo access</strong>
                <span>{sshDescription}</span>
              </div>
              {sshStorage ? (
                <div className="account-ssh-storage" data-testid="account-ssh-storage">
                  <span>Key path</span>
                  <code title={sshStorage}>{sshPathDisplay(ssh?.public_key_path) || sshStorage}</code>
                  {ssh?.public_key_path ? <CopyButton label="Copy path" value={ssh.public_key_path} className="ghost-action" /> : null}
                </div>
              ) : null}
              {ssh?.public_key ? (
                <div className="account-public-key-row">
                  <span>Public key</span>
                  <code className="account-public-key" data-testid="account-public-key" title={ssh.public_key}>{ssh.public_key}</code>
                  <CopyButton label="Copy full key" value={ssh.public_key} className="ghost-action" />
                </div>
              ) : ssh?.exists ? (
                <>
                  <div className="session-skills-empty error" role="alert">
                    {ssh.public_key_error || "Public key is missing for the existing SSH private key."}
                  </div>
                  <div className="session-loop-actions">
                    {onRefresh ? (
                      <button type="button" className="ghost-action" disabled={!!busy} onClick={() => void onRefresh()}>
                        Refresh
                      </button>
                    ) : null}
                  </div>
                </>
              ) : (
                <div className="session-skills-empty">No SSH key is configured.</div>
              )}
            </div>
            <details className="account-env-write" open={!settings || settings.env.length === 0}>
              <summary>
                <strong>Environment variables</strong>
                <span>{settings?.env.length ? `${settings.env.length} configured` : "No variables configured"}</span>
              </summary>
              <form className="session-loop-setup account-env-form" onSubmit={submitEnv}>
                <label>
                  <span>Environment variable</span>
                  <input value={name} onChange={(event) => setName(event.target.value)} placeholder="GITHUB_TOKEN" disabled={!!busy} />
                </label>
                <label>
                  <span>Value</span>
                  <input value={value} onChange={(event) => setValue(event.target.value)} placeholder="Stored server-side" type="password" disabled={!!busy} />
                </label>
                <button type="submit" className="secondary-action" disabled={!canSubmit}>
                  {busy === "env" ? "Saving" : "Save env"}
                </button>
                <p className="session-loop-setup-note">Saved values are never echoed.</p>
              </form>
            </details>
            {mutationStatus ? (
              <span className="account-settings-status" data-tone={mutationStatus.tone} role="status" aria-live="polite">
                {mutationStatus.message}
              </span>
            ) : null}
            {settings && envReviewFindings.length > 0 ? (
              <AccountEnvReviewQueue
                findings={envReviewFindings}
                onShowEnv={() => setEnvFilter("review")}
              />
            ) : null}
            {settings && settings.env.length > 0 ? (
              <div className="session-skills-controls">
                <AccountEnvFilters
                  env={settings.env}
                  reviewNames={envReviewNames}
                  value={envFilter}
                  onChange={setEnvFilter}
                />
                <label className="session-skills-search">
                  <span>Search env</span>
                  <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="name, configured, or empty" />
                </label>
                {trimmedQuery || envFilter !== "all" ? (
                  <button type="button" className="ghost-action" onClick={() => {
                    setQuery("");
                    setEnvFilter("all");
                  }}>
                    Clear
                  </button>
                ) : null}
                {trimmedQuery || envFilter !== "all" ? (
                  <span className="session-search-count" data-testid="account-env-search-count">
                    {visibleEnv.length} {visibleEnv.length === 1 ? "variable" : "variables"}
                    {trimmedQuery ? ` matching "${trimmedQuery}"` : ""}
                  </span>
                ) : null}
              </div>
            ) : null}
            {settings && settings.env.length > 0 ? (
              <div className="session-skills-list" data-testid="account-env-list">
                {visibleEnv.length > 0 ? visibleEnv.map((entry) => (
                  <div key={entry.name} className="session-skill-item account-env-item">
                    <span className="session-skill-title">
                      <strong>{entry.name}</strong>
                      <span>{entry.configured ? "configured" : "empty"}</span>
                    </span>
                    <span className="account-env-meta">
                      {entry.updated_at ? `Updated ${formatTimestamp(entry.updated_at)}` : "No update time"}
                    </span>
                    {onDeleteEnv ? confirmDeleteEnv === entry.name ? (
                      <div className="account-env-confirm" role="group" aria-label={`Confirm delete ${entry.name}`}>
                        <span>Delete {entry.name}?</span>
                        <button type="button" disabled={!!busy} onClick={() => setConfirmDeleteEnv(undefined)}>
                          Cancel
                        </button>
                        <button type="button" className="danger" disabled={!!busy} onClick={() => void deleteEnv(entry.name)}>
                          Confirm
                        </button>
                      </div>
                    ) : (
                      <button type="button" className="ghost-action danger-action" disabled={!!busy} onClick={() => setConfirmDeleteEnv(entry.name)}>
                        Delete
                      </button>
                    ) : null}
                  </div>
                )) : (
                  <div className="session-skills-empty">No environment variables matching "{trimmedQuery}".</div>
                )}
              </div>
            ) : null}
          </>
        ) : null}
      </div>
    </details>
  );
}

function AccountEnvFilters({
  env,
  reviewNames,
  value,
  onChange,
}: {
  env: readonly AccountSettingsResponse["env"][number][];
  reviewNames: ReadonlySet<string>;
  value: AccountEnvFilter;
  onChange: (value: AccountEnvFilter) => void;
}) {
  const counts = {
    all: env.length,
    configured: env.filter((entry) => entry.configured).length,
    empty: env.filter((entry) => !entry.configured).length,
    review: env.filter((entry) => reviewNames.has(entry.name)).length,
  };
  const options: Array<{ value: AccountEnvFilter; label: string; count: number }> = [
    { value: "all", label: "All", count: counts.all },
    { value: "configured", label: "Configured", count: counts.configured },
    { value: "empty", label: "Empty", count: counts.empty },
    { value: "review", label: "Needs review", count: counts.review },
  ];
  return (
    <div className="session-filter-pills" role="group" aria-label="Filter environment variables">
      {options.map((option) => (
        <button
          key={option.value}
          type="button"
          aria-pressed={value === option.value}
          disabled={option.count === 0 && value !== option.value}
          onClick={() => onChange(option.value)}
        >
          <span>{option.label}</span>
          <strong>{option.count}</strong>
        </button>
      ))}
    </div>
  );
}

function AccountEnvReviewQueue({
  findings,
  onShowEnv,
}: {
  findings: ReturnType<typeof accountEnvReviewFindings>;
  onShowEnv: () => void;
}) {
  const counts = findings.reduce<Record<string, number>>((acc, finding) => {
    acc[finding.kind] = (acc[finding.kind] ?? 0) + 1;
    return acc;
  }, {});
  return (
    <section className="account-env-review" data-testid="account-env-review" aria-label="Environment review queue">
      <div className="account-env-review-head">
        <span>Env review</span>
        <strong>{findings.length} {findings.length === 1 ? "finding" : "findings"}</strong>
        <button type="button" className="ghost-action" onClick={onShowEnv}>Show variables</button>
      </div>
      <div className="account-env-review-kinds">
        {Object.entries(counts).map(([kind, count]) => (
          <span key={kind}>
            {kind === "empty" ? "Empty" : "Incomplete"}
            <strong>{count}</strong>
          </span>
        ))}
      </div>
      <ul className="account-env-review-list">
        {findings.slice(0, 4).map((finding) => (
          <li key={`${finding.kind}:${finding.name}`}>
            <strong>{finding.name}</strong>
            <span>{finding.detail}</span>
          </li>
        ))}
      </ul>
    </section>
  );
}

function AccountConfigFocus({
  settings,
  busy,
  onRefresh,
  onEnsureSSHKey,
  gitHost,
  setGitHost,
  onVerifyGitAccess,
}: {
  settings: AccountSettingsResponse;
  busy?: string;
  onRefresh?: () => Promise<void> | void;
  onEnsureSSHKey?: () => Promise<void> | void;
  gitHost: string;
  setGitHost: (value: string) => void;
  onVerifyGitAccess?: (request: RunCommandExecutionRequest) => Promise<void> | void;
}) {
  const review = accountConfigReview(settings);
  const canVerifyGit = Boolean(settings.ssh.public_key && onVerifyGitAccess);
  return (
    <section className="account-config-focus" data-testid="account-config-focus" data-tone={review.tone} aria-label="Runtime config review">
      <div className="account-config-focus-head">
        <span>{review.tone === "ready" ? "Runtime ready" : review.tone === "attention" ? "Review needed" : "Setup needed"}</span>
        <strong>{review.headline}</strong>
        <small>{review.detail}</small>
      </div>
      <div className="account-config-focus-grid">
        <ConfigFocusFact label="Private Git" value={review.privateGit} />
        <ConfigFocusFact label="Public key" value={review.publicKey} />
        <ConfigFocusFact label="Key path" value={review.keyPath} detail={review.keyPathDetail} />
        <ConfigFocusFact label="Secrets" value={review.envCount} detail={review.latestEnvUpdate ? `updated ${formatTimestamp(review.latestEnvUpdate)}` : "none updated"} />
      </div>
      <div className="account-config-next">
        <span>Next action</span>
        <p>{review.nextAction}</p>
      </div>
      {settings.ssh.public_key ? (
        <div className="account-config-focus-actions">
          <CopyButton label="Copy public key" value={settings.ssh.public_key} className="secondary-action" />
          {settings.ssh.public_key_path ? <CopyButton label="Copy key path" value={settings.ssh.public_key_path} className="ghost-action" /> : null}
        </div>
      ) : null}
      {settings.ssh.public_key ? (
        <div className="account-config-verify" data-testid="account-config-verify">
          <label>
            <span>Test private Git host</span>
            <input value={gitHost} onChange={(event) => setGitHost(event.target.value)} placeholder="github.com" disabled={!!busy || !canVerifyGit} />
          </label>
          <button
            type="button"
            className="secondary-action"
            disabled={!!busy || !canVerifyGit}
            onClick={() => onVerifyGitAccess?.(accountGitAccessVerifyRequest(gitHost))}
          >
            Test SSH
          </button>
        </div>
      ) : null}
      <div className="account-config-focus-actions">
        {!settings.ssh.exists && onEnsureSSHKey ? (
          <button type="button" className="secondary-action" disabled={!!busy} onClick={() => void onEnsureSSHKey()}>
            {busy === "ssh" ? "Checking key" : "Generate SSH key"}
          </button>
        ) : null}
        {settings.ssh.exists && !settings.ssh.public_key && onRefresh ? (
          <button type="button" className="node-action" disabled={!!busy} onClick={() => void onRefresh()}>
            Refresh after fix
          </button>
        ) : null}
      </div>
    </section>
  );
}

function ConfigFocusFact({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="account-config-focus-fact">
      <span>{label}</span>
      <strong title={detail || value}>{value}</strong>
      {detail ? <small title={detail}>{detail}</small> : null}
    </div>
  );
}

function ConfigDashboard({ settings }: { settings: AccountSettingsResponse }) {
  const latestEnvUpdate = settings.env
    .map((entry) => entry.updated_at)
    .filter((value): value is string => Boolean(value))
    .sort()
    .at(-1);
  const ssh = settings.ssh;
  const keyPath = ssh.public_key_path ?? (ssh.exists ? "Path not reported" : "No key");
  const keyPathDisplay = sshPathDisplay(ssh.public_key_path) || keyPath;
  const sshState = ssh.public_key ? "Ready" : ssh.exists ? "Attention" : "Missing";
  const pathState = sshPathState(ssh.public_key_path, ssh.exists);
  return (
    <div className="account-config-dashboard" data-testid="account-config-dashboard">
      <div className="account-config-card" data-state={ssh.public_key ? "ready" : ssh.exists ? "attention" : "missing"}>
        <span>SSH</span>
        <strong>{sshState}</strong>
        <small>{ssh.public_key ? "private Git ready" : ssh.exists ? "public key unavailable" : "no key"}</small>
      </div>
      <div className="account-config-card">
        <span>Key path</span>
        <strong title={keyPath}>{keyPathDisplay}</strong>
        <small>{pathState}</small>
      </div>
      <div className="account-config-card">
        <span>Env</span>
        <strong>{settings.env.length}</strong>
        <small>{latestEnvUpdate ? `updated ${formatTimestamp(latestEnvUpdate)}` : "no saved values"}</small>
      </div>
    </div>
  );
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, { dateStyle: "medium", timeStyle: "short" });
}
