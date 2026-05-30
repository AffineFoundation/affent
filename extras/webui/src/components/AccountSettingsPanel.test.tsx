import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AccountSettingsPanel } from "./AccountSettingsPanel";

describe("AccountSettingsPanel", () => {
  it("shows an existing SSH public key and safe config evidence actions", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    const onVerifyGitAccess = vi.fn(async (request) => ({
      ...request,
      host: request.kind === "host" ? request.target : "github.com",
      status: "ok" as const,
      exit_code: request.kind === "host" ? 1 : 0,
      output:
        request.kind === "host"
          ? "successfully authenticated"
          : "repo reachable",
      duration_ms: 42,
      checked_at: "2026-05-29T00:00:00Z",
    }));
    Object.defineProperty(navigator, "clipboard", {
      configurable: true,
      value: { writeText },
    });
    render(
      <AccountSettingsPanel
        settings={{
          env: [
            {
              name: "GITHUB_TOKEN",
              configured: true,
              updated_at: "2026-05-27T10:00:00Z",
            },
          ],
          ssh: {
            exists: true,
            public_key: "ssh-ed25519 AAAA affent",
            public_key_path: "/workspace/.home/.ssh/id_ed25519.pub",
          },
        }}
        onVerifyGitAccess={onVerifyGitAccess}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("account-settings-panel");
    expect(panel).toHaveTextContent("Config");
    expect(panel).toHaveTextContent("1 env · SSH key");
    expect(panel).toHaveTextContent("SSH ready · 1 env");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "Private Git ready",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "Public key",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "~/.ssh/id_ed25519.pub",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "1 env",
    );
    expect(screen.queryByText(/Run tasks normally/)).toBeNull();
    expect(
      within(screen.getByTestId("account-config-focus")).getByRole("button", {
        name: "Copy public key",
      }),
    ).toBeInTheDocument();
    expect(
      within(screen.getByTestId("account-config-focus")).getByRole("button", {
        name: "Copy key path",
      }),
    ).toBeInTheDocument();
    expect(screen.queryByText("Private repo access")).toBeNull();
    expect(screen.getByTestId("account-public-key")).toHaveTextContent(
      "ssh-ed25519 AAAA affent",
    );
    expect(screen.getByTestId("account-config-verify")).toHaveTextContent(
      "Git access check",
    );
    expect(screen.getByTestId("account-config-verify")).toHaveTextContent(
      "Host",
    );
    expect(screen.getByTestId("account-config-verify")).toHaveTextContent(
      "Repository",
    );
    expect(
      screen.getByRole("button", { name: "Check GitHub" }),
    ).not.toBeDisabled();
    expect(
      screen.getByRole("button", { name: "Check GitLab" }),
    ).not.toBeDisabled();
    expect(screen.queryByText("does not create a chat turn")).toBeNull();
    expect(screen.getByRole("button", { name: "Check host" })).toBeDisabled();
    expect(
      screen.queryByRole("button", { name: "Check repository" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Generate SSH key" }),
    ).toBeNull();
    expect(
      screen.queryByRole("button", { name: "Use config as draft" }),
    ).toBeNull();
    expect(screen.getByRole("button", { name: /All secrets/ })).toHaveAttribute(
      "aria-expanded",
      "false",
    );
    expect(screen.queryByTestId("account-env-list")).toBeNull();
    await user.click(screen.getByRole("button", { name: /All secrets/ }));
    expect(screen.getByTestId("account-env-list")).toHaveTextContent(
      "GITHUB_TOKEN",
    );
    expect(screen.getByTestId("account-env-list")).toHaveTextContent(
      "configured",
    );

    await user.click(
      within(screen.getByTestId("account-config-focus")).getByRole("button", {
        name: "Copy public key",
      }),
    );
    expect(writeText).toHaveBeenCalledWith("ssh-ed25519 AAAA affent");
    await user.click(
      within(screen.getByTestId("account-config-focus")).getByRole("button", {
        name: "Copy key path",
      }),
    );
    expect(writeText).toHaveBeenCalledWith(
      "/workspace/.home/.ssh/id_ed25519.pub",
    );
    expect(screen.queryByRole("button", { name: "Copy full key" })).toBeNull();

    await user.click(
      within(screen.getByRole("group", { name: "Git host presets" })).getByRole(
        "button",
        { name: "Check GitLab" },
      ),
    );
    expect(screen.getByPlaceholderText("github.com or gitlab.com")).toHaveValue(
      "gitlab.com",
    );
    expect(onVerifyGitAccess).toHaveBeenCalledWith({
      kind: "host",
      target: "gitlab.com",
    });
    expect(screen.getByText("Reachable")).toBeInTheDocument();
    expect(screen.getByText("successfully authenticated")).toBeInTheDocument();
    await user.clear(screen.getByPlaceholderText("github.com or gitlab.com"));
    await user.type(
      screen.getByPlaceholderText("github.com or gitlab.com"),
      "git@gitlab.com:team/repo.git",
    );
    await user.click(screen.getByRole("button", { name: "Check host" }));
    expect(onVerifyGitAccess).toHaveBeenCalledWith({
      kind: "host",
      target: "gitlab.com",
    });
    expect(screen.getByText("Reachable")).toBeInTheDocument();
    expect(screen.getByText("successfully authenticated")).toBeInTheDocument();

    await user.click(screen.getByRole("tab", { name: "Repository" }));
    expect(
      screen.getByRole("button", { name: "Check repository" }),
    ).toBeDisabled();
    await user.type(
      screen.getByPlaceholderText(
        "git@github.com:owner/repo.git or https://github.com/owner/repo",
      ),
      "https://github.com/team/private-repo",
    );
    await user.click(screen.getByRole("button", { name: "Check repository" }));
    expect(onVerifyGitAccess).toHaveBeenCalledWith({
      kind: "remote",
      target: "git@github.com:team/private-repo.git",
    });
    expect(screen.getByText("repo reachable")).toBeInTheDocument();
    expect(screen.getByText("successfully authenticated")).toBeInTheDocument();
  });

  it("saves and confirms deletion for environment variables without displaying the value", async () => {
    const user = userEvent.setup();
    const onSetEnv = vi.fn().mockResolvedValue(undefined);
    const onDeleteEnv = vi.fn().mockResolvedValue(undefined);
    render(
      <AccountSettingsPanel
        settings={{
          env: [
            { name: "GITHUB_TOKEN", configured: true },
            { name: "EMPTY_TOKEN", configured: false },
          ],
          ssh: { exists: false },
        }}
        onSetEnv={onSetEnv}
        onDeleteEnv={onDeleteEnv}
        onEnsureSSHKey={vi.fn()}
        defaultOpen
      />,
    );

    await user.click(screen.getByRole("button", { name: "Add secret" }));
    await user.type(
      screen.getByPlaceholderText("GITHUB_TOKEN"),
      "GITLAB_TOKEN",
    );
    await user.type(
      screen.getByPlaceholderText("Stored server-side"),
      "gl_secret",
    );
    await user.click(screen.getByRole("button", { name: "Save env" }));

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "No SSH key configured",
    );
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "2 envs",
    );
    expect(screen.getByTestId("account-env-review")).toHaveTextContent(
      "Credential checklist",
    );
    expect(screen.getByTestId("account-env-review")).toHaveTextContent(
      "1 secret needs attention",
    );
    expect(screen.getByTestId("account-env-review")).toHaveTextContent(
      "1 empty",
    );
    expect(screen.getByTestId("account-env-review")).toHaveTextContent(
      "EMPTY_TOKEN",
    );
    expect(screen.getByTestId("account-env-review")).toHaveTextContent(
      "Empty value",
    );
    expect(
      within(screen.getByTestId("account-env-review")).getByRole("button", {
        name: "Set value",
      }),
    ).toBeInTheDocument();
    expect(onSetEnv).toHaveBeenCalledWith("GITLAB_TOKEN", "gl_secret");
    expect(screen.getByRole("status")).toHaveTextContent("GITLAB_TOKEN saved.");
    expect(screen.queryByText("gl_secret")).toBeNull();

    const envList = screen.getByTestId("account-env-list");
    await user.click(
      within(screen.getByTestId("account-env-review")).getByRole("button", {
        name: /EMPTY_TOKEN/,
      }),
    );
    expect(screen.getByTestId("account-env-search-count")).toHaveTextContent(
      "1 variable",
    );
    expect(envList).toHaveTextContent("EMPTY_TOKEN");
    expect(envList).not.toHaveTextContent("GITHUB_TOKEN");
    const selectedReviewRow = envList.querySelector(
      '[data-selected-review="true"]',
    );
    expect(selectedReviewRow).not.toBeNull();
    expect(selectedReviewRow).toHaveTextContent("EMPTY_TOKEN");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.type(
      screen.getByPlaceholderText("name, configured, or empty"),
      "github",
    );
    expect(screen.getByTestId("account-env-search-count")).toHaveTextContent(
      '1 variable matching "github"',
    );
    expect(envList).toHaveTextContent("GITHUB_TOKEN");
    expect(envList).not.toHaveTextContent("EMPTY_TOKEN");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(envList).toHaveTextContent("EMPTY_TOKEN");

    await user.click(
      within(envList).getAllByRole("button", { name: "Delete" })[0],
    );
    expect(onDeleteEnv).not.toHaveBeenCalled();
    const firstConfirm = within(envList).getByRole("group", {
      name: "Confirm delete GITHUB_TOKEN",
    });
    expect(firstConfirm).toHaveTextContent("Delete GITHUB_TOKEN?");
    await user.click(
      within(firstConfirm).getByRole("button", { name: "Cancel" }),
    );
    expect(
      within(envList).queryByRole("group", {
        name: "Confirm delete GITHUB_TOKEN",
      }),
    ).toBeNull();

    await user.click(
      within(envList).getAllByRole("button", { name: "Delete" })[0],
    );
    const confirm = within(envList).getByRole("group", {
      name: "Confirm delete GITHUB_TOKEN",
    });
    await user.click(within(confirm).getByRole("button", { name: "Confirm" }));
    expect(onDeleteEnv).toHaveBeenCalledWith("GITHUB_TOKEN");
    expect(screen.getByRole("status")).toHaveTextContent(
      "GITHUB_TOKEN deleted.",
    );
  });

  it("opens the env editor with missing review variables prefilled", async () => {
    const user = userEvent.setup();
    const onSetEnv = vi.fn().mockResolvedValue(undefined);
    render(
      <AccountSettingsPanel
        settings={{
          env: [{ name: "GOOGLE_API_KEY", configured: true }],
          ssh: { exists: false },
        }}
        onSetEnv={onSetEnv}
        defaultOpen
      />,
    );

    expect(screen.getByTestId("account-env-review")).toHaveTextContent(
      "Google search also needs GOOGLE_CSE_ID",
    );
    await user.click(
      within(screen.getByTestId("account-env-review")).getByRole("button", {
        name: "Add GOOGLE_CSE_ID",
      }),
    );

    expect(
      screen.getByRole("region", { name: "Add environment variable" }),
    ).toHaveTextContent("Add secret");
    expect(screen.getByDisplayValue("GOOGLE_CSE_ID")).toBeInTheDocument();
    await user.type(
      screen.getByPlaceholderText("Stored server-side"),
      "cx_123",
    );
    await user.click(screen.getByRole("button", { name: "Save env" }));

    expect(onSetEnv).toHaveBeenCalledWith("GOOGLE_CSE_ID", "cx_123");
    expect(screen.getByRole("status")).toHaveTextContent(
      "GOOGLE_CSE_ID saved.",
    );
    expect(screen.queryByText("cx_123")).toBeNull();
  });

  it("offers SSH key generation when no key exists", async () => {
    const user = userEvent.setup();
    const onEnsureSSHKey = vi.fn().mockResolvedValue(undefined);
    render(
      <AccountSettingsPanel
        settings={{ env: [], ssh: { exists: false } }}
        onEnsureSSHKey={onEnsureSSHKey}
        defaultOpen
      />,
    );

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "No config",
    );
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "No env vars or SSH key configured",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "SSH key missing",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "Generate an SSH key only when this runtime needs private repo access",
    );
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "Generate an SSH key only when this runtime needs private repo access.",
    );
    await user.click(screen.getByRole("button", { name: "Generate SSH key" }));

    expect(onEnsureSSHKey).toHaveBeenCalled();
    expect(screen.getByRole("status")).toHaveTextContent("SSH key ready.");
  });

  it("diagnoses failed git checks with an actionable cause", async () => {
    const user = userEvent.setup();
    const onVerifyGitAccess = vi.fn(async (request) => ({
      ...request,
      status: "failed" as const,
      exit_code: 255,
      output: "git@github.com: Permission denied (publickey).",
      duration_ms: 120,
      checked_at: "2026-05-30T00:00:00Z",
    }));
    render(
      <AccountSettingsPanel
        settings={{
          env: [],
          ssh: {
            exists: true,
            public_key: "ssh-ed25519 AAAA affent",
            public_key_path: "/workspace/.home/.ssh/id_ed25519.pub",
          },
        }}
        onVerifyGitAccess={onVerifyGitAccess}
        defaultOpen
      />,
    );

    await user.click(screen.getByRole("tab", { name: "Repository" }));
    await user.type(
      screen.getByPlaceholderText(
        "git@github.com:owner/repo.git or https://github.com/owner/repo",
      ),
      "git@github.com:team/private.git",
    );
    await user.click(screen.getByRole("button", { name: "Check repository" }));

    expect(onVerifyGitAccess).toHaveBeenCalledWith({
      kind: "remote",
      target: "git@github.com:team/private.git",
    });
    expect(screen.getByRole("status")).toHaveTextContent("Not reachable");
    expect(screen.getByRole("status")).toHaveTextContent(
      "SSH auth failed. Add this public key to the Git provider account.",
    );
    expect(screen.getByRole("status")).toHaveTextContent(
      "git@github.com:team/private.git · exit 255 · 120ms",
    );
  });

  it("keeps config forms usable and shows server failures inline", async () => {
    const user = userEvent.setup();
    const onSetEnv = vi
      .fn()
      .mockRejectedValue(new Error("settings storage is read-only"));
    const onEnsureSSHKey = vi
      .fn()
      .mockRejectedValue(new Error("ssh key path is not writable"));
    render(
      <AccountSettingsPanel
        settings={{ env: [], ssh: { exists: false } }}
        onSetEnv={onSetEnv}
        onEnsureSSHKey={onEnsureSSHKey}
        defaultOpen
      />,
    );

    await user.type(
      screen.getByPlaceholderText("GITHUB_TOKEN"),
      "GITHUB_TOKEN",
    );
    await user.type(
      screen.getByPlaceholderText("Stored server-side"),
      "secret-value",
    );
    await user.click(screen.getByRole("button", { name: "Save env" }));

    expect(screen.getByRole("status")).toHaveTextContent(
      "settings storage is read-only",
    );
    expect(screen.getByPlaceholderText("GITHUB_TOKEN")).toHaveValue(
      "GITHUB_TOKEN",
    );
    expect(screen.getByPlaceholderText("Stored server-side")).toHaveValue(
      "secret-value",
    );

    await user.click(screen.getByRole("button", { name: "Generate SSH key" }));
    expect(screen.getByRole("status")).toHaveTextContent(
      "ssh key path is not writable",
    );
  });

  it("does not offer generation when a private key exists but public key is unavailable", () => {
    render(
      <AccountSettingsPanel
        settings={{
          env: [],
          ssh: {
            exists: true,
            public_key_error:
              "private SSH key already exists but public key is missing and could not be derived",
          },
        }}
        defaultOpen
      />,
    );

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "SSH key issue",
    );
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "SSH key found; public key unavailable",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "SSH key needs review",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "Blocked",
    );
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent(
      "Fix or derive the public key in ~/.ssh",
    );
    expect(screen.getByRole("alert")).toHaveTextContent("missing or cannot be derived");
    expect(
      screen.queryByRole("button", { name: "Generate SSH key" }),
    ).toBeNull();
  });

  it("explains SSH public key permission failures without leaking storage paths", () => {
    render(
      <AccountSettingsPanel
        settings={{
          env: [],
          ssh: {
            exists: true,
            public_key_path: "/workspace/.home/.ssh/id_ed25519.pub",
            public_key_error:
              "open /workspace/.home/.ssh/id_ed25519.pub: permission denied",
          },
        }}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("account-settings-panel");
    expect(panel).toHaveTextContent(
      "Cannot read the SSH public key: permission denied.",
    );
    expect(panel).toHaveTextContent("Fix ~/.ssh file permissions");
    expect(panel).toHaveTextContent("~/.ssh/id_ed25519.pub");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Cannot read the SSH public key: permission denied.",
    );
    expect(panel).not.toHaveTextContent("open /workspace/.home");
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const onSetEnv = vi.fn().mockResolvedValue(undefined);
    const diagnostic =
      "API route /v1/settings returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<AccountSettingsPanel error={diagnostic} onSetEnv={onSetEnv} />);

    const summary = within(screen.getByTestId("account-settings-panel"))
      .getByText("Unavailable")
      .closest("summary");
    expect(summary).toHaveTextContent(
      "Config API failed: API route /v1/settings returned the WebUI app shell.",
    );
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
    expect(screen.getByTestId("account-settings-fallback")).toHaveTextContent(
      "Account settings unavailable",
    );
    expect(screen.getByTestId("account-settings-fallback")).toHaveTextContent(
      "Cannot read saved settings",
    );
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "Save environment secret",
    );
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent(
      "Env writes still work",
    );
    await userEvent.type(
      screen.getByPlaceholderText("GITHUB_TOKEN"),
      "GITLAB_TOKEN",
    );
    await userEvent.type(
      screen.getByPlaceholderText("Stored server-side"),
      "gl_secret",
    );
    await userEvent.click(screen.getByRole("button", { name: "Save env" }));
    expect(onSetEnv).toHaveBeenCalledWith("GITLAB_TOKEN", "gl_secret");
    expect(screen.queryByText("gl_secret")).toBeNull();
  });

  it("hides raw SSH storage paths from settings API permission errors", async () => {
    const error =
      "affentserve_error: read account settings: lstat /workspace/.home/.ssh/id_ed25519: permission denied";
    render(
      <AccountSettingsPanel error={error} onSetEnv={vi.fn()} defaultOpen />,
    );

    const panel = screen.getByTestId("account-settings-panel");
    expect(panel).toHaveTextContent(
      "Cannot read account SSH key: permission denied.",
    );
    expect(panel).not.toHaveTextContent("/workspace/.home/.ssh/id_ed25519");
    expect(screen.getByRole("alert")).toHaveTextContent(
      "Cannot read account SSH key: permission denied.",
    );
    expect(screen.getByTestId("account-settings-fallback")).toHaveTextContent(
      "Account settings unavailable",
    );
    expect(screen.getByTestId("account-settings-fallback")).toHaveTextContent(
      "Cannot read saved settings",
    );
  });
});
