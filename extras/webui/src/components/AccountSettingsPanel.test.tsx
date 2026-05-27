import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { AccountSettingsPanel } from "./AccountSettingsPanel";

describe("AccountSettingsPanel", () => {
  it("shows an existing SSH public key and never asks to overwrite it", () => {
    render(
      <AccountSettingsPanel
        settings={{
          env: [{ name: "GITHUB_TOKEN", configured: true, updated_at: "2026-05-27T10:00:00Z" }],
          ssh: { exists: true, public_key: "ssh-ed25519 AAAA affent", public_key_path: "/state/.affentserve/ssh/id_ed25519.pub" },
        }}
        defaultOpen
      />,
    );

    const panel = screen.getByTestId("account-settings-panel");
    expect(panel).toHaveTextContent("SSH public key ready");
    expect(panel).toHaveTextContent("Existing keys are shown, never overwritten");
    expect(screen.getByTestId("account-public-key")).toHaveTextContent("ssh-ed25519 AAAA affent");
    expect(screen.queryByRole("button", { name: "Generate SSH key" })).toBeNull();
    expect(screen.getByTestId("account-env-list")).toHaveTextContent("GITHUB_TOKEN");
    expect(screen.getByTestId("account-env-list")).toHaveTextContent("configured");
  });

  it("saves and deletes environment variables without displaying the value", async () => {
    const user = userEvent.setup();
    const onSetEnv = vi.fn().mockResolvedValue(undefined);
    const onDeleteEnv = vi.fn().mockResolvedValue(undefined);
    render(
      <AccountSettingsPanel
        settings={{
          env: [{ name: "GITHUB_TOKEN", configured: true }],
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

    expect(onSetEnv).toHaveBeenCalledWith("GITLAB_TOKEN", "gl_secret");
    expect(screen.queryByText("gl_secret")).toBeNull();
    await user.click(within(screen.getByTestId("account-env-list")).getByRole("button", { name: "Delete" }));
    expect(onDeleteEnv).toHaveBeenCalledWith("GITHUB_TOKEN");
  });

  it("offers SSH key generation when no key exists", async () => {
    const user = userEvent.setup();
    const onEnsureSSHKey = vi.fn().mockResolvedValue(undefined);
    render(<AccountSettingsPanel settings={{ env: [], ssh: { exists: false } }} onEnsureSSHKey={onEnsureSSHKey} defaultOpen />);

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

    expect(screen.getByTestId("account-settings-panel")).toHaveTextContent("SSH key found; public key unavailable");
    expect(screen.getByRole("alert")).toHaveTextContent("could not be derived");
    expect(screen.queryByRole("button", { name: "Generate SSH key" })).toBeNull();
  });

  it("keeps long API diagnostics out of the collapsed summary", async () => {
    const diagnostic = "API route /v1/settings returned the WebUI app shell. The affentserve build may not expose this route.";
    render(<AccountSettingsPanel error={diagnostic} />);

    const summary = within(screen.getByTestId("account-settings-panel")).getByText("Unavailable").closest("summary");
    expect(summary).toHaveTextContent("Open for route, proxy, or build details.");
    expect(summary).not.toHaveTextContent("returned the WebUI app shell");

    await userEvent.click(screen.getByText("Unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
  });
});
