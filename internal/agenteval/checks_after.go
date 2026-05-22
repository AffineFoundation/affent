package agenteval

import (
	"fmt"
	"strings"
)

// ToolNotCalledAfter passes when, after the FIRST successful call to
// triggerTool, none of the named restrictedTools appears in the rest
// of the timeline. Encodes the "delegate, then answer from the
// report" workflow: once the agent has subagent_run / a synthesis
// tool / a fetched report, doing the same exploration again in the
// parent context defeats the purpose of the delegation.
//
// Both conditions must hold:
//
//   - triggerTool was called at least once and the first call
//     succeeded (ExitCode == 0 && !IsErr). A failed trigger run is
//     not a green light to skip parent verification, so we don't
//     restrict subsequent tools in that case — the check passes
//     vacuously.
//   - No restrictedTool entry appears in t.Tools at an index AFTER
//     the first successful triggerTool call.
//
// Negative outcomes spell the violating call out so the failure
// report points the reviewer at a specific step in the trace.
func ToolNotCalledAfter(triggerTool string, restrictedTools []string) Check {
	restricted := map[string]bool{}
	for _, name := range restrictedTools {
		if name != "" {
			restricted[name] = true
		}
	}
	return Check{
		Name: "tool_not_called_after:" + triggerTool + ":" + previewSubstr(strings.Join(restrictedTools, ","), 48),
		Eval: func(t Trace) CheckResult {
			triggerIdx := -1
			for i, c := range t.Tools {
				if c.Tool == triggerTool && c.ExitCode == 0 && !c.IsErr {
					triggerIdx = i
					break
				}
			}
			if triggerIdx == -1 {
				// Trigger never succeeded — nothing to restrict.
				// Passing vacuously is the right call: if the scenario
				// needs the trigger to fire, pair this check with
				// ToolCalled(triggerTool).
				return CheckResult{Pass: true, Detail: "trigger did not succeed; restriction vacuous"}
			}
			for i := triggerIdx + 1; i < len(t.Tools); i++ {
				c := t.Tools[i]
				if restricted[c.Tool] {
					return CheckResult{
						Pass: false,
						Detail: fmt.Sprintf(
							"after %q succeeded at step %d, parent called restricted tool %q at step %d (call_id=%s); should answer from the prior result",
							triggerTool, triggerIdx, c.Tool, i, c.CallID),
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// MaxToolCallsAfter passes when, after the first successful call to
// triggerTool, the agent issues at most maxFollowup more tool calls
// (counted as anything in t.Tools at a higher index, regardless of
// tool name). Coarser than ToolNotCalledAfter — captures "stop
// exploring, answer now" without naming the specific tools to avoid.
//
// maxFollowup < 0 is treated as unbounded (the check always passes,
// useful when composed with other checks via a config-driven scenario).
func MaxToolCallsAfter(triggerTool string, maxFollowup int) Check {
	return Check{
		Name: fmt.Sprintf("max_tool_calls_after:%s:%d", triggerTool, maxFollowup),
		Eval: func(t Trace) CheckResult {
			if maxFollowup < 0 {
				return CheckResult{Pass: true}
			}
			triggerIdx := -1
			for i, c := range t.Tools {
				if c.Tool == triggerTool && c.ExitCode == 0 && !c.IsErr {
					triggerIdx = i
					break
				}
			}
			if triggerIdx == -1 {
				return CheckResult{Pass: true, Detail: "trigger did not succeed; restriction vacuous"}
			}
			followup := len(t.Tools) - triggerIdx - 1
			if followup > maxFollowup {
				var names []string
				for i := triggerIdx + 1; i < len(t.Tools); i++ {
					names = append(names, t.Tools[i].Tool)
				}
				return CheckResult{
					Pass: false,
					Detail: fmt.Sprintf(
						"after %q succeeded at step %d, expected at most %d followup tool calls; got %d (%s)",
						triggerTool, triggerIdx, maxFollowup, followup, strings.Join(names, ", ")),
				}
			}
			return CheckResult{Pass: true}
		},
	}
}
