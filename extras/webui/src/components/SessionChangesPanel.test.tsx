import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionChangesPanel } from "./SessionChangesPanel";
import type { SessionChangesView } from "../view/sessionChanges";

describe("SessionChangesPanel", () => {
  it("renders changed files as evidence and creates an adjustment draft", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<SessionChangesPanel defaultOpen changes={changes} onOpenWorkspacePath={onOpenWorkspacePath} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-changes-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("2 changed files");
    expect(screen.getByLabelText("Changes summary")).toHaveTextContent("Diff");
    expect(screen.getByTestId("session-changes-review")).toHaveTextContent("Review gap");
    expect(screen.getByTestId("session-changes-review")).toHaveTextContent("1 file needs current-file review");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("Diff");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("1/2");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("1 stale");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("Scale");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("+2 -1");
    expect(screen.getByTestId("session-changes-focus")).toHaveTextContent("Verify current file");
    expect(screen.getByTestId("session-changes-focus")).toHaveTextContent("Diff may predate latest change");
    expect(screen.getByLabelText("Search changes")).toBeInTheDocument();
    expect(within(screen.getByLabelText("Change filters")).getByRole("button", { name: /Verify/ })).toBeInTheDocument();
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Edit · changed · +2 -1 · turn 2");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Updated payment route");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Evidence: edit.txt");
    expect(screen.getByTestId("session-change-diff")).toHaveAccessibleName("Diff preview for src/payments.ts");
    expect(screen.getByTestId("session-change-diff")).toHaveTextContent("@@ -1,3 +1,4 @@");
    expect(screen.getByTestId("session-change-diff")).toHaveTextContent(/\+\s+return enabled;/);

    await user.click(within(screen.getByTestId("session-changes-list")).getAllByRole("button", { name: "Open current" })[0]);
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src/payments.ts");

    await user.click(within(screen.getByTestId("session-changes-list")).getAllByRole("button", { name: "Copy path" })[0]);
    expect(writeText).toHaveBeenCalledWith("src/payments.ts");

    await user.click(within(screen.getByTestId("session-changes-list")).getByRole("button", { name: "Copy diff" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Diff for src/payments.ts"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("+  return enabled;"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Diff preview may predate the latest change"));

    await user.click(within(screen.getByTestId("session-changes-list")).getByRole("button", { name: "Open evidence" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/edit.txt");

    await user.click(within(screen.getByTestId("session-changes-list")).getAllByRole("button", { name: "Verify file" })[0]);

    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Path: src/payments.ts"), "changed_file");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("+  return enabled;"), "changed_file");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Verify the current file before using this diff"), "changed_file");

    await user.type(screen.getByLabelText("Search changes"), "spec");
    expect(screen.getByTestId("session-changes-list")).not.toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("tests/payments.spec.ts");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("tests/payments.spec.ts");

    await user.type(screen.getByLabelText("Search changes"), "return enabled");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).not.toHaveTextContent("tests/payments.spec.ts");
    await user.click(screen.getByRole("button", { name: "Clear" }));

    await user.click(within(screen.getByLabelText("Change filters")).getByRole("button", { name: /Diff/ }));
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).not.toHaveTextContent("tests/payments.spec.ts");
    await user.click(within(screen.getByLabelText("Change filters")).getByRole("button", { name: /All/ }));

    await user.click(within(screen.getByLabelText("Change filters")).getByRole("button", { name: /Verify/ }));
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).not.toHaveTextContent("tests/payments.spec.ts");
    await user.click(within(screen.getByLabelText("Change filters")).getByRole("button", { name: /All/ }));

    await user.type(screen.getByLabelText("Search changes"), "missing.ts");
    expect(screen.queryByTestId("session-changes-list")).toBeNull();
    expect(panel).toHaveTextContent('No changed files matching "missing.ts".');
  });

  it("makes no-diff changes explicit and offers file review", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const onOpenFilesPanel = vi.fn();
    const onUseAsDraft = vi.fn();
    render(
      <SessionChangesPanel
        defaultOpen
        onOpenWorkspacePath={onOpenWorkspacePath}
        onOpenFilesPanel={onOpenFilesPanel}
        changes={{
          summary: "1 changed file",
          detail: "1 changed",
          files: [{
            path: "game2048.py",
            operation: "edit",
            status: "changed",
            turnNumber: 4,
            actionCount: 5,
            detail: "replaced 1 occurrence(s) in game2048.py",
          }],
        }}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    const panel = screen.getByTestId("session-changes-panel");
    expect(screen.getByTestId("session-changes-review")).toHaveTextContent("Review gap");
    expect(screen.getByTestId("session-changes-review")).toHaveTextContent("No diff preview for game2048.py");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("Evidence");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("0/1");
    expect(screen.getByLabelText("Change review facts")).toHaveTextContent("none captured");
    expect(panel).toHaveTextContent("No diff preview captured");
    expect(within(screen.getByLabelText("Change filters")).queryByRole("button", { name: /Diff/ })).toBeNull();
    expect(within(screen.getByLabelText("Change filters")).queryByRole("button", { name: /Issues/ })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Open current" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("game2048.py");
    await user.click(screen.getByRole("button", { name: "Open Files" }));
    expect(onOpenFilesPanel).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Review file" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("No diff preview was captured"), "changed_file");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Path: game2048.py"), "changed_file");
  });

  it("routes no-diff review gaps to workspace setup when the current file cannot be opened", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePanel = vi.fn();
    const onOpenFilesPanel = vi.fn();
    render(
      <SessionChangesPanel
        defaultOpen
        onOpenWorkspacePanel={onOpenWorkspacePanel}
        onOpenFilesPanel={onOpenFilesPanel}
        changes={{
          summary: "1 changed file",
          detail: "1 changed",
          files: [{
            path: "game2048.py",
            operation: "edit",
            status: "changed",
            turnNumber: 4,
            actionCount: 5,
            detail: "replaced 1 occurrence(s) in game2048.py",
          }],
        }}
      />,
    );

    expect(screen.queryByRole("button", { name: "Open current" })).toBeNull();
    await user.click(screen.getByRole("button", { name: "Open Files" }));
    expect(onOpenFilesPanel).toHaveBeenCalledTimes(1);
    await user.click(screen.getByRole("button", { name: "Open Workspace" }));
    expect(onOpenWorkspacePanel).toHaveBeenCalledTimes(1);
  });

  it("keeps the panel folded by default", () => {
    render(<SessionChangesPanel changes={changes} />);

    expect(screen.getByTestId("session-changes-panel")).not.toHaveAttribute("open");
  });
});

const changes: SessionChangesView = {
  summary: "2 changed files",
  detail: "2 changed",
  files: [
    {
      path: "src/payments.ts",
      operation: "edit",
      status: "changed",
      turnNumber: 2,
      actionCount: 1,
      additions: 2,
      deletions: 1,
      detail: "Updated payment route",
      artifactPath: ".affent/artifacts/tool-results/edit.txt",
      diffStale: true,
      diffPreview: [
        { kind: "meta", text: "diff --git a/src/payments.ts b/src/payments.ts" },
        { kind: "meta", text: "--- a/src/payments.ts" },
        { kind: "meta", text: "+++ b/src/payments.ts" },
        { kind: "hunk", text: "@@ -1,3 +1,4 @@" },
        { kind: "context", text: " export function pay() {" },
        { kind: "remove", text: "-  return false;" },
        { kind: "add", text: "+  const enabled = true;" },
        { kind: "add", text: "+  return enabled;" },
        { kind: "context", text: " }" },
      ],
    },
    {
      path: "tests/payments.spec.ts",
      operation: "write",
      status: "changed",
      turnNumber: 2,
      actionCount: 1,
    },
  ],
};
