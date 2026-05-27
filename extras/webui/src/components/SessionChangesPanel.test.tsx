import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionChangesPanel } from "./SessionChangesPanel";
import type { SessionChangesView } from "../view/sessionChanges";

describe("SessionChangesPanel", () => {
  it("renders changed files as evidence and creates an adjustment draft", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const onUseAsDraft = vi.fn();
    render(<SessionChangesPanel defaultOpen changes={changes} onOpenArtifact={onOpenArtifact} onUseAsDraft={onUseAsDraft} />);

    const panel = screen.getByTestId("session-changes-panel");
    expect(panel).toHaveAttribute("open");
    expect(panel).toHaveTextContent("2 changed files");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("src/payments.ts");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Edit · changed · turn 2");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Updated payment route");
    expect(screen.getByTestId("session-changes-list")).toHaveTextContent("Evidence artifact: .affent/artifacts/tool-results/edit.txt");

    await user.click(within(screen.getByTestId("session-changes-list")).getByRole("button", { name: "Open evidence" }));
    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/edit.txt");

    await user.click(within(screen.getByTestId("session-changes-list")).getAllByRole("button", { name: "Adjust" })[0]);

    expect(onUseAsDraft).toHaveBeenCalledWith("Review and adjust this changed file: src/payments.ts", "changed_file");
  });

  it("keeps the panel folded by default", () => {
    render(<SessionChangesPanel changes={changes} />);

    expect(screen.getByTestId("session-changes-panel")).not.toHaveAttribute("open");
  });
});

const changes: SessionChangesView = {
  summary: "2 changed files",
  detail: "2 changed",
  files: [
    {
      path: "src/payments.ts",
      operation: "edit",
      status: "changed",
      turnNumber: 2,
      actionCount: 1,
      detail: "Updated payment route",
      artifactPath: ".affent/artifacts/tool-results/edit.txt",
    },
    {
      path: "tests/payments.spec.ts",
      operation: "write",
      status: "changed",
      turnNumber: 2,
      actionCount: 1,
    },
  ],
};
