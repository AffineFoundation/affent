# Event And Trace Contract

Affent emits runtime events as JSON payloads over SSE and writes the same
`sse.Event` shape to trace JSONL files:

```json
{"id":1,"type":"tool.request","data":{...}}
```

## Trace Schema

New trace files start with a metadata record:

```json
{"id":0,"type":"trace.meta","data":{"schema_version":1}}
```

`schema_version` is the JSONL trace contract version. Consumers should treat a
missing `trace.meta` as a legacy trace with version `0`, and should reject
future versions they do not understand rather than silently mis-parsing them.

`affentctl` writes `trace.meta` only when creating a fresh trace. Resumed
sessions append to the existing trace and keep its original metadata.
`affentserve` applies the same rule to each durable session's `events.jsonl`
under the session state root.

`GET /v1/sessions/{id}/history` returns pages from that JSONL file. Its
`after` cursor is the zero-based JSONL line number (`next_after` in the previous
response), not `Event.id`; loop event ids are process-local and may repeat after
server restart.

`GET /v1/sessions/{id}/events` uses that same durable JSONL line number as the
SSE `id:` value. A client that reconnects with `Last-Event-ID: <cursor>` first
receives persisted records after that cursor, then continues with live events.
For `affentserve`, the SSE endpoint may reopen an inactive durable session so
the same reconnect contract works after a server or container restart.

## Event Envelope

Every event record has:

- `id`: monotonic event id. In `affentctl` traces this is the runtime loop id.
  In `affentserve` session logs and native session SSE it is the durable JSONL
  line cursor so reconnect and replay use the same ordering key. `trace.meta`
  uses `0` because it is a file header, not a loop event.
- `type`: event type string.
- `data`: type-specific JSON object.

Unknown event types must be ignored by default and may be counted for
diagnostics.

## Stable Event Types

### `trace.meta`

- `schema_version`: integer trace contract version.

### `turn.start`

- `turn_id`: runtime turn id.

### `user.message`

- `turn_id`: runtime turn id.
- `text`: user message text.

### `message.delta`

- `turn_id`: runtime turn id.
- `delta`: assistant text chunk.

### `message.done`

- `turn_id`: runtime turn id.
- `text`: complete assistant message text for the stream.
- `finish_reason`: optional upstream model finish reason.

### `thinking.delta`

- `turn_id`: runtime turn id.
- `delta`: reasoning text chunk when the provider exposes one.

### `thinking.done`

- `turn_id`: runtime turn id.
- `text`: complete reasoning text for the stream.

### `tool.request`

- `turn_id`: runtime turn id.
- `call_id`: model tool call id.
- `tool`: canonical runtime tool name.
- `args`: repaired argument object capped for event transport.
- `args_truncated`: whether event-level argument capping happened.
- `args_bytes`: repaired argument JSON byte count before event capping.
- `args_omitted_bytes`: original argument bytes omitted from `args`.
- `args_cap_bytes`: event cap used for arguments.
- `original_tool`: optional model-emitted tool name before canonicalization.
- `original_args_summary`: optional bounded preview of model-emitted args before
  repair.
- `canonicalized`: optional true when the runtime changed the tool name.
- `args_repaired`: optional true when the runtime repaired arguments.
- `repair_notes`: optional short diagnostics for canonicalization or argument
  repair.
- `delegation`: optional metadata when the tool call delegates work to a
  bounded child runtime.

### `tool.result`

- `turn_id`: runtime turn id. New runtime events include it so result-only
  consumers can filter by turn without joining against `tool.request`; older
  traces may omit it.
- `call_id`: model tool call id.
- `exit_code`: tool exit code. Non-zero means the call failed.
- `duration_ms`: optional measured implementation time for dispatched tools.
- `result_summary`: short UI preview, not parse-safe.
- `result`: event-capped tool output.
- `result_truncated`: whether event-level result capping happened.
- `result_bytes`: original result byte count before event capping.
- `result_omitted_bytes`: original result bytes omitted from `result`.
- `result_cap_bytes`: event cap used for results.
- `result_artifact_path`: optional workspace-relative path to the complete tool
  output when `result_truncated` is true and artifact persistence is enabled.
  In `affentserve`, this path is resolved under the durable session state root
  and exposed via `GET /v1/sessions/{id}/artifacts/{result_artifact_path}` so
  artifacts survive session workspace eviction and container restart.
- `delegation`: optional metadata mirroring the matching `tool.request` so
  result-only consumers can classify delegated work without joining events.

`delegation` fields:

- `kind`: stable delegation surface. Known values are `focused_task` and
  `subagent`.
- `task_type`: optional focused task type when `kind` is `focused_task`.
- `mode`: optional subagent mode when `kind` is `subagent`.

### `usage`

- `turn_id`: runtime turn id.
- `input_tokens`: input tokens reported by the provider.
- `output_tokens`: output tokens reported by the provider.

### `turn.end`

- `turn_id`: runtime turn id.
- `reason`: one of `completed`, `cancelled`, `error`, or `max_turns`.
- `tool_stats`: optional per-turn tool metrics.

`tool_stats` fields are optional and default to zero:

- `tool_requests`
- `tool_name_canonicalized`
- `tool_args_repaired`
- `tool_repair_calls`: count of tool calls that were repaired or
  canonicalized.
- `tool_repair_succeeded`: repaired/canonicalized calls whose final
  `tool.result.exit_code` was `0`.
- `tool_repair_failed`: repaired/canonicalized calls whose final
  `tool.result.exit_code` was non-zero.
- `tool_repair_notes`: total repair diagnostics emitted on `tool.request`.
- `tool_repair_by_kind`: object keyed by repair kind (`tool_name`,
  `malformed_json`, `wrapper_unwrap`, `scalar_wrap`, `alias_rename`,
  `enum_normalization`, `type_coercion`, `unknown_field_drop`, or `other`).
- `tool_failure_by_kind`: object keyed by structured tool failure kind. Tools
  that can explain recoverable failures should include a line like
  `Failure: kind=blocked` in their result or error text; the runtime aggregates
  those kinds here. Known web kinds include `invalid_args`, `blocked`,
  `not_found`, `rate_limited`, `server_error`, `http_error`,
  `private_network_blocked`, `timeout`, `network_error`, `empty_response`, and
  `non_text`.
- `tool_errors`
- `tool_duration_ms`
- `loop_guard_interventions`
- `forced_no_tools`

### `error`

- `turn_id`: runtime turn id.
- `code`: machine-readable error code.
- `message`: human-readable error.
- `recoverable`: whether the turn can continue.

## Compatibility Rules

- Adding new optional payload fields is backward compatible.
- Removing or renaming fields requires a new `schema_version`.
- Changing an event type's meaning requires a new `schema_version`.
- Bounded fields may be truncated only when a matching structured truncation
  flag and byte counters are present.
