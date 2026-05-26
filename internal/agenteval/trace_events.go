package agenteval

import (
	"encoding/json"
	"fmt"

	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/toolfailure"
)

func applyTraceEvent(t *Trace, pending map[string]int, typ string, data json.RawMessage, turnID string) (bool, error) {
	switch typ {
	case sse.TypeTraceMeta:
		var p sse.TraceMetaPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if p.SchemaVersion <= 0 {
			return false, fmt.Errorf("invalid trace schema_version %d", p.SchemaVersion)
		}
		if p.SchemaVersion > sse.TraceSchemaVersion {
			return false, fmt.Errorf("unsupported trace schema_version %d (max %d)", p.SchemaVersion, sse.TraceSchemaVersion)
		}
		t.SchemaVersion = p.SchemaVersion
	case sse.TypeRuntimeSurface:
		var p sse.RuntimeSurfacePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.RuntimeSurfaces = append(t.RuntimeSurfaces, p)
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
			TurnID:              p.TurnID,
			CallID:              p.CallID,
			Tool:                p.Tool,
			Args:                p.Args,
			ArgsTruncated:       p.ArgsTruncated,
			ArgsBytes:           p.ArgsBytes,
			ArgsOmittedBytes:    p.ArgsOmittedBytes,
			ArgsCapBytes:        p.ArgsCapBytes,
			OriginalTool:        p.OriginalTool,
			OriginalArgsSummary: p.OriginalArgsSummary,
			Canonicalized:       p.Canonicalized,
			ArgsRepaired:        p.ArgsRepaired,
			RepairNotes:         p.RepairNotes,
			Delegation:          p.Delegation,
		})
	case sse.TypeToolResult:
		var p sse.ToolResultPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		if idx, ok := pending[p.CallID]; ok {
			failureKinds := toolResultFailureKinds(t.Tools[idx].Tool, p)
			failureKind := firstString(failureKinds)
			if t.Tools[idx].TurnID == "" {
				t.Tools[idx].TurnID = p.TurnID
			}
			t.Tools[idx].Result = p.Result
			t.Tools[idx].ResultTruncated = p.ResultTruncated
			t.Tools[idx].ResultBytes = p.ResultBytes
			t.Tools[idx].ResultOmittedBytes = p.ResultOmittedBytes
			t.Tools[idx].ResultCapBytes = p.ResultCapBytes
			t.Tools[idx].ResultArtifactPath = p.ResultArtifactPath
			t.Tools[idx].FailureKind = failureKind
			t.Tools[idx].FailureKinds = failureKinds
			t.Tools[idx].ExitCode = p.ExitCode
			t.Tools[idx].DurationMS = p.DurationMS
			t.Tools[idx].IsErr = p.ExitCode != 0
			// If a stream-cut or replay split the request/result events,
			// the result might carry delegation while the request didn't
			// (or vice versa). Prefer whichever side has it; the runtime
			// publishes the same DelegationMeta on both, so this is a
			// stale-data fix-up, not a divergence.
			if t.Tools[idx].Delegation == nil && p.Delegation != nil {
				t.Tools[idx].Delegation = p.Delegation
			}
			return false, nil
		}
		failureKinds := toolResultFailureKinds("", p)
		failureKind := firstString(failureKinds)
		t.Tools = append(t.Tools, ToolCall{
			TurnID:             p.TurnID,
			CallID:             p.CallID,
			Result:             p.Result,
			ResultTruncated:    p.ResultTruncated,
			ResultBytes:        p.ResultBytes,
			ResultOmittedBytes: p.ResultOmittedBytes,
			ResultCapBytes:     p.ResultCapBytes,
			ResultArtifactPath: p.ResultArtifactPath,
			FailureKind:        failureKind,
			FailureKinds:       failureKinds,
			ExitCode:           p.ExitCode,
			DurationMS:         p.DurationMS,
			IsErr:              p.ExitCode != 0,
			Delegation:         p.Delegation,
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
					ToolRequests:               p.ToolStats.ToolRequests,
					ToolNameCanonicalized:      p.ToolStats.ToolNameCanonicalized,
					ToolArgsRepaired:           p.ToolStats.ToolArgsRepaired,
					ToolRepairCalls:            p.ToolStats.ToolRepairCalls,
					ToolRepairSucceeded:        p.ToolStats.ToolRepairSucceeded,
					ToolRepairFailed:           p.ToolStats.ToolRepairFailed,
					ToolRepairNotes:            p.ToolStats.ToolRepairNotes,
					ToolRepairByKind:           p.ToolStats.ToolRepairByKind,
					ToolFailureByKind:          p.ToolStats.ToolFailureByKind,
					ToolErrors:                 p.ToolStats.ToolErrors,
					ToolDurationMS:             p.ToolStats.ToolDurationMS,
					LoopGuardInterventions:     p.ToolStats.LoopGuardInterventions,
					ForcedNoTools:              p.ToolStats.ForcedNoTools,
					SourceAccessResults:        p.ToolStats.SourceAccessResults,
					SourceAccessVerified:       p.ToolStats.SourceAccessVerified,
					SourceAccessDiscoveryOnly:  p.ToolStats.SourceAccessDiscoveryOnly,
					SourceAccessNetwork:        p.ToolStats.SourceAccessNetwork,
					SourceAccessDynamicPartial: p.ToolStats.SourceAccessDynamicPartial,
					MemoryUpdates:              p.ToolStats.MemoryUpdates,
					MemoryUpdateAdd:            p.ToolStats.MemoryUpdateAdd,
					MemoryUpdateReplace:        p.ToolStats.MemoryUpdateReplace,
					MemoryUpdateRemove:         p.ToolStats.MemoryUpdateRemove,
					ToolContextTruncated:       p.ToolStats.ToolContextTruncated,
					ToolContextOmittedBytes:    p.ToolStats.ToolContextOmittedBytes,
				}
			}
			return true, nil
		}
	case sse.TypeLoopDecision:
		var p sse.LoopDecisionPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.LoopDecisions = append(t.LoopDecisions, LoopDecision{
			Kind:           p.Kind,
			Decision:       p.Decision,
			Trigger:        p.Trigger,
			Confidence:     p.Confidence,
			Reason:         p.Reason,
			RequiredAction: p.RequiredAction,
			TurnID:         p.TurnID,
			DecisionID:     p.DecisionID,
		})
	case sse.TypeContextCompact:
		var p sse.ContextCompactPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.ContextCompactions = append(t.ContextCompactions, ContextCompaction{
			TurnID:          p.TurnID,
			BeforeMessages:  p.BeforeMessages,
			AfterMessages:   p.AfterMessages,
			RemovedMessages: p.RemovedMessages,
			Reactive:        p.Reactive,
			Reason:          p.Reason,
			SummaryPresent:  p.SummaryPresent,
			SummaryBytes:    p.SummaryBytes,
		})
	case sse.TypeError:
		var p sse.ErrorPayload
		if err := json.Unmarshal(data, &p); err == nil && p.Message != "" {
			t.LoopErrors = append(t.LoopErrors, p.Message)
			if p.FailureKind != "" {
				t.LoopErrorKinds = append(t.LoopErrorKinds, p.FailureKind)
				t.RuntimeErrors = append(t.RuntimeErrors, RuntimeErrorExample{
					Kind:    p.FailureKind,
					Message: p.Message,
				})
			}
		}
	}
	return false, nil
}

func toolResultFailureKinds(tool string, p sse.ToolResultPayload) []string {
	var kinds []string
	if p.FailureKind != "" {
		kinds = append(kinds, p.FailureKind)
	}
	for _, kind := range p.FailureKinds {
		if !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	for _, kind := range toolfailure.KindsForResult(tool, p.Result, p.ExitCode != 0) {
		if !containsString(kinds, kind) {
			kinds = append(kinds, kind)
		}
	}
	return kinds
}

func firstString(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func traceEventMatchesTurn(eventTurnID, wantTurnID string) bool {
	return wantTurnID == "" || eventTurnID == "" || eventTurnID == wantTurnID
}
