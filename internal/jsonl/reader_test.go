package jsonl

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"testing"
)

func TestReadBoundedLineReadsNormalLines(t *testing.T) {
	r := bufio.NewReader(bytes.NewBufferString("one\nlast"))

	line, over, err := ReadBoundedLine(r, 8)
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != "one\n" || over {
		t.Fatalf("line = %q over=%v, want one newline and not over limit", string(line), over)
	}

	line, over, err = ReadBoundedLine(r, 8)
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != "last" || over {
		t.Fatalf("line = %q over=%v, want final partial line and not over limit", string(line), over)
	}

	_, _, err = ReadBoundedLine(r, 8)
	if !errors.Is(err, io.EOF) {
		t.Fatalf("final error = %v, want EOF", err)
	}
}

func TestReadBoundedLineSkipsOversizedLine(t *testing.T) {
	r := bufio.NewReader(bytes.NewBufferString("toolong\nnext\n"))

	line, over, err := ReadBoundedLine(r, 4)
	if err != nil {
		t.Fatal(err)
	}
	if len(line) != 0 || !over {
		t.Fatalf("oversized line = %q over=%v, want empty and over limit", string(line), over)
	}

	line, over, err = ReadBoundedLine(r, 8)
	if err != nil {
		t.Fatal(err)
	}
	if string(line) != "next\n" || over {
		t.Fatalf("line after oversized = %q over=%v, want next newline and not over limit", string(line), over)
	}
}
