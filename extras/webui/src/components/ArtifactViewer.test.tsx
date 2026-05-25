import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ArtifactViewer } from "./ArtifactViewer";

describe("ArtifactViewer", () => {
  it("renders a loaded chunk with byte metadata and search highlights", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    const onSearch = vi.fn();
    const onLoadMore = vi.fn();
    const onUseAsDraft = vi.fn();

    render(
      <ArtifactViewer
        artifact={{
          state: "ready",
          query: "needle",
          chunk: {
            path: ".affent/artifacts/tool-results/000001-c1.txt",
            bytes: 20,
            offset: 4,
            text: "hay needle stack",
            hasMore: true,
          },
        }}
        onClose={vi.fn()}
        onSearch={onSearch}
        onLoadMore={onLoadMore}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("File preview");
    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("000001-c1.txt");
    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("more available");
    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("File details");
    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("20 loaded");
    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("100% loaded");
    expect(screen.getByTestId("artifact-viewer")).toHaveTextContent("1 match");
    expect(screen.getByTestId("artifact-match-list")).toHaveTextContent("Line 1");
    expect(screen.getByTestId("artifact-match-list")).toHaveTextContent("hay needle stack");
    expect(screen.getAllByText("needle").every((node) => node.tagName.toLowerCase() === "mark")).toBe(true);
    await user.click(screen.getByRole("button", { name: "Copy matches" }));
    expect(writeText).toHaveBeenCalledWith(
      [
        "File: .affent/artifacts/tool-results/000001-c1.txt",
        "Query: needle",
        "Line 1: hay needle stack",
      ].join("\n"),
    );
    await user.click(screen.getByRole("button", { name: "Use matches" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Use this artifact evidence in the next step:",
        "File: .affent/artifacts/tool-results/000001-c1.txt",
        "Query: needle",
        "Matches:",
        "Line 1: hay needle stack",
      ].join("\n"),
      "evidence",
    );
    await user.type(screen.getByTestId("artifact-search"), "x");
    expect(onSearch).toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Load more" }));
    expect(onLoadMore).toHaveBeenCalled();
    await user.click(screen.getByRole("button", { name: "Use file" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Use this file in the next step: .affent/artifacts/tool-results/000001-c1.txt",
      "artifact",
    );
    await user.click(screen.getByRole("button", { name: "Use text" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Use this loaded file text in the next step:",
        "File: .affent/artifacts/tool-results/000001-c1.txt",
        "Text:\nhay needle stack",
      ].join("\n"),
      "artifact_text",
    );
  });

  it("shows loading and load error state without dropping the loaded chunk", () => {
    render(
      <ArtifactViewer
        artifact={{
          state: "ready",
          query: "",
          loadingMore: true,
          loadError: "network stalled",
          chunk: {
            path: "large.txt",
            bytes: 100,
            offset: 0,
            text: "first chunk",
            hasMore: true,
          },
        }}
        onClose={vi.fn()}
        onSearch={vi.fn()}
        onLoadMore={vi.fn()}
      />,
    );

    expect(screen.getByTestId("artifact-content")).toHaveTextContent("first chunk");
    expect(screen.getByRole("alert")).toHaveTextContent("network stalled");
    expect(screen.getByRole("button", { name: "Loading more" })).toBeDisabled();
  });

  it("formats loaded JSON artifacts without changing draft text", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();

    render(
      <ArtifactViewer
        artifact={{
          state: "ready",
          query: "",
          chunk: {
            path: ".affent/artifacts/report.json",
            bytes: 28,
            offset: 0,
            text: '{"status":"ok","items":[1,2]}',
            hasMore: false,
          },
        }}
        onClose={vi.fn()}
        onSearch={vi.fn()}
        onLoadMore={vi.fn()}
        onUseAsDraft={onUseAsDraft}
      />,
    );

    expect(screen.getByRole("button", { name: "Text" })).toHaveAttribute("aria-pressed", "true");
    await user.click(screen.getByRole("button", { name: "JSON" }));

    expect(screen.getByRole("button", { name: "JSON" })).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByTestId("artifact-content")).toHaveAttribute("data-view", "json");
    expect(screen.getByTestId("artifact-content")).toHaveTextContent('"status": "ok"');
    await user.click(screen.getByRole("button", { name: "Use text" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Use this loaded file text in the next step:",
        "File: .affent/artifacts/report.json",
        'Text:\n{"status":"ok","items":[1,2]}',
      ].join("\n"),
      "artifact_text",
    );
  });

  it("can close the viewer", async () => {
    const user = userEvent.setup();
    const onClose = vi.fn();

    render(
      <ArtifactViewer
        artifact={{ state: "error", path: "missing.txt", message: "artifact not found" }}
        onClose={onClose}
        onSearch={vi.fn()}
        onLoadMore={vi.fn()}
      />,
    );

    await user.click(screen.getByRole("button", { name: "Close" }));
    expect(onClose).toHaveBeenCalled();
  });
});
