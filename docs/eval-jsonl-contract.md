# Eval JSONL Contract

`affenteval --jsonl` emits one JSON object per line. Each line carries
`schema_version`; consumers should reject future versions they do not
understand.

## Schema Version 1

Shared metadata fields:

- `schema_version`: integer eval JSONL schema version.
- `type`: `scenario` or `summary`.
- `suite`: optional suite label.
- `model`: optional model id.
- `provider_label`: optional provider label for cross-run comparisons.
- `executor`: eval executor label, such as `local`, `sandbox`, or
  `docker:<container>`.
- `temperature`: sampling temperature passed to `affentctl`.
- `runtime_eval_mode`: optional boolean, present when the eval runner passed
  `affentctl run --eval-mode`.
- `timeout_ms`: per-scenario timeout in milliseconds.

## Scenario Record

Scenario records describe one eval case:

- `scenario`: scenario id.
- `ok`: whether the scenario passed every check.
- `duration_ms`: wall-clock scenario duration.
- `workspace`: scenario workspace path.
- `trace_path`: trace JSONL path.
- `trace_schema_version`: parsed runtime trace schema version, when available.
- `turn_end_reason`: runtime turn end reason, when available.
- `tool_calls`: number of tool calls in the trace.
- `tool_errors`: runtime tool error count.
- `tool_repaired`: runtime tool argument repair count.
- `tool_name_canonicalized`: runtime tool name canonicalization count.
- `tool_repair_calls`: count of tool calls that were repaired or
  canonicalized.
- `tool_repair_succeeded`: repaired/canonicalized tool calls with
  `exit_code == 0`.
- `tool_repair_failed`: repaired/canonicalized tool calls with
  `exit_code != 0`.
- `tool_repair_notes`: count of classified runtime repair diagnostics emitted
  on `tool.request` events. Omitted when no repair diagnostics were observed.
- `tool_repair_by_kind`: optional map of repair diagnostic kind to count. Known
  keys include `tool_name`, `malformed_json`, `wrapper_unwrap`,
  `scalar_wrap`, `alias_rename`, `enum_normalization`, `type_coercion`,
  `unknown_field_drop`, and `other`.
  New traces prefer the authoritative `turn.end.tool_stats` repair summary;
  older traces fall back to classifying `tool.request.repair_notes`.
- `loop_guard_interventions`: runtime loop guard intervention count.
- `forced_no_tools`: count of forced no-tool follow-up requests after repeated
  loop guard interventions.
- `tool_duration_ms`: total runtime tool dispatch duration.
- `tool_args_truncated`: count of event-capped tool requests.
- `tool_args_omitted_bytes`: omitted request bytes across tool events.
- `tool_results_truncated`: count of event-capped tool results.
- `tool_results_omitted_bytes`: omitted result bytes across tool events.
- `tool_result_artifacts`: count of tool result artifact references.
- `focused_task_calls`: optional number of delegated `run_task` calls.
- `focused_task_by_type`: optional map of focused task `task_type` to call
  count.
- `focused_task_errors`: optional count of focused task calls whose runtime
  tool exit code was non-zero.
- `subagent_calls`: optional number of delegated `subagent_run` calls.
- `subagent_by_mode`: optional map of subagent `mode` to call count.
- `subagent_errors`: optional count of subagent calls whose runtime tool exit
  code was non-zero.
- `plan_calls`: optional number of persisted-plan tool calls.
- `plan_by_action`: optional map of plan action (`view`, `set`, `update`,
  `clear`, or `unknown`) to call count.
- `plan_errors`: optional count of plan tool calls whose runtime tool exit code
  was non-zero.
- `verifier_command`: verifier shell command, when configured.
- `verifier_ran`: whether a verifier command ran.
- `verifier_ok`: whether the verifier exited successfully.
- `verifier_exit_code`: verifier process exit code, or `-1` for abnormal
  termination.
- `verifier_duration_ms`: verifier duration in milliseconds.
- `verifier_output_bytes`: verifier stdout/stderr byte count before capping.
- `verifier_output_truncated`: whether verifier output hit the cap.
- `verifier_output_omitted_bytes`: verifier output bytes omitted by the cap.
- `verifier_output_cap_bytes`: verifier output cap used for the run.
- `input_tokens`: summed provider input tokens.
- `output_tokens`: summed provider output tokens.
- `workspace_removed`: whether a passing workspace was cleaned up.
- `cleanup_error`: workspace cleanup error, when cleanup failed.
- `failures`: failure details for failed scenarios.
- `failure_kinds`: grouped failure counters for summaries.

Verifier output text is not written to JSONL. Failure reports may include a
bounded preview in `failures`; the structured verifier fields are the stable
surface for trend analysis.

## Summary Record

Summary records aggregate all scenario records from the same process:

- `scenarios`, `passed`, `failed`, `duration_ms`.
- Tool totals: `tool_calls`, `tool_errors`, `tool_repaired`,
  `tool_name_canonicalized`, `tool_repair_calls`, `tool_repair_succeeded`,
  `tool_repair_failed`, `tool_repair_notes`, `tool_repair_by_kind`,
  `loop_guard_interventions`, `forced_no_tools`, `tool_duration_ms`.
- Truncation totals: `tool_args_truncated`, `tool_args_omitted_bytes`,
  `tool_results_truncated`, `tool_results_omitted_bytes`,
  `tool_result_artifacts`.
- Delegation totals: `focused_task_calls`, `focused_task_by_type`,
  `focused_task_errors`, `subagent_calls`, `subagent_by_mode`,
  `subagent_errors`. These fields are omitted when no delegated tool calls were
  observed.
- Plan totals: `plan_calls`, `plan_by_action`, `plan_errors`. These fields are
  omitted when no plan tool calls were observed.
- Verifier totals: `verifier_runs`, `verifier_passed`, `verifier_failed`,
  `verifier_output_truncated`, `verifier_output_omitted_bytes`.
- Trace versions: `trace_schema_versions`.
- Token totals: `input_tokens`, `output_tokens`.
- Turn end totals: `end_completed`, `end_max_turns`, `end_errors`,
  `end_cancelled`, `end_unknown`.
- Failure totals: `failure_kinds`.
- Cleanup totals: `removed_workspaces`, `cleanup_errors`.

## Compatibility

Adding optional fields is backward compatible. Removing or renaming fields, or
changing the meaning of an existing field, requires incrementing
`schema_version`.
