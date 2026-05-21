package textutil

// AlignForward returns the smallest index i >= pos such that s[i] is a UTF-8
// leading byte, or len(s) if pos is past the end.
func AlignForward(s string, pos int) int {
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

// AlignBackward returns the largest index i <= pos such that s[i] is a UTF-8
// leading byte, or 0 if pos is at the start.
func AlignBackward(s string, pos int) int {
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
