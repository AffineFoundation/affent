package main

import (
	"reflect"
	"testing"
)

func TestAddSessionIDCandidateKeepsSmallestIDs(t *testing.T) {
	candidates := map[string]struct{}{}

	for _, id := range []string{"delta", "alpha", "charlie", "bravo"} {
		addSessionIDCandidate(candidates, id, 3)
	}

	got := sortedSessionIDCandidates(candidates, 3)
	want := []string{"alpha", "bravo", "charlie"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("sortedSessionIDCandidates() = %v, want %v", got, want)
	}
}

func TestAddSessionIDCandidateNoopsForNonPositiveCap(t *testing.T) {
	candidates := map[string]struct{}{}

	addSessionIDCandidate(candidates, "alpha", 0)
	addSessionIDCandidate(candidates, "bravo", -1)

	if len(candidates) != 0 {
		t.Fatalf("candidates = %v, want empty", candidates)
	}
}
