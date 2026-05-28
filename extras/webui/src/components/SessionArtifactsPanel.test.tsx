import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionArtifactsPanel } from "./SessionArtifactsPanel";

describe("SessionArtifactsPanel", () => {
  it("renders deliverable artifacts with open, download, and path actions", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();

    render(
      <SessionArtifactsPanel
        defaultOpen
        artifacts={[
          {
            path: ".affent/artifacts/tool-results/000001-test.txt",
            name: "000001-test.txt",
            source: "npm test -- checkout.spec.ts",
            tool: "shell",
            turnNumber: 3,
            callIndex: 2,
            summary: `checkout spec failed ${"log line ".repeat(40)}unreachable tail marker`,
            truncated: true,
            bytes: 8192,
            omittedBytes: 1024,
            capBytes: 4096,
          },
          {
            path: ".affent/artifacts/reports/checkout-report.md",
            name: "checkout-report.md",
            source: "final report",
            tool: "write_file",
            turnNumber: 4,
            callIndex: 1,
            summary: "checkout audit report",
            truncated: false,
            bytes: 2048,
          },
        ]}
        downloadHref={(path) => `/v1/sessions/s1/artifacts/${path}`}
        onOpenArtifact={onOpenArtifact}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    const panel = screen.getByTestId("session-artifacts-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("1 deliverable · 1 full output");
    expect(panel).toHaveTextContent("2 files · 10 KiB recorded");
    expect(screen.getByLabelText("Artifact evidence summary")).toHaveTextContent("Evidence files");
    expect(screen.getByLabelText("Artifact evidence summary")).toHaveTextContent("Full output");
    expect(screen.getByLabelText("Artifact review facts")).toHaveTextContent("Latest turn");
    expect(screen.getByLabelText("Artifact review facts")).toHaveTextContent("4");
    const sourceIndex = screen.getByLabelText("Artifact source index");
    expect(sourceIndex).toHaveTextContent("Sources");
    expect(sourceIndex).toHaveTextContent("shell: npm test -- checkout.spec.ts");
    expect(sourceIndex).toHaveTextContent("1 file · Full output · turn 3 · 8 KiB");
    expect(sourceIndex).toHaveTextContent("write_file");
    expect(screen.getByLabelText("Artifact evidence summary")).toHaveTextContent("000001-test.txt");
    const focus = screen.getByTestId("session-artifacts-focus");
    expect(focus).toHaveTextContent("turn 3 · shell · call 2");
    expect(focus).toHaveTextContent("npm test -- checkout.spec.ts");
    expect(focus).toHaveTextContent("checkout spec failed");
    expect(focus).toHaveTextContent("Open artifact");
    expect(within(focus).getByRole("link", { name: "Download" })).toHaveAttribute(
      "href",
      "/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-test.txt",
    );
    await user.click(within(focus).getByRole("button", { name: "Open artifact" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(focus).getByRole("button", { name: "Copy details" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Artifact evidence for .affent/artifacts/tool-results/000001-test.txt"));
    await user.click(within(focus).getByRole("button", { name: "Reference" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Source: npm test -- checkout.spec.ts"), "artifact");
    const filters = screen.getByLabelText("Artifact filters");
    expect(within(filters).getByText("Deliverables").closest("button")).toHaveTextContent("1");
    expect(within(filters).getByText("Full output").closest("button")).toHaveTextContent("1");
    expect(screen.getByLabelText("Search artifacts")).toBeInTheDocument();
    const list = screen.getByTestId("session-artifacts-list");
    expect(list).toHaveTextContent("000001-test.txt");
    expect(list).toHaveTextContent("checkout-report.md");
    expect(list).toHaveTextContent("Full output · turn 3 · shell · call 2 · npm test -- checkout.spec.ts");
    expect(list).not.toHaveTextContent("unreachable tail marker");
    const firstArtifact = within(list).getAllByRole("listitem")[0];
    expect(within(firstArtifact).getByRole("link", { name: "Download" })).toHaveAttribute(
      "href",
      "/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-test.txt",
    );
    expect(within(firstArtifact).getByRole("link", { name: "Download" })).toHaveAttribute("download", "000001-test.txt");

    await user.click(within(firstArtifact).getByRole("button", { name: "Open" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(firstArtifact).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(firstArtifact).getByRole("button", { name: "Copy details" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Full output available as artifact"));
    await user.click(within(firstArtifact).getByRole("button", { name: "Reference" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Artifact evidence for .affent/artifacts/tool-results/000001-test.txt"), "artifact");

    await user.click(within(filters).getByText("Deliverables").closest("button")!);
    expect(screen.getByTestId("session-artifacts-list")).not.toHaveTextContent("000001-test.txt");
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("checkout-report.md");
    await user.click(within(filters).getByText("All").closest("button")!);
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("000001-test.txt");
    await user.click(within(sourceIndex).getByRole("button", { name: /shell: npm test/ }));
    expect(screen.getByLabelText("Search artifacts")).toHaveValue("");
    expect(sourceIndex).toHaveTextContent("Clear source");
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("000001-test.txt");
    expect(screen.getByTestId("session-artifacts-list")).not.toHaveTextContent("checkout-report.md");
    await user.click(within(sourceIndex).getByRole("button", { name: "Clear source" }));
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("checkout-report.md");
    await user.type(screen.getByLabelText("Search artifacts"), "report");
    expect(screen.getByTestId("session-artifacts-list")).not.toHaveTextContent("000001-test.txt");
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("checkout-report.md");
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("000001-test.txt");
    expect(screen.getByTestId("session-artifacts-list")).toHaveTextContent("checkout-report.md");

    await user.type(screen.getByLabelText("Search artifacts"), "missing.log");
    expect(screen.queryByTestId("session-artifacts-list")).toBeNull();
    expect(panel).toHaveTextContent('No artifacts matching "missing.log".');
  });

  it("keeps empty artifacts state tied to Workbench boundaries", () => {
    render(<SessionArtifactsPanel defaultOpen artifacts={[]} />);

    const panel = screen.getByTestId("session-artifacts-panel");
    expect(panel).toHaveTextContent("No artifacts");
    expect(panel).toHaveTextContent("No artifacts yet");
    expect(panel).toHaveTextContent("No generated files or stored full outputs in this chat.");
    expect(screen.queryByLabelText("Search artifacts")).toBeNull();
  });
});
