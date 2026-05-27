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

### `runtime.surface`

- `turn_id`: runtime turn id.
- `tool_count`: count of tools registered for the turn.
- `tools`: optional effective tool list with `name`, `raw_name`, `group`, and
  `source` metadata.
- `capabilities`: broad runtime capability flags. `builtins` means the core
  workspace tools (`shell`, `read_file`, `write_file`, `edit_file`,
  `list_files`) are all present. `workspace_tools` lists the concrete
  workspace tools present for partial or expanded workspace surfaces.
- `max_turn_steps`, `max_tool_calls`: optional runtime limits.
- `tool_result_event_cap_bytes`, `tool_result_context_max_bytes`,
  `tool_result_context_budget_bytes`, `tool_result_artifact_prefix`: optional
  tool-result transport and model-context limits.
- `turn_tool_override`: optional true when the turn used a local tool override.

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
- `failure_kind`: optional primary machine-readable failure class extracted
  from a structured `Failure: kind=...` line in the tool output. For web
  tools, this may be present even when `exit_code` is `0` if the call produced
  no usable evidence, such as a dynamic shell, empty response, non-text body,
  or search no-results response.
- `failure_kinds`: optional ordered list of every structured failure class on
  the result. It is a superset of `failure_kind` when the original tool failure
  and a later runtime guard classification are both present.
  For web tools, known values include `invalid_args`, `blocked`, `not_found`,
  `rate_limited`, `server_error`, `http_error`, `private_network_blocked`,
  `timeout`, `network_error`, `empty_response`, `dynamic_shell`, `non_text`,
  `no_results`, and `search_error`. Browser inspection tools may also emit
  `stale_ref` when a previously visible element ref no longer matches the
  current page, or
  `not_interactable` when the element exists but is hidden, disabled, or
  covered. Runtime loop guards may emit `loop_guard_repeated_call`,
  `loop_guard_repeated_failed_input`, `loop_guard_repeated_failures`,
  `loop_guard_halted_tool`, `loop_guard_call_cap`, or
  `loop_guard_direct_reader_warning`, or `loop_guard_no_new_evidence`.
  Runtime workflow policies may emit
  `tool_policy_first_tool`, `tool_policy_repeat`, or
  `tool_policy_active`.
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
- `memory_update`: optional metadata on confirmed `memory` add/replace/remove
  results. It includes the action, target/topic location, and bounded previous
  and next previews so WebUI and trace consumers can show what changed even
  when request args or memory response bodies are capped.

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
  those kinds here. Web no-evidence results can contribute here even when their
  `tool.result.exit_code` is `0`; use `tool_errors` when you specifically need
  non-zero tool exits. Known web kinds include `invalid_args`, `blocked`,
  `not_found`, `rate_limited`, `server_error`, `http_error`,
  `private_network_blocked`, `timeout`, `network_error`, `empty_response`,
  `dynamic_shell`, `non_text`, `no_results`, `search_error`, `stale_ref`, and
  `not_interactable`. Known runtime policy kinds include
  `tool_policy_first_tool`, `tool_policy_repeat`, and `tool_policy_active`.
- `tool_errors`: count of tool results emitted with non-zero `exit_code`,
  including guard rejections and skipped calls. This is narrower than
  `tool_failure_by_kind`; a successful HTTP/browser call that returns no
  usable evidence can have a failure kind without incrementing `tool_errors`.
- `tool_duration_ms`
- `loop_guard_interventions`
- `forced_no_tools`: forced no-tool follow-ups after repeated blocking guard
  interventions. Soft guard warnings such as `loop_guard_repeated_failures`
  are counted as interventions but do not by themselves force tools off.
- `source_access_results`: count of dispatched tool results that included a
  normalized `SourceAccess:` header.
- `source_access_verified`: count of `SourceAccess:` results with an accessed
  URL that were not marked discovery-only.
- `source_access_discovery_only`: count of `SourceAccess:` results marked as
  search-result, not-found, or rendered-browser fallback discovery rather than
  factual evidence.
- `source_access_network`: count of `SourceAccess:` results read from captured
  browser XHR/fetch evidence (`browser_network_url` or
  `source_method=network_xhr_fetch`). Browser network reads may also carry a
  `ref=...` field linking the source evidence back to a prior
  `browser_network` result, plus optional response diagnostics such as
  `status=200` and `content_type=application/json`.
- `session_search_calls`: count of dispatched `session_search` tool calls.
- `session_search_results`: total prior-session hits reported by parsed
  `session_search` JSON responses.
- `session_search_context_hits`: count of `session_search` hits that included
  adjacent transcript context.
- `session_search_matched_terms`: count of unique matched query terms reported
  across parsed `session_search` responses.
- `tool_context_truncated`: count of tool results shortened before being fed
  back into the model conversation. This is separate from
  `tool.result.result_truncated`, which reports event-payload truncation.
- `tool_context_omitted_bytes`: total bytes omitted from tool results before
  model-context insertion.

### `context.compacted`

- `turn_id`: optional runtime turn id for the turn that compacted context.
- `before_messages`, `after_messages`, `removed_messages`: model-context
  message counts before and after the rewrite.
- `reactive`: true when compaction happened after an upstream context-overflow
  rejection; false for proactive threshold compaction.
- `reason`: compact reason, currently `threshold` or `context_overflow`.
- `summary_present`, `summary_bytes`, `summary_preview`: bounded diagnostics
  for the rolling summary inserted back into model context.
- `loop_protocol_anchor`: optional deterministic recovery anchor copied from a
  compacted `LOOP.md` feed summary. It keeps the protocol path, feed metadata,
  loop id/status, and active plan checkpoint visible even when
  `summary_preview` is truncated before the anchor.

### `loop.protocol_feed`

- `turn_id`: optional runtime turn id for the user turn that received the
  protocol block.
- `loop_id`: optional loop identity, usually the owning session id.
- `status`: optional loop lifecycle status from `.affent/loops/<id>/state.json`.
- `mode`: feed mode, currently `full` or `digest`.
- `feed_number`: durable per-loop feed sequence number.
- `protocol_feeds`: optional durable feed count after this feed.
- `protocol_path`: optional workspace/session-relative protocol path.
- `plan_label`: optional active persisted-plan summary label captured at feed
  time, such as `plan:1/3:active`.
- `plan_current_step_index`: optional 1-based current plan step index captured
  at feed time.
- `plan_current_step_status`: optional status for the current plan step.
- `plan_current_step`: optional compact current plan step text.

This event is emitted by `affentctl` traces and `affentserve` session streams
when an active `LOOP.md` is injected. It mirrors the sidecar
`.affent/loops/<id>/events.jsonl` feed record into the normal session trace/SSE
stream. After context compaction the loop state forces the next protocol feed
to `full`, so a post-compaction `mode=full` can occur outside the regular
first-three/every-sixth cadence. WebUI, replay, and eval tooling can inspect
loop context pressure
without separately reading loop files. Plan checkpoint fields are recovery
pointers only; the persisted plan state remains the step authority.

### `loop.decision`

- `turn_id`: optional runtime turn id.
- `loop_id`: optional loop identity.
- `decision_id`: stable short id for the decision class.
- `kind`: decision family, such as `evidence_quality` or
  `research_checkpoint`.
- `trigger`: compact trigger label, such as `source_access_dynamic_partial` or
  `external_calibration_requested`.
- `decision`: compact outcome, such as `defer` or `trigger`.
- `confidence`: optional confidence label.
- `reason`: bounded human-readable reason.
- `required_action`: bounded next action visible to users and evals.
- `visible_in_ui`: optional boolean; missing means visible.

`research_checkpoint` is emitted only for active loop turns where the user
request combines high-impact Affent/runtime/protocol design changes with
external-calibration signals. The paired model-context reminder asks the agent
to do a bounded research/review pass through the currently enabled
focused-task, web, or browser tools, or to state that the surface cannot support
external calibration. It does not start a separate controller agent.

### `error`

- `turn_id`: runtime turn id.
- `code`: machine-readable error code.
- `message`: human-readable error.
- `failure_kind`: optional primary machine-readable failure class for runtime
  errors, for example `llm_timeout`, `llm_incomplete_stream`, or
  `context_overflow`.
- `recoverable`: whether the turn can continue.

## Compatibility Rules

- Adding new optional payload fields is backward compatible.
- Removing or renaming fields requires a new `schema_version`.
- Changing an event type's meaning requires a new `schema_version`.
- Bounded fields may be truncated only when a matching structured truncation
  flag and byte counters are present.
