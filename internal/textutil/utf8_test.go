package textutil

import (
	"testing"
	"unicode/utf8"
)

// TestAlignBackward pins the contract for the function that gates
// every byte-bounded UTF-8 truncation in the codebase (memory
// snippet trim, session-search snippet, conversation log scan,
// project-context body cap, web_fetch result cap). A regression
// here splits a multi-byte rune and every downstream caller
// emits invalid UTF-8 into the model's context — silently.
func TestAlignBackward(t *testing.T) {
	const cjk = "你好世界" // each rune = 3 bytes (E4 BD A0 / E5 A5 BD / E4 B8 96 / E7 95 8C)
	cases := []struct {
		name string
		s    string
		pos  int
		want int
	}{
		// Boundary inputs — defensive against off-by-one.
		{"empty string, pos 0", "", 0, 0},
		{"empty string, pos past end", "", 5, 0},
		{"pos negative clamps to 0", "abc", -3, 0},
		{"pos at len", "abc", 3, 3},
		{"pos past len clamps to len", "abc", 99, 3},

		// ASCII — every byte is a leader; never moves.
		{"ASCII pos 0", "abc", 0, 0},
		{"ASCII pos 1", "abc", 1, 1},
		{"ASCII pos 2", "abc", 2, 2},

		// CJK — 4 runes × 3 bytes = 12 bytes total.
		// Leaders at 0, 3, 6, 9; continuation bytes at 1, 2, 4, 5, 7, 8, 10, 11.
		{"CJK leader pos 3 stays", cjk, 3, 3},
		{"CJK continuation pos 4 → leader 3", cjk, 4, 3},
		{"CJK continuation pos 5 → leader 3", cjk, 5, 3},
		{"CJK leader pos 6 stays", cjk, 6, 6},
		{"CJK continuation pos 7 → leader 6", cjk, 7, 6},
		{"CJK pos 11 (last continuation) → leader 9", cjk, 11, 9},
		{"CJK pos 12 (len)", cjk, 12, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AlignBackward(c.s, c.pos); got != c.want {
				t.Errorf("AlignBackward(%q, %d) = %d, want %d", c.s, c.pos, got, c.want)
			}
		})
	}
}

// TestAlignForward — the symmetric direction. Used less, but the
// centerSnippet helper in memory and a few other places rely on
// the same shape contract.
func TestAlignForward(t *testing.T) {
	const cjk = "你好世界"
	cases := []struct {
		name string
		s    string
		pos  int
		want int
	}{
		{"empty string", "", 0, 0},
		{"empty string past end", "", 5, 0},
		{"pos negative → 0", "abc", -1, 0},
		{"pos at len stays", "abc", 3, 3},
		{"pos past len → len", "abc", 99, 3},
		{"ASCII pos 1 stays", "abc", 1, 1},

		// Forward: from a continuation, advance to the NEXT leader.
		{"CJK leader pos 0 stays", cjk, 0, 0},
		{"CJK continuation pos 1 → leader 3", cjk, 1, 3},
		{"CJK continuation pos 2 → leader 3", cjk, 2, 3},
		{"CJK leader pos 3 stays", cjk, 3, 3},
		{"CJK continuation pos 4 → leader 6", cjk, 4, 6},
		{"CJK continuation pos 11 → len 12", cjk, 11, 12},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := AlignForward(c.s, c.pos); got != c.want {
				t.Errorf("AlignForward(%q, %d) = %d, want %d", c.s, c.pos, got, c.want)
			}
		})
	}
}

// TestAlign_TruncationRoundtrip pins the property that matters most
// to callers: s[:AlignBackward(s, n)] is always valid UTF-8, even
// when n falls inside a multi-byte rune. Every byte-cap call site
// in the codebase relies on this implicitly; a regression here
// produces an invalid-UTF-8 string the LLM provider may then reject
// or render as replacement chars. Mixed-width input ensures we
// exercise 1-byte ASCII, 2-byte Latin-1, 3-byte CJK, and 4-byte
// emoji boundaries in one sweep.
func TestAlign_TruncationRoundtrip(t *testing.T) {
	const s = "café 你好 🚀 mix"
	for n := 0; n <= len(s); n++ {
		head := s[:AlignBackward(s, n)]
		if !utf8.ValidString(head) {
			t.Errorf("AlignBackward(s, %d) → %q is not valid UTF-8", n, head)
		}
	}
}
