package loopstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProtocolPathUsesPerSessionLoopDir(t *testing.T) {
	dir := t.TempDir()
	got := ProtocolPath(dir, "market-run")
	want := filepath.Join(dir, ".affent", "loops", "market-run", "LOOP.md")
	if got != want {
		t.Fatalf("ProtocolPath = %q, want %q", got, want)
	}
	if rel := ProtocolRelPath("market-run"); rel != ".affent/loops/market-run/LOOP.md" {
		t.Fatalf("ProtocolRelPath = %q", rel)
	}
}

func TestReadProtocolRejectsSymlinkAndOversize(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "LOOP.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadProtocol(link); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("ReadProtocol symlink err = %v", err)
	}

	oversize := filepath.Join(dir, "oversize.md")
	if err := os.WriteFile(oversize, []byte(strings.Repeat("x", MaxProtocolBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadProtocol(oversize); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("ReadProtocol oversize err = %v", err)
	}
}

func TestWriteProtocolPersistsAtomicallyAndRejectsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".affent", "loops", "alpha", "LOOP.md")
	if err := WriteProtocol(path, "  # Loop\n\nstatus: running  "); err != nil {
		t.Fatalf("WriteProtocol: %v", err)
	}
	got, found, err := ReadProtocol(path)
	if err != nil || !found || got != "# Loop\n\nstatus: running" {
		t.Fatalf("ReadProtocol = %q found=%v err=%v", got, found, err)
	}
	if _, err := os.Lstat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temp file err = %v, want not exists", err)
	}
	if err := WriteProtocol(path, "updated"); err != nil {
		t.Fatalf("overwrite WriteProtocol: %v", err)
	}
	got, found, err = ReadProtocol(path)
	if err != nil || !found || got != "updated" {
		t.Fatalf("updated ReadProtocol = %q found=%v err=%v", got, found, err)
	}

	if err := WriteProtocol(filepath.Join(dir, "blank.md"), " \n\t "); err == nil || !strings.Contains(err.Error(), "content is required") {
		t.Fatalf("blank WriteProtocol err = %v", err)
	}
	if err := WriteProtocol(filepath.Join(dir, "too-big.md"), strings.Repeat("x", MaxProtocolBytes+1)); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversize WriteProtocol err = %v", err)
	}

	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteProtocol(link, "new"); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink WriteProtocol err = %v", err)
	}
	raw, err := os.ReadFile(outside)
	if err != nil || string(raw) != "outside" {
		t.Fatalf("outside content = %q err=%v", string(raw), err)
	}
}

func TestRemoveProtocolRejectsSymlinkAndRemovesRegularFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	if err := WriteProtocol(path, "protocol"); err != nil {
		t.Fatal(err)
	}
	removed, err := RemoveProtocol(path)
	if err != nil || !removed {
		t.Fatalf("RemoveProtocol = removed %v err %v", removed, err)
	}
	if _, err := os.Lstat(path); !os.IsNotExist(err) {
		t.Fatalf("protocol still exists err=%v", err)
	}
	removed, err = RemoveProtocol(path)
	if err != nil || removed {
		t.Fatalf("second RemoveProtocol = removed %v err %v", removed, err)
	}

	outside := filepath.Join(dir, "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.md")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if removed, err := RemoveProtocol(link); err == nil || removed || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("RemoveProtocol symlink = removed %v err %v", removed, err)
	}
	if _, err := os.Lstat(outside); err != nil {
		t.Fatalf("outside should remain: %v", err)
	}
}

func TestSummarizeFileExtractsMetadata(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "LOOP.md")
	content := `# Loop Protocol: market

## 0. Metadata

- loop_id: market-run
- owner_session: sess-market
- status: running

## 1. North Star

Keep market evidence cited.`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	got, found, err := SummarizeFile(path, ".affent/loops/market-run/LOOP.md")
	if err != nil {
		t.Fatalf("SummarizeFile: %v", err)
	}
	if !found {
		t.Fatal("expected summary")
	}
	if got.Path != ".affent/loops/market-run/LOOP.md" ||
		got.LoopID != "market-run" ||
		got.OwnerSession != "sess-market" ||
		got.Status != "running" ||
		got.UpdatedAt == "" ||
		got.Bytes != len([]byte(content)) ||
		!strings.Contains(got.Preview, "Keep market evidence cited.") {
		t.Fatalf("summary = %+v", got)
	}
}

func TestStatePersistsAtomicallyAndSummaryPrefersState(t *testing.T) {
	dir := t.TempDir()
	loopDir := ProtocolDir(dir, "market-run")
	protocolPath := filepath.Join(loopDir, ProtocolFileName)
	if err := WriteProtocol(protocolPath, `# Loop

- loop_id: from-markdown
- owner_session: from-markdown
- status: draft`); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(loopDir, StateFileName)
	state := State{
		Version:       1,
		LoopID:        "market-run",
		OwnerSession:  "sess-market",
		Status:        "running",
		UpdatedAt:     "2026-05-27T00:00:00Z",
		EventCount:    2,
		LastEventType: "loop.protocol_update",
	}
	if err := WriteState(statePath, state); err != nil {
		t.Fatalf("WriteState: %v", err)
	}
	gotState, found, err := ReadState(statePath)
	if err != nil || !found {
		t.Fatalf("ReadState found=%v err=%v", found, err)
	}
	if gotState.LoopID != "market-run" || gotState.Status != "running" || gotState.EventCount != 2 {
		t.Fatalf("state = %+v", gotState)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("state is not valid JSON: %s", string(raw))
	}
	summary, found, err := SummarizeFile(protocolPath, ProtocolRelPath("market-run"))
	if err != nil || !found {
		t.Fatalf("SummarizeFile found=%v err=%v", found, err)
	}
	if summary.LoopID != "market-run" || summary.OwnerSession != "sess-market" || summary.Status != "running" || summary.State == nil {
		t.Fatalf("summary did not prefer state: %+v", summary)
	}

	outside := filepath.Join(dir, "outside.json")
	if err := os.WriteFile(outside, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "state-link.json")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if err := WriteState(link, State{LoopID: "bad"}); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink WriteState err = %v", err)
	}
}

func TestAppendAndReadRecentEventsRejectsUnsafeTargets(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".affent", "loops", "alpha", EventsFileName)
	for i := 0; i < 3; i++ {
		ev, err := AppendEvent(path, Event{
			Type:    "loop.protocol_update",
			Summary: "update",
			Reason:  "test",
		})
		if err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
		if ev.Seq != i+1 || ev.Time == "" {
			t.Fatalf("event %d = %+v", i, ev)
		}
	}
	count, err := CountEvents(path)
	if err != nil || count != 3 {
		t.Fatalf("CountEvents = %d err=%v", count, err)
	}
	events, found, err := ReadRecentEvents(path, 2)
	if err != nil || !found {
		t.Fatalf("ReadRecentEvents found=%v err=%v", found, err)
	}
	if len(events) != 2 || events[0].Seq != 2 || events[1].Seq != 3 {
		t.Fatalf("recent events = %+v", events)
	}

	outside := filepath.Join(dir, "outside.jsonl")
	if err := os.WriteFile(outside, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "events-link.jsonl")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	if _, err := AppendEvent(link, Event{Type: "loop.protocol_update"}); err == nil || !strings.Contains(err.Error(), "must not be a symlink") {
		t.Fatalf("symlink AppendEvent err = %v", err)
	}
}
