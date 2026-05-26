import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import type { ToolCallState } from "../store/sessionState";
import { ToolCallCard } from "./ToolCallCard";

describe("ToolCallCard", () => {
  it("shows artifact file and size in the details view", async () => {
    const user = userEvent.setup();
    const call = artifactCall();

    render(<ToolCallCard call={call} events={[]} />);

    expect(screen.getByRole("button", { name: /shell/ })).toBeInTheDocument();

    await user.click(screen.getByRole("button", { name: /shell/ }));

    expect(screen.getByText("full output")).toBeInTheDocument();
    expect(screen.getByText("000001-c1.txt (8 KiB, 1 MiB omitted)")).toBeInTheDocument();
  });

  it("surfaces memory updates while collapsed and expands the saved content", async () => {
    const user = userEvent.setup();
    const call = memoryAddCall();

    render(<ToolCallCard call={call} events={[]} />);

    const toggle = screen.getByRole("button", { name: /memory/ });
    expect(toggle).toHaveTextContent("Saved memory");
    expect(toggle).toHaveTextContent("memory:markets");
    expect(toggle).toHaveTextContent("MEM-STOCK-73");

    await user.click(toggle);

    expect(screen.getByTestId("memory-update-card")).toHaveTextContent("Saved memory");
    expect(screen.getByTestId("memory-update-card")).toHaveTextContent("source-led confidence");
  });
});

function artifactCall(): ToolCallState {
  return {
    callId: "c1",
    tool: "shell",
    args: { command: "cat big.log" },
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    exitCode: 0,
    durationMs: 1250,
    resultSummary: "report body",
    result: "report body",
    resultTruncated: true,
    resultBytes: 8192,
    resultOmittedBytes: 1048576,
    resultCapBytes: 8192,
    resultArtifactPath: ".affent/artifacts/tool-results/000001-c1.txt",
  };
}

function memoryAddCall(): ToolCallState {
  return {
    callId: "c2",
    tool: "memory",
    args: {
      action: "add",
      target: "memory",
      topic: "markets",
      content: "Alpha Coast market reports must include marker MEM-STOCK-73 and source-led confidence.",
    },
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    exitCode: 0,
    durationMs: 7,
    resultSummary: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\",\"message\":\"added\"}",
    result: "{\"ok\":true,\"target\":\"memory\",\"topic\":\"markets\",\"message\":\"added\"}",
    resultTruncated: false,
  };
}
