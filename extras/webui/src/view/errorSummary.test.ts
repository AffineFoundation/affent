import { describe, expect, it } from "vitest";
import { summarizeUserError } from "./errorSummary";

describe("summarizeUserError", () => {
  it("turns provider connection failures into a readable runtime summary", () => {
    expect(summarizeUserError(
      "llm_request",
      'chat request: Post "http://127.0.0.1:9/chat/completions": dial tcp 127.0.0.1:9: connect: connection refused',
    )).toEqual({
      title: "Runtime provider is unreachable",
      detail: "The model endpoint at 127.0.0.1:9 refused the connection.",
    });
  });

  it("summarizes upstream status and timeout errors without losing the code", () => {
    expect(summarizeUserError("upstream_5xx", "provider returned 503")).toEqual({
      title: "Provider returned an error",
      detail: "The model provider returned HTTP 503.",
    });
    expect(summarizeUserError("request_timeout", "deadline exceeded")).toEqual({
      title: "Provider request timed out",
      detail: "The model provider did not respond before the request timed out.",
    });
  });
});
