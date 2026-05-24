package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSummarizeDurableSessionTreatsBlankPlanAsMissing(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "blank-plan")
	if err := os.WriteFile(filepath.Join(pool.sessionDirPath("blank-plan"), "plan.json"), []byte(" \n\t "), 0o644); err != nil {
		t.Fatalf("write blank plan: %v", err)
	}

	summary, found, err := summarizeDurableSession(pool, "blank-plan")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if summary.HasPlan || summary.PlanSummary != nil {
		t.Fatalf("blank plan should be treated as missing, got %+v", summary)
	}
}
