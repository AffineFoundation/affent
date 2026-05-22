package agenteval

import (
	"strings"
	"testing"
)

// The fixtures below model real trace shapes the framework will see
// in subagent / delegation scenarios. Each subtest pins one corner
// of the contract; combined they document what "answer from the
// report" means as a measurable property of a Trace.

func TestToolNotCalledAfter(t *testing.T) {
	t.Run("passes when no restricted tools appear after trigger", func(t *testing.T) {
		// Delegation, then a small synthesis hop (memory write) — fine.
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "subagent_run", Result: "report", ExitCode: 0},
			{CallID: "c2", Tool: "memory"},
		}}
		res := ToolNotCalledAfter("subagent_run",
			[]string{"read_file", "list_files", "shell"}).Eval(trace)
		if !res.Pass {
			t.Errorf("expected pass; got %+v", res)
		}
	})

	t.Run("fails when parent re-reads after subagent returned", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "subagent_run", Result: "report", ExitCode: 0},
			{CallID: "c2", Tool: "read_file", Args: map[string]any{"path": "main.go"}},
		}}
		res := ToolNotCalledAfter("subagent_run",
			[]string{"read_file", "list_files", "shell"}).Eval(trace)
		if res.Pass {
			t.Errorf("expected fail (parent re-read after delegate); got pass")
		}
		if !strings.Contains(res.Detail, "subagent_run") || !strings.Contains(res.Detail, "read_file") {
			t.Errorf("detail should name both trigger and violating tool: %s", res.Detail)
		}
		if !strings.Contains(res.Detail, "step 0") || !strings.Contains(res.Detail, "step 1") {
			t.Errorf("detail should locate the call indices: %s", res.Detail)
		}
	})

	t.Run("passes vacuously when trigger never succeeded", func(t *testing.T) {
		// trigger ran but exited with error. The check does NOT restrict
		// the subsequent parent-side calls because a failed delegation
		// is exactly when parent verification is expected.
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "subagent_run", Result: "boom", ExitCode: 1, IsErr: true},
			{CallID: "c2", Tool: "read_file"},
		}}
		res := ToolNotCalledAfter("subagent_run",
			[]string{"read_file"}).Eval(trace)
		if !res.Pass {
			t.Errorf("vacuous pass on failed trigger; got %+v", res)
		}
	})

	t.Run("passes when trigger never called at all", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "read_file"},
		}}
		res := ToolNotCalledAfter("subagent_run",
			[]string{"read_file"}).Eval(trace)
		if !res.Pass {
			t.Errorf("vacuous pass when trigger absent; got %+v", res)
		}
	})

	t.Run("only the FIRST successful trigger anchors the restriction", func(t *testing.T) {
		// Two subagent_run calls; the first succeeded. The restricted
		// tool appears between them. Should fail because the read_file
		// is after the first successful trigger.
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "subagent_run", Result: "report1", ExitCode: 0},
			{CallID: "c2", Tool: "read_file"},
			{CallID: "c3", Tool: "subagent_run", Result: "report2", ExitCode: 0},
		}}
		res := ToolNotCalledAfter("subagent_run",
			[]string{"read_file"}).Eval(trace)
		if res.Pass {
			t.Errorf("read_file between two subagent calls should still violate; got pass")
		}
	})

	t.Run("restricted set respects nil and empty correctly", func(t *testing.T) {
		// Empty restriction list = no tool is restricted = always pass
		// after a successful trigger. Defensive: a scenario that forgot
		// to fill the list shouldn't blow up here.
		trace := Trace{Tools: []ToolCall{
			{CallID: "c1", Tool: "subagent_run", Result: "report", ExitCode: 0},
			{CallID: "c2", Tool: "read_file"},
		}}
		res := ToolNotCalledAfter("subagent_run", nil).Eval(trace)
		if !res.Pass {
			t.Errorf("empty restriction list should be vacuous pass; got %+v", res)
		}
	})

	t.Run("restricted tool BEFORE trigger is irrelevant", func(t *testing.T) {
		// Parent explored the area, then delegated. That's a different
		// (also-suboptimal) pattern but not what this check measures.
		// The check should only inspect tools AFTER the trigger.
		trace := Trace{Tools: []ToolCall{
			{CallID: "c0", Tool: "read_file"},
			{CallID: "c1", Tool: "subagent_run", Result: "report", ExitCode: 0},
		}}
		res := ToolNotCalledAfter("subagent_run",
			[]string{"read_file"}).Eval(trace)
		if !res.Pass {
			t.Errorf("read_file before trigger should not violate; got %+v", res)
		}
	})
}

func TestMaxToolCallsAfter(t *testing.T) {
	t.Run("passes at the cap", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "subagent_run", ExitCode: 0},
			{Tool: "memory"},
			{Tool: "memory"},
		}}
		if res := (MaxToolCallsAfter("subagent_run", 2)).Eval(trace); !res.Pass {
			t.Errorf("at-cap should pass; got %+v", res)
		}
	})

	t.Run("fails over the cap", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "subagent_run", ExitCode: 0},
			{Tool: "read_file"},
			{Tool: "list_files"},
			{Tool: "shell"},
		}}
		res := MaxToolCallsAfter("subagent_run", 1).Eval(trace)
		if res.Pass {
			t.Errorf("3 followups vs cap=1 must fail; got pass")
		}
		if !strings.Contains(res.Detail, "read_file") {
			t.Errorf("detail should list the followups: %s", res.Detail)
		}
	})

	t.Run("negative cap means unbounded", func(t *testing.T) {
		trace := Trace{Tools: []ToolCall{
			{Tool: "subagent_run", ExitCode: 0},
			{Tool: "read_file"},
			{Tool: "shell"},
		}}
		if res := (MaxToolCallsAfter("subagent_run", -1)).Eval(trace); !res.Pass {
			t.Errorf("negative cap = unbounded should pass; got %+v", res)
		}
	})

	t.Run("vacuous pass when trigger absent or failed", func(t *testing.T) {
		// Failed trigger.
		trace := Trace{Tools: []ToolCall{
			{Tool: "subagent_run", ExitCode: 1, IsErr: true},
			{Tool: "read_file"},
			{Tool: "read_file"},
			{Tool: "read_file"},
		}}
		if res := (MaxToolCallsAfter("subagent_run", 0)).Eval(trace); !res.Pass {
			t.Errorf("failed trigger -> vacuous pass; got %+v", res)
		}
		// Trigger never called.
		trace2 := Trace{Tools: []ToolCall{{Tool: "read_file"}, {Tool: "read_file"}}}
		if res := (MaxToolCallsAfter("subagent_run", 0)).Eval(trace2); !res.Pass {
			t.Errorf("missing trigger -> vacuous pass; got %+v", res)
		}
	})
}
