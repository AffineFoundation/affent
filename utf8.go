package affent

// utf8AlignForward returns the smallest index i >= pos such that
// s[i] is a UTF-8 leading byte (or len(s) if pos is past the end).
// Useful when an arithmetic byte offset may have landed inside a
// multi-byte rune and the caller needs the next safe split point.
func utf8AlignForward(s string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(s) {
		return len(s)
	}
	for pos < len(s) && (s[pos]&0xC0) == 0x80 {
		pos++
	}
	return pos
}

// utf8AlignBackward returns the largest index i <= pos such that
// s[i] is a UTF-8 leading byte (or 0 if pos is at the start). Use it
// when truncating to keep the prefix UTF-8 valid.
func utf8AlignBackward(s string, pos int) int {
	if pos <= 0 {
		return 0
	}
	if pos >= len(s) {
		return len(s)
	}
	for pos > 0 && (s[pos]&0xC0) == 0x80 {
		pos--
	}
	return pos
}
