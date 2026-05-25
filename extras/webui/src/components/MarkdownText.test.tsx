import { render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MarkdownText } from "./MarkdownText";

describe("MarkdownText", () => {
  it("renders structured markdown as scannable document elements", () => {
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
});
