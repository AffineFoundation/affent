# Affent Architecture

Affent is organized around product entry points rather than a public Go SDK.
The supported external surfaces are `affentctl`, `affentserve`, configuration,
state directories, and the OpenAI-compatible HTTP shape exposed by the server.

## Runtime Boundary

The agent runtime lives in `internal/agent`. It owns the session loop, LLM
streaming, tool registry, built-in tools, conversation persistence, context
compaction, memory wiring, project context, and session search wiring.

Supporting packages sit behind narrower boundaries:

- `internal/memory`: file-backed persistent memory store.
- `internal/sessionsearch`: transcript retrieval implementation.
- `internal/projectcontext`: project instruction file loading.
- `internal/textutil`: shared UTF-8-safe truncation helpers.
- `internal/e2e`: cross-package end-to-end coverage.

The repository root intentionally has no Go package. It is the project doorway:
README, module metadata, and repo-level configuration.

## Entry Points

`cmd/affentctl` is the local CLI for one-shot runs, interactive sessions,
tracing, MCP, memory, project context, and executor selection.

`cmd/affentserve` is the HTTP runtime. It exposes OpenAI-compatible chat
completions, session event streams, session lifecycle endpoints, health, models,
and stats.

Optional tool families live outside the runtime:

- `extras/web`: web fetch and search tools.
- `extras/browser`: browser automation tools.
- `mcp`: MCP client and tool registration.
- `executor`: local, Docker, and file-operation execution boundaries.
- `sse`: event contract shared by CLI and HTTP surfaces.

## Design Rules

- Keep product interfaces explicit: CLI flags, server config, HTTP routes,
  state layout, and event contracts matter more than preserving a root package.
- Keep root clean: implementation belongs in packages with clear ownership.
- Prefer internal packages for runtime implementation until there is a real
  external consumer that justifies a stable Go package.
- Keep tools opt-in. Shell, file, browser, web, memory, and MCP capabilities are
  registered by configuration, not assumed globally.
- Treat storage as inspectable. Conversations and memory use files that can be
  reviewed and recovered without a database service.
