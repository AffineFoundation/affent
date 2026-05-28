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
    expect(panel).toHaveTextContent("1 failed command");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("npm test -- checkout.spec.ts");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("failed · exit 1 · 1.48s · turn 2");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("Cwd: extras/webui");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("checkout spec failed");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("Next: update payment route then rerun");
    expect(screen.getByTestId("session-run-list")).toHaveTextContent("Output artifact: .affent/artifacts/tool-results/test.txt");

    await user.click(within(screen.getByTestId("session-run-list")).getByRole("button", { name: "Copy command" }));
    expect(writeText).toHaveBeenCalledWith("npm test -- checkout.spec.ts");

    await user.click(within(screen.getByTestId("session-run-list")).getByRole("button", { name: "Open command output" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/test.txt");

    await user.click(within(screen.getByTestId("session-run-list")).getByRole("button", { name: "Rerun" }));

    expect(onUseAsDraft).toHaveBeenCalledWith(
      "Rerun this command and report the result:\nnpm test -- checkout.spec.ts\nWorking directory: extras/webui\nUse this recovery hint: update payment route then rerun",
      "run_command",
    );
  });

  it("keeps the panel folded by default", () => {
    render(<SessionRunPanel run={run} />);

    expect(screen.getByTestId("session-run-panel")).not.toHaveAttribute("open");
  });
});

const run: SessionRunView = {
  summary: "1 failed command",
  detail: "1 failed",
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
  ],
};
