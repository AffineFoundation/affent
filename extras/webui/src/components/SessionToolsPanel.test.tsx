import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { SessionToolsPanel } from "./SessionToolsPanel";

describe("SessionToolsPanel", () => {
  it("groups tools by source and exposes MCP raw names", async () => {
    const user = userEvent.setup();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(
      <SessionToolsPanel
        surface={{
          headline: "Visible tool surface",
          detail: "These tools are the subset currently exposed to this session.",
          tone: "ready",
          status: "allowed",
          warnings: [],
          disabled_reasons: [],
        }}
        tools={[
          {
            name: "read_file",
            group: "Workspace",
            description: "Read a file from the workspace.",
            parameters: { type: "object" },
          },
          {
            name: "web_fetch",
            group: "Research",
            description: "Fetch a URL.",
            parameters: { type: "object", properties: { url: { type: "string" } } },
          },
          {
            name: "taostats_query",
            group: "MCP",
            source: "taostats",
            raw_name: "query",
            description: "Query TAO stats.",
            parameters: { type: "object" },
          },
        ]}
      />,
    );

    const panel = screen.getByTestId("session-tools-panel");
    await user.click(within(panel).getByText("3 tools available"));

    expect(within(panel).getByText("Allow / filter status")).toBeInTheDocument();
    expect(within(panel).getByText("Visible tool surface")).toBeInTheDocument();
    expect(within(panel).getByText("These tools are the subset currently exposed to this session.")).toBeInTheDocument();
    expect(within(panel).getByText("Surface")).toBeInTheDocument();
    expect(within(panel).getByText("Allowed")).toBeInTheDocument();
    expect(within(panel).getByRole("tab", { name: "All 3" })).toBeInTheDocument();
    expect(within(panel).getByRole("tab", { name: "Workspace 1" })).toBeInTheDocument();
    expect(within(panel).getByRole("tab", { name: "Research 1" })).toBeInTheDocument();
    expect(within(panel).getByRole("tab", { name: "MCP · taostats 1" })).toBeInTheDocument();
    expect(within(panel).getByText("Workspace 1 · Research 1 · MCP · taostats 1")).toBeInTheDocument();

    await user.click(within(panel).getByRole("tab", { name: "Research 1" }));
    expect(within(panel).getByRole("tab", { name: "Research 1", selected: true })).toBeInTheDocument();
    expect(within(panel).queryByText("read_file")).not.toBeInTheDocument();
    expect(within(panel).queryByText("taostats_query")).not.toBeInTheDocument();
    expect(within(panel).getByText("web_fetch")).toBeInTheDocument();
    expect(within(panel).getAllByText("Research · 1 visible group")).toHaveLength(2);
    await user.click(within(panel).getByText("web_fetch"));
    expect(within(panel).getByText("Schema 1 field")).toBeInTheDocument();
    expect(within(panel).getByText("Description 3 words")).toBeInTheDocument();
    await user.type(within(panel).getByTestId("session-tools-search"), "missing");
    expect(within(panel).getByRole("button", { name: "Clear" })).toBeInTheDocument();
    expect(within(panel).getByRole("tab", { name: "All 0" })).toBeInTheDocument();
    expect(within(panel).getByText("No matching tools.")).toBeInTheDocument();
    expect(within(panel).getByText("Research · no tools match this filter")).toBeInTheDocument();
    expect(within(panel).getByText("Research · no matching tools")).toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: "Clear" }));
    expect(within(panel).queryByRole("button", { name: "Clear" })).not.toBeInTheDocument();
    expect(within(panel).getAllByText("Research · 1 visible group")).toHaveLength(2);
    await user.click(within(panel).getByRole("button", { name: "Copy diagnostic" }));
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Tool diagnostic");
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Visible tool surface");
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Filter: Research");
    await user.click(within(panel).getByRole("button", { name: "Copy names" }));
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Tool names");
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Research (1)");
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("- web_fetch");
    await user.click(within(panel).getByRole("button", { name: "Copy filtered catalog" }));
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Tool catalog");
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("Research (1 tool)");
    expect(writeText.mock.calls.at(-1)?.[0]).toContain("web_fetch — Fetch a URL.");
    await user.click(within(panel).getByRole("tab", { name: "All 1" }));
    expect(within(panel).getByRole("tab", { name: "All 3", selected: true })).toBeInTheDocument();
    expect(within(panel).getByRole("button", { name: "Collapse all" })).toBeInTheDocument();

    await user.click(within(panel).getByRole("button", { name: /Workspace 1 tool/i }));
    expect(within(panel).queryByText("read_file")).not.toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: /Workspace 1 tool/i }));
    expect(within(panel).getByText("read_file")).toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: "Collapse all" }));
    expect(within(panel).queryByText("read_file")).not.toBeInTheDocument();
    expect(within(panel).queryByText("web_fetch")).not.toBeInTheDocument();
    expect(within(panel).queryByText("taostats_query")).not.toBeInTheDocument();
    await user.click(within(panel).getByRole("button", { name: "Expand all" }));
    expect(within(panel).getByText("read_file")).toBeInTheDocument();

    const mcpGroup = within(panel).getByLabelText("MCP · taostats");
    expect(within(mcpGroup).getByText("taostats_query")).toBeInTheDocument();
    await user.click(within(mcpGroup).getByText("taostats_query"));
    expect(within(mcpGroup).getByText("Raw name: query")).toBeInTheDocument();
    expect(within(mcpGroup).getByRole("button", { name: "Copy raw name" })).toBeInTheDocument();
  });
});
