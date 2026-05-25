import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { Composer } from "./Composer";
import type { RuntimeCapabilityView } from "../view/runtimeCapabilities";

describe("Composer", () => {
  it("submits with Enter and keeps Shift+Enter as a newline", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy={false} onSubmit={onSubmit} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-active", "false");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Follow-up");
    await user.type(input, "first line{Shift>}{Enter}{/Shift}second line");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-active", "true");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Ready to send");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("2 lines");
    expect(input).toHaveValue("first line\nsecond line");

    await user.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledWith("first line\nsecond line");
    expect(input).toHaveValue("");
  });

  it("keeps the draft when submit fails", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockRejectedValue(new Error("send failed"));
    render(<Composer disabled={false} busy={false} onSubmit={onSubmit} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "do not lose this");
    await user.keyboard("{Enter}");

    expect(onSubmit).toHaveBeenCalledWith("do not lose this");
    expect(input).toHaveValue("do not lose this");
    expect(input).toHaveFocus();
  });

  it("switches the primary action to Start when no session exists yet", () => {
    render(<Composer disabled={false} busy={false} hasSession={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByPlaceholderText("Message Affent...")).toBeVisible();
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("New task");
    expect(screen.getByRole("button", { name: "Start" })).toBeDisabled();
  });

  it("labels saved history follow-ups as resuming the chat", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy={false} hasSession resumeSession onSubmit={onSubmit} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-resume-idle", "true");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Follow-up");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("continue this chat");
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();

    await user.type(input, "continue with the next concrete step");

    expect(screen.getByTestId("composer")).toHaveAttribute("data-resume-idle", "false");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Ready to send");
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
    await user.click(screen.getByRole("button", { name: "Send" }));
    expect(onSubmit).toHaveBeenCalledWith("continue with the next concrete step");
  });

  it("loads starter drafts as editable starting tasks", () => {
    render(
      <Composer
        disabled={false}
        busy={false}
        hasSession={false}
        draft={{ id: 1, content: "Inspect this project.", source: "starter" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Inspect this project.");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Draft ready");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Starting from draft");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Starting from draft");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Replaced");
    expect(screen.getByRole("button", { name: "Start" })).toBeEnabled();
  });

  it("shows a live turn state while busy", () => {
    render(<Composer disabled={false} busy hasSession onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("composer")).toHaveAttribute("data-busy", "true");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Working on this");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Affent is working");
    expect(screen.getByRole("button", { name: "Stop" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Working" })).toBeDisabled();
  });

  it("lets the user add a note while a turn is running", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy hasSession onSubmit={onSubmit} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "focus on the webui first");

    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Ready to add note");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Add a note to the current run");
    expect(screen.getByRole("button", { name: "Stop" })).toBeVisible();
    expect(screen.getByRole("button", { name: "Add note" })).toBeEnabled();

    await user.click(screen.getByRole("button", { name: "Add note" }));

    expect(onSubmit).toHaveBeenCalledWith("focus on the webui first");
    expect(input).toHaveValue("");
  });

  it("warns in the composer when a current-web task cannot use live sources", async () => {
    const user = userEvent.setup();
    render(
      <Composer
        disabled={false}
        busy={false}
        runtimeCapabilities={runtime("off")}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByPlaceholderText("Message Affent..."), "Analyze recent market trends for Affine");

    expect(screen.getByTestId("composer-task-hint")).toHaveAttribute("data-tone", "warning");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Current web info unavailable");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("results may be incomplete");
  });

  it("warns before sending an external information-gathering task with limited web access", async () => {
    const user = userEvent.setup();
    render(
      <Composer
        disabled={false}
        busy={false}
        runtimeCapabilities={runtime("limited")}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByPlaceholderText("Message Affent..."), "Affine 是 Bittensor 的一个子网，请收集信息向我介绍");

    expect(screen.getByTestId("composer-task-hint")).toHaveAttribute("data-tone", "warning");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Current web access limited");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("search or page browsing is only partially available");
  });

  it("loads a suggested note while a turn is running", () => {
    render(
      <Composer
        disabled={false}
        busy
        hasSession
        draft={{ id: 1, content: "Note for current work:", source: "tool_guidance" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const input = screen.getByPlaceholderText("Message Affent...");
    expect(input).toHaveValue("Note for current work:");
    expect(input).toHaveFocus();
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Ready to add note");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using suggested next step");
    expect(screen.getByRole("button", { name: "Add note" })).toBeEnabled();
  });

  it("replaces current text when editing a sent note receipt", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Composer disabled={false} busy hasSession onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const input = screen.getByPlaceholderText("Message Affent...");

    await user.type(input, "scratch note");
    rerender(
      <Composer
        disabled={false}
        busy
        hasSession
        draft={{ id: 1, content: "Note for current work: check tests first", source: "guidance_receipt" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(input).toHaveValue("Note for current work: check tests first");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Editing sent note");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Replaced");
    expect(screen.getByRole("button", { name: "Add note" })).toBeEnabled();
  });

  it("uses read-only copy when the current surface cannot accept input", () => {
    render(<Composer disabled busy={false} hasSession={false} disabledReason="Connect affentserve to send messages." onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("composer")).toHaveAttribute("data-readonly", "true");
    expect(screen.getByRole("status")).toHaveTextContent("Preview replay");
    expect(screen.getByRole("status")).toHaveTextContent("Connect affentserve to send messages.");
    expect(screen.queryByRole("textbox")).toBeNull();
    expect(screen.queryByRole("button", { name: "Demo" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Working" })).toBeNull();
  });

  it("appends external drafts to a hand-written message", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const input = screen.getByPlaceholderText("Message Affent...");

    await user.type(input, "old draft");
    rerender(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Continue: check the Makefile path", source: "tool_guidance" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(input).toHaveValue("old draft\n\nContinue: check the Makefile path");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using suggested next step");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Added");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Continue: check the Makefile path");
    expect(screen.getByRole("button", { name: "Send follow-up" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();
    expect(input).toHaveFocus();
  });

  it("replaces an existing imported draft with the next imported draft", async () => {
    const { rerender } = render(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Use this file in the next step: a.txt", source: "artifact" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const input = screen.getByPlaceholderText("Message Affent...");
    expect(input).toHaveValue("Use this file in the next step: a.txt");

    rerender(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 2, content: "Use this loaded file text in the next step:", source: "artifact_text" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(input).toHaveValue("Use this loaded file text in the next step:");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using file text");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Use this loaded file text in the next step:");
  });

  it("replaces hand-written text when editing a previous message", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const input = screen.getByPlaceholderText("Message Affent...");

    await user.type(input, "new thought");
    rerender(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "list the files", source: "previous_message" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(input).toHaveValue("list the files");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Editing previous message");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Replaced");
    expect(screen.getByRole("button", { name: "Send edited" })).toBeEnabled();
  });

  it("removes imported draft context with the draft", async () => {
    const user = userEvent.setup();
    const props = {
      disabled: false,
      busy: false,
      draft: { id: 1, content: "Use this output in the next step:", source: "tool_result" as const },
      onSubmit: vi.fn(),
      onCancel: vi.fn(),
    };
    const { rerender } = render(
      <Composer
        {...props}
      />,
    );

    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using action output");
    await user.click(screen.getByRole("button", { name: "Remove" }));

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("");
    expect(screen.queryByTestId("composer-context")).toBeNull();
    expect(screen.getByRole("button", { name: "Send" })).toBeDisabled();

    rerender(<Composer {...props} />);

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("");
    expect(screen.queryByTestId("composer-context")).toBeNull();
  });

  it("removes appended external context without deleting hand-written text", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const input = screen.getByPlaceholderText("Message Affent...");

    await user.type(input, "compare this output");
    rerender(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Use this loaded file text in the next step:", source: "artifact_text" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    expect(input).toHaveValue("compare this output\n\nUse this loaded file text in the next step:");

    await user.click(screen.getByRole("button", { name: "Remove" }));

    expect(input).toHaveValue("compare this output");
    expect(screen.queryByTestId("composer-context")).toBeNull();
    expect(screen.getByRole("button", { name: "Send" })).toBeEnabled();
  });

  it("focuses the message box when requested by the shell", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    await user.tab();
    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
    await user.tab();
    expect(screen.getByPlaceholderText("Message Affent...")).not.toHaveFocus();

    rerender(<Composer disabled={false} busy={false} focusSignal={1} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveFocus();
  });

  it("clears an editable draft from the composer", async () => {
    const user = userEvent.setup();
    render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const input = screen.getByPlaceholderText("Message Affent...");

    await user.type(input, "draft to remove");
    expect(screen.getByRole("button", { name: "Clear" })).toBeVisible();

    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(input).toHaveValue("");
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();

    await user.type(input, "another draft");
    await user.keyboard("{Escape}");
    expect(input).toHaveValue("");
  });

  it("accepts dropped text and appends it to the message", async () => {
    const user = userEvent.setup();
    render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const composer = screen.getByTestId("composer");
    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "review this");

    fireEvent.dragEnter(composer, {
      dataTransfer: { getData: () => "", files: [] },
    });
    expect(composer).toHaveAttribute("data-dragging", "true");
    expect(screen.getByRole("status")).toHaveTextContent("Drop into message");

    fireEvent.drop(composer, {
      dataTransfer: {
        getData: (type: string) => (type === "text/plain" ? "extras/webui/src/App.tsx" : ""),
        files: [],
      },
    });

    expect(composer).toHaveAttribute("data-dragging", "false");
    expect(screen.queryByRole("status")).toBeNull();
    expect(input).toHaveValue("review this\nextras/webui/src/App.tsx");
  });

  it("uses dropped file names when no text payload exists", () => {
    render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const composer = screen.getByTestId("composer");
    const input = screen.getByPlaceholderText("Message Affent...");
    fireEvent.drop(composer, {
      dataTransfer: {
        getData: () => "",
        files: [new File([""], "README.md"), new File([""], "package.json")],
      },
    });

    expect(input).toHaveValue("README.md\npackage.json");
  });
});

function runtime(research: RuntimeCapabilityView["research"]): RuntimeCapabilityView {
  return {
    headline: "Local work only",
    detail: "Web search and browser are off",
    tone: research === "ready" ? "ready" : research === "unknown" ? "unknown" : "warning",
    research,
    chips: [],
  };
}
