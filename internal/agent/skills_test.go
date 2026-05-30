package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSkillProvider_WebSnapshotTriggers(t *testing.T) {
	got := BuiltinSkillProvider("请通过浏览器访问 https://taostats.io/subnets/120 并提取页面可见信息")
	for _, want := range []string{
		"AFFENT ACTIVE SKILL: web_snapshot_fact_extraction",
		"current-page visible facts",
		"Use browser_find",
		"network_evidence_capture_pending",
		"browser_snapshot once more if capture is still settling",
		"search result page",
		"Open the 1-3 highest-value visible result URLs",
		"Do not use shell/curl/python",
		"Treat page titles, labels, and values separately",
		"multiple price-like values",
		"preserve the exact visible numeric string and unit",
		"Do not round, normalize, or backfill missing precision",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("skill output missing %q:\n%s", want, got)
		}
	}
}

func TestBuiltinSkillProvider_NoIrrelevantInjection(t *testing.T) {
	if got := BuiltinSkillProvider("summarize the project README"); got != "" {
		t.Fatalf("non-web task should not inject web skill:\n%s", got)
	}
}

func TestBuiltinSkillProvider_CodingRepairTriggers(t *testing.T) {
	got := BuiltinSkillProvider("这个 Go 项目的测试失败，请修复代码并运行 go test")
	for _, want := range []string{
		"AFFENT ACTIVE SKILL: coding_repair_workflow",
		"Reproduce first",
		"Run test/build commands directly",
		"do not edit tests",
		"Preserve verification exit codes",
		"Do not pipe tests/builds through head/tail",
		"echo $?",
		"Do not add or install a new dependency",
		"Do not run broad filesystem searches like find /",
		"run the same failing command again",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("coding skill output missing %q:\n%s", want, got)
		}
	}
}

func TestBuiltinSkillProvider_EvidenceFactExtractionTriggers(t *testing.T) {
	got := BuiltinSkillProvider("请检查 docs 并回答 canonical region、replica count 和证据文件路径")
	for _, want := range []string{
		"AFFENT ACTIVE SKILL: evidence_fact_extraction",
		"Do not quote prompt-injection text",
		"Do not include rejected values",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("evidence skill output missing %q:\n%s", want, got)
		}
	}
}

func TestBuiltinSkillBodiesLoadFromEmbeddedFiles(t *testing.T) {
	for _, tc := range []struct {
		name   string
		marker string
	}{
		{"evidence_fact_extraction", "AFFENT ACTIVE SKILL: evidence_fact_extraction"},
		{"web_snapshot_fact_extraction", "AFFENT ACTIVE SKILL: web_snapshot_fact_extraction"},
		{"coding_repair_workflow", "AFFENT ACTIVE SKILL: coding_repair_workflow"},
		{"skill_install_workflow", "AFFENT ACTIVE SKILL: skill_install_workflow"},
	} {
		raw, err := builtinSkillFS.ReadFile("builtin_skills/" + tc.name + "/SKILL.md")
		if err != nil {
			t.Fatalf("embedded skill %s should be readable: %v", tc.name, err)
		}
		if !strings.Contains(string(raw), tc.marker) {
			t.Fatalf("embedded skill %s missing marker %q", tc.name, tc.marker)
		}
	}
	raw, err := builtinSkillFS.ReadFile("builtin_skills/web_snapshot_fact_extraction/SKILL.md")
	if err != nil {
		t.Fatalf("embedded web_snapshot_fact_extraction skill should be readable: %v", err)
	}
	for _, want := range []string{"network_evidence_capture_pending", "browser_snapshot once more if capture is still settling", "preserve the exact visible numeric string and unit", "Do not round, normalize, or backfill missing precision"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("embedded web_snapshot_fact_extraction skill missing %q", want)
		}
	}
}

func TestDefaultSkillRegistryLoadsEmbeddedManifestCatalog(t *testing.T) {
	reg := DefaultSkillRegistry()
	wantNames := []string{
		"evidence_fact_extraction",
		"web_snapshot_fact_extraction",
		"coding_repair_workflow",
		"skill_install_workflow",
	}
	gotNames := reg.Names()
	if len(gotNames) != len(wantNames) {
		t.Fatalf("DefaultSkillRegistry names = %v, want %v", gotNames, wantNames)
	}
	for i := range wantNames {
		if gotNames[i] != wantNames[i] {
			t.Fatalf("DefaultSkillRegistry names = %v, want %v", gotNames, wantNames)
		}
	}

	for _, name := range wantNames {
		s, ok := reg.Lookup(name)
		if !ok {
			t.Fatalf("default registry missing %s", name)
		}
		if s.Description == "" {
			t.Fatalf("%s should expose a catalog description", name)
		}
		if !strings.Contains(s.Source, "/"+name+"/SKILL.md") {
			t.Fatalf("%s source = %q, want embedded SKILL.md path", name, s.Source)
		}
		if !s.AutoActivation.hasRules() {
			t.Fatalf("%s should declare manifest auto-activation rules", name)
		}
	}
}

func TestDefaultRuntimeBoundariesIncludesRuntimeSkillCaps(t *testing.T) {
	got := DefaultRuntimeBoundaries()
	if got.SkillActionBytes != maxSkillActionBytes {
		t.Fatalf("SkillActionBytes = %d, want %d", got.SkillActionBytes, maxSkillActionBytes)
	}
	if got.SkillNameBytes != maxSkillNameBytes {
		t.Fatalf("SkillNameBytes = %d, want %d", got.SkillNameBytes, maxSkillNameBytes)
	}
	if got.SkillDescriptionBytes != maxRuntimeSkillDescriptionBytes {
		t.Fatalf("SkillDescriptionBytes = %d, want %d", got.SkillDescriptionBytes, maxRuntimeSkillDescriptionBytes)
	}
	if got.SkillBodyBytes != maxRuntimeSkillBodyBytes {
		t.Fatalf("SkillBodyBytes = %d, want %d", got.SkillBodyBytes, maxRuntimeSkillBodyBytes)
	}
	if got.SkillSourceBytes != maxRuntimeSkillSourceBytes {
		t.Fatalf("SkillSourceBytes = %d, want %d", got.SkillSourceBytes, maxRuntimeSkillSourceBytes)
	}
	if got.SkillTriggers != maxRuntimeSkillTriggers {
		t.Fatalf("SkillTriggers = %d, want %d", got.SkillTriggers, maxRuntimeSkillTriggers)
	}
	if got.SkillTriggerBytes != maxRuntimeSkillTriggerBytes {
		t.Fatalf("SkillTriggerBytes = %d, want %d", got.SkillTriggerBytes, maxRuntimeSkillTriggerBytes)
	}
	if got.SkillRequiredTools != maxRuntimeSkillRequiredTools {
		t.Fatalf("SkillRequiredTools = %d, want %d", got.SkillRequiredTools, maxRuntimeSkillRequiredTools)
	}
	if got.SkillRequiredToolBytes != maxRuntimeSkillRequiredToolBytes {
		t.Fatalf("SkillRequiredToolBytes = %d, want %d", got.SkillRequiredToolBytes, maxRuntimeSkillRequiredToolBytes)
	}
	if got.RuntimeSkills != maxRuntimeSkills {
		t.Fatalf("RuntimeSkills = %d, want %d", got.RuntimeSkills, maxRuntimeSkills)
	}
	if got.RuntimeSkillDirReadBatch != runtimeSkillDirReadBatch {
		t.Fatalf("RuntimeSkillDirReadBatch = %d, want %d", got.RuntimeSkillDirReadBatch, runtimeSkillDirReadBatch)
	}
	if got.RuntimeSkillManifestBytes != maxRuntimeSkillManifestBytes {
		t.Fatalf("RuntimeSkillManifestBytes = %d, want %d", got.RuntimeSkillManifestBytes, maxRuntimeSkillManifestBytes)
	}
	if got.RuntimeSkillProposalBytes != maxRuntimeSkillProposalBytes {
		t.Fatalf("RuntimeSkillProposalBytes = %d, want %d", got.RuntimeSkillProposalBytes, maxRuntimeSkillProposalBytes)
	}
	if got.RuntimeSkillProposalIDBytes != len(runtimeSkillProposalID(Skill{Name: "demo", Body: "AFFENT ACTIVE SKILL: demo"})) {
		t.Fatalf("RuntimeSkillProposalIDBytes = %d", got.RuntimeSkillProposalIDBytes)
	}
}

func TestBuiltinSkillProvider_SkillInstallWorkflowTriggers(t *testing.T) {
	got := BuiltinSkillProvider("我想安装一个能帮我做 Go 代码审查的 skill，可以从 github 找")
	for _, want := range []string{
		"AFFENT ACTIVE SKILL: skill_install_workflow",
		"Do not install from a source you have not read",
		"look for the relevant `SKILL.md`",
		"raw GitHub files",
		"ask the user to paste the exact skill body",
		"Ask for explicit user confirmation",
		"Do not install in the same response",
		"active in the current session without restarting",
		"persisted for future sessions",
		"skill action=propose_install",
		"skill action=confirm_install",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("skill install workflow missing %q:\n%s", want, got)
		}
	}
}

func TestBuiltinSkillProvider_DoesNotTreatCodebaseSearchAsRepair(t *testing.T) {
	got := BuiltinSkillProvider("请搜索代码库，找出默认 request timeout 的证据文件")
	if strings.Contains(got, "coding_repair_workflow") {
		t.Fatalf("codebase fact lookup should not inject coding repair workflow:\n%s", got)
	}
	got = BuiltinSkillProvider("这次发布失败的原因是什么？先帮我整理现象")
	if strings.Contains(got, "coding_repair_workflow") {
		t.Fatalf("generic failure analysis should not inject coding repair workflow:\n%s", got)
	}
}

func TestInstallRuntimeSkillRejectsSymlinkBodyFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := InstallRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("InstallRuntimeSkill symlink body err = %v, want symlink rejection", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "outside" {
		t.Fatalf("outside file was overwritten through symlink: %q", raw)
	}
	if _, err := os.Lstat(filepath.Join(dir, "skill.json")); !os.IsNotExist(err) {
		t.Fatalf("manifest should not be written after body symlink rejection, err=%v", err)
	}
}

func TestInstallRuntimeSkillRejectsSymlinkSkillDir(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, filepath.Join(root, "demo")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	_, err := InstallRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	})
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("InstallRuntimeSkill symlink dir err = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(filepath.Join(outsideDir, "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("outside dir should not receive skill body, err=%v", err)
	}
}

func TestRuntimeSkillOperationsRejectSymlinkRoot(t *testing.T) {
	parent := t.TempDir()
	outsideRoot := t.TempDir()
	root := filepath.Join(parent, "skills")
	if err := os.Symlink(outsideRoot, root); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	skill := Skill{
		Name:   "demo",
		Source: "https://example.invalid/demo",
		Body:   "AFFENT ACTIVE SKILL: demo\nUse demo.",
	}

	if _, err := InstallRuntimeSkill(root, skill); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("InstallRuntimeSkill symlink root err = %v, want symlink rejection", err)
	}
	if _, err := ProposeRuntimeSkill(root, skill); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ProposeRuntimeSkill symlink root err = %v, want symlink rejection", err)
	}
	if _, err := LoadSkillDir(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("LoadSkillDir symlink root err = %v, want symlink rejection", err)
	}
	if _, err := ConfirmRuntimeSkillProposal(root, "0123456789abcdef"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ConfirmRuntimeSkillProposal symlink root err = %v, want symlink rejection", err)
	}
	if err := DeleteRuntimeSkill(root, "demo"); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("DeleteRuntimeSkill symlink root err = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(filepath.Join(outsideRoot, "demo")); !os.IsNotExist(err) {
		t.Fatalf("outside root should not receive installed skill dir, err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(outsideRoot, ".pending")); !os.IsNotExist(err) {
		t.Fatalf("outside root should not receive pending proposal dir, err=%v", err)
	}
}

func TestLoadSkillDirRejectsSymlinkBodyFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "skill.json"), []byte(`{"name":"demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("AFFENT ACTIVE SKILL: outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "SKILL.md")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := LoadSkillDir(root); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("LoadSkillDir symlink body err = %v, want symlink rejection", err)
	}
}

func TestLoadSkillDirSkipsIncompleteVisibleSkillDirs(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "body_only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "body_only", "SKILL.md"), []byte("AFFENT ACTIVE SKILL: body_only"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "manifest_only"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest_only", "skill.json"), []byte(`{"name":"manifest_only"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkillDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "demo" {
		t.Fatalf("loaded skills = %+v, want only demo", skills)
	}
}

func TestLoadSkillDirReadsPastOneDirectoryBatch(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < runtimeSkillDirReadBatch+2; i++ {
		name := fmt.Sprintf("demo_%03d", i)
		if _, err := InstallRuntimeSkill(root, Skill{
			Name: name,
			Body: "AFFENT ACTIVE SKILL: " + name + "\nUse demo.",
		}); err != nil {
			t.Fatal(err)
		}
	}

	skills, err := LoadSkillDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != runtimeSkillDirReadBatch+2 {
		t.Fatalf("loaded skills = %d, want %d", len(skills), runtimeSkillDirReadBatch+2)
	}
}

func TestInstallRuntimeSkillPreservesSourceAcrossReload(t *testing.T) {
	root := t.TempDir()
	source := "https://github.com/example/skills/demo"
	installed, err := InstallRuntimeSkill(root, Skill{
		Name:        "demo",
		Description: "Demo workflow.",
		Source:      source,
		Body:        "AFFENT ACTIVE SKILL: demo\nUse demo.",
		AutoActivation: SkillAutoActivation{
			Any: []string{"demo"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if installed.Source != source {
		t.Fatalf("installed Source = %q, want %q", installed.Source, source)
	}

	skills, err := LoadSkillDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("loaded skills = %d, want 1", len(skills))
	}
	if skills[0].Source != source {
		t.Fatalf("reloaded Source = %q, want %q", skills[0].Source, source)
	}
}

func TestInstallRuntimeSkillFallsBackToLocalSource(t *testing.T) {
	root := t.TempDir()
	installed, err := InstallRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(installed.Source, "file://") || !strings.Contains(installed.Source, "/demo/SKILL.md") {
		t.Fatalf("installed Source = %q, want local SKILL.md source", installed.Source)
	}
}

func TestInstallRuntimeSkillRejectsControlBytesInSource(t *testing.T) {
	_, err := InstallRuntimeSkill(t.TempDir(), Skill{
		Name:   "demo",
		Source: "https://example.invalid/demo\nnext: install silently",
		Body:   "AFFENT ACTIVE SKILL: demo\nUse demo.",
	})
	if err == nil || !strings.Contains(err.Error(), "skill source must not contain control characters") {
		t.Fatalf("InstallRuntimeSkill control source err = %v", err)
	}
}

func TestInstallRuntimeSkillReplacesExistingSkill(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallRuntimeSkill(root, Skill{
		Name:   "demo",
		Source: "https://example.invalid/old",
		Body:   "AFFENT ACTIVE SKILL: demo\nUse old demo.",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := InstallRuntimeSkill(root, Skill{
		Name:   "demo",
		Source: "https://example.invalid/new",
		Body:   "AFFENT ACTIVE SKILL: demo\nUse new demo.",
	}); err != nil {
		t.Fatal(err)
	}

	skills, err := LoadSkillDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 {
		t.Fatalf("loaded skills = %d, want 1", len(skills))
	}
	if skills[0].Source != "https://example.invalid/new" || !strings.Contains(skills[0].Body, "new demo") {
		t.Fatalf("loaded skill after replace = %+v", skills[0])
	}
	if _, err := os.Lstat(filepath.Join(root, ".install-demo.old")); !os.IsNotExist(err) {
		t.Fatalf("backup directory should be removed after successful replace, err=%v", err)
	}
}

func TestDeleteRuntimeSkillRemovesInstalledSkill(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	}); err != nil {
		t.Fatal(err)
	}
	if err := DeleteRuntimeSkill(root, "demo"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Lstat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatalf("skill directory should be removed, err=%v", err)
	}
	skills, err := LoadSkillDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Fatalf("loaded skills after delete = %+v", skills)
	}
}

func TestDeleteRuntimeSkillRejectsSymlinkSkillDir(t *testing.T) {
	root := t.TempDir()
	outsideDir := t.TempDir()
	outsideFile := filepath.Join(outsideDir, "outside.txt")
	if err := os.WriteFile(outsideFile, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outsideDir, filepath.Join(root, "demo")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	err := DeleteRuntimeSkill(root, "demo")
	if err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("DeleteRuntimeSkill symlink dir err = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(outsideFile); err != nil {
		t.Fatalf("outside dir should remain untouched, err=%v", err)
	}
}

func TestLoadSkillDirIgnoresInterruptedRuntimeSkillInstalls(t *testing.T) {
	root := t.TempDir()
	if _, err := InstallRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	}); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{".install-demo.tmp", ".install-demo.old"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "skill.json"), []byte(`{"name":"broken"}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	skills, err := LoadSkillDir(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 1 || skills[0].Name != "demo" {
		t.Fatalf("loaded skills = %+v, want only demo", skills)
	}
}

func TestLoadSkillDirRejectsTooManySkills(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < maxRuntimeSkills+1; i++ {
		name := fmt.Sprintf("demo_%03d", i)
		if _, err := InstallRuntimeSkill(root, Skill{
			Name: name,
			Body: "AFFENT ACTIVE SKILL: " + name + "\nUse demo.",
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := LoadSkillDir(root); err == nil || !strings.Contains(err.Error(), "more than") {
		t.Fatalf("LoadSkillDir too many err = %v, want max skills rejection", err)
	}
}

func TestProposeRuntimeSkillRejectsSymlinkPendingFile(t *testing.T) {
	root := t.TempDir()
	skill := Skill{
		Name:   "demo",
		Source: "https://example.invalid/demo",
		Body:   "AFFENT ACTIVE SKILL: demo\nUse demo.",
	}
	normalized, err := normalizeRuntimeSkill(skill)
	if err != nil {
		t.Fatal(err)
	}
	id := runtimeSkillProposalID(normalized)
	pending := filepath.Join(root, ".pending")
	if err := os.MkdirAll(pending, 0o755); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.json")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(pending, id+".json")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}

	if _, err := ProposeRuntimeSkill(root, skill); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ProposeRuntimeSkill symlink pending err = %v, want symlink rejection", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "outside" {
		t.Fatalf("outside proposal was overwritten through symlink: %q", raw)
	}
}

func TestConfirmRuntimeSkillRejectsSymlinkPendingDir(t *testing.T) {
	root := t.TempDir()
	outsidePending := t.TempDir()
	pending := filepath.Join(root, ".pending")
	if err := os.Symlink(outsidePending, pending); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	id := "0123456789abcdef"
	if err := os.WriteFile(filepath.Join(outsidePending, id+".json"), []byte(`{"id":"0123456789abcdef","name":"demo","body":"AFFENT ACTIVE SKILL: demo"}`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := ConfirmRuntimeSkillProposal(root, id); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("ConfirmRuntimeSkillProposal symlink pending dir err = %v, want symlink rejection", err)
	}
	if _, err := os.Lstat(filepath.Join(root, "demo")); !os.IsNotExist(err) {
		t.Fatalf("skill should not install from symlink pending dir, err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(outsidePending, id+".json")); err != nil {
		t.Fatalf("outside pending proposal should remain, err=%v", err)
	}
}

func TestConfirmRuntimeSkillProposalNormalizesIDCase(t *testing.T) {
	root := t.TempDir()
	proposal, err := ProposeRuntimeSkill(root, Skill{
		Name: "demo",
		Body: "AFFENT ACTIVE SKILL: demo\nUse demo.",
	})
	if err != nil {
		t.Fatal(err)
	}

	installed, err := ConfirmRuntimeSkillProposal(root, strings.ToUpper(proposal.ID))
	if err != nil {
		t.Fatalf("ConfirmRuntimeSkillProposal uppercase id: %v", err)
	}
	if installed.Name != "demo" {
		t.Fatalf("installed skill = %+v, want demo", installed)
	}
	if _, err := os.Lstat(filepath.Join(root, ".pending", proposal.ID+".json")); !os.IsNotExist(err) {
		t.Fatalf("confirmed proposal should be removed, err=%v", err)
	}
}

func TestBuiltinSkillProvider_CanReturnMultipleSkills(t *testing.T) {
	got := BuiltinSkillProvider("修复这个网页抽取代码，并访问 https://example.com 验证")
	for _, want := range []string{
		"web_snapshot_fact_extraction",
		"coding_repair_workflow",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("combined skill output missing %q:\n%s", want, got)
		}
	}
}

// TestBuiltinSkillProvider_NoDomainSpecificTriggers pins that the
// trigger list stays generic. An earlier draft included the literal
// "taostats" — a specific site name from a single eval incident —
// alongside the shape-based "url / browser / page" indicators. That
// kind of leak biases the router on unrelated traffic and adds zero
// value (the URL alone already fires when "taostats" is mentioned in
// context). Test plants a sentence whose ONLY web-ish signal is a
// site name and asserts the skill stays silent. A regression that
// re-adds a domain-specific trigger fires this test.
func TestBuiltinSkillProvider_NoDomainSpecificTriggers(t *testing.T) {
	if got := BuiltinSkillProvider("how does taostats compare to coingecko"); got != "" {
		t.Fatalf("mentioning a site name without a URL / browser / page indicator should NOT trigger the web skill:\n%s", got)
	}
	if got := BuiltinSkillProvider("compare github and gitlab pricing"); got != "" {
		t.Fatalf("'github' is a site name, not a web-task signal; got skill:\n%s", got)
	}
}

func TestSkillProviderForToolsFiltersUnavailableRequiredTools(t *testing.T) {
	skills := DefaultSkillRegistry()
	withoutBrowser := NewRegistry()
	if got := SkillProviderForTools(skills, withoutBrowser)("访问 https://example.com 并读取页面标题"); got != "" {
		t.Fatalf("browser skill must not activate without browser tools:\n%s", got)
	}

	withBrowser := NewRegistry()
	withBrowser.Add(&Tool{Name: "browser_navigate"})
	if got := SkillProviderForTools(skills, withBrowser)("访问 https://example.com 并读取页面标题"); got != "" {
		t.Fatalf("browser skill must not activate with a partial browser surface:\n%s", got)
	}
	withBrowser.Add(&Tool{Name: "browser_wait"})
	withBrowser.Add(&Tool{Name: "browser_snapshot"})
	if got := SkillProviderForTools(skills, withBrowser)("访问 https://example.com 并读取页面标题"); got != "" {
		t.Fatalf("browser skill must not activate without browser_find:\n%s", got)
	}
	withBrowser.Add(&Tool{Name: "browser_find"})
	if got := SkillProviderForTools(skills, withBrowser)("访问 https://example.com 并读取页面标题"); got != "" {
		t.Fatalf("browser skill must not activate without browser_network tools:\n%s", got)
	}
	withBrowser.Add(&Tool{Name: "browser_network"})
	withBrowser.Add(&Tool{Name: "browser_network_read"})
	got := SkillProviderForTools(skills, withBrowser)("访问 https://example.com 并读取页面标题")
	if !strings.Contains(got, "web_snapshot_fact_extraction") {
		t.Fatalf("browser skill should activate when its required tool exists:\n%s", got)
	}

	got = SkillProviderForTools(skills, withoutBrowser)("这个 Go 项目的测试失败，请修复代码并运行 go test")
	if !strings.Contains(got, "coding_repair_workflow") {
		t.Fatalf("non-browser skill should still activate without browser tools:\n%s", got)
	}
}

// TestSkillRegistry_CustomSkillExtensionPoint pins the data-driven
// router contract: adding a brand-new skill should be a Skill struct
// literal + one Register call, with no router code changes. This
// test stands up an empty registry, plants a one-off skill, and
// verifies activation flows through Provide → SkillProvider →
// Loop.appendUserMessage exactly like the builtins do.
//
// If a future refactor accidentally hardcodes the dispatch back to
// the two builtins (web_snapshot, coding_repair), this test fires.
func TestSkillRegistry_CustomSkillExtensionPoint(t *testing.T) {
	reg := &SkillRegistry{}
	reg.Register(Skill{
		Name:        "test_skill",
		Description: "test description",
		Source:      "test://skill",
		Body:        "AFFENT ACTIVE SKILL: test_skill\nplant marker",
		Triggers:    []string{"sentinel-trigger-xyz"},
	})

	t.Run("fires on trigger", func(t *testing.T) {
		got := reg.Provide("this contains sentinel-trigger-xyz somewhere")
		if !strings.Contains(got, "plant marker") {
			t.Errorf("custom skill should fire on its trigger; got %q", got)
		}
	})
	t.Run("silent without trigger", func(t *testing.T) {
		if got := reg.Provide("unrelated text"); got != "" {
			t.Errorf("custom skill must NOT fire without trigger; got %q", got)
		}
	})
	t.Run("Names lists registration order", func(t *testing.T) {
		names := reg.Names()
		if len(names) != 1 || names[0] != "test_skill" {
			t.Errorf("Names() = %v, want [test_skill]", names)
		}
	})
	t.Run("Catalog excludes body", func(t *testing.T) {
		catalog := reg.Catalog()
		if len(catalog) != 1 || catalog[0].Name != "test_skill" || catalog[0].Description != "test description" || catalog[0].Source != "test://skill" {
			t.Fatalf("Catalog() = %+v", catalog)
		}
		if !catalog[0].AutoActivates {
			t.Fatalf("Catalog AutoActivates = false, want true")
		}
		if len(catalog[0].Triggers) != 1 || catalog[0].Triggers[0] != "sentinel-trigger-xyz" {
			t.Fatalf("Catalog triggers = %+v, want sentinel trigger", catalog[0].Triggers)
		}
	})
	t.Run("Lookup returns skill body", func(t *testing.T) {
		s, ok := reg.Lookup("test_skill")
		if !ok || !strings.Contains(s.Body, "plant marker") {
			t.Fatalf("Lookup(test_skill) = %+v, %v", s, ok)
		}
		if _, ok := reg.Lookup("missing"); ok {
			t.Fatal("Lookup(missing) should fail")
		}
	})
}

func TestSkillRegistry_CatalogAutoActivatesOnlyWithActivationRules(t *testing.T) {
	reg := &SkillRegistry{}
	reg.Register(Skill{
		Name: "manual_skill",
		Body: "AFFENT ACTIVE SKILL: manual_skill\nmanual body",
	})
	reg.Register(Skill{
		Name:           "auto_skill",
		Body:           "AFFENT ACTIVE SKILL: auto_skill\nauto body",
		AutoActivation: SkillAutoActivation{Any: []string{"auto trigger"}},
	})

	catalog := reg.Catalog()
	if len(catalog) != 2 {
		t.Fatalf("Catalog() len = %d, want 2: %+v", len(catalog), catalog)
	}
	if catalog[0].Name != "manual_skill" || catalog[0].AutoActivates {
		t.Fatalf("manual catalog entry = %+v, want non-auto-activating", catalog[0])
	}
	if catalog[1].Name != "auto_skill" || !catalog[1].AutoActivates {
		t.Fatalf("auto catalog entry = %+v, want auto-activating", catalog[1])
	}
	manual, ok := reg.Lookup("manual_skill")
	if !ok || manual.HasActivationRules() {
		t.Fatalf("manual HasActivationRules = %v, found=%v", manual.HasActivationRules(), ok)
	}
	auto, ok := reg.Lookup("auto_skill")
	if !ok || !auto.HasActivationRules() {
		t.Fatalf("auto HasActivationRules = %v, found=%v", auto.HasActivationRules(), ok)
	}
}

// TestSkillRegistry_CustomMatchPredicate pins the Match override
// path. Trigger lists handle "any-of substring" cases; some skills
// need a real predicate (regex, length floor, multi-signal AND).
// When Match is non-nil, Triggers must be ignored — the predicate
// owns the decision.
func TestSkillRegistry_CustomMatchPredicate(t *testing.T) {
	reg := &SkillRegistry{}
	reg.Register(Skill{
		Name: "long_text_skill",
		Body: "AFFENT ACTIVE SKILL: long_text_skill\nplant",
		// Triggers populated but Match must override and be the
		// sole source of truth.
		Triggers: []string{"this-substring-should-be-ignored"},
		Match: func(lower string) bool {
			return len(lower) > 50
		},
	})

	if got := reg.Provide("this-substring-should-be-ignored is here"); got != "" {
		t.Errorf("Match override should make Triggers inert; got %q", got)
	}
	long := strings.Repeat("a", 60)
	if got := reg.Provide(long); !strings.Contains(got, "plant") {
		t.Errorf("Match predicate should activate the skill; got %q", got)
	}
}

// TestSkillRegistry_RegisterDropsInvalidSkills pins the silent-drop
// safety: a skill missing Name or with whitespace-only Body is
// operator error, and the previous behavior of "ignore and keep
// going" is preferred to failing the whole deploy on a typo in one
// of N registered skills.
func TestSkillRegistry_RegisterDropsInvalidSkills(t *testing.T) {
	reg := &SkillRegistry{}
	reg.Register(Skill{Name: "", Body: "x", Triggers: []string{"a"}})
	reg.Register(Skill{Name: "n", Body: "   \t\n", Triggers: []string{"a"}})
	reg.Register(Skill{Name: "valid", Body: "v", Triggers: []string{"a"}})

	names := reg.Names()
	if len(names) != 1 || names[0] != "valid" {
		t.Errorf("only the valid skill should register; got %v", names)
	}
}

// TestSkillRegistry_NilSafe pins that a nil *SkillRegistry safely
// returns the empty string. Lets a Loop with no skills wired up
// stay quiet without an extra nil check at the call site.
func TestSkillRegistry_NilSafe(t *testing.T) {
	var reg *SkillRegistry
	if got := reg.Provide("anything"); got != "" {
		t.Errorf("nil registry must return empty; got %q", got)
	}
	if got := reg.Names(); got != nil {
		t.Errorf("nil registry Names() must be nil; got %v", got)
	}
}

func TestUserConfirmedRuntimeSkillProposalRequiresLatestUserApproval(t *testing.T) {
	proposalID := "0123456789abcdef"
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if UserConfirmedRuntimeSkillProposal(conv, proposalID) {
		t.Fatal("empty conversation should not confirm proposal")
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "please install a skill"}); err != nil {
		t.Fatal(err)
	}
	if UserConfirmedRuntimeSkillProposal(conv, proposalID) {
		t.Fatal("user text without proposal id should not confirm proposal")
	}
	if err := conv.Append(ChatMessage{Role: "assistant", Content: "proposal_id=0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if UserConfirmedRuntimeSkillProposal(conv, proposalID) {
		t.Fatal("assistant text must not count as user confirmation")
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "不要安装 proposal_id=0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if UserConfirmedRuntimeSkillProposal(conv, proposalID) {
		t.Fatal("negative confirmation should not authorize install")
	}
	if err := conv.Append(ChatMessage{Role: "user", Content: "确认安装 proposal_id=0123456789abcdef"}); err != nil {
		t.Fatal(err)
	}
	if !UserConfirmedRuntimeSkillProposal(conv, proposalID) {
		t.Fatal("latest explicit user confirmation with proposal id should authorize install")
	}
}

func TestUserConfirmedRuntimeSkillProposalHandlesNoProblem(t *testing.T) {
	proposalID := "0123456789abcdef"
	if !userTextConfirmsRuntimeSkillProposal("no problem, install proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("no problem with install should count as confirmation")
	}
	if !userTextConfirmsRuntimeSkillProposal("sure, proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("sure with proposal id should count as confirmation")
	}
	if !userTextConfirmsRuntimeSkillProposal("sounds good, proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("sounds good with proposal id should count as confirmation")
	}
	if userTextConfirmsRuntimeSkillProposal("no problem, do not install proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("explicit rejection must override no problem")
	}
	if userTextConfirmsRuntimeSkillProposal("not sure, proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("not sure should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("not ok, proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("not ok should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("not approved, proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("not approved should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("no, proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("plain no should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("不可以，proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("Chinese rejection should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("不需要安装 proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("Chinese no-need rejection should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("不用安装 proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("Chinese don't-need rejection should not confirm proposal")
	}
	if userTextConfirmsRuntimeSkillProposal("无需安装 proposal_id=0123456789abcdef", proposalID) {
		t.Fatal("Chinese no-need variant should not confirm proposal")
	}
}

func TestLoopAppendUserMessageInjectsActiveSkillBeforeUser(t *testing.T) {
	conv, err := OpenConversationAt(filepath.Join(t.TempDir(), "sess.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	loop := &Loop{
		Conv:          conv,
		SkillProvider: BuiltinSkillProvider,
	}
	if err := loop.appendUserMessage("turn_skill", "访问 https://example.com 并读取页面标题", TurnOptions{}); err != nil {
		t.Fatal(err)
	}
	msgs := conv.Snapshot()
	if len(msgs) != 2 {
		t.Fatalf("expected skill system message + user message, got %d: %+v", len(msgs), msgs)
	}
	if msgs[0].Role != "system" || !strings.Contains(msgs[0].Content, "web_snapshot_fact_extraction") {
		t.Fatalf("first message should be active skill system block: %+v", msgs[0])
	}
	if msgs[1].Role != "user" || !strings.Contains(msgs[1].Content, "example.com") {
		t.Fatalf("second message should be the user request: %+v", msgs[1])
	}
}
