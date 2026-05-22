package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/affinefoundation/affent/internal/sse"
)

func TestHandleStats_EmptyPool(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	h := handleStats(pool.cfg, pool)

	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if got := w.Result().StatusCode; got != 200 {
		t.Fatalf("status = %d, want 200", got)
	}
	if ct := w.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %q", ct)
	}
	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if resp.ActiveSessions != 0 {
		t.Fatalf("ActiveSessions = %d, want 0", resp.ActiveSessions)
	}
	if len(resp.Sessions) != 0 {
		t.Fatalf("Sessions = %d entries, want 0", len(resp.Sessions))
	}
	if resp.MaxSessions != pool.cfg.MaxSessions {
		t.Fatalf("MaxSessions = %d, want %d", resp.MaxSessions, pool.cfg.MaxSessions)
	}
	if resp.ServerTime == "" {
		t.Fatal("ServerTime must be populated")
	}
}

func TestHandleStats_ListsSessionsSorted(t *testing.T) {
	pool := newTestPool(t, 8, "5m")
	for _, id := range []string{"charlie", "alpha", "bravo"} {
		if _, err := pool.GetOrCreate(id); err != nil {
			t.Fatalf("GetOrCreate %s: %v", id, err)
		}
	}
	h := handleStats(pool.cfg, pool)
	r := httptest.NewRequest("GET", "/v1/stats", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	var resp statsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ActiveSessions != 3 {
		t.Fatalf("ActiveSessions = %d, want 3", resp.ActiveSessions)
	}
	got := make([]string, len(resp.Sessions))
	for i, s := range resp.Sessions {
		got[i] = s.ID
	}
	want := []string{"alpha", "bravo", "charlie"}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("session order: got %v, want %v", got, want)
		}
	}
	for _, s := range resp.Sessions {
		if s.CreatedAt == "" || s.LastUsedAt == "" {
			t.Fatalf("session %s has empty timestamps: %+v", s.ID, s)
		}
	}
}

func TestSession_CancelTurn_IsIdempotentWithoutActiveTurn(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("idle")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	// Loop has no active turn; CancelTurn must not panic and must
	// return promptly. Calling it twice in a row must be safe.
	s.CancelTurn()
	s.CancelTurn()
}

// TestSession_UsageSnapshot_AccumulatesFromEvents pins the per-session
// token counter contract. fanout observes every event flowing through
// and bumps the counters on sse.TypeUsage / sse.TypeTurnEnd. Operators
// polling /v1/stats use this to track spend per session without
// subscribing to the event stream.
//
// The test bypasses the real Loop and feeds events directly to the
// session's events channel, which fanout drains in a background
// goroutine. We poll UsageSnapshot until the counters reflect the
// planted events, with a generous deadline to absorb scheduler jitter.
func TestSession_UsageSnapshot_AccumulatesFromEvents(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("usage-test")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}

	// Plant two usage events and two turn-end events. Counters should
	// sum the usage payloads and increment Turns by 2.
	for _, p := range []sse.UsagePayload{
		{TurnID: "t1", InputTokens: 100, OutputTokens: 20},
		{TurnID: "t2", InputTokens: 200, OutputTokens: 40},
	} {
		ev, err := sse.NewEvent(sse.TypeUsage, p)
		if err != nil {
			t.Fatal(err)
		}
		s.events <- ev
	}
	for _, p := range []sse.TurnEndPayload{
		{TurnID: "t1", Reason: sse.TurnEndCompleted},
		{TurnID: "t2", Reason: sse.TurnEndCompleted},
	} {
		ev, err := sse.NewEvent(sse.TypeTurnEnd, p)
		if err != nil {
			t.Fatal(err)
		}
		s.events <- ev
	}

	// fanout is async; poll briefly until counters reach the expected
	// totals.
	deadline := time.Now().Add(2 * time.Second)
	for {
		u := s.UsageSnapshot()
		if u.InputTokens == 300 && u.OutputTokens == 60 && u.Turns == 2 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("UsageSnapshot never reached expected totals: got %+v, want input=300 output=60 turns=2", u)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSession_BrowserStatsSnapshot_ZeroWhenNoBrowser(t *testing.T) {
	pool := newTestPool(t, 4, "5m")
	s, err := pool.GetOrCreate("no-browser")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	stats := s.BrowserStatsSnapshot()
	if stats != (BrowserStatsSnapshot{}) {
		t.Fatalf("session without browser must yield zero stats, got %+v", stats)
	}
}
