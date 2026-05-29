import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionRunPanel } from "./SessionRunPanel";
import type { SessionRunView } from "../view/sessionRun";

describe("SessionRunPanel", () => {
  it("renders command status, recovery, output artifact, and rerun draft", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
    render(<SessionRunPanel defaultOpen run={run} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-run-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("2 commands");
    expect(screen.getByLabelText("Run summary")).toHaveTextContent("Failed");
    expect(screen.getByLabelText("Run summary")).toHaveTextContent("Passed");
    expect(screen.getByTestId("session-run-review")).toHaveTextContent("Unresolved failure");
    expect(screen.getByLabelText("Run review facts")).toHaveTextContent("Failures");
    expect(screen.getByLabelText("Run review facts")).toHaveTextContent("unresolved");
    expect(screen.getByLabelText("Run review facts")).toHaveTextContent("Output");
    expect(screen.getByLabelText("Run review facts")).toHaveTextContent("1");
    expect(screen.getByLabelText("Search commands")).toBeInTheDocument();
    const focus = screen.getByTestId("session-run-focus");
    expect(focus).toHaveTextContent("Recovery needed");
    expect(focus).toHaveTextContent("npm test -- checkout.spec.ts");
    expect(focus).toHaveTextContent("failed");
    expect(focus).toHaveTextContent("1.48s");
    expect(focus).toHaveTextContent("Turn");
    expect(focus).toHaveTextContent("2");
    expect(focus).toHaveTextContent("Status");
    expect(focus).toHaveTextContent("Kind");
    expect(focus).toHaveTextContent("test");
    expect(focus).toHaveTextContent("Duration");
    expect(focus).toHaveTextContent("Cwd: extras/webui");
    expect(focus).toHaveTextContent("Next: update payment route then rerun");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("npm run build");
    expect(screen.getByTestId("session-run-list")).not.toHaveTextContent("npm test -- checkout.spec.ts");

    await user.click(within(focus).getByRole("button", { name: "Copy command" }));
    expect(writeText).toHaveBeenCalledWith("npm test -- checkout.spec.ts");
    await user.click(within(focus).getByRole("button", { name: "Open command output" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/test.txt");

    await user.click(within(focus).getByRole("button", { name: "Rerun as draft" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Rerun or recover from this command, then report the result:",
        "npm test -- checkout.spec.ts",
        "",
        "Run evidence for npm test -- checkout.spec.ts",
        "Status: failed",
        "Kind: test",
        "Exit: 1",
        "Duration: 1.48s",
        "Turn: 2",
        "Working directory: extras/webui",
        "Output: checkout spec failed",
        "Next: update payment route then rerun",
        "Output artifact: .affent/artifacts/tool-results/test.txt",
      ].join("\n"),
      "run_command",
    );

    await user.click(screen.getByText("Run command"));
    await user.type(screen.getByLabelText("Command"), "npm run build");
    await user.type(screen.getByLabelText("Working directory"), "extras/webui");
    await user.click(screen.getByRole("button", { name: "Use command as draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(
      [
        "Run this command in the session workspace, then report the exit code, working directory, and relevant output:",
        "npm run build",
        "Working directory: extras/webui",
      ].join("\n"),
      "run_command",
    );

    await user.type(screen.getByLabelText("Search commands"), "build");
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("Latest verification");
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("npm run build");
    expect(screen.queryByTestId("session-run-list")).toBeNull();
    await user.click(screen.getByRole("button", { name: "Clear" }));
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("Recovery needed");
    expect(screen.getByTestId("session-run-list")).not.toHaveTextContent("npm test -- checkout.spec.ts");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("npm run build");

    await user.click(within(screen.getByLabelText("Run filters")).getByRole("button", { name: /Passed/ }));
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("Latest verification");
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("npm run build");
    expect(screen.queryByTestId("session-run-list")).toBeNull();
    await user.click(within(screen.getByLabelText("Run filters")).getByRole("button", { name: /All/ }));
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("Recovery needed");

    await user.type(screen.getByLabelText("Search commands"), "missing command");
    expect(screen.queryByTestId("session-run-focus")).toBeNull();
    expect(screen.queryByTestId("session-run-list")).toBeNull();
    expect(panel).toHaveTextContent('No commands matching "missing command".');
  });

  it("keeps the panel folded by default", () => {
    render(<SessionRunPanel run={run} />);

    expect(screen.getByTestId("session-run-panel")).not.toHaveAttribute("open");
  });

  it("can run a command immediately or keep it as a draft", async () => {
    const user = userEvent.setup();
    const onRunCommand = vi.fn().mockResolvedValue(undefined);
    const onUseAsDraft = vi.fn();
    render(<SessionRunPanel defaultOpen run={run} onRunCommand={onRunCommand} onUseAsDraft={onUseAsDraft} />);

    expect(screen.getByTestId("session-run-quick")).toHaveTextContent("Quick commands");
    expect(screen.getByTestId("session-run-quick")).toHaveTextContent("Git status");
    await user.click(within(screen.getByTestId("session-run-quick")).getByRole("button", { name: "Git status" }));
    expect(onRunCommand).toHaveBeenCalledWith({ command: "git status --short --branch", cwd: "extras/webui" });

    const focus = screen.getByTestId("session-run-focus");
    await user.click(within(focus).getByRole("button", { name: "Rerun now" }));
    expect(onRunCommand).toHaveBeenCalledWith({ command: "npm test -- checkout.spec.ts", cwd: "extras/webui" });

    await user.click(screen.getByText("Run command"));
    await user.type(screen.getByLabelText("Command"), "npm test -- checkout.spec.ts");
    await user.type(screen.getByLabelText("Working directory"), "extras/webui");
    await user.click(screen.getByRole("button", { name: "Run now" }));

    expect(onRunCommand).toHaveBeenCalledWith({ command: "npm test -- checkout.spec.ts", cwd: "extras/webui" });
    expect(screen.getByLabelText("Command")).toHaveValue("");

    await user.type(screen.getByLabelText("Command"), "npm run build");
    expect(screen.queryByRole("button", { name: "Use command as draft" })).toBeNull();
  });

  it("surfaces the latest passed command as verification focus", () => {
    render(<SessionRunPanel defaultOpen run={{ ...run, summary: "1 passed command", detail: "1 passed", tone: undefined, commands: [run.commands[1]] }} />);

    const focus = screen.getByTestId("session-run-focus");
    expect(focus).toHaveTextContent("Latest verification");
    expect(focus).toHaveTextContent("npm run build");
    expect(focus).toHaveTextContent("production build passed");
    expect(focus).toHaveTextContent("passed");
    expect(focus).toHaveTextContent("Exit");
    expect(focus).toHaveTextContent("0");
    expect(screen.queryByTestId("session-run-list")).toBeNull();
  });

  it("treats earlier failures as recovered and can jump back to failure evidence", async () => {
    const user = userEvent.setup();
    render(
      <SessionRunPanel
        defaultOpen
        run={{
          ...run,
          commands: [
            { ...run.commands[0], turnNumber: 1, sequence: 1 },
            { ...run.commands[1], command: "npm test -- checkout.spec.ts", detail: "checkout spec passed", turnNumber: 2, sequence: 2 },
          ],
        }}
      />,
    );

    expect(screen.getByTestId("session-run-review")).toHaveTextContent("Recovered");
    expect(screen.getByTestId("session-run-review")).toHaveTextContent("1 earlier failure followed by a pass");
    expect(screen.getByLabelText("Run review facts")).toHaveTextContent("recovered");
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("Latest verification");
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("npm test -- checkout.spec.ts");

    await user.click(screen.getByRole("button", { name: "Inspect latest failure" }));

    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("Recovery needed");
    expect(screen.getByTestId("session-run-focus")).toHaveTextContent("npm test -- checkout.spec.ts");
    expect(screen.queryByTestId("session-run-list")).toBeNull();
  });
});

const run: SessionRunView = {
  summary: "2 commands",
  detail: "1 failed · 1 passed",
  tone: "error",
  commands: [
    {
      command: "npm test -- checkout.spec.ts",
      cwd: "extras/webui",
      status: "failed",
      turnNumber: 2,
      exitCode: 1,
      durationMs: 1480,
      detail: "checkout spec failed",
      next: "update payment route then rerun",
      artifactPath: ".affent/artifacts/tool-results/test.txt",
    },
    {
      command: "npm run build",
      cwd: "extras/webui",
      status: "passed",
      turnNumber: 1,
      exitCode: 0,
      durationMs: 3100,
      detail: "production build passed",
    },
  ],
};
