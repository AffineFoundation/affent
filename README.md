# affent

Small, embeddable **agent loop core** for any environment that talks to
an OpenAI-compatible chat completions endpoint (OpenAI, vLLM, Chutes,
SGLang, ...).

Drop into different scenarios that share a single loop:

- [`affine-agents`](https://github.com/AffineFoundation/affine-agents) —
  multi-user web platform: chat UI, per-session Docker sandboxes,
  schedules, Postgres + Redis.
- training / RL rigs that need an agent rollout
- batch eval pipelines
- one-off scripts via `affentctl`

Tiny dep graph: `uuid` + `zerolog` + stdlib.

## What you get

**The loop** (`loop.go`)

- streaming LLM responses with first-class **reasoning** channel
  (`reasoning_content` / `<think>...</think>`) emitted as separate
  `thinking.delta` events
- **cancellation** mid-turn (parent ctx cancel beats any in-flight
  retry; visible content already streamed makes a stream-cut error
  non-retryable to keep the UI's delta accumulator coherent)
- **transient-error retry** for HTTP 408 / 429 / 5xx, network
  resets, mid-stream EOF, per-call timeouts; honors server
  `Retry-After` header (capped at `MaxRespectedRetryAfter = 5m`)
- **stream watchdog**: `StreamIdleTimeout=60s` between chunks,
  `StreamPostFinishTimeout=5s` after `finish_reason` to defend
  against upstream proxies that forget to send `[DONE]`
- **budgets**: `MaxTurnSteps` (assistant↔tool round trips per user
  turn, default 10), `PerCallTimeout` (per chat completion, default
  3m), `MaxTransientRetries` (default 3, exponential backoff)
- **tool result caps**: `MaxToolResultBytesInContext=8KiB` (what the
  model sees), `MaxToolResultPreviewInEvent=4KiB` (what the SSE event
  carries) — full bytes still go to consumers that care

**Tools**

- builtin: `shell`, `read_file`, `write_file`, `edit_file`,
  `list_files`. File tools are sandboxed: paths are resolved via
  `safeWorkspacePath` and refuse to escape the workspace root.
- `shell` runs through an `executor.Executor` interface — `LocalExecutor`
  for in-process / scripts, your own impl for Docker / Firecracker /
  remote.
- MCP (stdio transport): plug in any number of MCP servers; their
  tools surface as `<server>_<tool>` alongside builtins.

**Persistence**

- `Conversation`: append-only JSONL chat log on disk, includes system
  prompt + user/assistant/tool messages with tool_call IDs preserved
  for resume.

**Observability**

- 12 SSE event types streamed on a single channel — see below.
- `affentctl --trace <path>` mirrors every event into a JSONL file
  for replay / regression diffing (`-` for stdout, empty for stderr).

## Layout

```
loop.go            agent loop (LLM <-> tools, streaming, reasoning, cancel)
llm.go             OpenAI-compat streaming client (incl. reasoning_content, watchdog, retry classification)
builtins.go        shell, read_file, write_file, edit_file, list_files (workspace-sandboxed)
conversation.go    JSONL-on-disk chat log, append-only
tool.go            Tool + Registry
executor/          Executor interface + LocalExecutor (in-process)
sse/               canonical event type constants + payload structs
mcp/               stdio MCP client (initialize, tools/list, tools/call) + Registry adapter
cmd/affentctl/     CLI: run / chat / sessions
```

## SSE events (the wire contract)

Every loop emits these on `Loop.Events`. UIs, trace files, and tests
all consume the same stream.

| type             | when                                              |
|------------------|---------------------------------------------------|
| `turn.start`     | user message accepted, turn starts                |
| `user.message`   | echoes the user's text (so SSE replays are full)  |
| `thinking.delta` | model's reasoning channel, token by token         |
| `message.delta`  | model's visible content, token by token           |
| `message.end`    | assistant message complete                        |
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

| flag                  | default                                  | env                       |
|-----------------------|------------------------------------------|---------------------------|
| `--workspace`         | `./affent-workspace`                     |                           |
| `--base-url`          |                                          | `AFFENTCTL_BASE_URL`      |
| `--api-key`           |                                          | `AFFENTCTL_API_KEY`       |
| `--model`             |                                          | `AFFENTCTL_MODEL`         |
| `--prompt`            | (run) literal, `-` for stdin, `@file`    |                           |
| `--max-turns`         | 10                                       |                           |
| `--max-call-timeout`  | 3m                                       |                           |
| `--retry-transient`   | 3                                        |                           |
| `--retry-backoff`     | 4s (doubles each attempt)                |                           |
| `--trace`             | stderr; `-` stdout, `<path>` JSONL file  |                           |
| `--system-prompt`     | builtin (dev-box flavored); `-` / file / literal | |
| `--quiet`             | false                                    |                           |
| `--session-id`        | new session                              |                           |
| `--continue`          | resume newest session under `--workspace`|                           |
| `--mcp-config`        |                                          | `AFFENTCTL_MCP_CONFIG`    |

### MCP config

`--mcp-config FILE` plugs in any number of MCP servers; their tools
are exposed alongside the builtins, namespaced `<server>_<tool>`.

```json
{
  "servers": [
    {"name":"fs","command":"npx","args":["-y","@modelcontextprotocol/server-filesystem","/tmp/task"]},
    {"name":"git","command":"uvx","args":["mcp-server-git","--repository","/tmp/task"]}
  ]
}
```

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
    Workspace: "/tmp/task",
    Executor:  executor.NewLocalExecutor("session-1", "/tmp/task"),
})

conv, _ := affent.NewConversation("/tmp/task", "session-1")
events := make(chan sse.Event, 256)

loop := &affent.Loop{
    LLM:    affent.NewLLMClient(baseURL, apiKey, model),
    Tools:  reg,
    Conv:   conv,
    Events: events,
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

## Status

Working: native loop with reasoning streaming + cancel; transient-error
retry with `Retry-After` honor + stream watchdog; multi-turn REPL +
session resume; MCP stdio client (filesystem / git / generic);
workspace path sandboxing in builtin file tools.

In flight: TodoWrite-style plan tool, MCP HTTP/SSE transport, batch
runner for evaluation pipelines.
