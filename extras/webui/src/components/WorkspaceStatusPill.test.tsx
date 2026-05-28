import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { WorkspaceStatusPill } from "./WorkspaceStatusPill";

describe("WorkspaceStatusPill", () => {
  it("renders a compact workspace status and opens Workspace on click", async () => {
    const user = userEvent.setup();
    const onOpen = vi.fn();

    render(
      <WorkspaceStatusPill
        workspace={{
          hasData: true,
          summary: "affent",
          shortStatus: "affent · main · dirty",
          detail: "/repo/affent · branch main · dirty",
          label: "affent",
          path: "/repo/affent",
          branch: "main",
          dirtyState: "dirty",
        }}
        onOpen={onOpen}
      />,
    );

    expect(screen.getByTestId("workspace-status-pill")).toHaveTextContent("affent · main · dirty");
    expect(screen.getByTestId("workspace-status-pill")).toHaveAttribute("title", "/repo/affent · branch main · dirty");
    await user.click(screen.getByTestId("workspace-status-pill"));
    expect(onOpen).toHaveBeenCalledTimes(1);
  });

  it("stays hidden without workspace evidence", () => {
    render(
      <WorkspaceStatusPill
        workspace={{
          hasData: false,
          summary: "No workspace evidence",
          shortStatus: "No workspace evidence",
          detail: "No workspace binding or command cwd recorded.",
        }}
        onOpen={vi.fn()}
      />,
    );

    expect(screen.queryByTestId("workspace-status-pill")).toBeNull();
  });
});
