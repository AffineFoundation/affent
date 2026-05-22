package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestNormalizeID_AllJSONNumericForms pins the four JSON-id encodings
// affent has actually seen in production. The comment on normalizeID
// says "We only ever issue integer ids ourselves, but a polite server
// might echo them as float64 (after a json round-trip)". Go's
// default decoder turns JSON numbers into float64 inside an `any`,
// which is by far the most common path; int / int64 / json.Number
// only show up when a caller used a custom decoder or built the id
// programmatically. Cover all four to keep refactor freedom on
// dispatch's any-typed id.
func TestNormalizeID_AllJSONNumericForms(t *testing.T) {
	cases := []struct {
		name string
		in   any
		want int64
		ok   bool
	}{
		{"float64 (default json.Unmarshal into any)", float64(42), 42, true},
		{"int64", int64(7), 7, true},
		{"int (Go-native)", int(123), 123, true},
		{"json.Number / valid integer", json.Number("99"), 99, true},
		{"json.Number / fractional rejected", json.Number("1.5"), 0, false},
		{"json.Number / non-numeric rejected", json.Number("nope"), 0, false},
		{"string id rejected (non-numeric type)", "abc", 0, false},
		{"nil rejected", nil, 0, false},
		{"bool rejected", true, 0, false},
		{"float64 truncates toward zero", float64(7.9), 7, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := normalizeID(c.in)
			if ok != c.ok || got != c.want {
				t.Errorf("normalizeID(%v) = (%d, %v), want (%d, %v)", c.in, got, ok, c.want, c.ok)
			}
		})
	}
}

// TestFlattenContent_KnownBlockKinds pins the model-facing rendering
// for every contentBlock.Type we expect from MCP servers. text →
// inline; image → "[image MIME, N bytes (omitted)]" so the model
// doesn't ask to "see" something we can't ship in chat-completions;
// resource → bracketed ref so the model can pass it back as evidence;
// unknown type → bracketed marker so an MCP server adding new block
// types doesn't silently produce an empty tool result.
func TestFlattenContent_KnownBlockKinds(t *testing.T) {
	t.Run("text-only", func(t *testing.T) {
		got := flattenContent([]contentBlock{{Type: "text", Text: "hello"}})
		if got != "hello" {
			t.Errorf("text-only: got %q, want hello", got)
		}
	})
	t.Run("multi-block joins with newline", func(t *testing.T) {
		got := flattenContent([]contentBlock{
			{Type: "text", Text: "line1"},
			{Type: "text", Text: "line2"},
		})
		if got != "line1\nline2" {
			t.Errorf("multi-block join: got %q", got)
		}
	})
	t.Run("image becomes omitted marker", func(t *testing.T) {
		got := flattenContent([]contentBlock{
			{Type: "image", MimeType: "image/png", Data: "AAAA"},
		})
		if !strings.Contains(got, "[image image/png") || !strings.Contains(got, "(omitted)") {
			t.Errorf("image marker shape changed: %q", got)
		}
	})
	t.Run("resource carries the raw ref", func(t *testing.T) {
		got := flattenContent([]contentBlock{
			{Type: "resource", Resource: json.RawMessage(`{"uri":"file://x"}`)},
		})
		if !strings.Contains(got, "[resource ref:") || !strings.Contains(got, "file://x") {
			t.Errorf("resource marker missing ref body: %q", got)
		}
	})
	t.Run("unknown type gets a typed marker", func(t *testing.T) {
		got := flattenContent([]contentBlock{{Type: "audio"}})
		if !strings.Contains(got, `[content type="audio"]`) {
			t.Errorf("unknown-type marker shape changed: %q", got)
		}
	})
	t.Run("empty input is empty string", func(t *testing.T) {
		if got := flattenContent(nil); got != "" {
			t.Errorf("nil blocks: got %q, want empty", got)
		}
	})
	t.Run("preserves block order across mixed types", func(t *testing.T) {
		got := flattenContent([]contentBlock{
			{Type: "text", Text: "first"},
			{Type: "image", MimeType: "image/jpeg", Data: "x"},
			{Type: "text", Text: "third"},
		})
		// Newline-joined, order-preserving.
		parts := strings.Split(got, "\n")
		if len(parts) != 3 || parts[0] != "first" || parts[2] != "third" {
			t.Errorf("mixed-type order: got %q", got)
		}
	})
}
