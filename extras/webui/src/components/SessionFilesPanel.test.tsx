import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { SessionFilesView } from "../view/sessionFiles";
import { SessionFilesPanel } from "./SessionFilesPanel";

describe("SessionFilesPanel", () => {
  it("renders file evidence as an explorer plus editor", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<SessionFilesPanel defaultOpen files={files} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-files-panel");
    const explorer = screen.getByLabelText("File explorer");
    const tree = screen.getByTestId("session-files-list");
    expect(panel).toHaveAttribute("open");
    expect(screen.getByTestId("session-files-ide")).toBeInTheDocument();
    expect(screen.queryByLabelText("File work summary")).toBeNull();
    expect(screen.queryByText("Review queue")).toBeNull();
    expect(explorer).toHaveTextContent("Explorer");
    expect(tree).toHaveTextContent("src");
    expect(tree).toHaveTextContent("payments.ts");
    expect(tree).toHaveTextContent("Read + Changed");
    expect(screen.queryByTestId("session-files-selected")).toBeNull();
    expect(within(tree).queryByRole("button", { name: "Copy path" })).not.toBeInTheDocument();
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("snapshot before latest change");
    expect(screen.getByTestId("session-file-preview-content")).toHaveTextContent("export function checkout");
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("Review file");
    expect(screen.getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "Wrap" }));
    expect(screen.getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "false");

    await user.type(screen.getByLabelText("Search file snapshot"), "route");
    expect(screen.getByTestId("session-file-preview-content")).toHaveTextContent("route");
    expect(screen.getByRole("button", { name: /2 return route/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /1 export function checkout/ }));
    expect(screen.getByTestId("session-file-range-actions")).toHaveTextContent("Lines 1-1");
    await user.click(screen.getByRole("button", { name: "Copy range" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("File range for src/payments.ts"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Lines: 1-1"));
    await user.click(screen.getByRole("button", { name: /3 }/ }));
    expect(screen.getByTestId("session-file-range-actions")).toHaveTextContent("Lines 1-3");
    await user.click(screen.getByRole("button", { name: "Ask about range" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("File: src/payments.ts"), "file_range");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Lines: 1-3"), "file_range");
    await user.click(screen.getByRole("button", { name: "Edit range" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Edit this selected file range"), "file_range");
    await user.clear(screen.getByLabelText("Go to line"));
    await user.type(screen.getByLabelText("Go to line"), "2");
    await user.click(screen.getByRole("button", { name: "Go" }));
    expect(screen.getByTestId("session-file-range-actions")).toHaveTextContent("Lines 2-2");

    await user.click(within(tree).getByRole("button", { name: /src Listed/ }));
    expect(screen.getByTestId("session-file-inspector")).toHaveTextContent("src");
    expect(screen.getByTestId("session-file-inspector")).toHaveTextContent("Output captured");
    expect(screen.getByTestId("session-file-inspector")).not.toHaveTextContent("list.txt");
    await user.click(within(screen.getByTestId("session-file-inspector")).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith("src");
    await user.click(within(screen.getByTestId("session-file-inspector")).getByRole("button", { name: "Use listing" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Use this listed directory in the next step"), "file_evidence");
    expect(screen.getByTestId("session-file-inspector")).toHaveTextContent("Turn");
    await user.click(within(tree).getByRole("button", { name: /payments.ts Read/ }));
    await waitFor(() => expect(screen.getByTestId("session-file-preview")).toHaveTextContent("src/payments.ts"));
    await user.click(screen.getByRole("button", { name: "Copy snapshot" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("File snapshot for src/payments.ts"));
    await user.click(within(screen.getByTestId("session-file-preview")).getByRole("button", { name: "Evidence" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/read.txt");
    await user.click(within(screen.getByTestId("session-file-preview")).getByRole("button", { name: "Review file" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review this changed file in the next step"), "file_evidence");

    await user.clear(screen.getByLabelText("Search file snapshot"));
    await user.type(screen.getByLabelText("Search files"), "listed");
    expect(tree).toHaveTextContent("src");
    expect(tree).not.toHaveTextContent("payments.ts");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(tree).toHaveTextContent("payments.ts");

    await user.click(within(screen.getByRole("group", { name: "File filters" })).getByRole("button", { name: /Changed/ }));
    expect(tree).toHaveTextContent("payments.ts");
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("payments.ts");

    await user.type(screen.getByLabelText("Search files"), "missing.ts");
    expect(screen.queryByTestId("session-files-list")).toBeNull();
    expect(panel).toHaveTextContent('No changed result matching "missing.ts".');
  });

  it("keeps the panel folded by default", () => {
    render(<SessionFilesPanel files={files} />);

    expect(screen.getByTestId("session-files-panel")).not.toHaveAttribute("open");
  });

  it("surfaces the primary file attention without rebuilding a review queue", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const onUseAsDraft = vi.fn();
    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    const attention = screen.getByTestId("session-files-attention");
    expect(attention).toHaveTextContent("Verify current file");
    expect(attention).toHaveTextContent("src/payments.ts");
    expect(attention).toHaveTextContent("snapshot");
    expect(screen.queryByText("Review queue")).toBeNull();

    await user.click(within(attention).getByRole("button", { name: "Open current" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src/payments.ts");
    await user.click(within(attention).getByRole("button", { name: "Ask Affent" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review this changed file in the next step"), "file_evidence");
  });

  it("keeps idle workspace access inside the explorer", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{ state: "idle", workspacePath: "/work/affent" }}
        onOpenWorkspacePath={onOpenWorkspacePath}
      />,
    );

    await waitFor(() => expect(onOpenWorkspacePath).toHaveBeenCalledWith("."));
    expect(onOpenWorkspacePath).toHaveBeenCalledTimes(1);
    expect(screen.queryByTestId("session-workspace-browser")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Root" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledTimes(2);
  });

  it("keeps file evidence selectable when no workspace binding can open paths", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePanel = vi.fn();
    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        onOpenWorkspacePanel={onOpenWorkspacePanel}
      />,
    );

    expect(screen.queryByLabelText("Workspace path")).toBeNull();
    expect(screen.getByLabelText("File explorer")).toHaveTextContent("Agent file evidence");
    expect(screen.getByText("Workspace unavailable")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Open Workspace" }));
    expect(onOpenWorkspacePanel).toHaveBeenCalledTimes(1);
    await user.click(within(screen.getByTestId("session-files-list")).getByRole("button", { name: /src Listed/ }));
    expect(onOpenWorkspacePanel).toHaveBeenCalledTimes(1);
    expect(screen.getByTestId("session-file-inspector")).toHaveTextContent("src");
    await user.click(within(screen.getByTestId("session-file-inspector")).getByRole("button", { name: "Open Workspace" }));
    expect(onOpenWorkspacePanel).toHaveBeenCalledTimes(2);
  });

  it("summarizes stale workspace paths without raw filesystem noise", () => {
    render(
      <SessionFilesPanel
        defaultOpen
        files={{ summary: "No files", detail: "No file evidence", items: [] }}
        workspaceBrowser={{
          state: "error",
          path: ".",
          workspacePath: "/workspace/sessions/missing",
          error: "workspace_unavailable: workspace not available: lstat /workspace/sessions/missing: no such file or directory",
        }}
        onOpenWorkspacePath={vi.fn()}
      />,
    );

    expect(screen.getByLabelText("File explorer")).toHaveTextContent("Workspace missing");
    expect(screen.getByLabelText("File explorer")).toHaveTextContent("The saved workspace path no longer exists in this container.");
    expect(screen.getByLabelText("File explorer")).not.toHaveTextContent("lstat /workspace");
  });

  it("browses workspace directories and file snapshots", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          file: {
            path: ".",
            kind: "directory",
            title: "Workspace root",
            detail: "1 directory · 1 file",
            entries: [
              { name: "src", path: "src", kind: "directory" },
              { name: "README.md", path: "README.md", kind: "file", size: "2 KiB" },
            ],
            lines: [],
            hasMore: false,
          },
        }}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByLabelText("File explorer")).toHaveTextContent("Workspace root");
    expect(screen.getByTestId("session-files-editor-chrome")).toHaveTextContent("Workspace directory");
    expect(screen.getByTestId("session-workspace-directory-preview")).toHaveTextContent("Workspace root");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("README.md");
    expect(screen.getByLabelText("Workspace path breadcrumbs")).toHaveTextContent(".");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("src");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("README.md");
    await user.type(screen.getByLabelText("Search files"), "readme");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-browser-list")).not.toHaveTextContent("src");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-directory-table")).not.toHaveTextContent("src");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    await user.click(within(screen.getByTestId("session-workspace-browser-list")).getByRole("button", { name: /src/ }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    await user.click(within(screen.getByTestId("session-workspace-directory-table")).getByRole("button", { name: /README.md/ }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("README.md");
    await user.clear(screen.getByLabelText("Workspace path"));
    await user.type(screen.getByLabelText("Workspace path"), "src/main.go");
    await user.click(screen.getByRole("button", { name: "Open" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src/main.go");
    await user.click(within(screen.getByTestId("session-workspace-directory-preview")).getByRole("button", { name: "Reference listing" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("- file README.md (2 KiB)"), "file_snapshot");
  });

  it("shows loaded workspace file content", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });

    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          file: {
            path: "src/main.go",
            kind: "file",
            title: "main.go",
            detail: "26 B · loaded",
            entries: [],
            text: "package main\nfunc main() {}\n",
            lines: ["package main", "func main() {}", ""],
            hasMore: false,
            size: "26 B",
          },
        }}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    const preview = screen.getByTestId("session-workspace-file-preview");
    expect(preview).toHaveTextContent("src/main.go");
    expect(preview).toHaveTextContent("package main");
    expect(screen.getByLabelText("Workspace path breadcrumbs")).toHaveTextContent(".srcmain.go");
    await user.type(screen.getByLabelText("Search workspace file"), "func");
    expect(within(preview).getByRole("button", { name: /2 func main/ })).toBeInTheDocument();
    await user.click(within(preview).getByRole("button", { name: /1 package main/ }));
    expect(screen.getByTestId("session-workspace-file-range-actions")).toHaveTextContent("Lines 1-1");
    await user.click(within(preview).getByRole("button", { name: /2 func main/ }));
    expect(screen.getByTestId("session-workspace-file-range-actions")).toHaveTextContent("Lines 1-2");
    await user.click(within(screen.getByTestId("session-workspace-file-range-actions")).getByRole("button", { name: "Copy range" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Workspace file range for src/main.go"));
    await user.click(within(screen.getByTestId("session-workspace-file-range-actions")).getByRole("button", { name: "Ask about range" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review this selected workspace file range"), "file_range");
    await user.click(within(screen.getByTestId("session-workspace-file-range-actions")).getByRole("button", { name: "Edit range" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Edit this selected workspace file range"), "file_range");
    await user.clear(screen.getByLabelText("Go to workspace line"));
    await user.type(screen.getByLabelText("Go to workspace line"), "2");
    await user.click(within(preview).getByRole("button", { name: "Go" }));
    expect(screen.getByTestId("session-workspace-file-range-actions")).toHaveTextContent("Lines 2-2");
    expect(within(preview).getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "true");
    await user.click(within(preview).getByRole("button", { name: "Wrap" }));
    expect(within(preview).getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "false");
    await user.click(within(screen.getByLabelText("Workspace path breadcrumbs")).getByRole("button", { name: "src" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    await user.click(within(preview).getByRole("button", { name: "Up" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    await user.click(within(preview).getByRole("button", { name: "Copy file" }));
    expect(writeText).toHaveBeenCalledWith("package main\nfunc main() {}\n");
    await user.click(within(preview).getByRole("button", { name: "Ask about file" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Path: src/main.go"), "file_snapshot");
  });
});

const files: SessionFilesView = {
  summary: "2 file references",
  detail: "1 read · 1 listed · 1 changed",
  items: [
    {
      path: "src/payments.ts",
      actions: ["read", "changed"],
      status: "available",
      turnNumber: 2,
      actionCount: 2,
      detail: "Updated payment route",
      next: "rerun checkout tests",
      artifactPath: ".affent/artifacts/tool-results/read.txt",
      contentPreview: "export function checkout() {\n  return route('/checkout');\n}\n",
      contentSource: "read_file",
      contentTruncated: false,
      contentStale: true,
      contentBytes: 58,
    },
    {
      path: "src",
      actions: ["listed"],
      status: "available",
      turnNumber: 1,
      actionCount: 1,
      artifactPath: ".affent/artifacts/tool-results/list.txt",
    },
  ],
};
