package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/affinefoundation/affent/internal/agent"
)

func TestSummarizeDurableSessionIncludesRuntimeSkillNames(t *testing.T) {
	memRoot := t.TempDir()
	pool := newPoolWithMemoryRoot(t, memRoot)
	createDurableSessionDir(t, pool, "skills")
	skillDir := agent.DefaultWorkspaceSkillDir(pool.sessionDirPath("skills"))
	for _, name := range []string{"deploy_review", "incident_triage"} {
		if _, err := agent.InstallRuntimeSkill(skillDir, agent.Skill{
			Name: name,
			Body: "AFFENT ACTIVE SKILL: " + name + "\nUse this workflow.",
		}); err != nil {
			t.Fatalf("install %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(skillDir, ".pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "note.txt"), []byte("not a skill"), 0o644); err != nil {
		t.Fatal(err)
	}

	summary, found, err := summarizeDurableSession(pool, "skills")
	if err != nil {
		t.Fatalf("summarizeDurableSession: %v", err)
	}
	if !found {
		t.Fatal("durable session should be found")
	}
	if !summary.HasRuntimeSkills {
		t.Fatalf("HasRuntimeSkills = false, summary=%+v", summary)
	}
	want := []string{"deploy_review", "incident_triage"}
	if !reflect.DeepEqual(summary.RuntimeSkillNames, want) {
		t.Fatalf("RuntimeSkillNames = %+v, want %+v", summary.RuntimeSkillNames, want)
	}
}

func TestDurableRuntimeSkillNamesIgnoresPendingAndSymlinks(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".pending"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "half_skill"), 0o755); err != nil {
		t.Fatal(err)
	}
	outside := t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "SKILL.md"), []byte("AFFENT ACTIVE SKILL: outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "linked_skill")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if got := durableRuntimeSkillNames(root); len(got) != 0 {
		t.Fatalf("durableRuntimeSkillNames = %+v, want none", got)
	}
}
