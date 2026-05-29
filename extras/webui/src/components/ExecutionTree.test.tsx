import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { normalizeEvents } from "../normalize/normalizeEvent";
import type { TurnState } from "../store/sessionState";
import { ExecutionTree } from "./ExecutionTree";

describe("ExecutionTree", () => {
  it("auto-opens the active running path and keeps manual collapse", async () => {
    const user = userEvent.setup();
    const turn = runningTurn();

    const { rerender } = render(<ExecutionTree turn={turn} events={[]} />);

    const node = screen.getByTestId("execution-node");
    expect(node).toHaveAttribute("data-active-path", "true");
    expect(node).toHaveAttribute("data-status", "running");
    expect(screen.getByRole("button", { name: /Command List current directory running/ })).toHaveAttribute("aria-expanded", "true");
    expect(screen.getByTestId("tool-details")).toBeVisible();

    await user.click(screen.getByRole("button", { name: /Command List current directory running/ }));
    expect(screen.getByRole("button", { name: /Command List current directory running/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByTestId("tool-details")).toBeNull();

    rerender(<ExecutionTree turn={turn} events={[]} />);
    expect(screen.getByRole("button", { name: /Command List current directory running/ })).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByTestId("tool-details")).toBeNull();
  });

  it("shows artifact file and size in the overview when a tool result was saved", async () => {
    const user = userEvent.setup();
    const turn = artifactTurn();

    render(<ExecutionTree turn={turn} events={[]} />);

    await user.click(screen.getByRole("button", { name: /Command cat report\.txt/ }));

    expect(screen.getByText("000001-c2.txt (8 KiB, 1 MiB omitted)")).toBeInTheDocument();
    expect(screen.getByText("output file")).toBeInTheDocument();
    expect(screen.getByText("Status done · Exit 0 · File 000001-c2.txt (8 KiB, 1 MiB omitted) · Limit truncated")).toBeInTheDocument();
  });

  it("opens artifact files from nested raw trace rows", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();
    const events = normalizeEvents([
      {
        id: 5,
        type: "tool.result",
        data: {
          turn_id: "t2",
          call_id: "c2",
          exit_code: 0,
          result_summary: "Saved full command output",
          result_truncated: true,
          result_artifact_path: ".affent/artifacts/tool-results/000001-c2.txt",
        },
      },
    ]);

    render(<ExecutionTree turn={artifactTurn()} events={events} sessionId="s1" onOpenArtifact={onOpenArtifact} />);

    await user.click(screen.getByRole("button", { name: /Command cat report\.txt/ }));
    await user.click(screen.getByText("Raw trace"));
    await user.click(screen.getByText("Action finished"));
    await user.click(within(screen.getByTestId("event-trace")).getByRole("button", { name: "Open artifact" }));

    expect(onOpenArtifact).toHaveBeenCalledWith(".affent/artifacts/tool-results/000001-c2.txt");
  });

  it("keeps the output file visible in the action summary when duration would otherwise crowd it out", async () => {
    const user = userEvent.setup();
    const turn = artifactTurn();
    turn.toolCalls[0] = {
      ...turn.toolCalls[0],
      durationMs: 1250,
    };

    render(<ExecutionTree turn={turn} events={[]} />);

    await user.click(screen.getByRole("button", { name: /Command cat report\.txt/ }));

    expect(screen.getByText(/Status done · Duration 1\.25s · Exit 0 · File 000001-c2\.txt \(8 KiB, 1 MiB omitted\) · Limit truncated/)).toBeInTheDocument();
  });

  it("shows delegated result size merged into the parent context", async () => {
    const user = userEvent.setup();
    const turn = delegatedTurn();

    render(<ExecutionTree turn={turn} events={[]} />);

    expect(screen.getByText("merged ~360 tokens")).toBeInTheDocument();
    await user.click(screen.getByRole("button", { name: /Delegated work Inspect docs/ }));
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Merged ~360 tokens");
  });

  it("shows when a tool output was trimmed before entering model context", async () => {
    const user = userEvent.setup();
    const turn = contextTrimmedTurn();

    render(<ExecutionTree turn={turn} events={[]} />);

    await user.click(screen.getByRole("button", { name: /Command cat big\.log/ }));

    expect(screen.getByText("context trimmed")).toBeInTheDocument();
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Tool context 4 KiB · 2 KiB omitted");
    expect(screen.getByText(/Status done · Exit 0 · Tool context 4 KiB · 2 KiB omitted/)).toBeInTheDocument();
  });

  it("keeps web fetch raw artifact storage out of the chat action details", async () => {
    const user = userEvent.setup();
    const onOpenArtifact = vi.fn();

    render(<ExecutionTree turn={webFetchArtifactTurn()} events={[]} sessionId="s1" onOpenArtifact={onOpenArtifact} />);

    await user.click(screen.getByRole("button", { name: /Web action Fetch example\.test/ }));

    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Status done");
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Duration 131ms");
    expect(screen.getByTestId("action-inspector-summary")).toHaveTextContent("Exit 0");
    expect(screen.getByTestId("action-inspector-summary")).not.toHaveTextContent("000001-call_a4fe2c62e5714c3b9140cf43.txt");
    expect(screen.getByTestId("action-inspector-summary")).not.toHaveTextContent("Tool context");
    expect(screen.queryByRole("button", { name: "Open artifact" })).toBeNull();
    expect(screen.queryByText("output file")).toBeNull();
  });
});

function runningTurn(): TurnState {
  return {
    id: "t1",
    status: "running",
    userText: "inspect docs",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [
      {
        callId: "c1",
        tool: "shell",
        args: { command: "ls" },
        argsTruncated: false,
        argsRepaired: false,
        canonicalized: false,
        status: "running",
        resultTruncated: false,
      },
    ],
  };
}

function artifactTurn(): TurnState {
  return {
    id: "t2",
    status: "completed",
    userText: "generate report",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [
      {
        callId: "c2",
        tool: "shell",
        args: { command: "cat report.txt" },
        argsTruncated: false,
        argsRepaired: false,
        canonicalized: false,
        status: "success",
        exitCode: 0,
        result: "report body",
        resultSummary: "report body",
        resultTruncated: true,
        resultBytes: 8192,
        resultOmittedBytes: 1048576,
        resultCapBytes: 8192,
        resultArtifactPath: ".affent/artifacts/tool-results/000001-c2.txt",
      },
    ],
  };
}

function delegatedTurn(): TurnState {
  return {
    id: "t3",
    status: "completed",
    userText: "delegate",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [
      {
        callId: "c3",
        tool: "subagent_run",
        args: { task: "Inspect docs" },
        argsTruncated: false,
        argsRepaired: false,
        canonicalized: false,
        status: "success",
        exitCode: 0,
        result: JSON.stringify({ summary: "done" }),
        resultSummary: "done",
        resultTruncated: false,
        contextBytes: 1440,
        contextOmittedBytes: 0,
        contextEstimatedTokens: 360,
      },
    ],
  };
}

function contextTrimmedTurn(): TurnState {
  return {
    id: "t4",
    status: "completed",
    userText: "inspect large output",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [
      {
        callId: "c4",
        tool: "shell",
        args: { command: "cat big.log" },
        argsTruncated: false,
        argsRepaired: false,
        canonicalized: false,
        status: "success",
        exitCode: 0,
        result: "large output preview",
        resultSummary: "large output preview",
        resultTruncated: false,
        contextBytes: 4096,
        contextOmittedBytes: 2048,
        contextEstimatedTokens: 1024,
      },
    ],
  };
}

function webFetchArtifactTurn(): TurnState {
  return {
    id: "t5",
    status: "completed",
    userText: "fetch page",
    thinkingText: "",
    thinkingStreaming: false,
    assistantText: "",
    messageStreaming: false,
    toolCalls: [
      {
        callId: "fetch",
        tool: "web_fetch",
        args: { url: "https://example.test" },
        argsTruncated: false,
        argsRepaired: false,
        canonicalized: false,
        status: "success",
        exitCode: 0,
        durationMs: 131,
        result: "Example Domain",
        resultSummary: "Example Domain",
        resultTruncated: false,
        resultBytes: 8192,
        resultOmittedBytes: 0,
        resultCapBytes: 262144,
        resultArtifactPath: ".affent/artifacts/tool-results/000001-call_a4fe2c62e5714c3b9140cf43.txt",
        contextBytes: 5632,
        contextOmittedBytes: 3174,
        contextEstimatedTokens: 1408,
      },
    ],
  };
}
