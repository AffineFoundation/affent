package agenteval

import (
	"encoding/json"

	"github.com/affinefoundation/affent/internal/sse"
)

func applyTraceEvent(t *Trace, pending map[string]int, typ string, data json.RawMessage, turnID string) (bool, error) {
	switch typ {
	case sse.TypeMessageDone:
		var p sse.MessageDonePayload
		if err := json.Unmarshal(data, &p); err == nil {
			if !traceEventMatchesTurn(p.TurnID, turnID) {
				return false, nil
			}
			t.FinalText = p.Text
			t.FinishReason = p.FinishReason
		}
	case sse.TypeToolRequest:
		var p sse.ToolRequestPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		pending[p.CallID] = len(t.Tools)
		t.Tools = append(t.Tools, ToolCall{
			CallID:              p.CallID,
			Tool:                p.Tool,
			Args:                p.Args,
			OriginalTool:        p.OriginalTool,
			OriginalArgsSummary: p.OriginalArgsSummary,
			Canonicalized:       p.Canonicalized,
			ArgsRepaired:        p.ArgsRepaired,
			RepairNotes:         p.RepairNotes,
		})
	case sse.TypeToolResult:
		var p sse.ToolResultPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if idx, ok := pending[p.CallID]; ok {
			t.Tools[idx].Result = p.Result
			t.Tools[idx].ExitCode = p.ExitCode
			t.Tools[idx].DurationMS = p.DurationMS
			t.Tools[idx].IsErr = p.ExitCode != 0
			return false, nil
		}
		t.Tools = append(t.Tools, ToolCall{
			CallID:     p.CallID,
			Result:     p.Result,
			ExitCode:   p.ExitCode,
			DurationMS: p.DurationMS,
			IsErr:      p.ExitCode != 0,
		})
	case sse.TypeUsage:
		var p sse.UsagePayload
		if err := json.Unmarshal(data, &p); err == nil {
			if !traceEventMatchesTurn(p.TurnID, turnID) {
				return false, nil
			}
			t.Usage.InputTokens += p.InputTokens
			t.Usage.OutputTokens += p.OutputTokens
		}
	case sse.TypeTurnEnd:
		var p sse.TurnEndPayload
		if err := json.Unmarshal(data, &p); err == nil && p.Reason != "" {
			if !traceEventMatchesTurn(p.TurnID, turnID) {
				return false, nil
			}
			t.TurnEndReason = p.Reason
			if p.ToolStats != nil {
				t.ToolStats = ToolRuntimeStats{
					ToolRequests:           p.ToolStats.ToolRequests,
					ToolNameCanonicalized:  p.ToolStats.ToolNameCanonicalized,
					ToolArgsRepaired:       p.ToolStats.ToolArgsRepaired,
					ToolErrors:             p.ToolStats.ToolErrors,
					LoopGuardInterventions: p.ToolStats.LoopGuardInterventions,
					ForcedNoTools:          p.ToolStats.ForcedNoTools,
				}
			}
			return true, nil
		}
	case sse.TypeError:
		var p sse.ErrorPayload
		if err := json.Unmarshal(data, &p); err == nil && p.Message != "" {
			t.LoopErrors = append(t.LoopErrors, p.Message)
		}
	}
	return false, nil
}

func traceEventMatchesTurn(eventTurnID, wantTurnID string) bool {
	return wantTurnID == "" || eventTurnID == "" || eventTurnID == wantTurnID
}
