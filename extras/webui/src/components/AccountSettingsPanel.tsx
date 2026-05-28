import { useMemo, useState, type FormEvent } from "react";
import type { AccountSettingsResponse } from "../api/settings";
import {
  accountConfigDetail,
  accountConfigEvidenceText,
  accountConfigSummary,
  accountEnvMatchesQuery,
  sshAccessDescription,
  sshStorageDescription,
} from "../view/accountConfig";
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
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [query, setQuery] = useState("");
  const [confirmDeleteEnv, setConfirmDeleteEnv] = useState<string | undefined>();
  const [mutationStatus, setMutationStatus] = useState<{ tone: "success" | "error"; message: string } | undefined>();
  const ssh = settings?.ssh;
  const trimmedQuery = query.trim();
  const visibleEnv = useMemo(() => {
    const env = settings?.env ?? [];
    if (!trimmedQuery) return env;
    return env.filter((entry) => accountEnvMatchesQuery(entry, trimmedQuery));
  }, [settings?.env, trimmedQuery]);
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
              <div className="account-settings-actions">
                <CopyButton label="Copy config evidence" value={accountConfigEvidenceText(settings)} className="node-action" />
              </div>
            ) : null}
            <div className="account-settings-section">
              <div>
                <strong>SSH key</strong>
                <span>{sshDescription}</span>
              </div>
              {sshStorage ? (
                <div className="account-ssh-storage" data-testid="account-ssh-storage">
                  <span>Storage</span>
                  <code>{sshStorage}</code>
                  {ssh?.public_key_path ? <CopyButton label="Copy path" value={ssh.public_key_path} className="ghost-action" /> : null}
                </div>
              ) : null}
              {ssh?.public_key ? (
                <>
                  <pre className="session-loop-protocol account-public-key" data-testid="account-public-key">{ssh.public_key}</pre>
                  <div className="session-loop-actions">
                    <CopyButton label="Copy public key" value={ssh.public_key} className="ghost-action" />
                    {onRefresh ? (
                      <button type="button" className="ghost-action" disabled={!!busy} onClick={() => void onRefresh()}>
                        Refresh
                      </button>
                    ) : null}
                  </div>
                </>
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
                <div className="session-loop-actions">
                  {onEnsureSSHKey ? (
                    <button type="button" className="secondary-action" disabled={!!busy} onClick={() => void ensureSSHKey()}>
                      {busy === "ssh" ? "Checking key" : "Generate SSH key"}
                    </button>
                  ) : null}
                </div>
              )}
            </div>
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
              <p className="session-loop-setup-note">Values are injected into shell commands but are not shown back in the UI.</p>
            </form>
            {mutationStatus ? (
              <span className="account-settings-status" data-tone={mutationStatus.tone} role="status" aria-live="polite">
                {mutationStatus.message}
              </span>
            ) : null}
            {settings && settings.env.length > 0 ? (
              <div className="session-skills-controls">
                <label className="session-skills-search">
                  <span>Search env</span>
                  <input value={query} onChange={(event) => setQuery(event.target.value)} placeholder="name, configured, or empty" />
                </label>
                {trimmedQuery ? (
                  <button type="button" className="ghost-action" onClick={() => setQuery("")}>
                    Clear
                  </button>
                ) : null}
                {trimmedQuery ? (
                  <span className="session-search-count" data-testid="account-env-search-count">
                    {visibleEnv.length} {visibleEnv.length === 1 ? "variable" : "variables"} matching "{trimmedQuery}"
                  </span>
                ) : null}
              </div>
            ) : null}
            <div className="session-skills-list" data-testid="account-env-list">
              {settings && visibleEnv.length > 0 ? visibleEnv.map((entry) => (
                <div key={entry.name} className="session-skill-item account-env-item">
                  <span className="session-skill-title">
                    <strong>{entry.name}</strong>
                    <span>{entry.configured ? "configured" : "empty"}</span>
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
              )) : settings && settings.env.length > 0 ? (
                <div className="session-skills-empty">No environment variables matching "{trimmedQuery}".</div>
              ) : (
                <div className="session-skills-empty">No environment variables configured.</div>
              )}
            </div>
          </>
        ) : null}
      </div>
    </details>
  );
}

function formatPanelError(err: unknown): string {
  if (err instanceof Error) return err.message;
  return "Unknown error";
}
