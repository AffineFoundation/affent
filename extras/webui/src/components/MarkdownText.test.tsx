import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { MarkdownText } from "./MarkdownText";

describe("MarkdownText", () => {
  it("renders structured markdown as scannable document elements", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(
      <MarkdownText
        text={[
          "## Affine status",
          "",
          "| Source | Signal |",
          "| --- | --- |",
          "| TAOstats | netuid 120 |",
          "",
          "- Reason mining",
          "- 256 active keys",
        ].join("\n")}
      />,
    );

    expect(screen.getByRole("heading", { name: "Affine status", level: 2 })).toBeInTheDocument();
    const table = screen.getByRole("table");
    expect(table.parentElement).toHaveClass("markdown-table-scroll");
    expect(within(table).getByRole("columnheader", { name: "Source" })).toBeInTheDocument();
    expect(within(table).getByRole("cell", { name: "netuid 120" })).toBeInTheDocument();
    expect(screen.getByRole("list")).toHaveTextContent("Reason mining");
    expect(screen.getByText("Table")).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: "Copy table" }));

    expect(writeText).toHaveBeenCalledWith("Source\tSignal\nTAOstats\tnetuid 120");
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("falls back to highlighted plain text while searching", () => {
    render(<MarkdownText text="## Affine status" query="affine" />);

    expect(screen.queryByRole("heading")).toBeNull();
    expect(screen.getByText("Affine")).toBeInTheDocument();
  });

  it("keeps source links compact without losing the full destination", () => {
    render(
      <MarkdownText
        text={[
          "| Source | URL |",
          "| --- | --- |",
          "| GitHub | <https://github.com/AffineFoundation/affine-cortex> |",
          "| Site | [Affine dashboard](https://www.affine.io/) |",
        ].join("\n")}
      />,
    );

    const compact = screen.getByRole("link", { name: "github.com/AffineFoundation/affine-cortex" });
    expect(compact).toHaveAttribute("href", "https://github.com/AffineFoundation/affine-cortex");
    expect(compact).toHaveAttribute("title", "https://github.com/AffineFoundation/affine-cortex");
    expect(compact).toHaveAttribute("target", "_blank");
    expect(compact).toHaveAttribute("rel", "noreferrer");
    expect(screen.queryByRole("link", { name: "https://github.com/AffineFoundation/affine-cortex" })).toBeNull();

    const labelled = screen.getByRole("link", { name: "Affine dashboard" });
    expect(labelled).toHaveAttribute("href", "https://www.affine.io/");
    expect(labelled).toHaveAttribute("target", "_blank");
  });

  it("copies fenced code blocks with readable language context", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(
      <MarkdownText
        text={[
          "Run `npm test` after the patch.",
          "",
          "```bash",
          "npm test -- --run src/components/MarkdownText.test.tsx",
          "npm run build",
          "```",
        ].join("\n")}
      />,
    );

    expect(screen.getByText("npm test")).toBeInTheDocument();
    expect(screen.getByText("Shell")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: "Copy shell code" }));

    expect(writeText).toHaveBeenCalledWith(
      "npm test -- --run src/components/MarkdownText.test.tsx\nnpm run build",
    );
    expect(screen.getByRole("button", { name: "Copied" })).toBeInTheDocument();
  });

  it("keeps unlabeled code blocks generic", () => {
    render(<MarkdownText text={["```", "{\"ok\": true}", "```"].join("\n")} />);

    expect(screen.getByText("Code")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy code" })).toBeInTheDocument();
  });
});
