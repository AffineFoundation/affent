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

func TestErrNoPageGuidesRecovery(t *testing.T) {
	msg := ErrNoPage.Error()
	for _, want := range []string{
		"browser session has no active page",
		"Failure: kind=no_page",
		"Next:",
		"browser_navigate",
		"http:// or https://",
	} {
		if !strings.Contains(msg, want) {
			t.Fatalf("ErrNoPage missing %q: %q", want, msg)
		}
	}
}

func TestFormatScrollTelemetry_NoMovementGivesNextStep(t *testing.T) {
	got := formatScrollTelemetry(browserScrollTelemetry{
		Direction: "down",
		BeforeY:   1200,
		AfterY:    1200,
		MaxY:      1200,
	})
	for _, want := range []string{
		"SCROLL: direction=down",
		"movement=none",
		"boundary=bottom",
		"Next:",
		"browser_network/browser_network_read",
		"mark the field unavailable",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("scroll telemetry missing %q:\n%s", want, got)
		}
	}
}

func TestFormatScrollTelemetry_MovementStaysCompact(t *testing.T) {
	got := formatScrollTelemetry(browserScrollTelemetry{
		Direction: "page_down",
		BeforeY:   0,
		AfterY:    700,
		MaxY:      1400,
	})
	if !strings.Contains(got, "SCROLL: direction=page_down") || !strings.Contains(got, "movement=moved") {
		t.Fatalf("scroll telemetry missing movement:\n%s", got)
	}
	if strings.Contains(got, "Next:") {
		t.Fatalf("moving scroll should not add recovery guidance:\n%s", got)
	}
}
