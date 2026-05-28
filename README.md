# Affent

Affent is an agentic software-work system for turning AI coding agents into
durable engineering operators.

The project is built around a larger shift in how software work can happen.
Instead of forcing agents to live inside a terminal transcript, a TUI, or an IDE
extension, Affent treats the browser as the primary operating surface for real
engineering tasks. The agent should be able to do real work through one
coherent, auditable workspace: plan the task, inspect the code, make changes,
verify results, recover after interruption, and explain what happened.

## Quick Start

Start the full runtime and embedded Workbench with Docker:

```bash
AFFENTCTL_BASE_URL=https://api.openai.com/v1 \
AFFENTCTL_API_KEY="$OPENAI_API_KEY" \
AFFENTCTL_MODEL=gpt-4o-mini \
make image-serve-up
```

Then open:

```text
http://127.0.0.1:7777
```

This builds the Affent image, starts `affentserve`, serves the embedded
Workbench, and persists session state under `.tmp/runtime-workspace/`.

Use `make image-serve-status`, `make image-serve-logs`,
`make image-serve-restart`, and `make image-serve-stop` to inspect or manage
the local runtime container.

Use `AFFENTSERVE_*` variables when you want server-specific values to override
the shared `AFFENTCTL_*` defaults. For example:

```bash
AFFENTSERVE_BASE_URL=http://host.docker.internal:8000/v1 \
AFFENTSERVE_API_KEY=local \
AFFENTSERVE_MODEL=qwen3-coder \
make image-serve-up
```

For detailed Docker, CLI, and deployment options, see the
[technical manual](docs/technical-manual.md).

## Direction

Affent's runtime is built around durable session truth. The important parts of
a session are stored as ordinary inspectable files, and every surface reads from
that same state instead of inventing its own version of what the agent is doing.

The Workbench is the product surface for that truth. It is not meant to be a
decorative chat shell or a terminal clone. Its job is to make the agent's work
understandable and controllable while it is happening, from the current task and
evidence to changes, verification, memory, skills, config, and trace.

The agent system is developed against long-running behavior, not only short
prompt demos. Runtime mechanisms such as loop protocols, compaction, recovery,
tool governance, memory, skills, and delegation need trace evidence and eval
coverage. The point is to make drift, repeated tool misuse, stale context, and
failed recovery visible enough to fix.

Affent is intentionally file-backed where possible. Memory and skills should be
durable, reviewable, replaceable, and useful across sessions without turning
the model prompt into an unbounded dump of old context. Tool calls should have
clear limits, failure kinds, and next steps so the system can recover from
errors instead of hiding them in prose.

## Project Shape

The main runtime lives in `internal/agent`, with `cmd/affentserve` exposing the
HTTP service and embedded Workbench, `cmd/affentctl` providing the CLI, and
`cmd/affenteval` running scenario evaluation. The React Workbench is in
`extras/webui`; browser and web extraction support live under `extras/browser`
and `extras/web`.

Design and operating details are kept in the project documentation:
[architecture](docs/architecture.md), [technical manual](docs/technical-manual.md),
[event trace contract](docs/event-trace-contract.md),
[eval JSONL contract](docs/eval-jsonl-contract.md),
[focused tasks](docs/focused-tasks.md), and
[browser access architecture](docs/browser-access-architecture.md).

Affent is early and moving quickly. The current work is mostly about making the
agent runtime reliable enough for real software tasks before expanding product
surface area too aggressively.
