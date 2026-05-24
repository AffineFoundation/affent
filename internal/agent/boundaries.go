package agent

// RuntimeBoundaries is a read-only snapshot of hard runtime guardrails.
// It is intentionally not configuration: callers use it for diagnostics
// and docs so cap visibility stays in sync with the loop implementation.
type RuntimeBoundaries struct {
	LLMRequestBodyBytes    int
	LLMErrorBodyBytes      int
	StreamContentBytes     int
	StreamReasoningBytes   int
	StreamToolArgBytes     int
	StreamToolCalls        int
	StreamScannerBytes     int
	ToolRequestArgString   int
	ToolRequestArgsEvent   int
	ToolResultContextBytes int
	ToolResultPreviewBytes int
	ToolResultEventBytes   int
	RepairableToolArgBytes int
	ProjectContextBytes    int
	PlanSteps              int
	PlanStepTextBytes      int
	PlanNoteBytes          int
	PlanEvidenceRefs       int
	PlanEvidenceRefBytes   int
	PlanStateBytes         int
	ActivePlanStepBytes    int
	ActivePlanNoteBytes    int
	ActivePlanEvidenceRefs int
	ActivePlanEvidenceRef  int
}

func DefaultRuntimeBoundaries() RuntimeBoundaries {
	return RuntimeBoundaries{
		LLMRequestBodyBytes:    maxLLMRequestBodyBytes,
		LLMErrorBodyBytes:      maxLLMErrorBodyBytes,
		StreamContentBytes:     maxStreamContentBytes,
		StreamReasoningBytes:   maxStreamReasoningBytes,
		StreamToolArgBytes:     maxStreamToolArgBytes,
		StreamToolCalls:        maxStreamToolCalls,
		StreamScannerBytes:     streamScannerMaxBytes,
		ToolRequestArgString:   maxToolRequestArgStringBytes,
		ToolRequestArgsEvent:   maxToolRequestArgsEventBytes,
		ToolResultContextBytes: MaxToolResultBytesInContext,
		ToolResultPreviewBytes: MaxToolResultPreviewInEvent,
		ToolResultEventBytes:   MaxToolResultBytesInEvent,
		RepairableToolArgBytes: maxRepairableToolArgBytes,
		ProjectContextBytes:    MaxProjectContextBytes,
		PlanSteps:              maxPlanSteps,
		PlanStepTextBytes:      maxPlanStepTextBytes,
		PlanNoteBytes:          maxPlanNoteBytes,
		PlanEvidenceRefs:       maxPlanEvidence,
		PlanEvidenceRefBytes:   maxPlanEvidenceBytes,
		PlanStateBytes:         maxPlanStateBytes,
		ActivePlanStepBytes:    maxActivePlanStepTextBytes,
		ActivePlanNoteBytes:    maxActivePlanNoteBytes,
		ActivePlanEvidenceRefs: maxActivePlanEvidenceRefs,
		ActivePlanEvidenceRef:  maxActivePlanEvidenceRefBytes,
	}
}
