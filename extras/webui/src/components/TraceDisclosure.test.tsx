import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { normalizeEvents } from "../normalize/normalizeEvent";
import { TraceDisclosure } from "./TraceDisclosure";

describe("TraceDisclosure", () => {
  it("summarizes collapsed history with a readable first item", async () => {
    const user = userEvent.setup();

    render(
      <TraceDisclosure
        className="raw-history"
        events={normalizeEvents([
          { id: 0, type: "trace.meta", data: { schema_version: 1 } },
          {
            id: 1,
            type: "tool.request",
            data: {
              turn_id: "t1",
              call_id: "c1",
              tool: "read_file",
              args: { path: "README.md" },
            },
          },
        ])}
      />,
    );

    expect(screen.getByText("Raw trace")).toBeInTheDocument();
    expect(screen.getByText(/2 trace entries · Started action · read_file/)).toBeInTheDocument();

    await user.click(screen.getByText("Raw trace"));
    expect(screen.getByText("Metadata")).toBeInTheDocument();
    expect(screen.getByText("Started action")).toBeInTheDocument();
  });

  it("prefers tool result artifacts in the collapsed summary when they are available", async () => {
    const user = userEvent.setup();

    render(
      <TraceDisclosure
        className="raw-history"
        events={normalizeEvents([
          { id: 0, type: "trace.meta", data: { schema_version: 1 } },
          {
            id: 1,
            type: "tool.request",
            data: {
              turn_id: "t1",
              call_id: "c1",
              tool: "read_file",
              args: { path: "README.md" },
            },
          },
          {
            id: 2,
            type: "tool.result",
            data: {
              turn_id: "t1",
              call_id: "c1",
              exit_code: 0,
              duration_ms: 1250,
              result_summary: "Updated extras/webui/src/components/TraceDisclosure.tsx",
              result: "Updated extras/webui/src/components/TraceDisclosure.tsx",
              result_truncated: true,
              result_artifact_path: ".affent/artifacts/c1.txt",
            },
          },
        ])}
      />,
    );

    expect(screen.getByText(/3 trace entries · Action finished · artifact c1.txt/)).toBeInTheDocument();
    expect(screen.getByText(/Action finished · artifact c1.txt/)).toBeInTheDocument();

    await user.click(screen.getByText("Raw trace"));
    expect(screen.getByText("Action finished")).toBeInTheDocument();
  });
});
