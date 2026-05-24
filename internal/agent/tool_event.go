package agent

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
	"github.com/affinefoundation/affent/internal/textutil"
)

func previewN(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:textutil.AlignBackward(s, n)] + "..."
}

func summarizeOriginalToolArgs(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	return previewN(raw, 512)
}

func toolRequestArgsView(args json.RawMessage) map[string]any {
	return toolRequestArgsEventView(args).Args
}

type toolRequestArgsEvent struct {
	Args         map[string]any
	Truncated    bool
	Bytes        int
	OmittedBytes int
	CapBytes     int
}

func toolRequestArgsEventView(args json.RawMessage) toolRequestArgsEvent {
	view := toolRequestArgsEvent{
		Args:     map[string]any{},
		Bytes:    len(args),
		CapBytes: maxToolRequestArgsEventBytes,
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		return view
	}
	cappedAny, omitted := capToolRequestArgValue(obj)
	capped, ok := cappedAny.(map[string]any)
	if !ok || capped == nil {
		return view
	}
	raw, err := json.Marshal(capped)
	if err == nil && len(raw) <= maxToolRequestArgsEventBytes {
		view.Args = capped
		view.Truncated = omitted > 0
		view.OmittedBytes = omitted
		return view
	}
	view.Args = map[string]any{
		"__affent_truncated":      fmt.Sprintf("tool request args exceeded %d-byte event cap", maxToolRequestArgsEventBytes),
		"__affent_original_bytes": len(args),
	}
	view.Truncated = true
	view.OmittedBytes = len(args)
	return view
}

func capToolRequestArgValue(v any) (any, int) {
	switch x := v.(type) {
	case string:
		if len(x) <= maxToolRequestArgStringBytes {
			return x, 0
		}
		cut := textutil.AlignBackward(x, maxToolRequestArgStringBytes)
		return x[:cut] + fmt.Sprintf("\n... [%d more bytes truncated from tool.request arg string]", len(x)-cut), len(x) - cut
	case map[string]any:
		out := make(map[string]any, len(x))
		omitted := 0
		for k, v := range x {
			capped, n := capToolRequestArgValue(v)
			out[k] = capped
			omitted += n
		}
		return out, omitted
	case []any:
		out := make([]any, len(x))
		omitted := 0
		for i, v := range x {
			capped, n := capToolRequestArgValue(v)
			out[i] = capped
			omitted += n
		}
		return out, omitted
	default:
		return v, 0
	}
}

func toolRuntimeStatsPtr(stats sse.ToolRuntimeStats) *sse.ToolRuntimeStats {
	if stats.ToolRequests == 0 &&
		stats.ToolNameCanonicalized == 0 &&
		stats.ToolArgsRepaired == 0 &&
		stats.ToolErrors == 0 &&
		stats.ToolDurationMS == 0 &&
		stats.LoopGuardInterventions == 0 &&
		stats.ForcedNoTools == 0 {
		return nil
	}
	return &stats
}

func toolResultEventPayload(callID string, exitCode int, result string) sse.ToolResultPayload {
	cappedResult, truncated, omitted := truncateForEventWithStats(result, MaxToolResultBytesInEvent)
	return sse.ToolResultPayload{
		CallID:             callID,
		ExitCode:           exitCode,
		ResultSummary:      previewN(result, MaxToolResultPreviewInEvent),
		Result:             cappedResult,
		ResultTruncated:    truncated,
		ResultBytes:        len(result),
		ResultOmittedBytes: omitted,
		ResultCapBytes:     MaxToolResultBytesInEvent,
	}
}

func toolResultEventPayloadForTurn(turnID, callID string, exitCode int, result string) sse.ToolResultPayload {
	payload := toolResultEventPayload(callID, exitCode, result)
	payload.TurnID = turnID
	return payload
}

func toolResultEventPayloadWithDuration(callID string, exitCode int, result string, duration time.Duration) sse.ToolResultPayload {
	payload := toolResultEventPayload(callID, exitCode, result)
	if duration >= time.Millisecond {
		payload.DurationMS = duration.Milliseconds()
	}
	return payload
}

func toolResultEventPayloadWithDurationForTurn(turnID, callID string, exitCode int, result string, duration time.Duration) sse.ToolResultPayload {
	payload := toolResultEventPayloadForTurn(turnID, callID, exitCode, result)
	if duration >= time.Millisecond {
		payload.DurationMS = duration.Milliseconds()
	}
	return payload
}

func truncateForEvent(s string, max int) string {
	out, _, _ := truncateForEventWithStats(s, max)
	return out
}

func truncateForEventWithStats(s string, max int) (string, bool, int) {
	if len(s) <= max {
		return s, false, 0
	}
	omitted := len(s) - max
	for {
		marker := fmt.Sprintf("\n... [%d more bytes truncated from tool.result event payload]", omitted)
		limit := max - len(marker)
		if limit <= 0 {
			cut := textutil.AlignBackward(s, max)
			return s[:cut], true, len(s) - cut
		}
		cut := textutil.AlignBackward(s, limit)
		actualOmitted := len(s) - cut
		if actualOmitted == omitted {
			return s[:cut] + marker, true, actualOmitted
		}
		omitted = actualOmitted
	}
}

func truncateForContext(s string, max int) string {
	if len(s) <= max {
		return s
	}
	cut := textutil.AlignBackward(s, max)
	return s[:cut] + fmt.Sprintf(
		"\n\n[... %d more bytes truncated. Re-run the command piping through head/tail/grep/sed, or save the output to a file inside the configured workspace and read it in chunks, if you need more.]",
		len(s)-cut,
	)
}
