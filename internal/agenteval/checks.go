package agenteval

import (
	"fmt"
	"path/filepath"
	"regexp"
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

// ToolResultContains passes when a named tool returned output containing
// substr. It is useful for runtime guard evals: the model may attempt a
// bad call, but the loop must surface a corrective tool result instead
// of crashing or spinning.
func ToolResultContains(toolName, substr string) Check {
	return Check{
		Name: "tool_result_contains:" + toolName + ":" + previewSubstr(substr, 32),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != toolName {
					continue
				}
				if strings.Contains(c.Result, substr) {
					return CheckResult{Pass: true, Detail: "matched call_id=" + c.CallID}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("expected %q result to contain %q; tools=%s", toolName, substr, toolNamesSummary(t.Tools)),
			}
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

// ShellCommandMatching passes when at least one shell tool call's
// `command` argument matches the given pattern. Pattern is a Go
// regexp; on regex compile failure it falls back to plain substring
// match so scenarios authored as substrings keep working.
//
// Used to pin "the agent must have actually run pytest" / "the agent
// must have run go test ./..." style scenario expectations.
func ShellCommandMatching(pattern string) Check {
	re, reErr := regexp.Compile(pattern)
	return Check{
		Name: "shell_command_matching:" + previewSubstr(pattern, 48),
		Eval: func(t Trace) CheckResult {
			var observed []string
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				observed = append(observed, cmd)
				if reErr == nil {
					if re.MatchString(cmd) {
						return CheckResult{Pass: true}
					}
				} else if strings.Contains(cmd, pattern) {
					return CheckResult{Pass: true}
				}
			}
			return CheckResult{
				Pass:   false,
				Detail: fmt.Sprintf("missing required command match %q; commands=%v", pattern, observed),
			}
		},
	}
}

// ShellCommandLacksUnguarded is ShellCommandLacks with one important
// twist: commands that the runtime's shell guard already rejected
// (exit code != 0 plus a guard-shaped error message) DO NOT count as
// failures. That distinction matters because the whole point of the
// guard is to let the model attempt the dangerous shape and still
// produce a correct outcome — penalizing the model for the attempt
// would discourage exploration of the guard's coverage.
//
// Guard rejection is detected by ExitCode != 0 plus a "masks a
// test/build exit code" or "unbounded filesystem scan" substring in
// the result — the two messages the current shell guards emit.
func ShellCommandLacksUnguarded(forbidden string) Check {
	lower := strings.ToLower(forbidden)
	return Check{
		Name: "shell_command_lacks_unguarded:" + previewSubstr(forbidden, 32),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				cmd, ok := c.Args["command"].(string)
				if !ok || cmd == "" {
					continue
				}
				if !strings.Contains(strings.ToLower(cmd), lower) {
					continue
				}
				if guardRejected(c) {
					continue
				}
				return CheckResult{
					Pass:   false,
					Detail: fmt.Sprintf("forbidden command substring %q in %q", forbidden, cmd),
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// FileNotEdited passes when no write_file / edit_file tool call
// targeted any of the named paths. Paths match against the trailing
// segment of args.path (so "test_slug.py" catches both
// `test_slug.py` and `pkg/test_slug.py`), preserving the
// CheckBatchTrace semantics that scenarios were written against.
//
// Used to pin "the agent must not edit the test file" in repair
// scenarios where editing tests would be cheating.
func FileNotEdited(paths []string) Check {
	return Check{
		Name: "file_not_edited:" + previewSubstr(strings.Join(paths, ","), 64),
		Eval: func(t Trace) CheckResult {
			for _, c := range t.Tools {
				if c.Tool != "write_file" && c.Tool != "edit_file" {
					continue
				}
				rawPath, _ := c.Args["path"].(string)
				slash := filepath.ToSlash(rawPath)
				for _, name := range paths {
					if slash == name || strings.HasSuffix(slash, "/"+name) {
						return CheckResult{
							Pass:   false,
							Detail: fmt.Sprintf("modified protected file through %s: %v", c.Tool, rawPath),
						}
					}
				}
			}
			return CheckResult{Pass: true}
		},
	}
}

// guardRejected returns true when the shell tool's own guard refused
// to execute the command (vs the command ran and exited non-zero on
// its own merits). The signal is exit_code != 0 + a guard-shaped
// substring in the tool result; new guards that surface a different
// substring must be added here to keep ShellCommandLacksUnguarded
// honest.
func guardRejected(c ToolCall) bool {
	if c.ExitCode == 0 {
		return false
	}
	for _, marker := range []string{
		"masks a test/build exit code",
		"unbounded filesystem scan",
	} {
		if strings.Contains(c.Result, marker) {
			return true
		}
	}
	return false
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
