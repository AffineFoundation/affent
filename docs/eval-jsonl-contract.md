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
- `top_p`: optional top-p sampling value passed to `affentctl`.
- `max_tokens`: optional max output token limit passed to `affentctl`.
- `seed`: optional deterministic-sampling seed passed to `affentctl`.
- `runtime_eval_mode`: optional boolean, present when the eval runner passed
  `affentctl run --eval-mode`. This is the default for `affenteval`.
- `runtime_tools`: optional comma-separated tool allowlist passed via
  `--runtime-tools` / `affentctl --eval-tools`.
- `runtime_all_tools`: optional boolean, present when the eval runner passed
  `--runtime-all-tools` / `affentctl --eval-all-tools`.
- `runtime_memory`: optional boolean, present when the eval runner explicitly
  opted memory back into the runtime with `--runtime-memory`.
- `runtime_web`: optional boolean, present when the eval runner explicitly
  enabled web search/runtime web tools with `--runtime-web`.
- `runtime_browser`: optional boolean, present when the eval runner explicitly
  enabled browser runtime tools with `--runtime-browser`.
- `trace_deltas`: optional boolean, present when the eval runner retained
  streaming message delta events with `--trace-deltas`. When omitted, the
  runner passed `affentctl --trace-skip-deltas` to keep traces compact.
- `runtime_mcp`: optional boolean, present when the eval runner passed a runtime
  MCP config. The config path itself is not written to JSONL.
- `timeout_ms`: per-scenario timeout in milliseconds.
- `quality_profile`: optional string, present when the eval runner expanded a
  built-in quality gate profile such as `longrun` or `web-evidence`. Profile
  thresholds are still written as ordinary gate fields, and explicitly supplied
  gate flags override profile defaults.
- `min_pass_rate`, `min_completion_rate`, `min_memory_update_rate`,
  `min_runtime_surface_rate`, `min_source_network_rate`,
  `min_source_access_verified_rate`,
  `min_expectation_capability_pass_rate`,
  `min_each_expectation_capability_pass_rate`,
  `min_session_search_context_hit_rate`,
  `min_tool_repair_success_rate`, `min_verifier_pass_rate`,
  `max_focused_task_error_rate`, `max_forced_no_tools_rate`,
  `max_loop_guard_intervention_rate`, `max_plan_error_rate`,
  `max_source_discovery_only_rate`,
  `max_source_dynamic_partial_rate`, `max_subagent_error_rate`,
  `max_tool_error_rate`,
  `max_tool_context_truncation_rate`, `max_tool_result_truncation_rate`,
  `max_avg_runtime_errors`, `max_avg_context_compactions`,
  `max_avg_reactive_context_compactions`,
  `max_avg_context_removed_messages`, `max_avg_context_summary_bytes`,
  `max_avg_tool_calls`, `max_avg_duration_ms`, `max_avg_total_tokens`:
  optional quality gate thresholds configured for the run. Disabled gates are
  omitted.

## Scenario Record

Scenario records describe one eval case:

- `scenario`: scenario id.
- `ok`: whether the scenario passed every check.
- `duration_ms`: wall-clock scenario duration.
- `workspace`: scenario workspace path.
- `trace_path`: trace JSONL path.
- `trace_schema_version`: parsed runtime trace schema version, when available.
- `trace_events`: total parsed trace events, when a trace was parsed.
- `trace_event_types`: per-event-type counts from the parsed trace, such as
  `message.delta`, `tool.request`, `tool.result`, and `runtime.surface`.
- `debug_manifest_path`: retained debug manifest path, when the workspace was
  not removed.
- `timeline_path`: retained human-readable timeline path, when the workspace
  was not removed. It summarizes runtime surface, tools, result previews,
  truncation/artifact pointers, child transcript refs, loop decisions,
  compactions, and errors.
- `final_text_path`, `stdout_path`, `stderr_path`: retained files containing
  the final assistant text, full `affentctl` stdout, and full `affentctl`
  stderr for local debugging.
- `affentctl_command`: redacted command argv used for the scenario run. API
  keys are replaced with `<redacted>`.
- `expectations`: optional structured copy of the scenario's declarative checks,
  including required/forbidden tools, tool counts, source-access requirements,
  loop-decision requirements, context-compaction requirements, plan/delegation
  constraints, protected files, and related max-turn/compaction settings. This
  lets batch-analysis scripts inspect why a scenario passed or failed without
  reopening the debug manifest.
- `expectation_capability_names`, `expectation_capability_outcome`,
  `expectation_capability_passed_names`, and
  `expectation_capability_failed_names`: optional scenario-level derived
  capability families and their scenario outcome. These use the same broad
  capability inference as summary records, so consumers can group individual
  scenario rows without reimplementing expectation parsing.
- `runtime_surface`: compact copy of the latest effective runtime surface when
  the trace reached turn start. It records sorted tool names, broad
  capabilities such as `web_fetch`, `web_search`, `browser`, `memory`, and
  `subagent`, partial `workspace_tools` when only some workspace tools are
  enabled, plus key tool-result limits. Retained debug manifests include the
  fuller `runtime_surface` block with per-tool group/source metadata.
- `run_exit_code`: `affentctl run` exit code for the scenario.
- `turn_end_reason`: runtime turn end reason, when available.
- `tool_calls`: number of tool calls in the trace.
- `tool_errors`: runtime tool error count, limited to non-zero tool exits.
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
- `tool_repair_examples`: optional bounded examples of repaired or
  canonicalized tool calls. Each sample includes tool index, call id, final
  tool name, original tool name when present, canonicalized/args-repaired
  flags, compact original argument preview, repair notes/kinds, exit code, and
  whether the repaired call succeeded.
- `tool_failure_by_kind`: optional map of structured tool failure kind to
  count. New traces prefer `turn.end.tool_stats`; replay of older or partial
  traces may derive counts from per-call `tool.result.failure_kinds`,
  `tool.result.failure_kind`, or structured `Failure: kind=...` result text. A
  single tool result may contribute multiple kinds when the original tool
  failure and a later runtime guard are both present. Web tool failures use
  this field to distinguish blocked pages, empty responses, non-text
  responses, timeouts, search no-results, stale or non-interactable browser
  refs, argument errors, HTTP/network classes, and loop-guard rejections such
  as repeated failed URL/query inputs. This field can count Web no-evidence
  results whose `tool.result.exit_code` is `0`; it is the right field for
  retrieval quality diagnostics, while `tool_errors` is the right field for
  non-zero tool exits.
- `tool_failure_hints`: optional map of structured tool failure kind to a short
  operator hint explaining likely cause and next diagnostic action.
- `tool_failure_examples`: optional map of failure kind to bounded sample tool
  failures. Scenario records derive samples from that scenario; summary
  records carry a small cross-scenario sample per kind. Each sample includes
  the tool name, a compact
  `args_summary` such as `url="..."` or `query="..."`, the exit code, and a
  compact `result_summary` containing the failure reason and `Next:` guidance
  when the trace carried it. This is diagnostic context for operators; counts
  in `tool_failure_by_kind` remain the aggregation source of truth.
- `runtime_error_by_kind`: optional map of structured `error.failure_kind`
  counts, such as `llm_timeout`, `llm_incomplete_stream`, or
  `context_overflow`. This is separate from `failure_kinds` so recoverable LLM
  retry errors can be tracked without turning an otherwise passing scenario
  into a scenario assertion failure.
- `runtime_error_hints`: optional map of runtime error kind to a short
  operator hint explaining likely cause and next diagnostic action.
- `runtime_error_examples`: optional map of runtime error kind to bounded
  message samples. Scenario records derive samples from that scenario; summary
  records carry a small cross-scenario sample per kind. These preserve the
  specific timeout, endpoint, or incomplete-stream detail from `error` events
  without requiring operators to inspect the full trace.
- `debug_brief`: optional machine-readable triage block for failed or
  diagnostic-heavy scenarios. It contains sorted `tags` such as
  `outcome:failed`, `tool_failure:blocked`, `runtime_error:llm_timeout`,
  `source_dynamic_partial`, `memory_update:replace`, `empty_recall`,
  `delegation_error:focused_task`, `plan_error`,
  `context_compaction:reactive`, and `truncation`, plus ordered `items` with a
  severity, message, inspect targets, and relevant counts. This is the compact
  "what to inspect first" index for long-run batch analysis.
- `loop_guard_interventions`: runtime loop guard intervention count.
- `forced_no_tools`: count of forced no-tool follow-up requests after repeated
  blocking loop guard interventions. Soft warnings such as
  `loop_guard_repeated_failures` still count under `loop_guard_interventions`
  but do not by themselves force a no-tool follow-up, so API/text fallback
  tools can still run after a recoverable web failure pattern.
- `loop_guard_examples`: optional bounded examples of loop-guard and tool
  policy rejections. Each sample includes the failure kind, category
  (`loop_guard` or `tool_policy`), tool index, call id, tool name, compact
  argument/result previews, and exit code. Use this when `loop_guard_interventions`
  or `forced_no_tools` spike and the generic failure-kind counts do not show
  which attempted call was blocked.
- `tool_duration_ms`: total runtime tool dispatch duration.
- `source_access_results`: count of tool results with a normalized
  `SourceAccess:` evidence header.
- `source_access_verified`: count of `SourceAccess:` results with an accessed
  URL that were not discovery-only.
- `source_access_discovery_only`: count of search-result, not-found, or
  rendered fallback `SourceAccess:` results that are navigation aids rather
  than factual evidence.
- `source_access_network`: count of `SourceAccess:` results read from captured
  browser XHR/fetch evidence.
- `source_access_examples`: optional bounded examples of normalized
  `SourceAccess:` evidence. Each sample includes tool index, call id, tool
  name, evidence status (`verified`, `network`, `dynamic_partial`, or
  `discovery_only`), accessed/requested URLs, URL field, source method, and
  JSON path when present.
- `session_search_calls`: count of dispatched `session_search` tool calls.
- `session_search_results`: total prior-session hits reported by parsed
  `session_search` JSON responses.
- `session_search_context_hits`: count of `session_search` hits that included
  adjacent transcript context.
- `session_search_matched_terms`: count of unique matched query terms reported
  across parsed `session_search` responses.
- `session_search_examples`: optional bounded examples of parsed
  `session_search` responses. Each sample includes tool index, call id, query,
  total result count, matched session id, turn index, role, score, matched
  terms, context-included flag, and a compact snippet/message preview.
- `memory_update_examples`: optional bounded per-scenario examples of confirmed
  durable memory mutations. Each sample includes the tool index, call id,
  action, target/topic location, and compact previous/next previews when
  available. These examples make long-run memory drift auditable without
  opening the full trace or markdown timeline.
- `context_compaction_examples`: optional bounded examples of context
  compaction events. Each sample includes the turn id, before/after message
  counts, removed message count, reactive/proactive flag, reason, and summary
  byte size.
- `tool_truncation_examples`: optional bounded examples of tool calls whose
  request args, event result, result artifact, or model-context insertion were
  truncated. Each sample includes the tool index, call id, tool name, omitted
  byte counts, cap byte counts, artifact path when present, and context bytes
  omitted before the tool result was fed back to the model.
- `tool_context_truncated`: count of tool results shortened before being fed
  back into the model conversation.
- `tool_context_omitted_bytes`: total bytes omitted from tool results before
  model-context insertion.
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
- `plan_examples`: optional bounded examples of persisted-plan tool activity.
  Each sample includes tool index, call id, action, updated step index/status,
  compact step text, evidence refs, note preview, result progress/current step,
  and a compact error/result message when relevant.
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

- `scenarios`, `passed`, `failed`, `duration_ms`, `avg_duration_ms`.
- Normalized comparison metrics: `pass_rate`, `completion_rate`,
  `memory_update_rate`, `runtime_surface_rate`,
  `tool_error_rate`, `forced_no_tools_rate`, and
  `loop_guard_intervention_rate` when tool calls were observed,
  `focused_task_error_rate` when focused-task calls were observed,
  `subagent_error_rate` when subagent calls were observed,
  `plan_error_rate` when plan calls were observed,
  `tool_repair_success_rate` when repaired/canonicalized tool calls were
  observed, `verifier_pass_rate` when verifier commands ran,
  `source_access_verified_rate` when source evidence was observed,
  `source_network_rate` when source evidence was observed,
  `source_discovery_only_rate` when source evidence was observed,
  `source_dynamic_partial_rate` when source evidence was observed,
  `session_search_context_hit_rate` when session search returned prior-session
  hits,
  `avg_runtime_errors`, `avg_context_compactions`,
  `avg_reactive_context_compactions`, `avg_context_removed_messages`,
  `avg_context_summary_bytes`, `avg_tool_calls`,
  `tool_context_truncation_rate` and
  `tool_result_truncation_rate` when tool calls were observed,
  `avg_input_tokens`, `avg_output_tokens`, and `avg_total_tokens`.
- Tool totals: `tool_calls`, `tool_errors`, `tool_repaired`,
  `tool_name_canonicalized`, `tool_repair_calls`, `tool_repair_succeeded`,
  `tool_repair_failed`, `tool_repair_notes`, `tool_repair_by_kind`,
  `tool_repair_examples`,
  `tool_failure_by_kind`, `loop_guard_interventions`, `forced_no_tools`,
  `loop_guard_examples`,
  `source_access_results`, `source_access_verified`,
  `source_access_discovery_only`, `source_access_network`,
  `source_access_examples`,
  `memory_updates`, `memory_update_add`, `memory_update_replace`,
  `memory_update_remove`, `memory_update_examples`,
  `session_search_calls`, `session_search_results`,
  `session_search_context_hits`, `session_search_matched_terms`,
  `session_search_examples`,
  `tool_duration_ms`,
  `tool_context_truncated`, `tool_context_omitted_bytes`.
- Truncation totals: `tool_args_truncated`, `tool_args_omitted_bytes`,
  `tool_results_truncated`, `tool_results_omitted_bytes`,
  `tool_result_artifacts`, plus `tool_truncation_examples`, the first bounded
  samples across the batch.
- Delegation totals: `focused_task_calls`, `focused_task_by_type`,
  `focused_task_errors`, `focused_task_error_rate`, `subagent_calls`,
  `subagent_by_mode`, `subagent_errors`, and `subagent_error_rate`. These
  fields are omitted when no delegated tool calls were observed.
- Plan totals: `plan_calls`, `plan_by_action`, `plan_errors`,
  `plan_examples`, and `plan_error_rate`. These fields are omitted when no plan
  tool calls were observed.
- Verifier totals: `verifier_runs`, `verifier_passed`, `verifier_failed`,
  `verifier_output_truncated`, `verifier_output_omitted_bytes`.
- Trace versions: `trace_schema_versions`.
- Token totals: `input_tokens`, `output_tokens`.
- Turn end totals: `end_completed`, `end_max_turns`, `end_errors`,
  `end_cancelled`, `end_unknown`.
- Failure totals and examples: `failure_kinds`, plus `failure_examples`, a
  bounded per-kind sample containing scenario name, compact failure text, and
  retained trace/timeline/debug-manifest paths when available.
- Tool failure totals and diagnostics: `tool_failure_by_kind`,
  `tool_failure_hints`, `tool_failure_examples`.
- Runtime error totals and diagnostics: `runtime_error_by_kind`,
  `runtime_error_hints`, `runtime_error_examples`.
- Source evidence examples: `source_access_examples`, the first bounded
  samples across the batch.
- Context compaction examples: `context_compaction_examples`, the first bounded
  samples across the batch.
- Debug brief tag totals: `debug_brief_by_tag`, counting how many scenarios
  emitted each machine-readable triage tag.
- Expectation coverage totals:
  `expectation_scenarios` counts scenarios that carried declarative
  expectations; `expectation_suites` counts suite markers; `expectation_required_tools`
  counts tools mentioned by scenario requirements; `expectation_source_access`
  counts declared source-access statuses such as `network` or `verified`; and
  `expectation_capabilities` counts broad required capability families such as
  `workspace`, `memory`, `session_search`, `source_access`, `web`, `browser`,
  `delegation`, `plan`, `context_compaction`, `verifier`, and `mcp`.
  `expectation_capability_passed`, `expectation_capability_failed`, and
  `expectation_capability_pass_rate` split those declared capability families
  by scenario outcome. These are declaration/outcome counters, not observed
  runtime behavior. Use them with runtime-surface fields to see which
  capability classes a batch actually exercised and where failures cluster.
  `expectation_capability_total`,
  `expectation_capability_passed_total`,
  `expectation_capability_failed_total`, and
  `expectation_capability_pass_rate_total` aggregate the same declared
  capability instances into one batch-level rate for CI dashboards and quality
  gates.
  `expectation_capability_failure_examples` is a bounded map from capability
  family to failed scenario examples with failure kinds, debug-brief tags, and
  retained trace/timeline/debug-manifest paths when available. It is intended
  as the direct "open this failed case first" index for long-run triage.
  `min_each_expectation_capability_pass_rate` gates each family in
  `expectation_capability_pass_rate` independently, so a memory or browser
  regression cannot be hidden by unrelated passing capabilities.
- Quality gate outcome: `quality_profile` identifies any built-in profile used,
  `quality_gates_passed` is present on summary records when at least one quality
  gate threshold was configured, and
  `quality_gate_failures` lists failed gate comparisons when any thresholds
  were violated.
- Runtime surface totals: `runtime_surface_rate`, `runtime_surface_scenarios`,
  `runtime_surface_tools`, and `runtime_surface_capabilities`. Counts are
  per-scenario surface presence, not model call counts; use them to group pass
  rates and failures by the actual tool/capability surface exposed during the
  run. Partial workspace surfaces are counted as `workspace_partial`.
- Cleanup totals: `removed_workspaces`, `cleanup_errors`.

## Compatibility

Adding optional fields is backward compatible. Removing or renaming fields, or
changing the meaning of an existing field, requires incrementing
`schema_version`.
