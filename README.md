# affent

Small, embeddable **agent loop core** for any environment that talks to
an OpenAI-compatible chat completions endpoint (OpenAI, vLLM, Chutes,
SGLang, ...).

The loop is what's interesting: LLM ↔ tools, streaming responses with
reasoning, cancellation, transient-error retries, turn / call budgets,
JSONL conversation log on disk. Built-in tool surface is shell +
`read_file` / `write_file` / `edit_file` / `list_files`, optionally
extended with any number of MCP servers.

Designed to drop into different scenarios that share a single loop:

- [`affine-agents`](https://github.com/affine-io/affine-agents) —
  multi-user web platform: chat UI, per-session Docker sandboxes,
  schedules, Postgres + Redis.
- training / RL rigs that need an agent rollout
- batch eval pipelines
- one-off scripts via `affentctl`

Tiny dep graph: `uuid` + `zerolog` + stdlib.

## Layout

```
loop.go            agent loop (LLM <-> tools, streaming, reasoning, cancel)
llm.go             OpenAI-compat streaming client (incl. reasoning_content)
builtins.go        shell, read_file, write_file, edit_file, list_files
conversation.go    JSONL-on-disk chat log, append-only
tool.go            Tool + Registry
executor/          minimal "run a command" interface + LocalExecutor
sse/               event types streamed to UIs / trace files
mcp/               stdio MCP client (initialize, tools/list, tools/call)
cmd/affentctl/     CLI: run / chat / sessions
```

## affentctl (the CLI)

```
affentctl run --prompt "..." --workspace ./task        # one-shot
affentctl chat --workspace ./task                       # REPL
affentctl sessions --workspace ./task                   # list past sessions
```

`run` and `chat` accept `--session-id <id>` or `--continue` to resume
an existing conversation. Logs persist as JSONL under
`<workspace>/.affentctl/<session_id>.jsonl`.

Pass `--mcp-config FILE` to plug in any number of MCP servers; their
tools are exposed alongside the builtins, namespaced
`<server>_<tool>`. Example config:

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
)

reg := affent.NewRegistry()
affent.RegisterBuiltins(reg, affent.BuiltinDeps{
    Workspace: "/tmp/task",
    Executor:  executor.NewLocalExecutor(),
})

loop := &affent.Loop{
    LLM:      affent.NewLLMClient(baseURL, apiKey, model),
    Tools:    reg,
    // ... budgets, timeouts, hooks
}

result, err := loop.Run(ctx, conversation, userPrompt)
```

## Status

Working: native loop with reasoning streaming + cancel; transient-error
retry with `Retry-After` honor; multi-turn REPL + session resume; MCP
stdio client (filesystem / git / generic).

In flight: TodoWrite-style plan tool, MCP HTTP/SSE transport, batch
runner for evaluation pipelines.
