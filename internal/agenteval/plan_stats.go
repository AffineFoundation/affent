package agenteval

import (
	"strings"

	"github.com/affinefoundation/affent/internal/agent"
)

// PlanStats summarizes persisted-plan tool usage observed in a Trace.
// It is intentionally derived from the existing tool.request surface so
// evals can track plan-mode behavior without adding runtime state.
type PlanStats struct {
	Calls    int
	ByAction map[string]int
	Errors   int
}

func (s PlanStats) HasAny() bool {
	return s.Calls > 0
}

// PlanStats walks the trace tool timeline and aggregates plan calls by
// action (view/set/update/clear). Calls whose action is missing or not a
// string are grouped under "unknown" so malformed model behavior remains
// visible instead of disappearing from eval output.
func (t Trace) PlanStats() PlanStats {
	var s PlanStats
	for _, c := range t.Tools {
		if c.Tool != agent.PlanToolName {
			continue
		}
		s.Calls++
		if c.IsErr || c.ExitCode != 0 {
			s.Errors++
		}
		action := planActionFromArgs(c.Args)
		if s.ByAction == nil {
			s.ByAction = map[string]int{}
		}
		s.ByAction[action]++
	}
	return s
}

func planActionFromArgs(args map[string]any) string {
	action, _ := args["action"].(string)
	action = strings.ToLower(strings.TrimSpace(action))
	if action == "" {
		return "unknown"
	}
	return action
}
