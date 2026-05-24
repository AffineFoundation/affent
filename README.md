# Affent

Affent is an OpenAI-compatible agent runtime for building durable,
tool-using AI sessions. It is not just a chat-completions wrapper: Affent owns
the agent execution cycle, tool execution boundary, session state, memory,
trace stream, and deployment surfaces that sit between an LLM provider and a
real product.

The project is designed for teams that need agents to do practical work in a
workspace, keep context across turns, expose inspectable traces, and run behind
interfaces that existing clients already understand.

## Why Affent Exists

Modern agent systems tend to grow the same runtime layer again and again:
streaming model calls, tool calls, cancellation, retry, resumable state,
context compaction, memory, sandboxed execution, MCP integration, event traces,
and evaluation hooks. Those concerns are easy to underestimate when the first
prototype is only a prompt plus a few tools.

Affent makes that layer explicit. It is not positioned as an embeddable Go SDK;
the intended integration surfaces are the CLI, the HTTP API, durable state on
disk, and structured event streams. The core idea is that an agent runtime
should be observable, bounded, and deployable:

- Observable: every meaningful runtime action can be emitted as structured
  events for UI, replay, debugging, and eval.
- Bounded: tool output, context growth, retries, plans, memory, and child work
  all have runtime limits rather than relying on model self-restraint.
- Deployable: the same runtime can be driven by a CLI, an HTTP service, Docker
  images, or eval harnesses without turning the internals into a public SDK.

## Core Design

Affent is organized around product entry points rather than a root Go library.
The supported surfaces are the CLI, the HTTP server, configuration, state
directories, event contracts, and OpenAI-compatible request/response shapes.
The runtime implementation stays behind internal package boundaries until a
stable external consumer justifies another public API.

The agent execution cycle is deliberately explicit. A session owns conversation
state, streams model output, dispatches tools, records events, persists logs,
and applies runtime limits. Optional capabilities such as shell, file
operations, memory, session search, web tools, browser automation, MCP,
subagents, and Focused Tasks are registered by configuration instead of being
assumed globally.

Persistent state is file-backed and inspectable. Conversations and events are
stored as JSONL, plans as JSON, and memory as bounded topic files. That keeps
local development simple, makes failure recovery straightforward, and gives
eval or UI tooling concrete artifacts to replay.

## What Is In The Box

- `affentctl`: local CLI for one-shot runs, interactive sessions, session
  resume, plans, memory, tracing, and sandboxed tool execution.
- `affentserve`: OpenAI-compatible HTTP runtime with durable sessions, native
  event streams, lifecycle endpoints, health, models, and stats.
- `affenteval`: runtime evaluation entry point for scenario-based checks.
- Built-in shell and file tools scoped through an executor boundary.
- Optional Docker sandbox and full runtime image paths.
- Optional memory, session search, MCP, web fetch/search, and browser tool
  families.
- Structured SSE events for frontends, traces, replay, and evaluation.

## Documentation

The README is intentionally a project overview. Operational details live in
focused documents:

- [Technical manual](docs/technical-manual.md): build, run, Docker, server,
  configuration, state, memory, tools, and MCP usage.
- [Architecture](docs/architecture.md): repository boundaries, entry points,
  and design rules.
- [Focused Tasks](docs/focused-tasks.md): isolated auxiliary task model for
  recall, exploration, research, verification, and review.
- [Event trace contract](docs/event-trace-contract.md): runtime event shape for
  UI, replay, and eval consumers.
- [Eval JSONL contract](docs/eval-jsonl-contract.md): trace format consumed by
  evaluation tooling.

## First Commands

Build the CLI through the project Docker path:

```bash
make affentctl
```

Run a local setup check without calling a model:

```bash
./bin/affentctl doctor \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini
```

Start the HTTP runtime locally:

```bash
make image-serve-up
```

For full command details and deployment variants, use the
[technical manual](docs/technical-manual.md).

## Project Status

Affent is still evolving around runtime behavior, event contracts, evaluation
coverage, and product surfaces. The guiding constraint is to keep the runtime
small enough to reason about while making the important operational boundaries
explicit: what tools are available, where state is stored, how sessions resume,
what the model saw, what the tools returned, and why a turn ended.
