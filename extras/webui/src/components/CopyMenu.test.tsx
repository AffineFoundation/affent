import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { CopyMenu } from "./CopyMenu";

describe("CopyMenu", () => {
  it("renders the popup as a portal layer and closes it on outside click", async () => {
    const user = userEvent.setup();

    render(
      <div data-testid="host">
        <CopyMenu className="host-menu" panelClassName="host-panel">
          <button type="button">Copy plain text</button>
        </CopyMenu>
      </div>,
    );

    await user.click(screen.getByRole("button", { name: "Copy" }));

    const panel = document.body.querySelector(".host-panel");
    expect(panel).toBeInTheDocument();
    expect(within(panel as HTMLElement).getByRole("button", { name: "Copy plain text" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Copy" })).toHaveAttribute("aria-expanded", "true");

    await user.click(screen.getByTestId("host"));

    expect(screen.getByRole("button", { name: "Copy" })).toHaveAttribute("aria-expanded", "false");
    expect(document.body.querySelector(".host-panel")).toBeNull();
  });
});
