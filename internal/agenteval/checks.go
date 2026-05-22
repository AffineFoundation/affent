package agenteval

import (
	"fmt"
	"strings"
)

// ToolCalled passes when the agent invoked the named tool at least
// once during the run. Used to pin "the agent used edit_file" /
// "the agent used go test" requirements.
//
// argMatcher (optional) is an extra predicate over the tool's
// JSON-decoded args: when non-nil, only tool calls whose args satisfy
// it count. Lets a Check distinguish `read_file path=README.md` from
// `read_file path=other.txt`. Pass nil to match any invocation.
func ToolCalled(toolName string, argMatcher func(args map[string]any) bool) Check {
	return Check{
		Name: "tool_called:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if argMatcher == nil || argMatcher(c.Args) {
					return CheckResult{Pass: true, Detail: "matched call_id=" + c.CallID}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at least one %q invocation, got %d tool calls (%s)", toolName, len(t.Tools), toolNamesSummary(t.Tools)),
			}
		},
	}
}

// ToolNotCalled passes when the agent never invoked the named tool.
// Used to pin "the agent must not edit tests" / "the agent must not
// run broad shell scans".
//
// argMatcher (optional) restricts the prohibition: only calls whose
// args match count as a violation. Lets a Check forbid
// `edit_file path=*_test.go` without forbidding edit_file outright.
func ToolNotCalled(toolName string, argMatcher func(args map[string]any) bool) Check {
	return Check{
		Name: "tool_not_called:" + toolName,
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if argMatcher == nil || argMatcher(c.Args) {
					return CheckResult{
						Pass:   false,
						Detail: fmt.Sprintf("found forbidden %q call (call_id=%s args=%v)", toolName, c.CallID, c.Args),
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// ToolCalledBefore passes when at least one `earlier` call happens
// before the first `later` call, AND a `later` call was made.
//
// Models the load-bearing "reproduce-first" workflow: the agent
// should run the test BEFORE editing the impl. Failing the check
// either means it edited without reproducing (likely wrong) or
// it never edited at all.
func ToolCalledBefore(earlier, later string) Check {
	return Check{
		Name: "tool_called_before:" + earlier + "->" + later,
		Eval: func(t Trace) CheckResult {
			firstEarlier := -1
			firstLater := -1
			for i, c := range t.Tools {
				if c.Tool == earlier && firstEarlier == -1 {
					firstEarlier = i
				}
				if c.Tool == later && firstLater == -1 {
					firstLater = i
				}
			}
			switch {
			case firstLater == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed a %q call", later)}
			case firstEarlier == -1:
				return CheckResult{Pass: false, Detail: fmt.Sprintf("never observed a %q call before any %q", earlier, later)}
			case firstEarlier >= firstLater:
				return CheckResult{
					Pass:   false,
					Detail: fmt.Sprintf("expected %q before %q; first %q at step %d, first %q at step %d", earlier, later, earlier, firstEarlier, later, firstLater),
				}
			default:
				return CheckResult{Pass: true}
			}
		},
	}
}

// FinalTextContains passes when the agent's final assistant message
// contains the substring. Used for "the answer should mention X"
// assertions. Case-sensitive on purpose — eval is usually checking
// for a specific quoted value or section label, where the case
// matters.
func FinalTextContains(substr string) Check {
	return Check{
		Name: "final_text_contains:" + previewSubstr(substr, 32),
		Eval: func(t Trace) CheckResult {
			if strings.Contains(t.FinalText, substr) {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("final text did not contain %q; got %q", substr, previewSubstr(t.FinalText, 200)),
			}
		},
	}
}

// FinalTextLacks passes when the final assistant message does NOT
// contain the substring. Used for refusal/anti-leak assertions:
// "the answer must not include the raw stack trace", "the answer
// must not say 'I cannot help'".
func FinalTextLacks(substr string) Check {
	return Check{
		Name: "final_text_lacks:" + previewSubstr(substr, 32),
		Eval: func(t Trace) CheckResult {
			if !strings.Contains(t.FinalText, substr) {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("final text contained forbidden %q", substr),
			}
		},
	}
}

// ShellCommandLacks passes when no shell tool call's command string
// contains the given substring. The "no | head, no || true" guard
// the user named: shell commands that mask exit codes destroy the
// signal the agent's own verification step needs. Default-on for
// any code-repair scenario.
//
// Matches both `shell` and the read-only `read_only_shell` flavor
// (any tool whose first arg is named "command"). Keeps the check
// invariant to which shell variant the scenario wires in.
func ShellCommandLacks(forbidden string) Check {
	return Check{
		Name: "shell_command_lacks:" + previewSubstr(forbidden, 32),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok {
					continue
				}
				if strings.Contains(cmd, forbidden) {
					return CheckResult{
						Pass:   false,
						Detail: fmt.Sprintf("shell call_id=%s used forbidden %q in command %q", c.CallID, forbidden, cmd),
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// TurnEndedCleanly passes when TurnEndReason is "completed". Used as
// a smoke-level prerequisite on scenarios where any other end state
// (max_turns, error, cancelled) is itself a failure even before
// looking at content.
func TurnEndedCleanly() Check {
	return Check{
		Name: "turn_ended_cleanly",
		Eval: func(t Trace) CheckResult {
			if t.TurnEndReason == "completed" {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("turn ended with reason %q (expected completed)", t.TurnEndReason),
			}
		},
	}
}

// MaxToolCalls passes when the run made at most n tool invocations.
// Caps wasteful behavior — a scenario that repeats the same read
// five times wasted tool budget even if the final answer is right.
// Negative n is treated as unbounded (always passes).
func MaxToolCalls(n int) Check {
	return Check{
		Name: fmt.Sprintf("max_tool_calls:%d", n),
		Eval: func(t Trace) CheckResult {
			if n < 0 || len(t.Tools) <= n {
				return CheckResult{Pass: true}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected at most %d tool calls, observed %d (%s)", n, len(t.Tools), toolNamesSummary(t.Tools)),
			}
		},
	}
}

// toolNamesSummary returns "name1, name2, name3" for diagnostics.
// Order is invocation order; duplicates kept on purpose so the
// summary shows repeated calls.
func toolNamesSummary(tools []ToolCall) string {
	if len(tools) == 0 {
		return "no tool calls"
	}
	names := make([]string, 0, len(tools))
	for _, c := range tools {
		names = append(names, c.Tool)
	}
	return strings.Join(names, ", ")
}

// previewSubstr returns s truncated to n runes with an ellipsis when
// truncated. Used to keep Check.Name and Detail short when the
// asserted substring is long.
func previewSubstr(s string, n int) string {
	if n <= 0 || len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
