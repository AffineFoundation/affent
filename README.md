# affent

Small, embeddable **agent loop core** for any environment that talks to
an OpenAI-compatible chat completions endpoint (OpenAI, vLLM, Chutes,
SGLang, OpenRouter, Anthropic-via-OpenAI-compat, ...).

Drop into different scenarios that share a single loop:

- [`affine-agents`](https://github.com/AffineFoundation/affine-agents) —
  multi-user web platform: chat UI, per-session Docker sandboxes,
  schedules, Postgres + Redis.
- training / RL rigs that need an agent rollout
- batch eval pipelines (SWE-bench-style harness etc.)
- one-off scripts via `affentctl`

Tiny dep graph: `uuid` + `zerolog` + stdlib.

## What you get

**The loop** (`loop.go`)

- streaming LLM responses with first-class **reasoning** channel
  (`reasoning_content`), persisted on `ChatMessage.ReasoningContent`
  and surfaced as separate `thinking.delta` + `thinking.done` SSE
  events. Reasoning is local-only — stripped from outbound requests
  via `wireMessage` since DeepSeek / Kimi / GLM emit it on responses
  but reject it on inbound.
- **cancellation** mid-turn (parent ctx cancel beats any in-flight
  retry; visible content already streamed makes a stream-cut error
  non-retryable to keep the UI's delta accumulator coherent)
- **transient-error retry** for HTTP 408 / 429 / 5xx, network resets,
  mid-stream EOF, per-call timeouts; honors server `Retry-After`
  header (capped at `MaxRespectedRetryAfter = 5m`)
- **stream watchdog**: `StreamIdleTimeout=60s` between chunks,
  `StreamPostFinishTimeout=5s` after `finish_reason` to defend
  against upstream proxies that forget to send `[DONE]`
- **budgets**: `MaxTurnSteps` (assistant↔tool round trips per user
  turn, default 10), `PerCallTimeout` (per chat completion, default
  3m), `MaxTransientRetries` (default 3, exponential backoff)
- **tool result caps**: `MaxToolResultBytesInContext=8KiB` (what the
  model sees), `MaxToolResultPreviewInEvent=4KiB` (what the SSE event
  carries) — full bytes still go to consumers that care
- **monotonic event ids**: every SSE event carries a per-loop
  sequential `id` so trace consumers can detect drops, order events,
  and tell when filtered events were skipped (see `--trace-skip-deltas`)

**Context compaction** (`compaction.go`)

- `Compactor` interface; default `LLMSummaryCompactor` implements
  rolling summarization using OpenHands V1's
  [`summarizing_prompt.j2`](https://github.com/OpenHands/software-agent-sdk/blob/main/openhands-sdk/openhands/sdk/context/condenser/prompts/summarizing_prompt.j2)
  verbatim — structured fields (`USER_CONTEXT` / `TASK_TRACKING` /
  `COMPLETED` / `PENDING` / `CODE_STATE` / `TESTS` / `CHANGES` /
  `DEPS` / `VERSION_CONTROL_STATUS`), example-driven, with hard
  `PRESERVE TASK IDs` semantics.
- two activation paths: **proactive** (msg count > `TriggerMsgs`,
  default 240) and **reactive** (upstream returns context-overflow
  4xx — matched against keyword set covering OpenAI / DeepSeek /
  Kimi / Anthropic phrasings — emergency compact + retry outside the
  transient-retry budget).
- preserves head (`KeepFirst=2`) + rolling summary (single user
  message tagged `[summary of earlier work]`) + tail (`KeepLast=10`),
  with a boundary fixer that refuses to sever an
  `assistant.tool_calls` from its `role=tool` replies.

**Tools**

- builtin: `shell`, `read_file`, `write_file`, `edit_file`,
  `list_files`. File tools are sandboxed by `safeWorkspacePath`:
  relative paths join onto the workspace, absolute paths are taken
  literally and must fall inside the workspace (no sentinel /
  trim-the-leading-slash hacks).
- `shell` runs through an `executor.Executor` interface — `LocalExecutor`
  for in-process / scripts, your own impl for Docker / Firecracker /
  remote.
- **MCP**: stdio + streamable-http (spec rev 2025-03-26). Plug in any
  number of MCP servers — their tools surface as `<server>_<tool>`
  alongside the builtins.

**Persistence**

- `Conversation`: append-only JSONL chat log on disk, includes system
  prompt + user/assistant/tool messages with `tool_call_id` preserved
  for resume. `Replace()` rewrites atomically (used by the compactor
  after summarizing earlier turns).

**Observability**

- 13 SSE event types streamed on a single channel — see below.
- `affentctl --trace <path>` mirrors every event into a JSONL file
  for replay / regression diffing (`-` for stdout, empty for stderr).
- `--trace-skip-deltas` drops `thinking.delta` / `message.delta` from
  the trace (skipped events still consume sequence ids so consumers
  can tell what was filtered). Useful for batch eval / training where
  token-level replay isn't needed; the final text is in
  `thinking.done` / `message.done` regardless.

## Layout

```
loop.go            agent loop (LLM <-> tools, streaming, reasoning, cancel)
llm.go             OpenAI-compat streaming client (incl. reasoning_content, watchdog, retry classification)
builtins.go        shell, read_file, write_file, edit_file, list_files (workspace-sandboxed)
compaction.go      Compactor interface + LLMSummaryCompactor (OpenHands V1 prompt)
conversation.go    JSONL-on-disk chat log, append-only + atomic Replace
tool.go            Tool + Registry
executor/          Executor interface + LocalExecutor (in-process)
sse/               canonical event type constants + payload structs
mcp/               stdio + streamable-http MCP client; Registry adapter
cmd/affentctl/     CLI: run / chat / sessions
extras/            opt-in helper packages, separate sub-modules
  web/             web_fetch + web_search (HTML→markdown, Tavily search default)
```

The `extras/` directory holds **separate Go sub-modules** that aren't
in the import graph of root `affent` — sandboxed eval / training rigs
that don't want network access (or extra deps in their go.sum) simply
don't import them. See [extras/web README usage](#using-extrasweb)
below.

## SSE events (the wire contract)

Every loop emits these on `Loop.Events`. UIs, trace files, and tests
all consume the same stream.

Naming: **`.done`** means a streaming accumulator is complete (more
events for the same turn may still follow). **`.end`** is reserved for
turn-level boundaries (no more events for that turn).

| type             | when                                              |
|------------------|---------------------------------------------------|
| `turn.start`     | user message accepted, turn starts                |
| `user.message`   | echoes the user's text (so SSE replays are full)  |
| `thinking.delta` | model's reasoning channel, token by token         |
| `thinking.done`  | reasoning accumulation complete (full text)       |
| `message.delta`  | model's visible content, token by token           |
| `message.done`   | assistant message complete (full text)            |
| `tool.request`   | model called a tool — name + args                 |
| `tool.output`    | (gateway-only today) live stdout/stderr stream    |
| `tool.result`    | tool finished — exit code + truncated preview     |
| `file.changed`   | filesystem mutation (gateway watcher hook)        |
| `usage`          | input / output token totals for the turn          |
| `turn.end`       | reason: `completed` / `cancelled` / `error`       |
| `error`          | transient (recoverable=true) or terminal failure  |

## affentctl (the CLI)

```
affentctl run --prompt "..." --workspace ./task        # one-shot
affentctl chat --workspace ./task                       # REPL
affentctl sessions --workspace ./task                   # list past sessions
```

`run` and `chat` accept `--session-id <id>` or `--continue` to resume
an existing conversation. Logs persist as JSONL under
`<workspace>/.affentctl/<session_id>.jsonl`.

### Flags

| flag                    | default                                          | env                    |
|-------------------------|--------------------------------------------------|------------------------|
| `--workspace`           | `./affent-workspace`                             |                        |
| `--base-url`            |                                                  | `AFFENTCTL_BASE_URL`   |
| `--api-key`             |                                                  | `AFFENTCTL_API_KEY`    |
| `--model`               |                                                  | `AFFENTCTL_MODEL`      |
| `--prompt`              | (run) literal, `-` for stdin, `@file`            |                        |
| `--max-turns`           | 10                                               |                        |
| `--max-call-timeout`    | 3m                                               |                        |
| `--retry-transient`     | 3                                                |                        |
| `--retry-backoff`       | 4s (doubles each attempt)                        |                        |
| `--trace`               | stderr; `-` stdout, `<path>` JSONL file          |                        |
| `--trace-skip-deltas`   | false (set true for batch eval — drop deltas)    |                        |
| `--system-prompt`       | builtin (dev-box flavored); `-` / file / literal |                        |
| `--quiet`               | false                                            |                        |
| `--session-id`          | new session                                      |                        |
| `--continue`            | resume newest session under `--workspace`        |                        |
| `--mcp-config`          |                                                  | `AFFENTCTL_MCP_CONFIG` |
| `--compact-trigger`     | 240 (matches OpenHands V1 max_size); 0 disables  |                        |
| `--compact-keep-last`   | 10                                               |                        |

### MCP config

`--mcp-config FILE` plugs in any number of MCP servers; their tools
are exposed alongside the builtins, namespaced `<server>_<tool>`.
Each server picks its transport from whichever field is set: `URL`
for streamable-http, `command` for stdio. Setting both is an error.

```json
{
  "servers": [
    {
      "name": "fs",
      "command": "npx",
      "args": ["-y", "@modelcontextprotocol/server-filesystem", "/tmp/task"]
    },
    {
      "name": "git",
      "command": "uvx",
      "args": ["mcp-server-git", "--repository", "/tmp/task"]
    },
    {
      "name": "verify",
      "url": "http://host.docker.internal:8123/mcp",
      "headers": {"X-Auth-Token": "secret"}
    }
  ]
}
```

`Headers` (HTTP only) layers extra HTTP headers onto every request —
useful for auth tokens, version pinning. Sessions are tracked via
`Mcp-Session-Id` automatically.

## Quickstart

```bash
go build -o /tmp/affentctl ./cmd/affentctl

/tmp/affentctl run \
  --workspace /tmp/task \
  --base-url  https://api.openai.com/v1 \
  --api-key   "$OPENAI_API_KEY" \
  --model     gpt-4o-mini \
  --prompt    "list files in /tmp/task and summarize what's there"
```

## Embedding affent in your own program

```go
import (
    "github.com/affinefoundation/affent"
    "github.com/affinefoundation/affent/executor"
    "github.com/affinefoundation/affent/sse"
)

reg := affent.NewRegistry()
affent.RegisterBuiltins(reg, affent.BuiltinDeps{
    Executor:         executor.NewLocalExecutor("session-1", "/tmp/task"),
    HostWorkspaceDir: "/tmp/task",
})

conv, _ := affent.NewConversation("/tmp/task", "session-1")
events := make(chan sse.Event, 256)

llm := affent.NewLLMClient(baseURL, apiKey, model)
loop := &affent.Loop{
    LLM:    llm,
    Tools:  reg,
    Conv:   conv,
    Events: events,
    // Optional: shrink history when it grows beyond TriggerMsgs.
    Compactor: &affent.LLMSummaryCompactor{
        LLM:         llm,
        TriggerMsgs: 240,
        KeepFirst:   2,
        KeepLast:    10,
    },
    // MaxTurnSteps, PerCallTimeout, MaxTransientRetries are all
    // optional — zero falls back to the documented defaults.
}
_ = loop.EnsureSystemPrompt("")  // "" = use DefaultSystemPrompt

turnID, err := loop.SendUser(ctx, "list files and summarize")
// drain events until you see turn.end with matching turn_id
```

The default system prompt assumes a "dev box" environment (a
`/home/agent` + `/workspace` bind-mounted into a container) and
mentions `schedule_*` tools that the gateway registers. If you're
embedding affent outside that environment, pass your own prompt to
`EnsureSystemPrompt`.

## Using extras/web

`extras/web` ships `web_fetch` and `web_search` as opt-in tools. It's a
separate Go sub-module — `go get github.com/affinefoundation/affent`
won't pull `golang.org/x/net` or any search-backend deps unless you
also `go get .../extras/web`.

```go
import (
    "github.com/affinefoundation/affent"
    affentweb "github.com/affinefoundation/affent/extras/web"
)

reg := affent.NewRegistry()
affent.RegisterBuiltins(reg, deps)

// Just the fetch tool — no external API key needed.
affentweb.RegisterFetch(reg, affentweb.FetchConfig{})

// Both fetch + search; default Tavily backend reads TAVILY_API_KEY.
affentweb.RegisterAll(reg, affentweb.Options{})

// Custom search provider (Brave, SearXNG, internal index, …):
tool, _ := affentweb.SearchTool(affentweb.SearchConfig{Provider: myProvider})
reg.Add(tool)
```

`SearchProvider` is the seam — implement `Search(ctx, query, n) ([]SearchResult, error)`
and pass it via `Options.SearchProvider` or `SearchConfig.Provider`.

## Status

**Working**:

- native loop with reasoning streaming (`thinking.delta` + `thinking.done`)
  + cancel; transient-error retry with `Retry-After` honor + stream
  watchdog
- multi-turn REPL + session resume
- MCP stdio + streamable-http (spec rev 2025-03-26)
- workspace path sandboxing in builtin file tools (relative + absolute,
  no sentinel hacks)
- context compaction (OpenHands V1 LLMSummarizingCondenser prompt
  verbatim, rolling summary with `[summary of earlier work]` marker,
  proactive + reactive paths, tool-call boundary safety)
- monotonic event ids + `--trace-skip-deltas` for batch-eval traces
- `wireMessage` strips `reasoning_content` from outbound requests
  (DeepSeek / Kimi / GLM compat)

**Out of scope** (intentional):

- TodoWrite / structured task tracking — Claude Code-style policy,
  belongs in the embedder. affent provides the `Registry` mechanism;
  embedders register their own todo tool with whatever state-machine
  they prefer.
- IDE integration / GUI — affent is server-side / batch / training
  shaped, not IDE-shaped.
- OpenTelemetry traces — wrap externally if needed; the SSE event
  stream is the in-tree observability.
