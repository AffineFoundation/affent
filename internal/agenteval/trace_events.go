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
	case sse.TypeUserMessage:
		var p sse.UserMessagePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.UserMessages = append(t.UserMessages, UserMessage{
			TurnID:       p.TurnID,
			Text:         p.Text,
			DisplayText:  p.DisplayText,
			Mode:         p.Mode,
			Source:       p.Source,
			ScheduleID:   p.ScheduleID,
			ScheduleKind: p.ScheduleKind,
		})
	case sse.TypeConversationRepaired:
		var p sse.ConversationRepairedPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		t.ConversationRepairs = append(t.ConversationRepairs, p)
	case sse.TypeRuntimeSurface:
		var p sse.RuntimeSurfacePayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.RuntimeSurfaces = append(t.RuntimeSurfaces, p)
	case sse.TypeContextInjected:
		var p sse.ContextInjectedPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.ContextInjections = append(t.ContextInjections, ContextInjection{
			TurnID:          p.TurnID,
			Source:          p.Source,
			Title:           p.Title,
			Summary:         p.Summary,
			Preview:         p.Preview,
			Bytes:           p.Bytes,
			EstimatedTokens: p.EstimatedTokens,
		})
	case sse.TypeMessageDone:
		var p sse.MessageDonePayload
		if err := json.Unmarshal(data, &p); err == nil {
			if !traceEventMatchesTurn(p.TurnID, turnID) {
				return false, nil
			}
			t.FinalText = p.Text
			t.FinishReason = p.FinishReason
		}
	case sse.TypeMessageRejected:
		var p sse.MessageRejectedPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.MessageRejections = append(t.MessageRejections, MessageRejected{
			TurnID:         p.TurnID,
			Text:           p.Text,
			Reason:         p.Reason,
			Trigger:        p.Trigger,
			RequiredAction: p.RequiredAction,
		})
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
			t.Tools[idx].ResultSummary = p.ResultSummary
			t.Tools[idx].ResultTruncated = p.ResultTruncated
			t.Tools[idx].ResultBytes = p.ResultBytes
			t.Tools[idx].ResultOmittedBytes = p.ResultOmittedBytes
			t.Tools[idx].ResultCapBytes = p.ResultCapBytes
			t.Tools[idx].ResultArtifactPath = p.ResultArtifactPath
			t.Tools[idx].ContextBytes = p.ContextBytes
			t.Tools[idx].ContextOmittedBytes = p.ContextOmittedBytes
			t.Tools[idx].ContextEstimatedTokens = p.ContextEstimatedTokens
			t.Tools[idx].FailureKind = failureKind
			t.Tools[idx].FailureKinds = failureKinds
			t.Tools[idx].ExitCode = p.ExitCode
			t.Tools[idx].DurationMS = p.DurationMS
			t.Tools[idx].IsErr = p.ExitCode != 0
			t.Tools[idx].MemoryUpdate = p.MemoryUpdate
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
			TurnID:                 p.TurnID,
			CallID:                 p.CallID,
			Result:                 p.Result,
			ResultSummary:          p.ResultSummary,
			ResultTruncated:        p.ResultTruncated,
			ResultBytes:            p.ResultBytes,
			ResultOmittedBytes:     p.ResultOmittedBytes,
			ResultCapBytes:         p.ResultCapBytes,
			ResultArtifactPath:     p.ResultArtifactPath,
			ContextBytes:           p.ContextBytes,
			ContextOmittedBytes:    p.ContextOmittedBytes,
			ContextEstimatedTokens: p.ContextEstimatedTokens,
			FailureKind:            failureKind,
			FailureKinds:           failureKinds,
			ExitCode:               p.ExitCode,
			DurationMS:             p.DurationMS,
			IsErr:                  p.ExitCode != 0,
			Delegation:             p.Delegation,
			MemoryUpdate:           p.MemoryUpdate,
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
			hadTurnEnd := t.TurnEndReason != ""
			t.TurnEndReason = p.Reason
			if p.ToolStats != nil {
				stats := ToolRuntimeStats{
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
					MemorySearchCalls:          p.ToolStats.MemorySearchCalls,
					MemorySearchMisses:         p.ToolStats.MemorySearchMisses,
					SessionSearchCalls:         p.ToolStats.SessionSearchCalls,
					SessionSearchResults:       p.ToolStats.SessionSearchResults,
					SessionSearchContextHits:   p.ToolStats.SessionSearchContextHits,
					SessionSearchMatchedTerms:  p.ToolStats.SessionSearchMatchedTerms,
					SessionSearchRecent:        p.ToolStats.SessionSearchRecent,
					ToolContextTruncated:       p.ToolStats.ToolContextTruncated,
					ToolContextOmittedBytes:    p.ToolStats.ToolContextOmittedBytes,
				}
				if hadTurnEnd && turnID == "" {
					t.ToolStats = mergeToolRuntimeStats(t.ToolStats, stats)
				} else {
					t.ToolStats = stats
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
			TokenBudget:    p.TokenBudget,
			BudgetBytes:    p.BudgetBytes,
			TurnID:         p.TurnID,
			DecisionID:     p.DecisionID,
		})
	case sse.TypeLoopProtocolFeed:
		var p sse.LoopProtocolFeedPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.LoopProtocolFeeds = append(t.LoopProtocolFeeds, LoopProtocolFeed{
			TurnID:                     p.TurnID,
			LoopID:                     p.LoopID,
			Status:                     p.Status,
			Mode:                       p.Mode,
			FeedNumber:                 p.FeedNumber,
			ProtocolFeeds:              p.ProtocolFeeds,
			CalibrationAnswers:         p.CalibrationAnswers,
			LastCalibrationAnswer:      p.LastCalibrationAnswer,
			ProtocolPath:               p.ProtocolPath,
			CurrentSituation:           p.CurrentSituation,
			PlanLabel:                  p.PlanLabel,
			PlanCurrentStepIndex:       p.PlanCurrentStepIndex,
			PlanCurrentStepStatus:      p.PlanCurrentStepStatus,
			PlanCurrentStep:            p.PlanCurrentStep,
			LastTurnID:                 p.LastTurnID,
			LastTurnEndReason:          p.LastTurnEndReason,
			LastTurnToolRequests:       p.LastTurnToolRequests,
			LastTurnToolErrors:         p.LastTurnToolErrors,
			LastTurnForcedNoTools:      p.LastTurnForcedNoTools,
			LastTurnMemoryUpdates:      p.LastTurnMemoryUpdates,
			LastTurnMemorySearchCalls:  p.LastTurnMemorySearchCalls,
			LastTurnMemorySearchMisses: p.LastTurnMemorySearchMisses,
			LastTurnSessionSearchCalls: p.LastTurnSessionSearchCalls,
			LastTurnLoopGuards:         p.LastTurnLoopGuards,
			LastDecisionKind:           p.LastDecisionKind,
			LastDecisionTrigger:        p.LastDecisionTrigger,
			LastDecision:               p.LastDecision,
			LastDecisionConfidence:     p.LastDecisionConfidence,
			LastDecisionReason:         p.LastDecisionReason,
			LastDecisionAction:         p.LastDecisionAction,
		})
	case sse.TypeLoopCalibrationRequest:
		var p sse.LoopProtocolCalibrationPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		t.LoopProtocolCalibrationRequests = append(t.LoopProtocolCalibrationRequests, LoopProtocolCalibration{
			LoopID:                  p.LoopID,
			Status:                  p.Status,
			CalibrationQuestions:    p.CalibrationQuestions,
			LastCalibrationQuestion: p.LastCalibrationQuestion,
			CalibrationAnswers:      p.CalibrationAnswers,
			LastCalibrationAnswer:   p.LastCalibrationAnswer,
			ProtocolPath:            p.ProtocolPath,
			EventSeq:                p.EventSeq,
		})
	case sse.TypeLoopCalibration:
		var p sse.LoopProtocolCalibrationPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		t.LoopProtocolCalibrations = append(t.LoopProtocolCalibrations, LoopProtocolCalibration{
			LoopID:                  p.LoopID,
			Status:                  p.Status,
			CalibrationQuestions:    p.CalibrationQuestions,
			LastCalibrationQuestion: p.LastCalibrationQuestion,
			CalibrationAnswers:      p.CalibrationAnswers,
			LastCalibrationAnswer:   p.LastCalibrationAnswer,
			ProtocolPath:            p.ProtocolPath,
			EventSeq:                p.EventSeq,
		})
	case sse.TypeLoopTurnCheckpoint:
		var p sse.LoopTurnCheckpointPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.LoopTurnCheckpoints = append(t.LoopTurnCheckpoints, LoopTurnCheckpoint{
			TurnID:             p.TurnID,
			LoopID:             p.LoopID,
			Status:             p.Status,
			ProtocolPath:       p.ProtocolPath,
			EventSeq:           p.EventSeq,
			TurnCheckpoints:    p.TurnCheckpoints,
			EndReason:          p.EndReason,
			InputTokens:        p.InputTokens,
			OutputTokens:       p.OutputTokens,
			ToolRequests:       p.ToolRequests,
			ToolErrors:         p.ToolErrors,
			LoopGuards:         p.LoopGuards,
			ForcedNoTools:      p.ForcedNoTools,
			MemoryUpdates:      p.MemoryUpdates,
			MemorySearchCalls:  p.MemorySearchCalls,
			MemoryMisses:       p.MemoryMisses,
			SessionSearchCalls: p.SessionSearchCalls,
		})
	case sse.TypeContextCompact:
		var p sse.ContextCompactPayload
		if err := json.Unmarshal(data, &p); err != nil {
			return false, err
		}
		var raw struct {
			SummaryPresent *bool `json:"summary_present"`
		}
		_ = json.Unmarshal(data, &raw)
		if !traceEventMatchesTurn(p.TurnID, turnID) {
			return false, nil
		}
		t.ContextCompactions = append(t.ContextCompactions, ContextCompaction{
			TurnID:              p.TurnID,
			BeforeMessages:      p.BeforeMessages,
			AfterMessages:       p.AfterMessages,
			RemovedMessages:     p.RemovedMessages,
			BeforeBytes:         p.BeforeBytes,
			AfterBytes:          p.AfterBytes,
			ReducedBytes:        p.ReducedBytes,
			Reactive:            p.Reactive,
			Reason:              p.Reason,
			SummaryPresent:      p.SummaryPresent,
			SummaryPresentKnown: raw.SummaryPresent != nil,
			SummaryBytes:        p.SummaryBytes,
			SummaryPreview:      p.SummaryPreview,
			LoopProtocolAnchor:  p.LoopProtocolAnchor,
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

func appendTraceEventRef(t *Trace, typ string, data json.RawMessage, turnID string) {
	ref, ok := traceEventRefFromPayload(typ, data, turnID)
	if !ok {
		return
	}
	ref.Index = len(t.EventOrder) + 1
	t.EventOrder = append(t.EventOrder, ref)
}

func traceEventRefFromPayload(typ string, data json.RawMessage, turnID string) (TraceEventRef, bool) {
	switch typ {
	case sse.TypeLoopProtocolFeed:
		var p sse.LoopProtocolFeedPayload
		if err := json.Unmarshal(data, &p); err != nil || !traceEventMatchesTurn(p.TurnID, turnID) {
			return TraceEventRef{}, false
		}
		return TraceEventRef{
			Type:             typ,
			TurnID:           p.TurnID,
			LoopProtocolMode: p.Mode,
			LoopProtocolPath: p.ProtocolPath,
		}, true
	case sse.TypeContextInjected:
		var p sse.ContextInjectedPayload
		if err := json.Unmarshal(data, &p); err != nil || !traceEventMatchesTurn(p.TurnID, turnID) {
			return TraceEventRef{}, false
		}
		return TraceEventRef{
			Type:          typ,
			TurnID:        p.TurnID,
			ContextSource: p.Source,
		}, true
	case sse.TypeContextCompact:
		var p sse.ContextCompactPayload
		if err := json.Unmarshal(data, &p); err != nil || !traceEventMatchesTurn(p.TurnID, turnID) {
			return TraceEventRef{}, false
		}
		return TraceEventRef{
			Type:            typ,
			TurnID:          p.TurnID,
			ContextReason:   p.Reason,
			ContextReactive: p.Reactive,
		}, true
	case sse.TypeLoopTurnCheckpoint:
		var p sse.LoopTurnCheckpointPayload
		if err := json.Unmarshal(data, &p); err != nil || !traceEventMatchesTurn(p.TurnID, turnID) {
			return TraceEventRef{}, false
		}
		return TraceEventRef{
			Type:             typ,
			TurnID:           p.TurnID,
			LoopProtocolPath: p.ProtocolPath,
		}, true
	default:
		return TraceEventRef{}, false
	}
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

func mergeToolRuntimeStats(a, b ToolRuntimeStats) ToolRuntimeStats {
	a.ToolRequests += b.ToolRequests
	a.ToolNameCanonicalized += b.ToolNameCanonicalized
	a.ToolArgsRepaired += b.ToolArgsRepaired
	a.ToolRepairCalls += b.ToolRepairCalls
	a.ToolRepairSucceeded += b.ToolRepairSucceeded
	a.ToolRepairFailed += b.ToolRepairFailed
	a.ToolRepairNotes += b.ToolRepairNotes
	a.ToolRepairByKind = mergeStringIntMap(a.ToolRepairByKind, b.ToolRepairByKind)
	a.ToolFailureByKind = mergeStringIntMap(a.ToolFailureByKind, b.ToolFailureByKind)
	a.ToolErrors += b.ToolErrors
	a.ToolDurationMS += b.ToolDurationMS
	a.LoopGuardInterventions += b.LoopGuardInterventions
	a.ForcedNoTools += b.ForcedNoTools
	a.SourceAccessResults += b.SourceAccessResults
	a.SourceAccessVerified += b.SourceAccessVerified
	a.SourceAccessDiscoveryOnly += b.SourceAccessDiscoveryOnly
	a.SourceAccessNetwork += b.SourceAccessNetwork
	a.SourceAccessDynamicPartial += b.SourceAccessDynamicPartial
	a.MemoryUpdates += b.MemoryUpdates
	a.MemoryUpdateAdd += b.MemoryUpdateAdd
	a.MemoryUpdateReplace += b.MemoryUpdateReplace
	a.MemoryUpdateRemove += b.MemoryUpdateRemove
	a.MemorySearchCalls += b.MemorySearchCalls
	a.MemorySearchMisses += b.MemorySearchMisses
	a.SessionSearchCalls += b.SessionSearchCalls
	a.SessionSearchResults += b.SessionSearchResults
	a.SessionSearchContextHits += b.SessionSearchContextHits
	a.SessionSearchMatchedTerms += b.SessionSearchMatchedTerms
	a.SessionSearchRecent += b.SessionSearchRecent
	a.ToolContextTruncated += b.ToolContextTruncated
	a.ToolContextOmittedBytes += b.ToolContextOmittedBytes
	return a
}

func mergeStringIntMap(a, b map[string]int) map[string]int {
	if len(a) == 0 && len(b) == 0 {
		return nil
	}
	out := make(map[string]int, len(a)+len(b))
	for key, value := range a {
		out[key] += value
	}
	for key, value := range b {
		out[key] += value
	}
	return out
}
