import { describe, expect, it } from "vitest";
import type { TurnState } from "../store/sessionState";
import { buildTurnNavigatorView } from "./turnNavigator";

describe("turnNavigator view model", () => {
  it("marks the latest completed turn as current", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          userText: "inspect repo",
          toolCalls: [tool({ resultSummary: "README.md\nmain.go", result: "README.md\nmain.go" })],
        }),
        turnNumber: 1,
      },
      { turn: turn({ id: "t2", userText: "summarize findings" }), turnNumber: 2 },
    ]);

    expect(view.countLabel).toBe("2 messages");
    expect(view.summary).toBe("2 done · 1 action");
    expect(view.items.map((item) => item.current)).toEqual([false, true]);
    expect(view.items[0].summary).toBe("inspect repo");
    expect(view.items[0].activityLabel).toBe("Action summary");
    expect(view.items[0].activitySummary).toBe("README.md main.go");
    expect(view.current?.turnNumber).toBe(2);
    expect(view.current?.summary).toBe("summarize findings");
    expect(view.items[1].messageAriaLabel).toBe("Message 2: summarize findings (current)");
    expect(view.items[1].href).toBe("#turn-2");
  });

  it("marks the running turn as current before later completed history", () => {
    const view = buildTurnNavigatorView([
      { turn: turn({ id: "t1", status: "running", userText: "current work" }), turnNumber: 1 },
      { turn: turn({ id: "t2", userText: "older imported turn" }), turnNumber: 2 },
    ]);

    expect(view.summary).toBe("1 working");
    expect(view.items.map((item) => item.current)).toEqual([true, false]);
    expect(view.current?.turnNumber).toBe(1);
    expect(view.items[0].statusLabel).toBe("Working");
    expect(view.items[0].statusTone).toBe("running");
  });

  it("adds a pending follow-up as the current conversation step", () => {
    const view = buildTurnNavigatorView(
      [
        { turn: turn({ id: "t1", userText: "inspect repo" }), turnNumber: 1 },
      ],
      { text: "explain main.go" },
    );

    expect(view.countLabel).toBe("2 messages");
    expect(view.summary).toBe("1 sending");
    expect(view.items.map((item) => item.current)).toEqual([false, true]);
    expect(view.current).toMatchObject({
      id: "__pending__",
      turnNumber: 2,
      href: "#pending-turn",
      summary: "explain main.go",
      statusLabel: "Sending",
      statusTone: "running",
      pending: true,
    });
    expect(view.current?.activitySummary).toBe("Affent will add the next update here.");
  });

  it("includes processed activity in navigation labels", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          userText: "delegate docs inspection",
          toolCalls: [
            tool({
              tool: "subagent_run",
              args: { task: "Find requirements" },
              result: JSON.stringify({
                report: "Conclusion:\nWebUI needs a digest before the execution tree.",
                tool_calls: [{ tool: "read_file", args: { path: "docs/webui-product-design.md" }, exit_code: 0 }],
              }),
              resultSummary: "Subagent checked docs.",
            }),
          ],
        }),
        turnNumber: 1,
      },
    ]);

    expect(view.current?.activityLabel).toBe("Result");
    expect(view.current?.activitySummary).toBe("WebUI needs a digest before the execution tree.");
    expect(view.current?.messageAriaLabel).toBe(
      "Message 1: delegate docs inspection — Result: WebUI needs a digest before the execution tree. (current)",
    );
  });

  it("reports completed tool failures as handled work instead of attention", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          userText: "research affine",
          assistantText: "I found enough information to answer.",
          toolCalls: [
            tool({ status: "error" }),
            tool({ callId: "c2", status: "success" }),
          ],
        }),
        turnNumber: 1,
      },
    ]);

    expect(view.summary).toBe("1 done · 1 handled · 2 actions");
    expect(view.current?.statusLabel).toBe("Done");
    expect(view.current?.statusTone).toBe("completed");
  });

  it("keeps attention for failed turns without a final answer", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          userText: "research affine",
          toolCalls: [tool({ status: "error" })],
        }),
        turnNumber: 1,
      },
    ]);

    expect(view.summary).toBe("1 need attention · 1 action");
    expect(view.current?.statusTone).toBe("error");
  });

  it("treats a max-turn attempt as continued after a later message takes over", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          status: "max_turns",
          userText: "research affine",
          toolCalls: [tool({ status: "error" })],
        }),
        turnNumber: 1,
      },
      {
        turn: turn({
          id: "t2",
          userText: "continue with sources",
          assistantText: "Final report.",
          toolCalls: [tool({ callId: "c2" })],
        }),
        turnNumber: 2,
      },
    ]);

    expect(view.summary).toBe("1 done · 1 continued · 2 actions");
    expect(view.items[0].statusLabel).toBe("Continued");
    expect(view.items[0].statusTone).toBe("muted");
    expect(view.items[0].activityLabel).toBe("Handoff");
    expect(view.items[0].activitySummary).toBe("Ran 1 action; 1 issue carried forward; message 2 continued the task.");
  });

  it("keeps continued attempts separate from handled tool issues", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          status: "max_turns",
          userText: "research affine",
          toolCalls: [tool({ status: "error" })],
        }),
        turnNumber: 1,
      },
      {
        turn: turn({
          id: "t2",
          userText: "continue with sources",
          assistantText: "Final report.",
          toolCalls: [tool({ callId: "c2", status: "error" }), tool({ callId: "c3" })],
        }),
        turnNumber: 2,
      },
    ]);

    expect(view.summary).toBe("1 done · 1 continued · 1 handled · 3 actions");
  });

  it("summarizes cumulative token use across conversation steps", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          userText: "research affine",
          usage: { inputTokens: 1000, outputTokens: 250 },
        }),
        turnNumber: 1,
      },
      {
        turn: turn({
          id: "t2",
          userText: "finish the report",
          usage: { inputTokens: 2000, outputTokens: 350 },
        }),
        turnNumber: 2,
      },
    ]);

    expect(view.summary).toBe("2 done · 3.6k tokens");
  });

  it("uses the assistant answer as the map digest when no action summary exists", () => {
    const view = buildTurnNavigatorView([
      {
        turn: turn({
          id: "t1",
          userText: "summarize affine",
          assistantText: "# Affine report\n\nAffine is Bittensor subnet 120 with an active GitHub repo.",
        }),
        turnNumber: 1,
      },
    ]);

    expect(view.current?.activityLabel).toBe("Answer");
    expect(view.current?.activitySummary).toBe("Affine report Affine is Bittensor subnet 120 with an active GitHub repo.");
    expect(view.current?.activityTone).toBe("success");
    expect(view.current?.messageAriaLabel).toBe(
      "Message 1: summarize affine — Answer: Affine report Affine is Bittensor subnet 120 with an active GitHub repo. (current)",
    );
  });
});

function turn(overrides: Partial<TurnState> = {}): TurnState {
  return {
    id: "t1",
    status: "completed",
    userText: "list the files",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [],
    ...overrides,
  };
}

function tool(overrides: Partial<TurnState["toolCalls"][number]> = {}): TurnState["toolCalls"][number] {
  return {
    callId: "c1",
    tool: "list_files",
    args: {},
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    resultTruncated: false,
    ...overrides,
  };
}
