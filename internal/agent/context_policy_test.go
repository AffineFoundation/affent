package agent

import "testing"

func TestModelInputCapacityTokens(t *testing.T) {
	tests := []struct {
		name                     string
		modelContextWindowTokens int
		reservedOutputTokens     int
		wantHardInputLimitTokens int
	}{
		{name: "unknown window", wantHardInputLimitTokens: 0},
		{name: "no reserve", modelContextWindowTokens: 100_000, wantHardInputLimitTokens: 100_000},
		{name: "reserves output", modelContextWindowTokens: 100_000, reservedOutputTokens: 30_000, wantHardInputLimitTokens: 70_000},
		{name: "reserve cannot erase input capacity", modelContextWindowTokens: 1_000, reservedOutputTokens: 2_000, wantHardInputLimitTokens: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ModelInputCapacityTokens(tt.modelContextWindowTokens, tt.reservedOutputTokens)
			if got != tt.wantHardInputLimitTokens {
				t.Fatalf("ModelInputCapacityTokens(%d, %d) = %d, want %d", tt.modelContextWindowTokens, tt.reservedOutputTokens, got, tt.wantHardInputLimitTokens)
			}
		})
	}
}

func TestRequestInputPressureForLimit(t *testing.T) {
	tests := []struct {
		name          string
		estimated     int
		limit         int
		wantPercent   int
		wantRemaining int
	}{
		{name: "disabled", estimated: 100, wantPercent: 0, wantRemaining: 0},
		{name: "below limit", estimated: 750, limit: 1000, wantPercent: 75, wantRemaining: 250},
		{name: "over limit", estimated: 1500, limit: 1000, wantPercent: 150, wantRemaining: 0},
		{name: "small pressure rounds to zero", estimated: 111, limit: 80000, wantPercent: 0, wantRemaining: 79889},
		{name: "rounds to nearest percent", estimated: 63000, limit: 56000, wantPercent: 113, wantRemaining: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RequestInputPressureForLimit(tt.estimated, tt.limit)
			if got.Percent != tt.wantPercent || got.TokensUntilLimit != tt.wantRemaining {
				t.Fatalf("RequestInputPressureForLimit(%d, %d) = %+v, want percent=%d remaining=%d", tt.estimated, tt.limit, got, tt.wantPercent, tt.wantRemaining)
			}
		})
	}
}

func TestNewLLMSummaryCompactorForPolicyUsesDefaults(t *testing.T) {
	got := NewLLMSummaryCompactorForPolicy(SummaryCompactorPolicy{})
	if got.TriggerMsgs != DefaultSummaryTriggerMsgs ||
		got.TriggerBytes != DefaultSummaryTriggerBytes ||
		got.KeepLast != DefaultSummaryKeepLast ||
		got.MaxPromptBytes != DefaultSummaryPromptMaxBytes {
		t.Fatalf("default compactor = %+v", got)
	}
}

func TestNewLLMSummaryCompactorForPolicyAlignsWithModelWindow(t *testing.T) {
	policy := SummaryCompactorPolicy{
		TriggerMsgs:                12,
		ModelContextWindowTokens:   100_000,
		CompactTriggerInputPercent: 80,
		ReservedOutputTokens:       30_000,
		KeepLast:                   3,
	}
	got := NewLLMSummaryCompactorForPolicy(policy)
	if got.TriggerMsgs != 12 || got.KeepLast != 3 {
		t.Fatalf("explicit retention = trigger:%d keep_last:%d, want 12/3", got.TriggerMsgs, got.KeepLast)
	}
	if got.TriggerBytes != 224_000 {
		t.Fatalf("trigger bytes = %d, want 224000 from 80%% of 70k input capacity", got.TriggerBytes)
	}
	if got.MaxPromptBytes != DefaultSummaryPromptMaxBytes {
		t.Fatalf("large-window prompt cap = %d, want default %d", got.MaxPromptBytes, DefaultSummaryPromptMaxBytes)
	}
	resolved := ResolveSummaryCompactorPolicy(policy)
	if resolved.TriggerInputTokens != 56_000 ||
		resolved.HardInputLimitTokens != 70_000 ||
		resolved.TriggerBytes != got.TriggerBytes ||
		resolved.MaxPromptBytes != got.MaxPromptBytes {
		t.Fatalf("resolved policy = %+v, compactor = %+v", resolved, got)
	}
}

func TestNewLLMSummaryCompactorForPolicyDoesNotApplyModelBytesToExplicitTrigger(t *testing.T) {
	got := NewLLMSummaryCompactorForPolicy(SummaryCompactorPolicy{
		TriggerInputTokens:       42_000,
		ModelContextWindowTokens: 100_000,
		ReservedOutputTokens:     30_000,
	})
	if got.TriggerBytes != DefaultSummaryTriggerBytes {
		t.Fatalf("explicit request trigger should keep byte trigger default, got %d", got.TriggerBytes)
	}
}

func TestNewLLMSummaryCompactorForPolicyCapsSmallWindowPrompt(t *testing.T) {
	got := NewLLMSummaryCompactorForPolicy(SummaryCompactorPolicy{
		ModelContextWindowTokens:   200,
		CompactTriggerInputPercent: 80,
	})
	if got.TriggerBytes != 640 || got.MaxPromptBytes != 640 {
		t.Fatalf("small-window compactor = trigger_bytes:%d prompt:%d, want 640/640", got.TriggerBytes, got.MaxPromptBytes)
	}
}
