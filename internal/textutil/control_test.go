package textutil

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestStripASCIIControls(t *testing.T) {
	got := StripASCIIControls("hello\n\ttabbed\r\nworld\x00\x1b[31m")
	if !utf8.ValidString(got) {
		t.Fatalf("StripASCIIControls produced invalid UTF-8: %q", got)
	}
	if strings.ContainsAny(got, "\x00\x1b") {
		t.Fatalf("StripASCIIControls left control bytes behind: %q", got)
	}
	if !strings.Contains(got, "\n\ttabbed\r\n") {
		t.Fatalf("StripASCIIControls stripped allowed whitespace: %q", got)
	}
}

func TestStripASCIIControls_PassThroughCleanInput(t *testing.T) {
	got := StripASCIIControls("plain text")
	if got != "plain text" {
		t.Fatalf("StripASCIIControls changed clean input: %q", got)
	}
}

func TestStripASCIIControls_NormalizesInvalidUTF8(t *testing.T) {
	got := StripASCIIControls("bad:\xff")
	if got != "bad:�" {
		t.Fatalf("StripASCIIControls should normalize invalid UTF-8, got %q", got)
	}
	if !utf8.ValidString(got) {
		t.Fatalf("normalized string should be valid UTF-8: %q", got)
	}
}

func TestContainsASCIIControls(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"plain text", false},
		{"hello\nworld", false},
		{"tab\tok", false},
		{"bad\x00byte", true},
		{"bad\x1b[31m", true},
		{"bad\x7fdel", true},
	}
	for _, c := range cases {
		if got := ContainsASCIIControls(c.in); got != c.want {
			t.Fatalf("ContainsASCIIControls(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestContainsASCIIControlBytes(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"plain text", false},
		{"hello\nworld", true},
		{"tab\tok", true},
		{"bad\x00byte", true},
		{"bad\x1b[31m", true},
		{"bad\x7fdel", true},
	}
	for _, c := range cases {
		if got := ContainsASCIIControlBytes(c.in); got != c.want {
			t.Fatalf("ContainsASCIIControlBytes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
