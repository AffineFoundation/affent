# Affent Architecture

Affent is organized around product entry points rather than a public Go SDK.
The supported external surfaces are:

- `affentctl`
- `affentserve`
- `affenteval`
- configuration files and environment variables
- durable state directories
- OpenAI-compatible HTTP request/response shapes
- Affent-native SSE events and JSONL traces

`internal/agent.Loop` is an implementation boundary used by those product
entry points. It is not a stable integration contract for downstream Go
programs.

## Runtime Boundary

The agent runtime lives in `internal/agent`. It owns the session execution
cycle:

- LLM streaming and retry handling.
- Tool registry and dispatch.
- Conversation persistence.
- Structured event publication.
- Context compaction.
- Memory wiring.
- Project-context loading.
- Session search wiring.
- Plan, skill, subagent, and focused-task runtime tools.
- Runtime caps and loop guards.

The runtime is intentionally tool-agnostic at its core. Tool families are
registered by callers according to configuration and deployment posture.

## Supporting Packages

Supporting packages sit behind narrower boundaries:

- `internal/executor`: local, Docker, and file-operation execution boundaries.
- `internal/memory`: file-backed persistent memory store.
- `internal/sessionsearch`: transcript retrieval implementation.
- `internal/projectcontext`: project instruction file loading.
- `internal/mcp`: MCP client and tool registration.
- `internal/sse`: event contract shared by CLI and HTTP surfaces.
- `internal/eventlog`: JSONL event persistence.
- `internal/planstate`: persisted plan state.
- `internal/agenteval`: scenario runner support.
- `internal/textutil`: shared UTF-8-safe truncation helpers.
- `internal/e2e`: cross-package end-to-end coverage.

Optional tool families live outside the core runtime:

- `extras/web`: web fetch and search tools.
- `extras/browser`: browser automation tools.

## Entry Points

`cmd/affentctl` is the local CLI. It drives one-shot runs, interactive sessions,
session resume, tracing, plans, memory, MCP, and executor selection.

`cmd/affentserve` is the HTTP runtime. It exposes OpenAI-compatible chat
completions plus native session lifecycle, event, history, tool, artifact,
transcript, health, model, and stats endpoints.

`cmd/affenteval` is the scenario runner. It executes bounded agent tasks,
collects runtime traces, and emits text or JSONL summaries for comparison.

## State Model

Affent treats state as inspectable operational data, not as hidden process
memory. Durable state is stored as ordinary files:

- Conversations and events use JSONL.
- Plans use JSON.
- Memory uses bounded topic files.
- `affentserve` keeps project/task memory per session by default; local
  single-user deployments can opt into shared `target=user` memory for stable
  preferences across sessions.
- Runtime skills are persisted as manifests and skill bodies.
- Oversized tool outputs can be stored as artifacts.
- Child delegation work can be stored as transcripts.

This keeps local debugging simple and lets the HTTP server survive container
restart when the state root is backed by a host volume.

## Event Model

The event stream is the main observability contract. Both CLI traces and server
SSE streams use the same event shape for turn boundaries, assistant output,
reasoning output, tool requests, tool results, usage, errors, and delegation
metadata.

Event consumers should rely on the documented event envelope and schema version,
not on internal runtime structs.

## Design Rules

- Keep product interfaces explicit: CLI flags, server config, HTTP routes,
  state layout, and event contracts matter more than preserving a root package.
- Keep root clean: implementation belongs in packages with clear ownership.
- Prefer internal packages until there is a concrete external consumer that
  justifies a stable Go API.
- Keep tools opt-in. Shell, file, browser, web, memory, MCP, skills, subagents,
  and focused tasks are registered by configuration.
- Bound every runtime surface that can grow: prompt input, tool arguments, tool
  results, events, memory, plans, child tasks, retries, and loops.
- Treat storage as recoverable. Operators should be able to inspect, replay,
  back up, or remove durable state without a database service.
- Keep eval and production paths close enough that trace-level lessons from one
  can improve the other.

## Non-Goals

- Affent is not a complete security sandbox. Tool execution still needs an
  external isolation boundary for untrusted workloads.
- Affent is not a multi-tenant control plane. Use isolated processes, state
  roots, credentials, and network boundaries when tenants differ.
- Affent is not a general-purpose Go framework. Its public contract is the
  product surface, not internal packages.
