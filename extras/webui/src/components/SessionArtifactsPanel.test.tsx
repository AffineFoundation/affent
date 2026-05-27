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
        ]}
        downloadHref={(path) => `/v1/sessions/s1/artifacts/${path}`}
        onOpenArtifact={onOpenArtifact}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    const panel = screen.getByTestId("session-artifacts-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("1 artifact");
    expect(panel).toHaveTextContent("1 file · 1 full-output · 8 KiB recorded");
    const list = screen.getByTestId("session-artifacts-list");
    expect(list).toHaveTextContent("000001-test.txt");
    expect(list).toHaveTextContent("Full output · npm test -- checkout.spec.ts");
    expect(within(list).getByRole("link", { name: "Download" })).toHaveAttribute(
      "href",
      "/v1/sessions/s1/artifacts/.affent/artifacts/tool-results/000001-test.txt",
    );
    expect(within(list).getByRole("link", { name: "Download" })).toHaveAttribute("download", "000001-test.txt");

    await user.click(within(list).getByRole("button", { name: "Copy path" }));
    expect(writeText).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(list).getByRole("button", { name: "Open artifact" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-test.txt");
    await user.click(within(list).getByRole("button", { name: "Use artifact" }));
    expect(onUseAsDraft).toHaveBeenCalledWith("Use this artifact in the next step: .affent/artifacts/tool-results/000001-test.txt", "artifact");
  });
});
