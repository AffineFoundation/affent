package browser

import (
	"errors"
	"strings"
	"testing"
)

func TestBrowserNotInteractableError_Message(t *testing.T) {
	err := browserNotInteractableError(7, errors.New("covered"))
	msg := err.Error()
	for _, want := range []string{
		"ref 7",
		"not interactable",
		"covered",
		"Failure: kind=not_interactable",
		"Next:",
		"browser_snapshot",
		"scroll",
		"different visible ref",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("error message missing %q: %q", want, msg)
		}
	}
}
