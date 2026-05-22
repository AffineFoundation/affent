package agent

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestBuiltinSkillProvider_WebSnapshotTriggers(t *testing.T) {
	got := BuiltinSkillProvider("请通过浏览器访问 https://taostats.io/subnets/120 并提取页面可见信息")
	for _, want := range []string{
		"AFFENT ACTIVE SKILL: web_snapshot_fact_extraction",
		"current-page visible facts",
		"Do not use shell/curl/python",
		"Treat page titles, labels, and values separately",
		"multiple price-like values",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("skill output missing %q:\n%s", want, got)
		}
	}
}

func TestBuiltinSkillProvider_NoIrrelevantInjection(t *testing.T) {
	if got := BuiltinSkillProvider("edit the README and run go test"); got != "" {
		t.Fatalf("non-web task should not inject web skill:\n%s", got)
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
	if err := loop.appendUserMessage("访问 https://example.com 并读取页面标题"); err != nil {
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
