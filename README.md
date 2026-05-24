# Affent

Affent is an OpenAI-compatible agent runtime for durable, tool-using AI
sessions. It sits between an LLM provider and a product surface, owning the
parts that make agents reliable in practice: session state, tool execution,
runtime limits, recovery, memory, traces, and deployment entry points.

Affent is not positioned as an embeddable Go SDK. Its integration surfaces are
`affentctl`, `affentserve`, durable state on disk, and structured event streams.

## Why Affent

Most agent prototypes start as a chat-completions call plus tools. Production
systems quickly need more:

- A session must survive multiple turns, retries, cancellation, and restarts.
- Tool output must be bounded before it pollutes every future model request.
- The runtime must show what happened, not just the final assistant message.
- Memory and prior sessions must be searchable without dumping everything into
  the prompt.
- Tool access must be explicit, scoped, and deployable behind a sandbox.
- Eval tooling needs trace evidence, not screenshots of chat transcripts.

Affent is built around those runtime concerns. The result is a small,
inspectable foundation for agent products, eval harnesses, and local workflows
that need more control than a raw provider SDK gives them.

## What Affent Does Well

- **OpenAI-compatible surface**: drive Affent through familiar chat-completions
  request shapes while still getting Affent-native session and event streams.
- **Durable sessions**: conversations, events, plans, memory, runtime skills,
  transcripts, and artifacts are persisted as files that can be inspected,
  replayed, backed up, or deleted deliberately.
- **Bounded autonomy**: per-turn step caps, tool-call budgets, output caps,
  retry limits, loop guards, and compaction keep long-running sessions from
  drifting into unbounded work.
- **Observable execution**: every turn can produce structured events for model
  deltas, reasoning deltas, tool requests, tool results, usage, errors,
  delegation, and turn endings.
- **Configurable tool surface**: shell, file, memory, session search, MCP, web,
  browser, subagent, focused-task, and skill tools are enabled intentionally
  instead of assumed globally.
- **Workspace-aware execution**: file tools are scoped to a workspace, and shell
  commands go through an executor boundary that can be local, Docker-backed, or
  replaced by a stronger sandbox.
- **Weak-model tolerance**: argument repair, tool-name canonicalization,
  explicit plans, focused tasks, and loop guards help less consistent models
  complete practical work with fewer unrecoverable failures.
- **Eval-ready traces**: `affenteval` consumes the same runtime events that UIs
  and operators see, so product behavior and benchmark behavior share evidence.

## Core Design

Affent is organized around product entry points rather than a root Go package.
The supported external surfaces are:

- `affentctl`: local CLI for one-shot runs, interactive sessions, plans,
  memory, tracing, MCP, and executor selection.
- `affentserve`: HTTP runtime exposing OpenAI-compatible chat completions plus
  native session, event, artifact, transcript, and stats endpoints.
- `affenteval`: scenario runner for repeatable agent-runtime checks.
- JSONL/JSON state files: durable conversation, event, plan, memory, skill,
  transcript, and artifact records.
- SSE events: the stable observability stream for UIs, replay, and eval.

The internal runtime owns the model cycle, tool dispatch, event publication,
conversation persistence, compaction, memory wiring, project context, session
search, delegation, and runtime limits. Optional capabilities are registered by
configuration, so deployments can keep their tool surface as small as the job
requires.

## Documentation

- [Technical manual](docs/technical-manual.md): build, run, configuration,
  Docker, server, state, tools, eval, and security notes.
- [Architecture](docs/architecture.md): package boundaries, entry points, and
  design rules.
- [Event trace contract](docs/event-trace-contract.md): runtime event envelope
  and compatibility rules.
- [Eval JSONL contract](docs/eval-jsonl-contract.md): machine-readable eval
  output schema.

## First Commands

Build the CLI through the project Docker path:

```bash
make affentctl
```

Check local configuration without calling a model:

```bash
./bin/affentctl doctor \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini
```

Run one prompt:

```bash
./bin/affentctl run \
  --workspace ./workspace \
  --base-url https://api.openai.com/v1 \
  --api-key "$OPENAI_API_KEY" \
  --model gpt-4o-mini \
  --prompt "Inspect this workspace and summarize the project."
```

Start the HTTP runtime locally:

```bash
make image-serve-up
```

For the full operating guide, see the
[technical manual](docs/technical-manual.md).
