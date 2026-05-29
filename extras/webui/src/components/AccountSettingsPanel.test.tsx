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
      output: request.kind === "host" ? "successfully authenticated" : "repo reachable",
      duration_ms: 42,
      checked_at: "2026-05-29T00:00:00Z",
    }));
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(
      <AccountSettingsPanel
        settings={{
          env: [{ name: "GITHUB_TOKEN", configured: true, updated_at: "2026-05-27T10:00:00Z" }],
          ssh: { exists: true, public_key: "ssh-ed25519 AAAA affent", public_key_path: "/workspace/.home/.ssh/id_ed25519.pub" },
        }}
        onVerifyGitAccess={onVerifyGitAccess}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("account-settings-panel");
    expect(panel).toHaveTextContent("Config");
    expect(panel).toHaveTextContent("1 env · SSH key");
    expect(panel).toHaveTextContent("SSH ready · 1 env");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("Private Git ready");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("Public key");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("~/.ssh/id_ed25519.pub");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("1 env");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("Run tasks normally");
    expect(screen.getByTestId("account-config-dashboard")).toHaveTextContent("Env review");
    expect(screen.getByTestId("account-config-dashboard")).toHaveTextContent("Clean");
    expect(within(screen.getByTestId("account-config-focus")).getByRole("button", { name: "Copy public key" })).toBeInTheDocument();
    expect(within(screen.getByTestId("account-config-focus")).getByRole("button", { name: "Copy key path" })).toBeInTheDocument();
    expect(screen.queryByText("Private repo access")).toBeNull();
    expect(screen.getByTestId("account-public-key")).toHaveTextContent("ssh-ed25519 AAAA affent");
    expect(screen.getByTestId("account-config-verify")).toHaveTextContent("SSH host reachability");
    expect(screen.getByTestId("account-config-verify")).toHaveTextContent("Repository permission");
    expect(screen.getByTestId("account-config-verify")).toHaveTextContent("does not create a chat turn");
    expect(screen.getByRole("button", { name: "Check host" })).toBeDisabled();
    expect(screen.getByRole("button", { name: "Check repository" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Generate SSH key" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Use config as draft" })).toBeNull();
    expect(screen.getByTestId("account-env-list")).toHaveTextContent("GITHUB_TOKEN");
    expect(screen.getByTestId("account-env-list")).toHaveTextContent("configured");

    await user.click(within(screen.getByTestId("account-config-focus")).getByRole("button", { name: "Copy public key" }));
    expect(writeText).toHaveBeenCalledWith("ssh-ed25519 AAAA affent");
    await user.click(within(screen.getByTestId("account-config-focus")).getByRole("button", { name: "Copy key path" }));
    expect(writeText).toHaveBeenCalledWith("/workspace/.home/.ssh/id_ed25519.pub");
    await user.click(screen.getByRole("button", { name: "Copy full key" }));
    expect(writeText).toHaveBeenCalledWith("ssh-ed25519 AAAA affent");

    await user.click(within(screen.getByRole("group", { name: "Git host presets" })).getByRole("button", { name: "GitLab" }));
    expect(screen.getByPlaceholderText("github.com or gitlab.com")).toHaveValue("gitlab.com");
    await user.clear(screen.getByPlaceholderText("github.com or gitlab.com"));
    await user.type(screen.getByPlaceholderText("github.com or gitlab.com"), "git@gitlab.com:team/repo.git");
    await user.click(screen.getByRole("button", { name: "Check host" }));
    expect(onVerifyGitAccess).toHaveBeenCalledWith({ kind: "host", target: "gitlab.com" });
    expect(screen.getByText("Reachable")).toBeInTheDocument();
    expect(screen.getByText("successfully authenticated")).toBeInTheDocument();

    await user.type(screen.getByPlaceholderText("git@github.com:owner/repo.git"), "git@github.com:team/private-repo.git");
    await user.click(screen.getByRole("button", { name: "Check repository" }));
    expect(onVerifyGitAccess).toHaveBeenCalledWith({ kind: "remote", target: "git@github.com:team/private-repo.git" });
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

    await user.type(screen.getByPlaceholderText("GITHUB_TOKEN"), "GITLAB_TOKEN");
    await user.type(screen.getByPlaceholderText("Stored server-side"), "gl_secret");
    await user.click(screen.getByRole("button", { name: "Save env" }));

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("No SSH key configured");
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("2 envs");
    expect(screen.getByTestId("account-config-dashboard")).toHaveTextContent("Env review");
    expect(screen.getByTestId("account-config-dashboard")).toHaveTextContent("Empty 1");
    expect(screen.getByTestId("account-env-review")).toHaveTextContent("1 finding");
    expect(screen.getByTestId("account-env-review")).toHaveTextContent("EMPTY_TOKEN");
    expect(onSetEnv).toHaveBeenCalledWith("GITLAB_TOKEN", "gl_secret");
    expect(screen.getByRole("status")).toHaveTextContent("GITLAB_TOKEN saved.");
    expect(screen.queryByText("gl_secret")).toBeNull();

    const envList = screen.getByTestId("account-env-list");
    await user.click(within(screen.getByTestId("account-env-review")).getByRole("button", { name: "Show variables" }));
    expect(screen.getByTestId("account-env-search-count")).toHaveTextContent("1 variable");
    expect(envList).toHaveTextContent("EMPTY_TOKEN");
    expect(envList).not.toHaveTextContent("GITHUB_TOKEN");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.type(screen.getByPlaceholderText("name, configured, or empty"), "github");
    expect(screen.getByTestId("account-env-search-count")).toHaveTextContent('1 variable matching "github"');
    expect(envList).toHaveTextContent("GITHUB_TOKEN");
    expect(envList).not.toHaveTextContent("EMPTY_TOKEN");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(envList).toHaveTextContent("EMPTY_TOKEN");

    await user.click(within(envList).getAllByRole("button", { name: "Delete" })[0]);
    expect(onDeleteEnv).not.toHaveBeenCalled();
    const firstConfirm = within(envList).getByRole("group", { name: "Confirm delete GITHUB_TOKEN" });
    expect(firstConfirm).toHaveTextContent("Delete GITHUB_TOKEN?");
    await user.click(within(firstConfirm).getByRole("button", { name: "Cancel" }));
    expect(within(envList).queryByRole("group", { name: "Confirm delete GITHUB_TOKEN" })).toBeNull();

    await user.click(within(envList).getAllByRole("button", { name: "Delete" })[0]);
    const confirm = within(envList).getByRole("group", { name: "Confirm delete GITHUB_TOKEN" });
    await user.click(within(confirm).getByRole("button", { name: "Confirm" }));
    expect(onDeleteEnv).toHaveBeenCalledWith("GITHUB_TOKEN");
    expect(screen.getByRole("status")).toHaveTextContent("GITHUB_TOKEN deleted.");
  });

  it("offers SSH key generation when no key exists", async () => {
    const user = userEvent.setup();
    const onEnsureSSHKey = vi.fn().mockResolvedValue(undefined);
    render(<AccountSettingsPanel settings={{ env: [], ssh: { exists: false } }} onEnsureSSHKey={onEnsureSSHKey} defaultOpen />);

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("No config");
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("No env vars or SSH key configured");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("SSH key missing");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("Generate an SSH key only when this runtime needs private repo access");
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("Generate an SSH key only when this runtime needs private repo access.");
    await user.click(screen.getByRole("button", { name: "Generate SSH key" }));

    expect(onEnsureSSHKey).toHaveBeenCalled();
    expect(screen.getByRole("status")).toHaveTextContent("SSH key ready.");
  });

  it("keeps config forms usable and shows server failures inline", async () => {
    const user = userEvent.setup();
    const onSetEnv = vi.fn().mockRejectedValue(new Error("settings storage is read-only"));
    const onEnsureSSHKey = vi.fn().mockRejectedValue(new Error("ssh key path is not writable"));
    render(
      <AccountSettingsPanel
        settings={{ env: [], ssh: { exists: false } }}
        onSetEnv={onSetEnv}
        onEnsureSSHKey={onEnsureSSHKey}
        defaultOpen
      />,
    );

    await user.type(screen.getByPlaceholderText("GITHUB_TOKEN"), "GITHUB_TOKEN");
    await user.type(screen.getByPlaceholderText("Stored server-side"), "secret-value");
    await user.click(screen.getByRole("button", { name: "Save env" }));

    expect(screen.getByRole("status")).toHaveTextContent("settings storage is read-only");
    expect(screen.getByPlaceholderText("GITHUB_TOKEN")).toHaveValue("GITHUB_TOKEN");
    expect(screen.getByPlaceholderText("Stored server-side")).toHaveValue("secret-value");

    await user.click(screen.getByRole("button", { name: "Generate SSH key" }));
    expect(screen.getByRole("status")).toHaveTextContent("ssh key path is not writable");
  });

  it("does not offer generation when a private key exists but public key is unavailable", () => {
    render(
      <AccountSettingsPanel
        settings={{
          env: [],
          ssh: {
            exists: true,
            public_key_error: "private SSH key already exists but public key is missing and could not be derived",
          },
        }}
        defaultOpen
      />,
    );

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("SSH key issue");
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("SSH key found; public key unavailable");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("SSH key needs review");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("Blocked");
    expect(screen.getByTestId("account-config-focus")).toHaveTextContent("Fix or derive the public key in ~/.ssh");
    expect(screen.getByRole("alert")).toHaveTextContent("could not be derived");
    expect(screen.queryByRole("button", { name: "Generate SSH key" })).toBeNull();
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const onSetEnv = vi.fn().mockResolvedValue(undefined);
    const diagnostic = "API route /v1/settings returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<AccountSettingsPanel error={diagnostic} onSetEnv={onSetEnv} />);

    const summary = within(screen.getByTestId("account-settings-panel")).getByText("Unavailable").closest("summary");
    expect(summary).toHaveTextContent("Config API failed: API route /v1/settings returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
    expect(screen.getByTestId("account-settings-fallback")).toHaveTextContent("Config actions remain available");
    await userEvent.type(screen.getByPlaceholderText("GITHUB_TOKEN"), "GITLAB_TOKEN");
    await userEvent.type(screen.getByPlaceholderText("Stored server-side"), "gl_secret");
    await userEvent.click(screen.getByRole("button", { name: "Save env" }));
    expect(onSetEnv).toHaveBeenCalledWith("GITLAB_TOKEN", "gl_secret");
    expect(screen.queryByText("gl_secret")).toBeNull();
  });
});
