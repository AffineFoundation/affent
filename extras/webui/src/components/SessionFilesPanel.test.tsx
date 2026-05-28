import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionFilesPanel } from "./SessionFilesPanel";
import type { SessionFilesView } from "../view/sessionFiles";

describe("SessionFilesPanel", () => {
  it("renders file evidence with review-first actions", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<SessionFilesPanel defaultOpen files={files} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-files-panel");
    const dashboard = screen.getByLabelText("File work summary");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("2 file references");
    expect(screen.getByTestId("session-files-review")).toHaveTextContent("Changed file");
    expect(screen.getByTestId("session-files-review")).toHaveTextContent("src/payments.ts");
    expect(screen.getByLabelText("File review facts")).toHaveTextContent("Snapshots");
    expect(screen.getByLabelText("File review facts")).toHaveTextContent("1/2");
    expect(screen.queryByTestId("session-workspace-browser")).toBeNull();
    expect(within(dashboard).getByText("All").closest("button")).toHaveTextContent("2");
    expect(within(within(dashboard).getByRole("group", { name: "File filters" })).getByRole("button", { name: /Changed/ })).toHaveTextContent("1");
    expect(within(dashboard).getByText("Snapshot").closest("button")).toHaveTextContent("1");
    expect(within(within(dashboard).getByRole("group", { name: "File filters" })).queryByText("Issues")).toBeNull();
    expect(within(screen.getByTestId("session-files-review")).getByText("Changed file")).toBeInTheDocument();
    expect(screen.getAllByRole("button", { name: "View snapshot" }).length).toBeGreaterThan(0);
    expect(screen.getByLabelText("Search files")).toBeInTheDocument();
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Read + Changed · available · turn 2 · 2 actions");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Updated payment route");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Next: rerun checkout tests");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Evidence: read.txt");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("read_file snapshot available");
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-file-preview-content")).toHaveTextContent("export function checkout");

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
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("export function checkout"), "file_range");
    await user.click(screen.getByRole("button", { name: "Edit range" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Edit this selected file range"), "file_range");
    await user.clear(screen.getByLabelText("Go to line"));
    await user.type(screen.getByLabelText("Go to line"), "2");
    await user.click(screen.getByRole("button", { name: "Go" }));
    expect(screen.getByTestId("session-file-range-actions")).toHaveTextContent("Lines 2-2");

    await user.click(within(screen.getByTestId("session-files-list")).getAllByRole("button", { name: "Copy path" })[0]);
    expect(writeText).toHaveBeenCalledWith("src/payments.ts");

    await user.click(screen.getByRole("button", { name: "Copy snapshot" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("File snapshot for src/payments.ts"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("export function checkout"));

    await user.click(within(screen.getByTestId("session-files-list")).getByRole("button", { name: "Open evidence" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/read.txt");
    await user.click(within(screen.getByTestId("session-files-list")).getByRole("button", { name: "Review file" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review this changed file in the next step"), "file_evidence");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("File evidence for src/payments.ts"), "file_evidence");
    expect(screen.queryByRole("button", { name: "Copy all evidence" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Use all as draft" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Copy evidence" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Use text as draft" })).toBeNull();

    await user.clear(screen.getByLabelText("Search file snapshot"));
    await user.type(screen.getByLabelText("Search files"), "listed");
    expect(screen.getByTestId("session-files-list")).not.toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src");
    await user.click(within(screen.getByTestId("session-files-list")).getByRole("button", { name: "Use listing" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Use this listed directory in the next step"), "file_evidence");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src");

    await user.click(within(within(dashboard).getByRole("group", { name: "File filters" })).getByRole("button", { name: /Changed/ }));
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src/payments.ts");
    expect(screen.queryByTitle("src")).not.toBeInTheDocument();

    await user.type(screen.getByLabelText("Search files"), "missing.ts");
    expect(screen.queryByTestId("session-files-list")).toBeNull();
    expect(panel).toHaveTextContent('No changed result matching "missing.ts".');
  });

  it("keeps the panel folded by default", () => {
    render(<SessionFilesPanel files={files} />);

    expect(screen.getByTestId("session-files-panel")).not.toHaveAttribute("open");
  });

  it("does not render an idle workspace browser shell", () => {
    render(
      <SessionFilesPanel
        defaultOpen
        files={files}
        workspaceBrowser={{ state: "idle", workspacePath: "/work/affent" }}
        onOpenWorkspacePath={() => undefined}
      />,
    );

    expect(screen.queryByTestId("session-workspace-browser")).toBeNull();
    expect(screen.getByRole("button", { name: "Browse workspace" })).toBeInTheDocument();
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

    const changedItem = within(screen.getByTestId("session-files-list")).getAllByRole("listitem")[0];
    await user.click(within(changedItem).getByRole("button", { name: "Open current" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src/payments.ts");

    const browser = screen.getByTestId("session-workspace-browser");
    expect(browser).toHaveTextContent("Workspace browser");
    expect(browser).toHaveTextContent("Workspace root");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("src");
    expect(screen.getByTestId("session-workspace-browser-list")).toHaveTextContent("README.md");
    await user.click(within(screen.getByTestId("session-workspace-browser-list")).getByRole("button", { name: /src/ }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    await user.clear(screen.getByLabelText("Workspace path"));
    await user.type(screen.getByLabelText("Workspace path"), "src/main.go");
    await user.click(within(browser).getByRole("button", { name: "Open" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src/main.go");
    await user.click(within(browser).getByRole("button", { name: "Reference listing" }));
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
    await user.click(within(preview).getByRole("button", { name: "Up" }));
    expect(onOpenWorkspacePath).toHaveBeenCalledWith("src");
    await user.click(within(preview).getByRole("button", { name: "Copy file" }));
    expect(writeText).toHaveBeenCalledWith("package main\nfunc main() {}\n");
    await user.click(within(preview).getByRole("button", { name: "Reference file" }));
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
      contentBytes: 58,
    },
    {
      path: "src",
      actions: ["listed"],
      status: "available",
      turnNumber: 1,
      actionCount: 1,
    },
  ],
};
