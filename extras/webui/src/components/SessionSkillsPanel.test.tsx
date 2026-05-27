import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionSkillsPanel } from "./SessionSkillsPanel";

describe("SessionSkillsPanel", () => {
  it("lists skill titles and summaries, then loads full content on expand", async () => {
    const user = userEvent.setup();
    const onReadSkill = vi.fn(async () => ({
      name: "coding_repair_workflow",
      description: "Repair code by reproducing failures first.",
      source: "embed:skill",
      runtime: false,
      body_bytes: 96,
      body: "AFFENT ACTIVE SKILL: coding_repair_workflow\nReproduce first.",
    }));
    render(
      <SessionSkillsPanel
        skills={[
          {
            name: "coding_repair_workflow",
            description: "Repair code by reproducing failures first.",
            source: "embed:skill",
            runtime: false,
            triggers: ["fix", "repair"],
            required_tools: ["workspace"],
            body_preview: "AFFENT ACTIVE SKILL: coding_repair_workflow",
            body_bytes: 96,
          },
        ]}
        installEnabled
        onReadSkill={onReadSkill}
        onInstallSkill={vi.fn()}
      />,
    );

    await user.click(screen.getByText("1 skill"));
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("coding_repair_workflow");
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("Repair code by reproducing failures first.");
    expect(screen.getByTestId("session-skills-panel")).toHaveTextContent("1 triggerable");
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("2 triggers");
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("1 tool");
    expect(screen.getByTestId("session-skills-list")).not.toHaveTextContent("embed:skillembed:skill");

    await user.click(screen.getByText("coding_repair_workflow"));

    expect(onReadSkill).toHaveBeenCalledWith("coding_repair_workflow");
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("Source: embed:skill");
    expect(await screen.findByText(/Reproduce first/)).toBeInTheDocument();
  });

  it("submits a manually entered skill", async () => {
    const user = userEvent.setup();
    const onInstallSkill = vi.fn(async (request) => ({
      name: request.name,
      description: request.description,
      source: "user",
      runtime: true,
      body_bytes: request.body.length,
      body: request.body,
    }));
    render(<SessionSkillsPanel skills={[]} installEnabled onReadSkill={vi.fn()} onInstallSkill={onInstallSkill} />);

    await user.click(screen.getByText("0 skills"));
    await user.click(screen.getByRole("button", { name: "Add skill" }));
    await user.type(screen.getByLabelText("Name"), "manual_demo");
    await user.type(screen.getByLabelText("Summary"), "Manual workflow.");
    await user.type(screen.getByLabelText("Triggers"), "manual demo");
    await user.type(screen.getByLabelText("Required tools"), "workspace, browser");
    await user.type(screen.getByLabelText("Full content"), "AFFENT ACTIVE SKILL: manual_demo\nUse this workflow.");
    await user.click(screen.getByRole("button", { name: "Save skill" }));

    expect(onInstallSkill).toHaveBeenCalledWith({
      name: "manual_demo",
      description: "Manual workflow.",
      body: "AFFENT ACTIVE SKILL: manual_demo\nUse this workflow.",
      triggers: ["manual demo"],
      required_tools: ["workspace", "browser"],
    });
    expect(within(screen.getByTestId("session-skills-list")).queryByText("manual_demo")).toBeNull();
  });

  it("keeps empty skills state factual and avoids unusable search", () => {
    render(<SessionSkillsPanel skills={[]} defaultOpen />);

    const panel = screen.getByTestId("session-skills-panel");
    expect(panel).toHaveTextContent("0 skills");
    expect(panel).toHaveTextContent("No reusable workflows listed.");
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("No skills listed.");
    expect(screen.queryByPlaceholderText("Search title or summary")).toBeNull();
    expect(panel).not.toHaveTextContent("Built-in workflows ready");
    expect(panel).not.toHaveTextContent("No matching skills.");
  });

  it("separates empty search results from an empty skills list", async () => {
    const user = userEvent.setup();
    render(
      <SessionSkillsPanel
        defaultOpen
        skills={[
          {
            name: "coding_repair_workflow",
            description: "Repair code by reproducing failures first.",
            source: "embed:skill",
            runtime: false,
            body_bytes: 96,
          },
        ]}
      />,
    );

    expect(screen.getByTestId("session-skills-panel")).toHaveTextContent("1 built in");
    await user.type(screen.getByPlaceholderText("Search title or summary"), "browser");

    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("No matching skills.");
    expect(screen.getByTestId("session-skills-list")).not.toHaveTextContent("No skills listed.");
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const diagnostic = "API route /v1/skills returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<SessionSkillsPanel error={diagnostic} />);

    const summary = within(screen.getByTestId("session-skills-panel")).getByText("Skills unavailable").closest("summary");
    expect(summary).toHaveTextContent("Skills API failed: API route /v1/skills returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Skills unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
  });
});
