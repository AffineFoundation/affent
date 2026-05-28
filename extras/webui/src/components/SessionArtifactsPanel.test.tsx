import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionArtifactsPanel } from "./SessionArtifactsPanel";

describe("SessionArtifactsPanel", () => {
  it("renders artifact evidence with open, download, copy, and draft actions", async () => {
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
            summary: "checkout spec failed",
            truncated: true,
            bytes: 8192,
            omittedBytes: 1024,
            capBytes: 4096,
          },
          {
            path: ".affent/artifacts/reports/checkout-report.md",
            name: "checkout-report.md",
            source: "final report",
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
    expect(panel).toHaveTextContent("2 artifacts");
    expect(panel).toHaveTextContent("2 files · 1 full-output · 10 KiB recorded");
    expect(screen.getByLabelText("Search artifacts")).toBeInTheDocument();
    const list = screen.getByTestId("session-artifacts-list");
    expect(list).toHaveTextContent("000001-test.txt");
    expect(list).toHaveTextContent("checkout-report.md");
    expect(list).toHaveTextContent("Full output · npm test -- checkout.spec.ts");
    const firstArtifact = within(list).getAllByRole("listitem")[0];
    expect(within(firstArtifact).getByRole("link", { name: "Download" })).toHaveAttribute(
      "href",
      "/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-test.txt",
    );
    expect(within(firstArtifact).getByRole("link", { name: "Download" })).toHaveAttribute("download", "000001-test.txt");

    await user.click(within(firstArtifact).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(firstArtifact).getByRole("button", { name: "Copy details" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Artifact evidence for .affent/artifacts/tool-results/000001-test.txt"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Summary: checkout spec failed"));
    await user.click(within(firstArtifact).getByRole("button", { name: "Open" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(firstArtifact).getByRole("button", { name: "Reference" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Source: npm test -- checkout.spec.ts"), "artifact");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Summary: checkout spec failed"), "artifact");

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
    expect(panel).toHaveTextContent("No deliverable artifacts");
    expect(panel).toHaveTextContent("Raw command outputs are in Run. File reads and edits are in Files.");
    expect(screen.queryByLabelText("Search artifacts")).toBeNull();
  });
});
