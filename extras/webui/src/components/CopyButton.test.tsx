import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { CopyButton } from "./CopyButton";

describe("CopyButton", () => {
  it("falls back when the Clipboard API is unavailable", async () => {
    const user = userEvent.setup();
    const execCommand = vi.fn().mockReturnValue(true);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    Object.defineProperty(document, "execCommand", { configurable: true, value: execCommand });

    render(<CopyButton label="Copy message" value="hello" />);

    await user.click(screen.getByRole("button", { name: "Copy message" }));

    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(screen.getByRole("button", { name: "Copied" })).toHaveAttribute("data-copy-state", "copied");
  });

  it("keeps the user's viewport and focus stable during fallback copy", async () => {
    const user = userEvent.setup();
    const execCommand = vi.fn().mockImplementation(() => {
      Object.defineProperty(window, "scrollX", { configurable: true, value: 12 });
      Object.defineProperty(window, "scrollY", { configurable: true, value: 980 });
      return true;
    });
    const scrollTo = vi.fn();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    Object.defineProperty(document, "execCommand", { configurable: true, value: execCommand });
    Object.defineProperty(window, "scrollX", { configurable: true, value: 12 });
    Object.defineProperty(window, "scrollY", { configurable: true, value: 240 });
    Object.defineProperty(window, "scrollTo", { configurable: true, value: scrollTo });

    render(
      <>
        <button type="button">Keep focus</button>
        <CopyButton label="Copy message" value="hello" />
      </>,
    );

    const focusTarget = screen.getByRole("button", { name: "Keep focus" });
    focusTarget.focus();
    await user.click(screen.getByRole("button", { name: "Copy message" }));

    expect(execCommand).toHaveBeenCalledWith("copy");
    expect(scrollTo).toHaveBeenCalledWith(12, 240);
    expect(screen.getByRole("button", { name: "Copied" })).toHaveFocus();
  });

  it("shows an explicit failure state when both copy paths fail", async () => {
    const user = userEvent.setup();
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: undefined });
    Object.defineProperty(document, "execCommand", { configurable: true, value: vi.fn().mockReturnValue(false) });

    render(<CopyButton label="Copy message" value="hello" />);

    await user.click(screen.getByRole("button", { name: "Copy message" }));

    expect(screen.getByRole("button", { name: "Copy failed" })).toHaveAttribute("data-copy-state", "failed");
  });
});
