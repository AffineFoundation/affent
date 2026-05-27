import { useState, type FormEvent } from "react";
import type { AccountSettingsResponse } from "../api/settings";
import { CopyButton } from "./CopyButton";

export function AccountSettingsPanel({
  settings,
  loading = false,
  error,
  busy,
  defaultOpen = false,
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
  onRefresh?: () => Promise<void> | void;
  onSetEnv?: (name: string, value: string) => Promise<void> | void;
  onDeleteEnv?: (name: string) => Promise<void> | void;
  onEnsureSSHKey?: () => Promise<void> | void;
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const envCount = settings?.env.length ?? 0;
  const ssh = settings?.ssh;
  const title = loading ? "Loading" : error ? "Unavailable" : `${envCount} env${envCount === 1 ? "" : "s"}`;
  const detail = ssh?.exists ? "SSH public key ready" : "Generate an SSH key before cloning private repos";
  const canSubmit = !!name.trim() && !!onSetEnv && !busy;

  async function submitEnv(event: FormEvent) {
    event.preventDefault();
    if (!canSubmit) return;
    await onSetEnv?.(name.trim(), value);
    setName("");
    setValue("");
  }

  return (
    <details className="session-skills-panel account-settings-panel" data-testid="account-settings-panel" {...(defaultOpen ? { open: true } : {})}>
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Access</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      <div className="session-skills-body">
        {loading ? <div className="session-skills-empty">Loading account access settings...</div> : null}
        {!loading && error ? (
          <div className="session-skills-empty error" role="alert">
            {error}
          </div>
        ) : null}
        {!loading && !error ? (
          <>
            <div className="account-settings-section">
              <div>
                <strong>SSH key</strong>
                <span>Use this public key for GitHub or GitLab deploy access. Existing keys are shown, never overwritten.</span>
              </div>
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
              ) : (
                <div className="session-loop-actions">
                  {onEnsureSSHKey ? (
                    <button type="button" className="secondary-action" disabled={!!busy} onClick={() => void onEnsureSSHKey()}>
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
            <div className="session-skills-list" data-testid="account-env-list">
              {settings && settings.env.length > 0 ? settings.env.map((entry) => (
                <div key={entry.name} className="session-skill-item account-env-item">
                  <span className="session-skill-title">
                    <strong>{entry.name}</strong>
                    <span>{entry.configured ? "configured" : "empty"}</span>
                  </span>
                  {onDeleteEnv ? (
                    <button type="button" className="ghost-action danger-action" disabled={!!busy} onClick={() => void onDeleteEnv(entry.name)}>
                      Delete
                    </button>
                  ) : null}
                </div>
              )) : (
                <div className="session-skills-empty">No environment variables configured.</div>
              )}
            </div>
          </>
        ) : null}
      </div>
    </details>
  );
}
