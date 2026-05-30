package agent

import "testing"

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
	got := NewLLMSummaryCompactorForPolicy(SummaryCompactorPolicy{
		TriggerMsgs:                12,
		ModelContextWindowTokens:   100_000,
		CompactTriggerInputPercent: 80,
		ReservedOutputTokens:       30_000,
		KeepLast:                   3,
	})
	if got.TriggerMsgs != 12 || got.KeepLast != 3 {
		t.Fatalf("explicit retention = trigger:%d keep_last:%d, want 12/3", got.TriggerMsgs, got.KeepLast)
	}
	if got.TriggerBytes != 280_000 {
		t.Fatalf("trigger bytes = %d, want 280000 from 70k input capacity", got.TriggerBytes)
	}
	if got.MaxPromptBytes != DefaultSummaryPromptMaxBytes {
		t.Fatalf("large-window prompt cap = %d, want default %d", got.MaxPromptBytes, DefaultSummaryPromptMaxBytes)
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
