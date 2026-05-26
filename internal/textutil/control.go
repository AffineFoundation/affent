package textutil

import (
	"strings"
	"unicode/utf8"
)

// ContainsASCIIControls reports whether s contains any ASCII C0
// control other than tab, newline, or carriage return.
func ContainsASCIIControls(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\t' || c == '\n' || c == '\r' {
			continue
		}
		if c < 0x20 || c == 0x7F {
			return true
		}
	}
	return false
}

// ContainsASCIIControlBytes reports whether s contains any ASCII C0
// control byte, including tab, newline, and carriage return.
func ContainsASCIIControlBytes(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7F {
			return true
		}
	}
	return false
}

// StripASCIIControls removes ASCII C0 controls except tab, newline,
// and carriage return. Invalid UTF-8 is normalized by Go's rune
// decoding during reconstruction, which keeps downstream strings
// printable without requiring a separate validator.
func StripASCIIControls(s string) string {
	if s == "" {
		return s
	}
	clean := utf8.ValidString(s) && !ContainsASCIIControls(s)
	if clean {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			b.WriteRune(r)
		case r < 0x20 || r == 0x7F:
			// dropped: C0 controls (incl. NUL and ESC) and DEL
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
