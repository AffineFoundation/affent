import { fireEvent, render, screen, waitFor, within } from "@testing-library/react";
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
    expect(screen.queryByTestId("composer-intent")).toBeNull();
    await user.type(input, "first line{Shift>}{Enter}{/Shift}second line");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-active", "true");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("2 lines");
    expect(input).toHaveValue("first line\nsecond line");
    expect(screen.queryByTestId("composer-automation")).toBeNull();

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

  it("keeps the composer keyboard-first when no session exists yet", () => {
    render(<Composer disabled={false} busy={false} hasSession={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByPlaceholderText("Message Affent...")).toBeVisible();
    expect(screen.queryByTestId("composer-intent")).toBeNull();
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
    expect(screen.getByRole("button", { name: "Add context or automation" })).toBeVisible();
  });

  it("uses structured loop setup intent and keeps scheduled prompts editable", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    const onStartLoop = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy={false} hasSession onSubmit={onSubmit} onStartLoop={onStartLoop} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    await user.click(screen.getByRole("button", { name: "Add context or automation" }));
    await user.click(within(screen.getByTestId("composer-add")).getByRole("button", { name: "Loop" }));

    expect((input as HTMLTextAreaElement).value).toBe("");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Loop setup");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Loop setup ready");
    expect(screen.getByTestId("composer-add")).not.toHaveAttribute("open");
    await user.type(input, "monitor release readiness");
    await user.keyboard("{Enter}");
    expect(onStartLoop).toHaveBeenCalledWith("monitor release readiness");
    expect(onSubmit).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Add context or automation" }));
    await user.click(within(screen.getByTestId("composer-add")).getByRole("button", { name: "Scheduled task" }));

    expect((input as HTMLTextAreaElement).value).toBe("Every day at UTC+8 09:00,");
    expect((input as HTMLTextAreaElement).value).not.toContain("Schedule or frequency:");
  });

  it("closes the add menu when clicking outside it", async () => {
    const user = userEvent.setup();
    render(<Composer disabled={false} busy={false} hasSession onSubmit={vi.fn()} onCancel={vi.fn()} />);

    await user.click(screen.getByRole("button", { name: "Add context or automation" }));
    expect(screen.getByTestId("composer-add")).toHaveAttribute("open");

    await user.click(screen.getByPlaceholderText("Message Affent..."));
    expect(screen.getByTestId("composer-add")).not.toHaveAttribute("open");
  });

  it("adds selected text files to the editable message", async () => {
    const user = userEvent.setup();
    render(<Composer disabled={false} busy={false} hasSession onSubmit={vi.fn()} onCancel={vi.fn()} />);

    await user.click(screen.getByRole("button", { name: "Add context or automation" }));
    expect(within(screen.getByTestId("composer-add")).getByRole("button", { name: "Upload file" })).toBeVisible();

    const fileInput = document.querySelector(".composer-file-input") as HTMLInputElement | null;
    expect(fileInput).not.toBeNull();
    await user.upload(fileInput as HTMLInputElement, new File(["export const ok = true;\n"], "sample.ts", { type: "text/typescript" }));

    const input = screen.getByPlaceholderText("Message Affent...");
    await waitFor(() => expect((input as HTMLTextAreaElement).value).toContain("Attached file: sample.ts"));
    expect((input as HTMLTextAreaElement).value).toContain("export const ok = true;");
  });

  it("treats saved history follow-ups like normal chat sends", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy={false} hasSession resumeSession onSubmit={onSubmit} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-resume-idle", "false");
    expect(screen.queryByTestId("composer-intent")).toBeNull();
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();

    await user.type(input, "continue with the next concrete step");

    expect(screen.getByTestId("composer")).toHaveAttribute("data-resume-idle", "false");
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
    await user.keyboard("{Enter}");
    expect(onSubmit).toHaveBeenCalledWith("continue with the next concrete step");
  });

  it("keeps risky saved-chat resumes explicit", async () => {
    const user = userEvent.setup();
    render(
      <Composer
        disabled={false}
        busy={false}
        hasSession
        resumeSession
        runtimeCapabilities={runtime("off")}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByPlaceholderText("Message Affent..."), "check latest market news");

    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Needs current sources");
    expect(screen.queryByRole("button", { name: "Send anyway" })).toBeNull();
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
    expect(screen.getByTestId("composer-context")).not.toHaveTextContent("Replaced");
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
  });

  it("shows a live turn state while busy", () => {
    render(<Composer disabled={false} busy hasSession onSubmit={vi.fn()} onCancel={vi.fn()} />);

    expect(screen.getByTestId("composer")).toHaveAttribute("data-busy", "true");
    expect(screen.getByTestId("composer")).toHaveAttribute("data-cancelling", "false");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Live run");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Type guidance while Affent works");
    expect(screen.getByRole("button", { name: "Stop" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Send guidance" })).toBeNull();
  });

  it("shows a stopping state after cancel is requested", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy cancelling hasSession onSubmit={onSubmit} onCancel={vi.fn()} />);

    await user.type(screen.getByPlaceholderText("Message Affent..."), "one more instruction");

    expect(screen.getByTestId("composer")).toHaveAttribute("data-cancelling", "true");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Stopping run");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Waiting for Affent to stop safely");
    expect(screen.getByRole("button", { name: "Stopping" })).toBeDisabled();
    expect(screen.queryByRole("button", { name: "Send guidance" })).toBeNull();

    await user.keyboard("{Enter}");

    expect(onSubmit).not.toHaveBeenCalled();
  });

  it("lets the user send guidance while a turn is running", async () => {
    const user = userEvent.setup();
    const onSubmit = vi.fn().mockResolvedValue(undefined);
    render(<Composer disabled={false} busy hasSession onSubmit={onSubmit} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "focus on the webui first");

    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Guidance ready");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Sends into this run, not a new chat");
    expect(screen.getByRole("button", { name: "Stop" })).toBeVisible();
    expect(screen.queryByRole("button", { name: "Send guidance" })).toBeNull();

    await user.keyboard("{Enter}");

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
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Needs current sources");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("paste URLs, docs, or files");
    expect(screen.queryByRole("button", { name: "Send anyway" })).toBeNull();
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
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Direct sources help");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("paste official URLs, docs, or files");
    expect(screen.queryByRole("button", { name: "Send anyway" })).toBeNull();
  });

  it("stays quiet before the first research task when capability details are not loaded yet", async () => {
    const user = userEvent.setup();
    render(
      <Composer
        disabled={false}
        busy={false}
        hasSession={false}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByPlaceholderText("Message Affent..."), "check latest market news");

    expect(screen.queryByTestId("composer-task-hint")).toBeNull();
    expect(screen.queryByRole("button", { name: "Start" })).toBeNull();
  });

  it("does not advertise normal workspace search capability in the composer", async () => {
    const user = userEvent.setup();
    render(
      <Composer
        disabled={false}
        busy={false}
        runtimeCapabilities={runtimeWithRepoSearch()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByPlaceholderText("Message Affent..."), "find the implementation of session capability wiring");

    expect(screen.queryByTestId("composer-task-hint")).toBeNull();
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
  });

  it("guides skill installation through the runtime skill workflow", async () => {
    const user = userEvent.setup();
    render(
      <Composer
        disabled={false}
        busy={false}
        runtimeCapabilities={runtimeWithSkillInstall()}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    await user.type(screen.getByPlaceholderText("Message Affent..."), "install a skill from github");

    expect(screen.getByTestId("composer-task-hint")).toHaveAttribute("data-tone", "ready");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("Skill install ready");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("inspect a skill source");
    expect(screen.getByTestId("composer-task-hint")).toHaveTextContent("ask for confirmation");
    expect(screen.getByTestId("composer-task-hint")).not.toHaveTextContent("propose_install");
    expect(screen.getByTestId("composer-task-hint")).not.toHaveTextContent("confirm_install");
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
  });

  it("does not show unconfirmed workspace or skill confirmation hints", async () => {
    const user = userEvent.setup();
    render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);

    const input = screen.getByPlaceholderText("Message Affent...");
    await user.type(input, "search the repo for the session capability wiring");
    expect(screen.queryByTestId("composer-task-hint")).toBeNull();

    await user.clear(input);
    await user.type(input, "install a skill from github");
    expect(screen.queryByTestId("composer-task-hint")).toBeNull();
  });

  it("loads suggested guidance while a turn is running", () => {
    render(
      <Composer
        disabled={false}
        busy
        hasSession
        draft={{ id: 1, content: "Guidance for current run:", source: "tool_guidance" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    const input = screen.getByPlaceholderText("Message Affent...");
    expect(input).toHaveValue("Guidance for current run:");
    expect(input).toHaveFocus();
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Guidance ready");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Guidance added");
    expect(screen.queryByRole("button", { name: "Send guidance" })).toBeNull();
  });

  it("replaces current text when editing sent guidance", async () => {
    const user = userEvent.setup();
    const { rerender } = render(<Composer disabled={false} busy hasSession onSubmit={vi.fn()} onCancel={vi.fn()} />);
    const input = screen.getByPlaceholderText("Message Affent...");

    await user.type(input, "scratch note");
    rerender(
      <Composer
        disabled={false}
        busy
        hasSession
        draft={{ id: 1, content: "Guidance for current run: check tests first", source: "guidance_receipt" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(input).toHaveValue("Guidance for current run: check tests first");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Editing sent guidance");
    expect(screen.getByTestId("composer-context")).not.toHaveTextContent("Replaced");
    expect(screen.queryByRole("button", { name: "Send guidance" })).toBeNull();
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
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Guidance added");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Added");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Continue: check the Makefile path");
    expect(screen.queryByRole("button", { name: "Send follow-up" })).toBeNull();
    expect(screen.queryByRole("button", { name: "Clear" })).toBeNull();
    expect(input).toHaveFocus();
  });

  it("replaces an existing imported draft with the next imported draft", async () => {
    const { rerender } = render(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Use this artifact in the next step: a.txt", source: "artifact" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );
    const input = screen.getByPlaceholderText("Message Affent...");
    expect(input).toHaveValue("Use this artifact in the next step: a.txt");

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
    expect(screen.getByTestId("composer-context")).not.toHaveTextContent("Replaced");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Use this loaded file text in the next step:");
  });

  it("labels imported context into an empty message as added, not replaced", () => {
    render(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Use this artifact in the next step: a.txt", source: "artifact" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Use this artifact in the next step: a.txt");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Artifact added to message");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Added");
    expect(screen.getByTestId("composer-context")).not.toHaveTextContent("Replaced");
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
    expect(screen.getByTestId("composer-context")).not.toHaveTextContent("Replaced");
    expect(screen.queryByRole("button", { name: "Send edited" })).toBeNull();
  });

  it("treats reply retries as a dedicated replace draft", () => {
    render(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Retry from this reply:\n\nThere are two files.", source: "retry_reply" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Retry from this reply:\n\nThere are two files.");
    expect(screen.getByTestId("composer-intent")).toHaveTextContent("Retry ready");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Retrying from reply");
    expect(screen.queryByRole("button", { name: "Retry" })).toBeNull();
  });

  it("labels imported error diagnostics as draft context", () => {
    render(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Continue after upstream_5xx: provider returned 503", source: "error" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Continue after upstream_5xx: provider returned 503");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using error diagnostic");
  });

  it("labels imported final-answer requests as draft context", () => {
    render(
      <Composer
        disabled={false}
        busy={false}
        draft={{ id: 1, content: "Do not call more tools. Produce the final answer.", source: "continuation" }}
        onSubmit={vi.fn()}
        onCancel={vi.fn()}
      />,
    );

    expect(screen.getByPlaceholderText("Message Affent...")).toHaveValue("Do not call more tools. Produce the final answer.");
    expect(screen.getByTestId("composer-context")).toHaveTextContent("Using final answer request");
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
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();

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
    expect(screen.queryByRole("button", { name: "Send" })).toBeNull();
  });

  it("focuses the message box when requested by the shell", async () => {
    const { rerender } = render(<Composer disabled={false} busy={false} onSubmit={vi.fn()} onCancel={vi.fn()} />);
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

function runtimeWithSkillInstall(): RuntimeCapabilityView {
  return {
    headline: "Local work only",
    detail: "Web search and browser are off",
    tone: "warning",
    research: "off",
    chips: [
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Skills", label: "Skill install", detail: "Can install and activate runtime skills without restarting.", tone: "ready" },
    ],
  };
}

function runtimeWithRepoSearch(): RuntimeCapabilityView {
  return {
    headline: "project",
    detail: "project",
    tone: "ready",
    research: "off",
    chips: [
      { group: "Files", label: "Files + commands", detail: "Can inspect files and run local commands.", tone: "ready" },
      { group: "Discovery", label: "Repo search", detail: "Can search workspace text before broad file reads.", tone: "ready" },
    ],
  };
}
