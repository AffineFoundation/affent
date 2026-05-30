import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
    expect(panel).toHaveAttribute("data-surface", "true");
    expect(screen.getByTestId("session-files-ide")).toBeInTheDocument();
    expect(screen.queryByLabelText("File work summary")).toBeNull();
    expect(screen.queryByText("Review queue")).toBeNull();
    expect(explorer).toHaveTextContent("Workspace unavailable");
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
    expect(within(screen.getByTestId("session-file-inspector")).queryByRole("button", { name: "Use listing" })).toBeNull();
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
    await user.type(screen.getByLabelText("Filter files"), "listed");
    expect(tree).toHaveTextContent("src");
    expect(tree).not.toHaveTextContent("payments.ts");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(tree).toHaveTextContent("payments.ts");

    await user.click(within(screen.getByRole("group", { name: "File filters" })).getByRole("button", { name: /Changed/ }));
    expect(tree).toHaveTextContent("payments.ts");
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("payments.ts");

    await user.type(screen.getByLabelText("Filter files"), "missing.ts");
    expect(screen.queryByTestId("session-files-list")).toBeNull();
    expect(panel).toHaveTextContent('No changed result matching "missing.ts".');
  });

  it("renders as a non-collapsible file surface by default", () => {
    render(<SessionFilesPanel files={files} />);

    expect(screen.getByTestId("session-files-panel")).toHaveAttribute("data-surface", "true");
    expect(screen.queryByText("Workspace files")).toBeNull();
  });

  it("keeps agent evidence folded when workspace file access exists", () => {
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

    expect(screen.queryByText("Agent evidence")).toBeNull();
    expect(screen.queryByTestId("session-files-attention")).toBeNull();
    expect(screen.queryByText("Review queue")).toBeNull();
    expect(screen.queryByRole("button", { name: "Open current" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Ask Affent" })).toBeNull();
    expect(onOpenWorkspacePath).not.toHaveBeenCalled();
    expect(onUseAsDraft).not.toHaveBeenCalled();
  });

  it("keeps idle workspace access inside the explorer", async () => {
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
    expect(screen.getByLabelText("File explorer")).toHaveTextContent("Filter files");
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
    expect(screen.getByLabelText("File explorer")).toHaveTextContent("Workspace unavailable");
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

    expect(screen.getByLabelText("File explorer")).toHaveTextContent("Workspace path unavailable");
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
              { name: ".affent", path: ".affent", kind: "directory" },
              { name: "src", path: "src", kind: "directory" },
              { name: "session-state", path: "session-state", kind: "directory" },
              { name: "README.md", path: "README.md", kind: "file", size: "2 KiB", modTime: "2026-05-29T15:30:00Z" },
            ],
            lines: [],
            hasMore: false,
          },
        }}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByTestId("session-files-editor-chrome")).toHaveTextContent("Workspace");
    expect(screen.getByTestId("session-files-editor-chrome")).toHaveTextContent("/work/affent");
    expect(screen.getByTestId("session-workspace-directory-preview")).not.toHaveTextContent("Project root");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("Size");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("Modified");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("05-29 15:30");
    expect(screen.getByTestId("session-workspace-directory-table")).not.toHaveTextContent("Directory");
    await user.click(within(screen.getByTestId("session-workspace-directory-preview")).getByRole("button", { name: "Copy current path" }));
    expect(writeText).toHaveBeenCalledWith("/work/affent");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("src");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-browser-list")).not.toHaveTextContent(".affent");
    expect(screen.getByTestId("session-workspace-browser-list")).not.toHaveTextContent("session-state");
    const workspaceRootRow = screen.getByTestId("session-workspace-browser-list").querySelector('[data-path="."]');
    expect(workspaceRootRow).not.toBeNull();
    expect(workspaceRootRow).toHaveTextContent("Copy");
    await user.click(within(workspaceRootRow as HTMLElement).getByRole("button", { name: "Copy workspace root path" }));
    expect(writeText).toHaveBeenCalledWith("/work/affent");
    const srcTreeRow = screen.getByTestId("session-workspace-browser-list").querySelector('[data-path="src"]');
    expect(srcTreeRow).not.toBeNull();
    expect(srcTreeRow).toHaveTextContent("Copy");
    await user.click(within(srcTreeRow as HTMLElement).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith("/work/affent/src");
    expect(screen.getByTestId("session-files-editor-chrome")).toHaveTextContent("1 folder · 1 file");
    await user.type(screen.getByLabelText("Filter files"), "readme");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-browser-list")).not.toHaveTextContent("src");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("README.md");
    expect(screen.getByTestId("session-workspace-directory-table")).not.toHaveTextContent("src");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    await user.click(within(screen.getByLabelText("File explorer")).getByRole("button", { name: "Show hidden files" }));
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent(".affent");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("session-state");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent(".affent");
    expect(screen.getByTestId("session-files-editor-chrome")).toHaveTextContent("3 folders · 1 file");
    await user.click(within(screen.getByTestId("session-workspace-browser-list")).getByRole("button", { name: /src/ }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    const readmeRow = within(screen.getByTestId("session-workspace-directory-table")).getByText("README.md").closest("li");
    expect(readmeRow).not.toBeNull();
    expect(readmeRow).toHaveTextContent("Open");
    expect(readmeRow).toHaveTextContent("Copy");
    await user.click(within(readmeRow as HTMLElement).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith("/work/affent/README.md");
    await user.click(within(readmeRow as HTMLElement).getByRole("button", { name: "Open README.md" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("README.md");
    await user.click(within(readmeRow as HTMLElement).getByRole("button", { name: "README.md" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("README.md");
    expect(screen.queryByLabelText("Workspace path")).toBeNull();
    expect(within(screen.getByTestId("session-workspace-directory-preview")).queryByRole("button", { name: "Reference listing" })).toBeNull();
  });

  it("keeps loaded child directories expandable in the workspace tree", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
    const longFilePath = "src/very-long-component-file-name-that-should-truncate-inside-the-row.tsx";
    const { rerender } = render(
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
            detail: "1 directory",
            entries: [{ name: "src", path: "src", kind: "directory" }],
            lines: [],
            hasMore: false,
          },
        }}
        onOpenWorkspacePath={onOpenWorkspacePath}
      />,
    );

    await user.click(within(screen.getByTestId("session-workspace-browser-list")).getByRole("button", { name: "Expand directory" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");

    rerender(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          file: {
            path: "src",
            kind: "directory",
            title: "src",
            detail: "1 directory · 1 file",
            entries: [
              { name: "components", path: "src/components", kind: "directory" },
              {
                name: "very-long-component-file-name-that-should-truncate-inside-the-row.tsx",
                path: longFilePath,
                kind: "file",
                size: "18 KiB",
                modTime: "2026-05-29T18:04:00Z",
              },
            ],
            lines: [],
            hasMore: false,
          },
        }}
        onOpenWorkspacePath={onOpenWorkspacePath}
      />,
    );

    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("components");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("very-long-component-file-name");
    expect(screen.getByTestId("session-workspace-directory-table")).toHaveTextContent("05-29 18:04");

    const longFileRow = within(screen.getByTestId("session-workspace-directory-table"))
      .getByText("very-long-component-file-name-that-should-truncate-inside-the-row.tsx")
      .closest("li");
    expect(longFileRow).not.toBeNull();
    await user.click(within(longFileRow as HTMLElement).getByRole("button", { name: `Open ${longFilePath}` }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith(longFilePath);

    scrollIntoView.mockClear();
    rerender(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          file: {
            path: longFilePath,
            kind: "file",
            title: "very-long-component-file-name-that-should-truncate-inside-the-row.tsx",
            detail: "18 KiB · loaded",
            entries: [],
            text: "export const value = true;\n",
            lines: ["export const value = true;", ""],
            hasMore: false,
            size: "18 KiB",
          },
        }}
        onOpenWorkspacePath={onOpenWorkspacePath}
      />,
    );

    const selectedTreeRow = screen.getByTestId("session-workspace-browser-list").querySelector(`[data-path="${longFilePath}"]`);
    expect(selectedTreeRow).not.toBeNull();
    expect(selectedTreeRow).toHaveAttribute("data-selected", "true");
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" }));
  });

  it("uses reveal directories to locate an opened workspace file", async () => {
    const scrollIntoView = vi.fn();
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", { configurable: true, value: scrollIntoView });
    const path = "src/components/Button.tsx";

    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          revealDirectories: [
            {
              path: ".",
              kind: "directory",
              title: "Workspace root",
              detail: "1 directory",
              entries: [{ name: "src", path: "src", kind: "directory" }],
              lines: [],
              hasMore: false,
            },
            {
              path: "src",
              kind: "directory",
              title: "src",
              detail: "1 directory",
              entries: [{ name: "components", path: "src/components", kind: "directory" }],
              lines: [],
              hasMore: false,
            },
            {
              path: "src/components",
              kind: "directory",
              title: "src/components",
              detail: "1 file",
              entries: [{ name: "Button.tsx", path, kind: "file", size: "512 B" }],
              lines: [],
              hasMore: false,
            },
          ],
          file: {
            path,
            kind: "file",
            title: "Button.tsx",
            detail: "512 B · loaded",
            entries: [],
            text: "export function Button() {}\n",
            lines: ["export function Button() {}", ""],
            hasMore: false,
            size: "512 B",
          },
        }}
        onOpenWorkspacePath={vi.fn()}
      />,
    );

    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("components");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("Button.tsx");
    const selectedTreeRow = screen.getByTestId("session-workspace-browser-list").querySelector(`[data-path="${path}"]`);
    expect(selectedTreeRow).not.toBeNull();
    expect(selectedTreeRow).toHaveAttribute("data-selected", "true");
    await waitFor(() => expect(selectedTreeRow).toHaveAttribute("data-open", "true"));
    expect(within(selectedTreeRow as HTMLElement).getByTitle("Open")).toBeInTheDocument();
    await waitFor(() => expect(scrollIntoView).toHaveBeenCalledWith({ block: "nearest" }));
  });

  it("keeps unsaved workspace drafts across file switches and guards dirty tab close", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const mainFile = {
      path: "src/main.go",
      kind: "file" as const,
      title: "main.go",
      detail: "26 B · loaded",
      entries: [],
      text: "package main\nfunc main() {}\n",
      lines: ["package main", "func main() {}", ""],
      hasMore: false,
      size: "26 B",
    };
    const utilFile = {
      path: "src/util.go",
      kind: "file" as const,
      title: "util.go",
      detail: "18 B · loaded",
      entries: [],
      text: "package util\n",
      lines: ["package util", ""],
      hasMore: false,
      size: "18 B",
    };
    const { rerender } = render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{ state: "ready", workspacePath: "/work/affent", file: mainFile }}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUploadWorkspaceFile={vi.fn().mockResolvedValue(undefined)}
      />,
    );

    const editor = screen.getByLabelText("Workspace file editor");
    await user.type(editor, "const draft = true;\n");
    await waitFor(() => expect(screen.getByText("main.go").closest(".session-files-open-tab")).toHaveAttribute("data-dirty", "true"));

    rerender(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{ state: "ready", workspacePath: "/work/affent", file: utilFile }}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUploadWorkspaceFile={vi.fn().mockResolvedValue(undefined)}
      />,
    );
    expect(screen.getByLabelText("Workspace file editor")).toHaveValue("package util\n");
    expect(document.querySelector('.session-files-open-tab button[title="src/main.go"]')?.closest(".session-files-open-tab")).toHaveAttribute("data-dirty", "true");

    await user.click(screen.getByRole("button", { name: "Close src/main.go" }));
    expect(screen.getByTestId("session-files-close-prompt")).toHaveTextContent("main.go");
    expect(document.querySelector('.session-files-open-tab button[title="src/main.go"]')?.closest(".session-files-open-tab")).toHaveAttribute("data-dirty", "true");
    await user.click(within(screen.getByTestId("session-files-close-prompt")).getByRole("button", { name: "Cancel" }));
    expect(screen.queryByTestId("session-files-close-prompt")).toBeNull();

    rerender(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{ state: "ready", workspacePath: "/work/affent", file: mainFile }}
        onOpenWorkspacePath={onOpenWorkspacePath}
        onUploadWorkspaceFile={vi.fn().mockResolvedValue(undefined)}
      />,
    );
    expect(screen.getByLabelText<HTMLTextAreaElement>("Workspace file editor").value).toContain("const draft = true;");
    await user.click(screen.getByRole("button", { name: "Close src/main.go" }));
    await user.click(within(screen.getByTestId("session-files-close-prompt")).getByRole("button", { name: "Discard" }));
    await waitFor(() => expect(document.querySelector('.session-files-open-tab button[title="src/main.go"]')).toBeNull());
  });

  it("marks workspace save failures in tabs and the tree", async () => {
    const user = userEvent.setup();
    const onUploadWorkspaceFile = vi.fn().mockRejectedValue(new Error("disk full"));
    const path = "src/main.go";
    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          revealDirectories: [
            {
              path: ".",
              kind: "directory",
              title: "Workspace root",
              detail: "1 directory",
              entries: [{ name: "src", path: "src", kind: "directory" }],
              lines: [],
              hasMore: false,
            },
            {
              path: "src",
              kind: "directory",
              title: "src",
              detail: "1 file",
              entries: [{ name: "main.go", path, kind: "file", size: "26 B" }],
              lines: [],
              hasMore: false,
            },
          ],
          file: {
            path,
            kind: "file",
            title: "main.go",
            detail: "26 B · loaded",
            entries: [],
            text: "package main\n",
            lines: ["package main", ""],
            hasMore: false,
            size: "26 B",
          },
        }}
        onOpenWorkspacePath={vi.fn()}
        onUploadWorkspaceFile={onUploadWorkspaceFile}
      />,
    );

    const editor = screen.getByLabelText("Workspace file editor");
    fireEvent.change(editor, { target: { value: "package main\nfunc main() {}\n" } });
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onUploadWorkspaceFile).toHaveBeenCalledWith(path, expect.stringContaining("func main")));
    expect(screen.getByTestId("session-workspace-file-status")).toHaveTextContent("Save failed");
    expect(screen.getByTestId("session-workspace-file-status")).toHaveTextContent("disk full");
    expect(document.querySelector('.session-files-open-tab button[title="src/main.go"]')?.closest(".session-files-open-tab")).toHaveAttribute("data-error", "true");
    const treeRow = screen.getByTestId("session-workspace-browser-list").querySelector(`[data-path="${path}"]`);
    expect(treeRow).toHaveAttribute("data-error", "true");
    expect(within(treeRow as HTMLElement).getByTitle("Save failed")).toBeInTheDocument();

    fireEvent.change(editor, { target: { value: "package main\nfunc main() {}\n// retry\n" } });
    expect(document.querySelector('.session-files-open-tab button[title="src/main.go"]')?.closest(".session-files-open-tab")).toHaveAttribute("data-error", "false");
    expect(treeRow).toHaveAttribute("data-error", "false");
  });

  it("uploads a local text file into the current workspace directory", async () => {
    const user = userEvent.setup();
    const onUploadWorkspaceFile = vi.fn().mockResolvedValue(undefined);

    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          file: {
            path: "src",
            kind: "directory",
            title: "src",
            detail: "empty directory",
            entries: [],
            lines: [],
            hasMore: false,
          },
        }}
        onOpenWorkspacePath={vi.fn()}
        onUploadWorkspaceFile={onUploadWorkspaceFile}
      />,
    );

    const input = document.querySelector<HTMLInputElement>('input[type="file"]');
    expect(input).not.toBeNull();
    await user.upload(input!, new File(["hello upload\n"], "new-file.txt", { type: "text/plain" }));

    await waitFor(() => expect(onUploadWorkspaceFile).toHaveBeenCalledWith("src/new-file.txt", "hello upload\n"));
    expect(screen.getByText("Uploaded src/new-file.txt")).toBeInTheDocument();
  });

  it("uploads dropped files into the current workspace directory", async () => {
    const onUploadWorkspaceFile = vi.fn().mockResolvedValue(undefined);
    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{
          state: "ready",
          workspacePath: "/work/affent",
          file: {
            path: "src",
            kind: "directory",
            title: "src",
            detail: "empty directory",
            entries: [],
            lines: [],
            hasMore: false,
          },
        }}
        onOpenWorkspacePath={vi.fn()}
        onUploadWorkspaceFile={onUploadWorkspaceFile}
      />,
    );

    const ide = screen.getByTestId("session-files-ide");
    const dropped = [
      new File(["one\n"], "one.ts", { type: "text/plain" }),
      new File(["two\n"], "two.ts", { type: "text/plain" }),
    ];
    fireEvent.dragEnter(ide, { dataTransfer: { files: dropped } });
    expect(ide).toHaveAttribute("data-dragging", "true");
    fireEvent.drop(ide, { dataTransfer: { files: dropped } });

    await waitFor(() => expect(onUploadWorkspaceFile).toHaveBeenCalledWith("src/one.ts", "one\n"));
    expect(onUploadWorkspaceFile).toHaveBeenCalledWith("src/two.ts", "two\n");
    expect(screen.getByText("Uploaded 2 files to src")).toBeInTheDocument();
  });

  it("shows loaded workspace file content", async () => {
    const user = userEvent.setup();
    const onOpenWorkspacePath = vi.fn();
    const onUploadWorkspaceFile = vi.fn().mockResolvedValue(undefined);
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
        onUploadWorkspaceFile={onUploadWorkspaceFile}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    const preview = screen.getByTestId("session-workspace-file-preview");
    const status = within(preview).getByTestId("session-workspace-file-status");
    const editor = screen.getByLabelText("Workspace file editor");
    expect(screen.getByTestId("session-files-editor-chrome")).toHaveTextContent("src/main.go");
    expect(status).toHaveTextContent("Go");
    expect(status).toHaveTextContent("3 lines");
    expect(status).not.toHaveTextContent("Clean");
    await waitFor(() => expect(screen.getByText("main.go").closest(".session-files-open-tab")).toHaveAttribute("data-dirty", "false"));
    expect(editor).toHaveValue("package main\nfunc main() {}\n");
    await user.type(screen.getByLabelText("Search workspace file"), "func");
    expect(preview).toHaveTextContent("1 match");
    await user.clear(screen.getByLabelText("Go to workspace line"));
    await user.type(screen.getByLabelText("Go to workspace line"), "2");
    await user.click(within(preview).getByRole("button", { name: "Go to line" }));
    expect(editor).toHaveFocus();
    expect(within(preview).getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "true");
    await user.click(within(preview).getByRole("button", { name: "Wrap" }));
    expect(within(preview).getByRole("button", { name: "Wrap" })).toHaveAttribute("aria-pressed", "false");
    await user.click(within(preview).getByRole("button", { name: "Up" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    expect(screen.getByTestId("session-workspace-command-strip")).toHaveTextContent("Copy path");
    expect(screen.getByTestId("session-workspace-command-strip")).toHaveTextContent("Copy file");
    await user.click(within(preview).getByRole("button", { name: "Copy file" }));
    expect(writeText).toHaveBeenCalledWith("package main\nfunc main() {}\n");
    await user.click(within(preview).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith("/work/affent/src/main.go");
    await user.type(editor, "const x = 1;\n");
    expect(status).toHaveTextContent("Unsaved");
    await waitFor(() => expect(screen.getByText("main.go").closest(".session-files-open-tab")).toHaveAttribute("data-dirty", "true"));
    await user.click(within(preview).getByRole("button", { name: "Save" }));
    await waitFor(() => expect(onUploadWorkspaceFile).toHaveBeenCalledWith("src/main.go", expect.stringContaining("const x = 1;")));
    await waitFor(() => expect(screen.getByText("main.go").closest(".session-files-open-tab")).toHaveAttribute("data-dirty", "false"));
    expect(status).toHaveTextContent("Saved");
    expect(within(preview).queryByRole("button", { name: "Ask about file" })).toBeNull();
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
