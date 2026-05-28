import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AccountSettingsPanel } from "./AccountSettingsPanel";

describe("AccountSettingsPanel", () => {
  it("shows an existing SSH public key and safe config evidence actions", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(
      <AccountSettingsPanel
        settings={{
          env: [{ name: "GITHUB_TOKEN", configured: true, updated_at: "2026-05-27T10:00:00Z" }],
          ssh: { exists: true, public_key: "ssh-ed25519 AAAA affent", public_key_path: "/state/.affentserve/ssh/id_ed25519.pub" },
        }}
        onUseAsDraft={onUseAsDraft}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("account-settings-panel");
    expect(panel).toHaveTextContent("Config");
    expect(panel).toHaveTextContent("1 env · SSH key");
    expect(panel).toHaveTextContent("SSH public key ready");
    expect(panel).toHaveTextContent("Existing keys are shown, never overwritten");
    expect(screen.getByTestId("account-public-key")).toHaveTextContent("ssh-ed25519 AAAA affent");
    expect(screen.queryByRole("button", { name: "Generate SSH key" })).toBeNull();
    expect(screen.getByTestId("account-env-list")).toHaveTextContent("GITHUB_TOKEN");
    expect(screen.getByTestId("account-env-list")).toHaveTextContent("configured");

    await user.click(screen.getByRole("button", { name: "Copy config evidence" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Runtime config evidence"));
    expect(writeText).toHaveBeenCalledWith(expect.not.stringContaining("ssh-ed25519 AAAA affent"));

    await user.click(screen.getByRole("button", { name: "Use config as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Do not ask for or expose secret values"), "config");
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
    expect(onSetEnv).toHaveBeenCalledWith("GITLAB_TOKEN", "gl_secret");
    expect(screen.queryByText("gl_secret")).toBeNull();

    const envList = screen.getByTestId("account-env-list");
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
  });

  it("offers SSH key generation when no key exists", async () => {
    const user = userEvent.setup();
    const onEnsureSSHKey = vi.fn().mockResolvedValue(undefined);
    render(<AccountSettingsPanel settings={{ env: [], ssh: { exists: false } }} onEnsureSSHKey={onEnsureSSHKey} defaultOpen />);

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("No config");
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("No env vars or SSH key configured");
    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("Generate an SSH key only when this session needs private Git access");
    await user.click(screen.getByRole("button", { name: "Generate SSH key" }));

    expect(onEnsureSSHKey).toHaveBeenCalled();
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
    expect(screen.getByRole("alert")).toHaveTextContent("could not be derived");
    expect(screen.queryByRole("button", { name: "Generate SSH key" })).toBeNull();
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const diagnostic = "API route /v1/settings returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<AccountSettingsPanel error={diagnostic} />);

    const summary = within(screen.getByTestId("account-settings-panel")).getByText("Unavailable").closest("summary");
    expect(summary).toHaveTextContent("Config API failed: API route /v1/settings returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
  });
});
