import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionSkillsPanel } from "./SessionSkillsPanel";

describe("SessionSkillsPanel", () => {
  it("lists skill titles and summaries, then loads full content on expand", async () => {
    const user = userEvent.setup();
    const onUseAsDraft = vi.fn();
    const writeText = vi.fn().mockResolvedValue(undefined);
    Object.defineProperty(navigator, "clipboard", { configurable: true, value: { writeText } });
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
        onUseAsDraft={onUseAsDraft}
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
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("Origin: Built-in library");
    expect(await screen.findByText(/Reproduce first/)).toBeInTheDocument();

    await user.click(within(screen.getByTestId("session-skills-list")).getByRole("button", { name: "Copy details" }));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Skill evidence for coding_repair_workflow"));
    expect(writeText).toHaveBeenCalledWith(expect.stringContaining("Loaded content:"));

    await user.click(within(screen.getByTestId("session-skills-list")).getByRole("button", { name: "Start from skill" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("apply, update, or replace"), "skill");
    await user.click(within(screen.getByTestId("session-skills-list")).getByRole("button", { name: "Revise skill" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Review and update this reusable skill"), "skill");
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("Loaded content:"), "skill");
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

    await user.click(screen.getByText("No reusable workflows"));
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
    expect(screen.getByRole("status")).toHaveTextContent("manual_demo saved.");
    expect(within(screen.getByTestId("session-skills-list")).queryByText("manual_demo")).toBeNull();
  });

  it("keeps the manual skill form populated and reports install failures", async () => {
    const user = userEvent.setup();
    const onInstallSkill = vi.fn().mockRejectedValue(new Error("skill directory is read-only"));
    render(<SessionSkillsPanel skills={[]} installEnabled onReadSkill={vi.fn()} onInstallSkill={onInstallSkill} defaultOpen />);

    await user.click(screen.getByRole("button", { name: "Add skill" }));
    await user.type(screen.getByLabelText("Name"), "manual_demo");
    await user.type(screen.getByLabelText("Full content"), "AFFENT ACTIVE SKILL: manual_demo\nUse this workflow.");
    await user.click(screen.getByRole("button", { name: "Save skill" }));

    expect(screen.getByRole("status")).toHaveTextContent("skill directory is read-only");
    expect(screen.getByLabelText("Name")).toHaveValue("manual_demo");
    expect(screen.getByLabelText("Full content")).toHaveValue("AFFENT ACTIVE SKILL: manual_demo\nUse this workflow.");
  });

  it("keeps empty skills state factual and avoids unusable search", () => {
    render(<SessionSkillsPanel skills={[]} defaultOpen />);

    const panel = screen.getByTestId("session-skills-panel");
    expect(panel).toHaveTextContent("No reusable workflows");
    expect(panel).toHaveTextContent("No reusable workflows listed.");
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("No skills returned by this runtime.");
    expect(screen.queryByPlaceholderText("Search title or summary")).toBeNull();
    expect(panel).not.toHaveTextContent("Built-in workflows ready");
    expect(panel).not.toHaveTextContent("No matching skills.");
    expect(panel).not.toHaveTextContent("No skills listed.");
  });

  it("keeps an empty installable skills state tied to the save action", async () => {
    const user = userEvent.setup();
    render(<SessionSkillsPanel skills={[]} defaultOpen installEnabled onReadSkill={vi.fn()} onInstallSkill={vi.fn()} />);

    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("No reusable workflows saved yet.");
    await user.click(screen.getByRole("button", { name: "Add skill" }));
    expect(screen.getByLabelText("Name")).toBeVisible();
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
    expect(screen.getByTestId("session-skills-list")).not.toHaveTextContent("No skills returned by this runtime.");
  });

  it("shows why a skill matched search and clears the filter", async () => {
    const user = userEvent.setup();
    const onReadSkill = vi.fn(async () => ({
      name: "coding_repair_workflow",
      description: "Repair code by reproducing failures first.",
      source: "embed:skill",
      runtime: false,
      required_tools: ["workspace"],
      body_bytes: 96,
      body: "AFFENT ACTIVE SKILL: coding_repair_workflow\nUse workspace evidence.",
    }));
    render(
      <SessionSkillsPanel
        defaultOpen
        skills={[
          {
            name: "coding_repair_workflow",
            description: "Repair code by reproducing failures first.",
            source: "embed:skill",
            runtime: false,
            required_tools: ["workspace"],
            body_bytes: 96,
          },
          {
            name: "browser_source_workflow",
            description: "Verify browser network evidence.",
            source: "embed:skill",
            runtime: false,
            triggers: ["browser"],
            body_bytes: 88,
          },
        ]}
        onReadSkill={onReadSkill}
      />,
    );

    await user.type(screen.getByPlaceholderText("Search title or summary"), "workspace");

    expect(screen.getByTestId("session-skills-search-count")).toHaveTextContent('1 skill matching "workspace"');
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("coding_repair_workflow");
    expect(screen.getByTestId("session-skills-list")).not.toHaveTextContent("browser_source_workflow");
    expect(screen.getByTestId("skill-search-matches-coding_repair_workflow")).toHaveTextContent("Tool: workspace");
    expect(onReadSkill).not.toHaveBeenCalled();

    await user.click(screen.getByRole("button", { name: "Load full content" }));
    expect(onReadSkill).toHaveBeenCalledWith("coding_repair_workflow");

    await user.click(screen.getByRole("button", { name: "Clear" }));

    expect(screen.queryByTestId("session-skills-search-count")).toBeNull();
    expect(screen.getByTestId("session-skills-list")).toHaveTextContent("browser_source_workflow");
  });

  it("surfaces a compact API diagnostic in the collapsed summary", async () => {
    const onUseAsDraft = vi.fn();
    const diagnostic = "API route /v1/skills returned the WebUI app shell. The affentserve build may not expose this route. Use the current affentserve build.";
    render(<SessionSkillsPanel error={diagnostic} onUseAsDraft={onUseAsDraft} />);

    const summary = within(screen.getByTestId("session-skills-panel")).getByText("Skills unavailable").closest("summary");
    expect(summary).toHaveTextContent("Skills API failed: API route /v1/skills returned the WebUI app shell.");
    expect(summary).not.toHaveTextContent("Use the current affentserve build");

    await userEvent.click(screen.getByText("Skills unavailable"));
    expect(screen.getByRole("alert")).toHaveTextContent(diagnostic);
    expect(screen.getByTestId("session-skills-fallback")).toHaveTextContent("Create a reusable workflow in chat");
    await userEvent.click(screen.getByRole("button", { name: "Create skill" }));
    await userEvent.type(screen.getByLabelText("Name"), "trace_debugging");
    await userEvent.type(screen.getByLabelText("Full content"), "AFFENT ACTIVE SKILL: trace_debugging\nFilter failures first.");
    await userEvent.click(screen.getByRole("button", { name: "Prepare skill draft" }));
    expect(onUseAsDraft).toHaveBeenCalledWith(expect.stringContaining("trace_debugging"), "skill");
    expect(screen.getByRole("status")).toHaveTextContent("trace_debugging draft prepared.");
  });
});
