import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionFilesPanel } from "./SessionFilesPanel";
import type { SessionFilesView } from "../view/sessionFiles";

describe("SessionFilesPanel", () => {
  it("renders file evidence and creates a path draft", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<SessionFilesPanel defaultOpen files={files} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-files-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("2 file references");
    expect(screen.getByLabelText("Search files")).toBeInTheDocument();
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Read + Changed · available · turn 2 · 2 actions");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Updated payment route");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Next: rerun checkout tests");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("Evidence artifact: .affent/artifacts/tool-results/read.txt");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("read_file snapshot available");
    expect(screen.getByTestId("session-file-preview")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-file-preview-content")).toHaveTextContent("export function checkout");

    await user.type(screen.getByLabelText("Search file snapshot"), "route");
    expect(screen.getByTestId("session-file-preview-content")).toHaveTextContent("route");

    await user.click(within(screen.getByTestId("session-files-list")).getAllByRole("button", { name: "Copy path" })[0]);
    expect(writeText).toHaveBeenCalledWith("src/payments.ts");

    await user.click(within(screen.getByTestId("session-files-list")).getAllByRole("button", { name: "Copy evidence" })[0]);
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("File evidence for src/payments.ts"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Next: rerun checkout tests"));

    await user.click(screen.getByRole("button", { name: "Copy snapshot" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("File snapshot for src/payments.ts"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("export function checkout"));

    await user.click(within(screen.getByTestId("session-files-list")).getByRole("button", { name: "Open evidence" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/read.txt");

    await user.click(within(screen.getByTestId("session-files-list")).getAllByRole("button", { name: "Use file as draft" })[0]);
    await user.click(screen.getByRole("button", { name: "Use text as draft" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("File evidence for src/payments.ts"), "file_evidence");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Next: rerun checkout tests"), "file_evidence");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("export function checkout"), "file_snapshot");

    await user.clear(screen.getByLabelText("Search file snapshot"));
    await user.type(screen.getByLabelText("Search files"), "listed");
    expect(screen.getByTestId("session-files-list")).not.toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-files-list")).toHaveTextContent("src");

    await user.type(screen.getByLabelText("Search files"), "missing.ts");
    expect(screen.queryByTestId("session-files-list")).toBeNull();
    expect(panel).toHaveTextContent('No file evidence matching "missing.ts".');
  });

  it("keeps the panel folded by default", () => {
    render(<SessionFilesPanel files={files} />);

    expect(screen.getByTestId("session-files-panel")).not.toHaveAttribute("open");
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
