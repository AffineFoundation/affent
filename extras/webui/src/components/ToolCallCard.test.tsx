import { render, screen, within } from "@testing-library/react";
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

  it("shows structured failure kinds without opening raw trace JSON", async () => {
    const user = userEvent.setup();
    const call = failureKindCall();

    render(<ToolCallCard call={call} events={[]} />);

    const toggle = screen.getByRole("button", { name: /web_fetch/ });
    expect(toggle).toHaveTextContent("dynamic_shell");

    await user.click(toggle);

    expect(screen.getByText("failure")).toBeInTheDocument();
    expect(screen.getByText("dynamic_shell, no_verified_source")).toBeInTheDocument();
  });

  it("surfaces source evidence status on web tool calls", async () => {
    const user = userEvent.setup();
    render(<ToolCallCard call={sourceAccessCall()} events={[]} />);

    const toggle = screen.getByRole("button", { name: /browser_navigate/ });
    expect(toggle).toHaveTextContent("partial source");

    await user.click(toggle);

    const details = screen.getByTestId("tool-details");
    expect(within(details).getByText("source")).toBeInTheDocument();
    expect(within(details).getByText(/partial source/)).toBeInTheDocument();
    expect(within(details).getByText("https://taostats.io/subnets/120")).toBeInTheDocument();
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

function failureKindCall(): ToolCallState {
  return {
    callId: "c3",
    tool: "web_fetch",
    args: { url: "https://taostats.io/subnets/120" },
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    exitCode: 0,
    failureKind: "dynamic_shell",
    failureKinds: ["dynamic_shell", "no_verified_source"],
    resultSummary: "Only a dynamic shell was available.",
    result: "Failure: kind=dynamic_shell",
    resultTruncated: false,
  };
}

function sourceAccessCall(): ToolCallState {
  return {
    callId: "c4",
    tool: "browser_navigate",
    args: { url: "https://taostats.io/subnets/120", wait_until: "networkidle" },
    argsTruncated: false,
    argsRepaired: false,
    canonicalized: false,
    status: "success",
    exitCode: 0,
    durationMs: 42,
    resultSummary: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence",
    result: "SourceAccess: browser_rendered_url=https://taostats.io/subnets/120; page_text_below=partial_dynamic_page_evidence; rendered_browser_source_status=partial_dynamic_page_evidence\nPAGE TEXT:\nMarket Cap",
    resultTruncated: false,
  };
}
