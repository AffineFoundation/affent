package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/affinefoundation/affent/internal/loopstate"
)

func TestWithLoopProtocolSkillProviderInjectsWhenFileExists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 1. North Star\n\nKeep evidence cited."), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := WithLoopProtocolSkillProvider(path, func(userText string) string {
		if userText != "continue" {
			t.Fatalf("userText = %q, want continue", userText)
		}
		return "AFFENT ACTIVE SKILL: demo"
	})

	got := provider("continue")
	for _, want := range []string{
		"AFFENT LOOP PROTOCOL:",
		"feed_mode=full feed_number=1",
		"active long-run loop protocol",
		"Keep evidence cited.",
		"persisted plan state remains authoritative",
		"AFFENT ACTIVE SKILL: demo",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("loop protocol provider missing %q:\n%s", want, got)
		}
	}
}

func TestWithLoopProtocolSkillProviderSkipsMissingInvalidOrBlankFile(t *testing.T) {
	provider := WithLoopProtocolSkillProvider(filepath.Join(t.TempDir(), "missing.md"), func(string) string {
		return "next"
	})
	if got := provider("continue"); got != "next" {
		t.Fatalf("missing protocol provider = %q, want next", got)
	}

	dir := t.TempDir()
	blank := filepath.Join(dir, "blank.md")
	if err := os.WriteFile(blank, []byte(" \n\t"), 0o644); err != nil {
		t.Fatal(err)
	}
	provider = WithLoopProtocolSkillProvider(blank, nil)
	if got := provider("continue"); got != "" {
		t.Fatalf("blank protocol provider = %q, want empty", got)
	}

	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("protocol"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	provider = WithLoopProtocolSkillProvider(link, func(string) string { return "next" })
	if got := provider("continue"); got != "next" {
		t.Fatalf("invalid protocol provider = %q, want next", got)
	}
}

func TestWithLoopProtocolSkillProviderCompactsLargeFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "LOOP.md")
	if err := os.WriteFile(path, []byte(strings.Repeat("x", maxActiveLoopProtocolFullBytes+100)), 0o644); err != nil {
		t.Fatal(err)
	}
	got := WithLoopProtocolSkillProvider(path, nil)("continue")
	if !strings.Contains(got, strings.Repeat("x", maxActiveLoopProtocolFullBytes)+"...") {
		t.Fatalf("large protocol should be compacted, got length %d", len(got))
	}
	if strings.Contains(got, strings.Repeat("x", maxActiveLoopProtocolFullBytes+20)) {
		t.Fatalf("large protocol was not compacted, got length %d", len(got))
	}
}

func TestWithLoopProtocolSkillProviderUsesDigestBetweenFullFeeds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	archive := strings.Repeat("old archive detail ", 800)
	content := `# Loop Protocol

## 0. Metadata

- loop_id: digest-loop
- status: running

## 1. North Star

Keep long-run work anchored to evidence.

## Archive

` + archive
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	provider := WithLoopProtocolSkillProvider(path, nil)
	for i := 0; i < loopProtocolFullFirstFeeds; i++ {
		got := provider("continue")
		if !strings.Contains(got, "feed_mode=full") {
			t.Fatalf("feed %d should be full:\n%s", i+1, got)
		}
	}
	got := provider("continue")
	if !strings.Contains(got, "feed_mode=digest feed_number=4") {
		t.Fatalf("fourth feed should be digest:\n%s", got)
	}
	if !strings.Contains(got, "Keep long-run work anchored to evidence.") {
		t.Fatalf("digest missing north star:\n%s", got)
	}
	if strings.Contains(got, "old archive detail old archive detail") {
		t.Fatalf("digest should omit archive detail:\n%s", got)
	}
	for i := 5; i < loopProtocolFullEveryFeeds; i++ {
		_ = provider("continue")
	}
	got = provider("continue")
	if !strings.Contains(got, "feed_mode=full feed_number=6") {
		t.Fatalf("sixth feed should return to full:\n%s", got)
	}
}

func TestWithLoopProtocolSkillProviderPersistsFeedCadenceAcrossProviders(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := os.WriteFile(path, []byte("# Loop Protocol\n\n## 1. North Star\n\nPersist feed cadence."), 0o644); err != nil {
		t.Fatal(err)
	}
	first := WithLoopProtocolSkillProvider(path, nil)
	for i := 0; i < 4; i++ {
		_ = first("continue")
	}
	state, found, err := loopstate.ReadState(filepath.Join(dir, loopstate.StateFileName))
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if state.ProtocolFeeds != 4 || state.LastProtocolFeedMode != "digest" || state.LastEventType != "loop.protocol_feed" {
		t.Fatalf("state after first provider = %+v", state)
	}
	events, found, err := loopstate.ReadRecentEvents(filepath.Join(dir, loopstate.EventsFileName), 10)
	if err != nil || !found || len(events) != 4 {
		t.Fatalf("events found=%v len=%d err=%v", found, len(events), err)
	}
	if events[3].Type != "loop.protocol_feed" || events[3].Mode != "digest" || events[3].FeedNumber != 4 {
		t.Fatalf("fourth event = %+v", events[3])
	}

	resumed := WithLoopProtocolSkillProvider(path, nil)
	got := resumed("continue")
	if !strings.Contains(got, "feed_mode=digest feed_number=5") {
		t.Fatalf("resumed provider should continue persisted cadence:\n%s", got)
	}
	if !strings.Contains(got, "protocol_feeds=5") || !strings.Contains(got, "last_feed=digest") {
		t.Fatalf("state line should include persisted feed stats:\n%s", got)
	}
}
