import { describe, expect, it } from "vitest";
import { argsRepaired, completedSubagentTree, resultTruncated, toolError } from "../fixtures/scenarios";
import { reduceRawEvents } from "../store/reduce";
import { buildExecutionTree } from "./executionTree";

describe("buildExecutionTree", () => {
  it("promotes user-readable task titles while retaining raw tool names for search and copy", () => {
    const [subagent, focused] = buildExecutionTree(reduceRawEvents(completedSubagentTree).turns[0]);

    expect(subagent.title).toBe("Find the WebUI trace requirements");
    expect(subagent.label).toBe("Delegated work");
    expect(subagent.subtitle).toBe("Delegated worker");
    expect(subagent.tool).toBe("subagent_run");
    expect(subagent.preview).toContain("Conclusion: WebUI must render trace details");
    expect(subagent.children.find((child) => child.tool === "MCP_search")).toMatchObject({
      label: "MCP action",
      title: "Search",
      subtitle: "External MCP service",
      tool: "MCP_search",
    });
    expect(subagent.tokenUsage).toEqual({ inputTokens: 310, outputTokens: 82, totalTokens: 392, costUsd: undefined });
    expect(subagent.contextEstimatedTokens).toBe(186);
    expect(subagent.metrics).toEqual(expect.arrayContaining([
      { label: "merged", value: "~186 tokens" },
      { label: "tokens", value: "392 tokens" },
      { label: "input", value: "310" },
      { label: "output", value: "82" },
    ]));
    expect(focused.title).toBe("Verify trace tree requirements");
    expect(focused.label).toBe("Focused work · verify");
    expect(focused.subtitle).toBe("Focused worker");
    expect(focused.tool).toBe("run_task");
    expect(focused.preview).toBe("Trace UI needs hierarchical detail for focused tasks and subagents.");
    expect(focused.tokenUsage).toEqual({ inputTokens: 220, outputTokens: 58, totalTokens: 278, costUsd: undefined });
    expect(focused.contextEstimatedTokens).toBe(195);
  });

  it("summarizes shell work by the command instead of the raw tool name", () => {
    const [shell] = buildExecutionTree(reduceRawEvents(resultTruncated).turns[0]);

    expect(shell.title).toBe("cat big.log");
    expect(shell.label).toBe("Command");
    expect(shell.preview).toBe("line 1 line 2 …(truncated)");
    expect(shell.subtitle).toBeUndefined();
    expect(shell.tool).toBe("shell");
    expect(shell.contextEstimatedTokens).toBe(1024);
    expect(shell.metrics).toContainEqual({ label: "merged", value: "~1024 tokens" });
  });

  it("uses plain titles for simple shell directory listings", () => {
    const turn = reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      { id: 2, type: "tool.request", data: { turn_id: "t1", call_id: "c1", tool: "shell", args: { command: "ls" } } },
      { id: 3, type: "tool.result", data: { call_id: "c1", exit_code: 0, result: "a", result_summary: "a" } },
      { id: 4, type: "tool.request", data: { turn_id: "t1", call_id: "c2", tool: "shell", args: { command: "ls -la docs/" } } },
      { id: 5, type: "tool.result", data: { call_id: "c2", exit_code: 0, result: "b", result_summary: "b" } },
      { id: 6, type: "tool.request", data: { turn_id: "t1", call_id: "c3", tool: "shell", args: { command: "ls docs && pwd" } } },
      { id: 7, type: "tool.result", data: { call_id: "c3", exit_code: 0, result: "c", result_summary: "c" } },
    ]).turns[0];

    const [current, docs, compound] = buildExecutionTree(turn);

    expect(current.title).toBe("List current directory");
    expect(docs.title).toBe("List docs");
    expect(compound.title).toBe("ls docs && pwd");
  });

  it("extracts actionable Next guidance from failed tool output", () => {
    const [shell] = buildExecutionTree(reduceRawEvents(toolError).turns[0]);

    expect(shell.status).toBe("error");
    expect(shell.nextHint).toBe("check the Makefile path");
    expect(shell.preview).toBe("make: *** No rule to make target. Stop.");
  });

  it("preserves repair comparison fields from tool requests", () => {
    const [tool] = buildExecutionTree(reduceRawEvents(argsRepaired).turns[0]);

    expect(tool.originalTool).toBe("readFile");
    expect(tool.originalArgsSummary).toBe("{\"filename\":\"main.go\"}");
    expect(tool.repairNotes).toEqual(["renamed readFile -> read_file", "coerced filename -> path"]);
    expect(tool.args).toEqual({ path: "main.go" });
  });

  it("uses a plain title for current-directory file listing", () => {
    const [listFiles] = buildExecutionTree(reduceRawEvents([
      { id: 1, type: "turn.start", data: { turn_id: "t1" } },
      {
        id: 2,
        type: "tool.request",
        data: {
          turn_id: "t1",
          call_id: "c1",
          tool: "list_files",
          args: { path: "." },
        },
      },
      { id: 3, type: "tool.result", data: { call_id: "c1", exit_code: 0, result: "a", result_summary: "a" } },
    ]).turns[0]);

    expect(listFiles.title).toBe("List current directory");
    expect(listFiles.label).toBe("File action");
  });
});
