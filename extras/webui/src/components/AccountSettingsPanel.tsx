import { useMemo, useState, type FormEvent, type ReactNode } from "react";
import type {
  AccountGitCheckRequest,
  AccountGitCheckResponse,
  AccountSettingsResponse,
} from "../api/settings";
import {
  accountGitAccessVerifyRequest,
  accountGitRemoteVerifyRequest,
  accountConfigDetail,
  accountConfigReview,
  accountConfigSummary,
  accountEnvMatchesFilter,
  accountEnvMatchesQuery,
  accountEnvReviewFindings,
  accountEnvReviewNames,
  sshAccessDescription,
  sshPathDisplay,
  sshPublicKeyIssueDescription,
  sshStorageDescription,
  type AccountEnvFilter,
} from "../view/accountConfig";
import { CopyButton } from "./CopyButton";
import { panelErrorSummary } from "./panelErrorSummary";

type GitCheckState =
  | { kind: AccountGitCheckRequest["kind"]; status: "running" }
  | {
      kind: AccountGitCheckRequest["kind"];
      status: AccountGitCheckResponse["status"];
      target: string;
      exitCode: number;
      output: string;
      durationMs?: number;
    }
  | { kind: AccountGitCheckRequest["kind"]; status: "error"; message: string };

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
  onVerifyGitAccess?: (
    request: AccountGitCheckRequest,
  ) => Promise<AccountGitCheckResponse> | AccountGitCheckResponse;
}) {
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [query, setQuery] = useState("");
  const [gitHost, setGitHost] = useState("");
  const [gitRemote, setGitRemote] = useState("");
  const [confirmDeleteEnv, setConfirmDeleteEnv] = useState<
    string | undefined
  >();
  const [mutationStatus, setMutationStatus] = useState<
    { tone: "success" | "error"; message: string } | undefined
  >();
  const [envFilter, setEnvFilter] = useState<AccountEnvFilter>("all");
  const [envEditorOpen, setEnvEditorOpen] = useState(false);
  const [envListOpen, setEnvListOpen] = useState(false);
  const [selectedEnvReviewName, setSelectedEnvReviewName] = useState<
    string | undefined
  >();
  const ssh = settings?.ssh;
  const envReviewFindings = useMemo(
    () => accountEnvReviewFindings(settings),
    [settings],
  );
  const envIssueCount = envReviewFindings.length;
  const envReviewNames = useMemo(
    () => accountEnvReviewNames(settings),
    [settings],
  );
  const trimmedQuery = query.trim();
  const visibleEnv = useMemo(() => {
    const env = settings?.env ?? [];
    return env
      .filter((entry) =>
        accountEnvMatchesFilter(entry, envFilter, envReviewNames),
      )
      .filter(
        (entry) => !trimmedQuery || accountEnvMatchesQuery(entry, trimmedQuery),
      );
  }, [envFilter, envReviewNames, settings?.env, trimmedQuery]);
  const title = loading
    ? "Loading"
    : error
      ? "Unavailable"
      : accountConfigSummary(settings);
  const safeError = configErrorMessage(error);
  const detail = error
    ? panelErrorSummary("Config API", safeError)
    : accountConfigDetail(settings);
  const surfaceDetail = error ? "Account settings cannot be read" : detail;
  const sshDescription = sshAccessDescription(ssh);
  const sshStorage = sshStorageDescription(ssh);
  const sshPublicKeyIssue = sshPublicKeyIssueDescription(ssh);
  const canSubmit = !!name.trim() && !!onSetEnv && !busy;
  const showEnvWrite = Boolean(
    onSetEnv && (!settings || settings.env.length === 0 || envEditorOpen),
  );
  const envListExpanded =
    envListOpen ||
    Boolean(trimmedQuery) ||
    envFilter !== "all" ||
    Boolean(selectedEnvReviewName) ||
    Boolean(confirmDeleteEnv);

  function prepareEnvWrite(envName?: string) {
    setEnvEditorOpen(true);
    setEnvListOpen(false);
    setName(envName ?? "");
    setValue("");
    setMutationStatus(undefined);
  }

  async function submitEnv(event: FormEvent) {
    event.preventDefault();
    if (!canSubmit) return;
    const envName = name.trim();
    setMutationStatus(undefined);
    try {
      await onSetEnv?.(envName, value);
      setName("");
      setValue("");
      setEnvEditorOpen(false);
      setEnvListOpen(true);
      setMutationStatus({ tone: "success", message: `${envName} saved.` });
    } catch (err) {
      setMutationStatus({
        tone: "error",
        message: formatConfigPanelError(err),
      });
    }
  }

  async function deleteEnv(envName: string) {
    setMutationStatus(undefined);
    try {
      await onDeleteEnv?.(envName);
      setConfirmDeleteEnv(undefined);
      setMutationStatus({ tone: "success", message: `${envName} deleted.` });
    } catch (err) {
      setMutationStatus({
        tone: "error",
        message: formatConfigPanelError(err),
      });
    }
  }

  async function ensureSSHKey() {
    if (!onEnsureSSHKey) return;
    setMutationStatus(undefined);
    try {
      await onEnsureSSHKey();
      setMutationStatus({ tone: "success", message: "SSH key ready." });
    } catch (err) {
      setMutationStatus({
        tone: "error",
        message: formatConfigPanelError(err),
      });
    }
  }

  const envWritePanel = showEnvWrite ? (
    <section
      className="account-env-write"
      aria-label="Add environment variable"
    >
      <div className="account-env-write-head">
        <div>
          <strong>
            {settings?.env.length
              ? "Add secret"
              : error
                ? "Save environment secret"
                : "Add first secret"}
          </strong>
          <span>
            {error
              ? "Env writes still work without reading existing settings."
              : "Stored server-side; never echoed."}
          </span>
        </div>
        {settings?.env.length ? (
          <button
            type="button"
            className="ghost-action"
            disabled={!!busy}
            onClick={() => {
              setEnvEditorOpen(false);
              setName("");
              setValue("");
            }}
          >
            Cancel
          </button>
        ) : null}
      </div>
      <form
        className="session-loop-setup account-env-form"
        onSubmit={submitEnv}
      >
        <label>
          <span>Environment variable</span>
          <input
            value={name}
            onChange={(event) => setName(event.target.value)}
            placeholder="GITHUB_TOKEN"
            autoComplete="off"
            disabled={!!busy}
          />
        </label>
        <label>
          <span>Value</span>
          <input
            value={value}
            onChange={(event) => setValue(event.target.value)}
            placeholder="Stored server-side"
            type="password"
            autoComplete="new-password"
            disabled={!!busy}
          />
        </label>
        <button
          type="submit"
          className="secondary-action"
          disabled={!canSubmit}
        >
          {busy === "env" ? "Saving" : "Save env"}
        </button>
      </form>
    </section>
  ) : null;

  const envReviewPanel =
    settings && envReviewFindings.length > 0 ? (
      <AccountEnvReviewQueue
        findings={envReviewFindings}
        onShowEnv={() => {
          setQuery("");
          setEnvFilter("review");
          setSelectedEnvReviewName(envReviewFindings[0]?.name);
          setEnvListOpen(true);
        }}
        onOpenFinding={(finding) => {
          setQuery("");
          setEnvFilter("review");
          setSelectedEnvReviewName(finding.name);
          setEnvListOpen(true);
        }}
        onPrepareEnv={
          onSetEnv
            ? (envName, finding) => {
                setQuery("");
                setEnvFilter("review");
                setSelectedEnvReviewName(finding.name);
                prepareEnvWrite(envName);
              }
            : undefined
        }
      />
    ) : null;

  const panelBody = (
    <div className="session-skills-body account-settings-body">
      {loading ? (
        <div className="session-skills-empty">Loading config...</div>
      ) : null}
      {!loading && error ? (
        <section
          className="account-settings-read-error"
          data-testid="account-settings-fallback"
          role="alert"
        >
          <div>
            <strong>Account settings unavailable</strong>
            <span>
              Cannot read saved settings. Retry after fixing account storage
              permissions.
            </span>
          </div>
          <code title={safeError}>{safeError}</code>
          {onRefresh ? (
            <button
              type="button"
              className="ghost-action"
              disabled={!!busy}
              onClick={() => void onRefresh()}
            >
              Retry
            </button>
          ) : null}
        </section>
      ) : null}
      {!loading && (!error || settings || onSetEnv || onEnsureSSHKey) ? (
        <>
          {settings ? (
            <>
              <AccountConfigFocus
                settings={settings}
                busy={busy}
                onRefresh={onRefresh}
                onEnsureSSHKey={onEnsureSSHKey ? ensureSSHKey : undefined}
                gitHost={gitHost}
                setGitHost={setGitHost}
                gitRemote={gitRemote}
                setGitRemote={setGitRemote}
                onVerifyGitAccess={onVerifyGitAccess}
                envWriteSlot={envWritePanel}
                envReviewSlot={envReviewPanel}
              />
              <div className="account-settings-actions">
                {onRefresh ? (
                  <button
                    type="button"
                    className="node-action"
                    disabled={!!busy}
                    onClick={() => void onRefresh()}
                  >
                    Refresh
                  </button>
                ) : null}
              </div>
            </>
          ) : null}
          {ssh?.exists && !ssh.public_key ? (
            <div className="account-settings-section">
              <div className="account-settings-section-heading">
                <strong>Private repo access</strong>
                <span>{sshDescription}</span>
              </div>
              {sshStorage ? (
                <div
                  className="account-ssh-storage"
                  data-testid="account-ssh-storage"
                >
                  <span>Key path</span>
                  <code title={sshStorage}>
                    {sshPathDisplay(ssh?.public_key_path) || sshStorage}
                  </code>
                  {ssh?.public_key_path ? (
                    <CopyButton
                      label="Copy path"
                      value={ssh.public_key_path}
                      className="ghost-action"
                    />
                  ) : null}
                </div>
              ) : null}
              <div className="session-skills-empty error" role="alert">
                {sshPublicKeyIssue}
              </div>
              <div className="session-loop-actions">
                {onRefresh ? (
                  <button
                    type="button"
                    className="ghost-action"
                    disabled={!!busy}
                    onClick={() => void onRefresh()}
                  >
                    Refresh
                  </button>
                ) : null}
              </div>
            </div>
          ) : null}
          {!settings ? envWritePanel : null}
          {mutationStatus ? (
            <span
              className="account-settings-status"
              data-tone={mutationStatus.tone}
              role="status"
              aria-live="polite"
            >
              {mutationStatus.message}
            </span>
          ) : null}
          {settings && settings.env.length > 0 ? (
            <section
              className="account-env-details"
              data-open={envListExpanded ? "true" : "false"}
              aria-label="All secrets"
            >
              <div className="account-env-details-head">
                <button
                  type="button"
                  className="account-env-details-toggle"
                  aria-expanded={envListExpanded}
                  aria-controls="account-env-list-region"
                  onClick={() => setEnvListOpen((open) => !open)}
                >
                  <span>All secrets</span>
                  <strong>
                    {settings.env.length} total ·{" "}
                    {settings.env.filter((entry) => entry.configured).length}{" "}
                    configured
                  </strong>
                  <small>
                    {envIssueCount > 0
                      ? `${envIssueCount} need attention`
                      : "No credential blockers"}
                  </small>
                </button>
                {onSetEnv && !envEditorOpen ? (
                  <button
                    type="button"
                    className="secondary-action account-env-add-action"
                    disabled={!!busy}
                    onClick={() => prepareEnvWrite()}
                  >
                    Add secret
                  </button>
                ) : null}
              </div>
              {envListExpanded ? (
                <div
                  id="account-env-list-region"
                  className="account-env-detail-body"
                >
                  <div className="session-skills-controls account-env-controls">
                    <AccountEnvFilters
                      env={settings.env}
                      reviewNames={envReviewNames}
                      value={envFilter}
                      onChange={(nextFilter) => {
                        setEnvListOpen(true);
                        setEnvFilter(nextFilter);
                        if (nextFilter !== "review")
                          setSelectedEnvReviewName(undefined);
                      }}
                    />
                    <label className="session-skills-search">
                      <span>Search env</span>
                      <input
                        value={query}
                        onChange={(event) => {
                          setEnvListOpen(true);
                          setQuery(event.target.value);
                        }}
                        placeholder="name, configured, or empty"
                      />
                    </label>
                    {trimmedQuery || envFilter !== "all" ? (
                      <button
                        type="button"
                        className="ghost-action"
                        onClick={() => {
                          setQuery("");
                          setEnvFilter("all");
                          setSelectedEnvReviewName(undefined);
                          setEnvListOpen(true);
                        }}
                      >
                        Clear
                      </button>
                    ) : null}
                    {trimmedQuery || envFilter !== "all" ? (
                      <span
                        className="session-search-count"
                        data-testid="account-env-search-count"
                      >
                        {visibleEnv.length}{" "}
                        {visibleEnv.length === 1 ? "variable" : "variables"}
                        {trimmedQuery ? ` matching "${trimmedQuery}"` : ""}
                      </span>
                    ) : null}
                  </div>
                  <div
                    className="account-env-table"
                    data-testid="account-env-list"
                    aria-label="Saved environment variables"
                  >
                    <div className="account-env-table-head" aria-hidden="true">
                      <span>Name</span>
                      <span>Status</span>
                      <span>Updated</span>
                      <span>Actions</span>
                    </div>
                    {visibleEnv.length > 0 ? (
                      visibleEnv.map((entry) => {
                        const needsReview = envReviewNames.has(entry.name);
                        return (
                          <div
                            key={entry.name}
                            className="account-env-row"
                            data-state={
                              entry.configured ? "configured" : "empty"
                            }
                            data-review={needsReview ? "true" : undefined}
                            data-selected-review={
                              selectedEnvReviewName === entry.name
                                ? "true"
                                : undefined
                            }
                          >
                            <strong title={entry.name}>{entry.name}</strong>
                            <span
                              className="account-env-state"
                              data-state={
                                entry.configured ? "configured" : "empty"
                              }
                            >
                              {entry.configured ? "configured" : "empty"}
                            </span>
                            <time
                              className="account-env-updated"
                              dateTime={entry.updated_at || undefined}
                            >
                              {entry.updated_at
                                ? formatTimestamp(entry.updated_at)
                                : "Never"}
                            </time>
                            <div className="account-env-actions">
                              {onDeleteEnv ? (
                                confirmDeleteEnv === entry.name ? (
                                  <div
                                    className="account-env-confirm"
                                    role="group"
                                    aria-label={`Confirm delete ${entry.name}`}
                                  >
                                    <span>Delete {entry.name}?</span>
                                    <button
                                      type="button"
                                      disabled={!!busy}
                                      onClick={() =>
                                        setConfirmDeleteEnv(undefined)
                                      }
                                    >
                                      Cancel
                                    </button>
                                    <button
                                      type="button"
                                      className="danger"
                                      disabled={!!busy}
                                      onClick={() => void deleteEnv(entry.name)}
                                    >
                                      Confirm
                                    </button>
                                  </div>
                                ) : (
                                  <button
                                    type="button"
                                    className="ghost-action danger-action"
                                    disabled={!!busy}
                                    onClick={() => {
                                      setEnvListOpen(true);
                                      setConfirmDeleteEnv(entry.name);
                                    }}
                                  >
                                    Delete
                                  </button>
                                )
                              ) : null}
                            </div>
                          </div>
                        );
                      })
                    ) : (
                      <div className="session-skills-empty">
                        No environment variables matching "{trimmedQuery}".
                      </div>
                    )}
                  </div>
                </div>
              ) : null}
            </section>
          ) : null}
        </>
      ) : null}
    </div>
  );

  if (surface) {
    return (
      <section
        className="account-settings-panel account-settings-surface"
        data-testid="account-settings-panel"
        data-surface="true"
      >
        <header className="account-settings-header">
          <span className="session-skills-kicker">Config</span>
          <div className="account-settings-header-title">
            <strong>{title}</strong>
            <span>{surfaceDetail}</span>
          </div>
        </header>
        {panelBody}
      </section>
    );
  }

  return (
    <details
      className="session-skills-panel account-settings-panel"
      data-testid="account-settings-panel"
      {...(defaultOpen ? { open: true } : {})}
    >
      <summary className="session-skills-summary">
        <span className="session-skills-kicker">Config</span>
        <strong>{title}</strong>
        <span>{detail}</span>
      </summary>
      {panelBody}
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
  const options: Array<{
    value: AccountEnvFilter;
    label: string;
    count: number;
  }> = [
    { value: "all", label: "All", count: counts.all },
    { value: "configured", label: "Configured", count: counts.configured },
    { value: "empty", label: "Empty", count: counts.empty },
    { value: "review", label: "Needs review", count: counts.review },
  ];
  return (
    <div
      className="session-filter-pills"
      role="group"
      aria-label="Filter environment variables"
    >
      {options
        .filter(
          (option) =>
            option.value === "all" ||
            option.count > 0 ||
            value === option.value,
        )
        .map((option) => (
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
  onOpenFinding,
  onPrepareEnv,
}: {
  findings: ReturnType<typeof accountEnvReviewFindings>;
  onShowEnv: () => void;
  onOpenFinding: (
    finding: ReturnType<typeof accountEnvReviewFindings>[number],
  ) => void;
  onPrepareEnv?: (
    name: string,
    finding: ReturnType<typeof accountEnvReviewFindings>[number],
  ) => void;
}) {
  const counts = findings.reduce<Record<string, number>>((acc, finding) => {
    acc[finding.kind] = (acc[finding.kind] ?? 0) + 1;
    return acc;
  }, {});
  return (
    <section
      className="account-env-review"
      data-testid="account-env-review"
      aria-label="Credential checklist"
    >
      <div className="account-env-review-head">
        <span>Credential checklist</span>
        <strong>
          {findings.length} secret{" "}
          {findings.length === 1 ? "needs" : "items need"} attention
        </strong>
        <small>{credentialChecklistSummary(counts)}</small>
        <button type="button" className="ghost-action" onClick={onShowEnv}>
          Show in table
        </button>
      </div>
      <ul className="account-env-review-list">
        {findings.slice(0, 4).map((finding) => {
          const prepareName =
            finding.kind === "incomplete" ? finding.related?.[0] : finding.name;
          const label =
            finding.kind === "empty" ? "Empty value" : "Missing companion";
          return (
            <li
              key={`${finding.kind}:${finding.name}`}
              data-kind={finding.kind}
            >
              <button
                type="button"
                className="account-env-review-finding"
                onClick={() => onOpenFinding(finding)}
              >
                <span>{label}</span>
                <strong>{prepareName ?? finding.name}</strong>
                <small>{finding.detail}</small>
              </button>
              {onPrepareEnv && prepareName ? (
                <button
                  type="button"
                  className="account-env-review-action"
                  onClick={() => onPrepareEnv(prepareName, finding)}
                >
                  {finding.kind === "empty"
                    ? "Set value"
                    : `Add ${prepareName}`}
                </button>
              ) : null}
            </li>
          );
        })}
      </ul>
    </section>
  );
}

function credentialChecklistSummary(counts: Record<string, number>): string {
  return [
    counts.empty ? `${counts.empty} empty` : undefined,
    counts.incomplete ? `${counts.incomplete} incomplete` : undefined,
  ]
    .filter((part): part is string => Boolean(part))
    .join(" · ");
}

function AccountConfigFocus({
  settings,
  busy,
  onRefresh,
  onEnsureSSHKey,
  gitHost,
  setGitHost,
  gitRemote,
  setGitRemote,
  onVerifyGitAccess,
  envWriteSlot,
  envReviewSlot,
}: {
  settings: AccountSettingsResponse;
  busy?: string;
  onRefresh?: () => Promise<void> | void;
  onEnsureSSHKey?: () => Promise<void> | void;
  gitHost: string;
  setGitHost: (value: string) => void;
  gitRemote: string;
  setGitRemote: (value: string) => void;
  onVerifyGitAccess?: (
    request: AccountGitCheckRequest,
  ) => Promise<AccountGitCheckResponse> | AccountGitCheckResponse;
  envWriteSlot?: ReactNode;
  envReviewSlot?: ReactNode;
}) {
  const [hostGitCheck, setHostGitCheck] = useState<GitCheckState | undefined>();
  const [remoteGitCheck, setRemoteGitCheck] = useState<
    GitCheckState | undefined
  >();
  const [gitProbeMode, setGitProbeMode] = useState<"host" | "remote">("host");
  const review = accountConfigReview(settings);
  const canVerifyGit = Boolean(settings.ssh.public_key && onVerifyGitAccess);
  const canVerifyPresetHost =
    canVerifyGit && hostGitCheck?.status !== "running";
  const canVerifyHost =
    canVerifyGit &&
    gitHost.trim().length > 0 &&
    hostGitCheck?.status !== "running";
  const canVerifyRemote =
    canVerifyGit &&
    gitRemote.trim().length > 0 &&
    remoteGitCheck?.status !== "running";
  const previousGitCheck =
    gitProbeMode === "host" ? remoteGitCheck : hostGitCheck;

  async function verify(
    kind: AccountGitCheckRequest["kind"],
    request: AccountGitCheckRequest,
  ) {
    if (!onVerifyGitAccess) return;
    const setCheck = kind === "host" ? setHostGitCheck : setRemoteGitCheck;
    setCheck({ kind, status: "running" });
    try {
      const result = await onVerifyGitAccess(request);
      setCheck({
        kind,
        status: result.status,
        target: result.host || result.target,
        exitCode: result.exit_code,
        output: result.output,
        durationMs: result.duration_ms,
      });
    } catch (err) {
      setCheck({ kind, status: "error", message: formatConfigPanelError(err) });
    }
  }

  return (
    <section
      className="account-config-focus"
      data-testid="account-config-focus"
      data-tone={review.tone}
      aria-label="Runtime config review"
    >
      <div className="account-config-focus-head">
        <span>
          {review.tone === "ready"
            ? "Runtime ready"
            : review.tone === "attention"
              ? "Review needed"
              : "Setup needed"}
        </span>
        <strong>{review.headline}</strong>
        <small>{review.detail}</small>
      </div>
      <div
        className="account-config-credentials"
        data-testid="account-config-credentials"
        data-tone={review.tone}
      >
        {review.tone !== "ready" ? (
          <div className="account-config-next">
            <span>Next action</span>
            <p>{review.nextAction}</p>
          </div>
        ) : null}
        <div className="account-config-focus-grid">
          <ConfigFocusFact
            label="Private Git"
            value={review.privateGit}
            detail={review.keyPath}
          />
          <ConfigFocusFact
            label="Secrets"
            value={review.envCount}
            detail={
              review.latestEnvUpdate
                ? `updated ${formatTimestamp(review.latestEnvUpdate)}`
                : "none updated"
            }
          />
        </div>
      </div>
      {settings.ssh.public_key ? (
        <div className="account-public-key-row">
          <span>Public key</span>
          <code
            className="account-public-key"
            data-testid="account-public-key"
            title={settings.ssh.public_key}
          >
            {settings.ssh.public_key}
          </code>
          <div className="account-public-key-actions">
            <CopyButton
              label="Copy public key"
              value={settings.ssh.public_key}
              className="secondary-action"
            />
            {settings.ssh.public_key_path ? (
              <CopyButton
                label="Copy key path"
                value={settings.ssh.public_key_path}
                className="ghost-action"
              />
            ) : null}
          </div>
        </div>
      ) : null}
      {envReviewSlot}
      <div className="account-config-workbench">
        {envWriteSlot ? (
          <div className="account-config-credentials">{envWriteSlot}</div>
        ) : null}
        {settings.ssh.public_key ? (
          <section
            className="account-config-verify-panel"
            data-testid="account-config-verify"
            aria-label="Git access check"
          >
            <div className="account-config-verify-head">
              <div>
                <span>Git access check</span>
                <strong>Verify private clone access</strong>
              </div>
              <div
                className="account-config-probe-mode"
                role="tablist"
                aria-label="Git access check mode"
              >
                <button
                  type="button"
                  role="tab"
                  aria-selected={gitProbeMode === "host"}
                  onClick={() => setGitProbeMode("host")}
                >
                  Host
                </button>
                <button
                  type="button"
                  role="tab"
                  aria-selected={gitProbeMode === "remote"}
                  onClick={() => setGitProbeMode("remote")}
                >
                  Repository
                </button>
              </div>
            </div>
            {gitProbeMode === "host" ? (
              <div className="account-config-verify-row account-config-verify-row-host">
                <div
                  className="account-config-host-presets"
                  role="group"
                  aria-label="Git host presets"
                >
                  <button
                    type="button"
                    className="ghost-action"
                    disabled={!!busy || !canVerifyPresetHost}
                    onClick={() => {
                      setGitHost("github.com");
                      void verify(
                        "host",
                        accountGitAccessVerifyRequest("github.com"),
                      );
                    }}
                  >
                    Check GitHub
                  </button>
                  <button
                    type="button"
                    className="ghost-action"
                    disabled={!!busy || !canVerifyPresetHost}
                    onClick={() => {
                      setGitHost("gitlab.com");
                      void verify(
                        "host",
                        accountGitAccessVerifyRequest("gitlab.com"),
                      );
                    }}
                  >
                    Check GitLab
                  </button>
                </div>
                <label>
                  <span>Custom host</span>
                  <input
                    value={gitHost}
                    onChange={(event) => setGitHost(event.target.value)}
                    placeholder="github.com or gitlab.com"
                    disabled={!!busy || !canVerifyGit}
                  />
                </label>
                <button
                  type="button"
                  className="secondary-action"
                  disabled={!!busy || !canVerifyHost}
                  onClick={() =>
                    void verify("host", accountGitAccessVerifyRequest(gitHost))
                  }
                >
                  {hostGitCheck?.status === "running"
                    ? "Checking host"
                    : "Check host"}
                </button>
                <GitCheckResult state={hostGitCheck} kind="host" />
              </div>
            ) : (
              <div className="account-config-verify-row">
                <label>
                  <span>Repository</span>
                  <input
                    value={gitRemote}
                    onChange={(event) => setGitRemote(event.target.value)}
                    placeholder="git@github.com:owner/repo.git or https://github.com/owner/repo"
                    disabled={!!busy || !canVerifyGit}
                  />
                </label>
                <button
                  type="button"
                  className="secondary-action"
                  disabled={!!busy || !canVerifyRemote}
                  onClick={() =>
                    void verify(
                      "remote",
                      accountGitRemoteVerifyRequest(gitRemote),
                    )
                  }
                >
                  {remoteGitCheck?.status === "running"
                    ? "Checking repository"
                    : "Check repository"}
                </button>
                <GitCheckResult state={remoteGitCheck} kind="remote" />
              </div>
            )}
            {previousGitCheck ? (
              <div
                className="account-config-verify-history"
                aria-label="Previous Git access check"
              >
                <span>Previous check</span>
                <GitCheckResult
                  state={previousGitCheck}
                  kind={previousGitCheck.kind}
                />
              </div>
            ) : null}
          </section>
        ) : null}
      </div>
      <div className="account-config-focus-actions">
        {!settings.ssh.exists && onEnsureSSHKey ? (
          <button
            type="button"
            className="secondary-action"
            disabled={!!busy}
            onClick={() => void onEnsureSSHKey()}
          >
            {busy === "ssh" ? "Checking key" : "Generate SSH key"}
          </button>
        ) : null}
        {settings.ssh.exists && !settings.ssh.public_key && onRefresh ? (
          <button
            type="button"
            className="node-action"
            disabled={!!busy}
            onClick={() => void onRefresh()}
          >
            Refresh after fix
          </button>
        ) : null}
      </div>
    </section>
  );
}

function ConfigFocusFact({
  label,
  value,
  detail,
}: {
  label: string;
  value: string;
  detail?: string;
}) {
  return (
    <div className="account-config-focus-fact">
      <span>{label}</span>
      <strong title={detail || value}>{value}</strong>
      {detail ? <small title={detail}>{detail}</small> : null}
    </div>
  );
}

function GitCheckResult({
  state,
  kind,
}: {
  state?: GitCheckState;
  kind: AccountGitCheckRequest["kind"];
}) {
  if (!state || state.kind !== kind) return null;
  if (state.status === "running") {
    return (
      <div
        className="account-config-check-result"
        data-state="running"
        role="status"
      >
        Checking...
      </div>
    );
  }
  if (state.status === "error") {
    return (
      <div
        className="account-config-check-result"
        data-state="failed"
        role="alert"
      >
        {state.message}
      </div>
    );
  }
  const meta = [
    state.target,
    `exit ${state.exitCode}`,
    state.durationMs != null ? `${state.durationMs}ms` : undefined,
  ]
    .filter(Boolean)
    .join(" · ");
  const diagnostic =
    state.status === "ok" ? meta : gitCheckDiagnostic(state, kind);
  return (
    <div
      className="account-config-check-result"
      data-state={state.status}
      role="status"
    >
      <strong>{state.status === "ok" ? "Reachable" : "Not reachable"}</strong>
      <span>{diagnostic}</span>
      {state.status !== "ok" && meta ? <small>{meta}</small> : null}
      {state.output ? <pre>{state.output}</pre> : null}
    </div>
  );
}

function gitCheckDiagnostic(
  state: Extract<GitCheckState, { status: AccountGitCheckResponse["status"] }>,
  kind: AccountGitCheckRequest["kind"],
): string {
  const output = state.output.toLowerCase();
  if (
    /permission denied|publickey|authentication failed|access denied/.test(
      output,
    )
  ) {
    return "SSH auth failed. Add this public key to the Git provider account.";
  }
  if (
    /repository not found|not found|does not exist|could not read from remote repository/.test(
      output,
    )
  ) {
    return "Repository path or account permission failed.";
  }
  if (
    /could not resolve|name or service|temporary failure|no route to host|connection timed out/.test(
      output,
    )
  ) {
    return "Network or DNS cannot reach the Git host.";
  }
  if (/host key verification failed/.test(output)) {
    return "Host key verification failed for this runtime.";
  }
  return kind === "remote"
    ? "Clone probe failed for this repository."
    : "Host probe failed.";
}

function formatConfigPanelError(err: unknown): string {
  if (err instanceof Error) return configErrorMessage(err.message);
  return "Unknown error";
}

function configErrorMessage(error?: string): string {
  const message = (error ?? "").replace(/\s+/g, " ").trim();
  if (!message) return "";
  if (
    /read account settings/i.test(message) &&
    /permission denied/i.test(message) &&
    /(?:\.ssh|ssh)/i.test(message)
  ) {
    return "Cannot read account SSH key: permission denied.";
  }
  if (/permission denied/i.test(message) && /(?:\.ssh|ssh)/i.test(message)) {
    return "SSH key permission denied.";
  }
  const withoutPrefix = message.replace(/^affentserve_error:\s*/i, "");
  return withoutPrefix.replace(/(?:lstat|stat|open) \S+/gi, (match) => {
    const op = match.split(" ")[0] || "read";
    if (/(?:\.ssh|ssh)/i.test(match)) return `${op} account SSH key`;
    if (/workspace|account|session-state/i.test(match))
      return `${op} runtime storage`;
    return match;
  });
}

function formatTimestamp(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString(undefined, {
    dateStyle: "medium",
    timeStyle: "short",
  });
}
