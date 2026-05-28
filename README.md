# Affent

Affent is an OpenAI-compatible agent runtime for durable, tool-using AI
sessions. It owns the parts that make agents work in practice — session
state, tool execution, recovery, memory, traces, long-running discipline,
and bounded autonomy — and exposes them through a CLI, an HTTP service,
and a web Workbench.

## Why Affent

Real agent work is more than a chat-completions call plus tools. Sessions
need to survive multi-turn drift, restarts, and cancellation. Tool output
needs bounds before it floods every future model prompt. Long-running work
needs structured state, not rolling text. Memory and prior sessions need
to be searchable without dumping everything into the prompt. Tool access
needs to be scoped, governed, and revocable. Eval needs trace evidence,
not screenshots. The operator needs to see what actually happened, not
just the final assistant message.

Affent is built around those concerns. Every session is inspectable on
disk. Every event is on a versioned schema. Every tool call goes through
bounded execution. Every long-running task is observable in a Workbench
panel.

## What You Get

### Durable, inspectable sessions

Conversations, events, plans, memory, runtime skills, transcripts, and
artifacts are persisted as ordinary files. Inspect them with `tail`,
search them with `grep`, diff them across runs, back them up like any
other directory tree. The HTTP server resumes mid-stream after restart;
the CLI replays from the same trace files an evaluator reads.

### Bounded autonomy by default

Per-turn step caps, tool-call budgets, output caps, retry limits, loop
guards over repeated calls and repeated failures, structured context
compaction, and a hard cap on delegation depth. The runtime stops a
runaway turn before it drains the model context or the operator's wallet.

### Governed tool execution

Tool calls go through deterministic governance, not LLM-mediated etiquette.
Argument repair handles malformed JSON, schema-driven coercion, alias
rename, and wrapper unwrap. Loop guards catch repeated calls, repeated
failures, host-level fetch traps, and no-new-evidence patterns. Three
policy mechanisms — first-tool, post-result, and pre-dispatch — let the
runtime steer a turn without coupling the loop to feature-specific logic.

### Long-running discipline

The loop protocol gives a session a structured north-star file
(`LOOP.md`), a sidecar event log, and per-turn checkpoints. Long sessions
get a digest of the protocol fed back into context on a cadence; after
context compaction the next feed is a full re-injection. Plans persist
across turns and across restarts with explicit current-step semantics.
Compaction preserves delegated reports, artifact references, and the
loop-protocol anchor instead of summarizing them away.

### First-class delegation

`run_task` runs a typed, bounded child agent (recall, explore,
web-extract, research, verify, review) with a per-profile tool whitelist
and a structured output schema. `subagent_run` runs a looser child agent
in one of four exploratory modes when the work needs more freedom. Both
are bounded in budget and depth, observable through trace, and treated as
first-class delegation surfaces with their own delegation metadata on
every tool event.

### Skill-driven workflows

Skills are reusable workflow files with `auto_activation` rules and
required-tools gating. Built-in skills cover code repair, evidence
extraction, web-snapshot facts, and skill install workflows; runtime
skills go through a propose/confirm/install lifecycle so an agent can
suggest a new workflow without writing it silently.

### Workspace-scoped execution

File tools are scoped to a workspace. Shell commands go through an
executor abstraction — a `LocalExecutor` for plain CLI use, a
`DockerExecExecutor` for per-session container isolation, or any future
backend behind the same interface. Web fetch enforces an SSRF guard and
size caps. Browser tools record source-access evidence and capture
same-site XHR responses for dynamic dashboards.

### Eval as a first-class product surface

`affenteval` ships forty-plus scenarios across four suites covering
small-model tool stability, hard agent tasks, long-running recovery, and
live web evidence. Scenarios assert structured runtime evidence — tool
order, loop decisions, source-access quality, session search hits, plan
state, tool-repair kinds — not just final text. Failed scenarios emit a
`DebugRecoveryGuide` with the exact inspect commands and full-trace
reproduction line, so a regression is a five-minute investigation, not a
half-day grep.

### Web Workbench

`affentserve` serves a React Workbench that opens as a clean Chat by
default and reveals professional surfaces on demand — Workspace, Run,
Files, Changes, Memory, Skills, Automation, Config, Trace. Runtime
internals stay in the Trace surface; everyday use sees only what the
agent is doing and what it produced.

### OpenAI-compatible surface, plus native endpoints

Drive Affent through familiar chat-completions request shapes when an
existing client integration is the easier path. Use the native session,
event, history, plan, loop-protocol, schedule, memory, skill, artifact,
transcript, and stats endpoints when you want the full runtime contract.
Both speak the same SSE event stream.

## Documentation

- [Architecture](docs/architecture.md): package boundaries, runtime
  governance, state model, design rules.
- [Technical manual](docs/technical-manual.md): build, run, configuration,
  Docker, server, state, tools, eval, security.
- [Event trace contract](docs/event-trace-contract.md): runtime event
  envelope, payload schemas, compatibility rules.
- [Eval JSONL contract](docs/eval-jsonl-contract.md): machine-readable
  eval output schema and quality gates.
- [Focused tasks](docs/focused-tasks.md): typed delegation surface, task
  profiles, output contract.
- [Browser access architecture](docs/browser-access-architecture.md):
  rendered web access pipeline, source-access evidence, network capture.

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
