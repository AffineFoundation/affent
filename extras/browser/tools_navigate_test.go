package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestResolveBrowserWaitTimeout(t *testing.T) {
	cases := []struct {
		name    string
		in      int
		want    time.Duration
		wantErr bool
	}{
		{"default", 0, time.Duration(defaultBrowserWaitTimeoutMS) * time.Millisecond, false},
		{"minimum", minBrowserWaitTimeoutMS, time.Duration(minBrowserWaitTimeoutMS) * time.Millisecond, false},
		{"maximum", maxBrowserWaitTimeoutMS, time.Duration(maxBrowserWaitTimeoutMS) * time.Millisecond, false},
		{"below minimum", minBrowserWaitTimeoutMS - 1, 0, true},
		{"above maximum", maxBrowserWaitTimeoutMS + 1, 0, true},
		{"negative", -1, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := resolveBrowserWaitTimeout(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("resolveBrowserWaitTimeout(%d) expected error", c.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveBrowserWaitTimeout(%d): %v", c.in, err)
			}
			if got != c.want {
				t.Fatalf("resolveBrowserWaitTimeout(%d) = %s, want %s", c.in, got, c.want)
			}
		})
	}
}

func TestNavigateToolRejectsBlankURLAndPublishesMinLength(t *testing.T) {
	tool := NavigateTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"minLength": 1`) {
		t.Fatalf("schema should publish url minLength: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("blank URL error = %v, want url is required", err)
	}
}

func TestWaitToolRejectsBlankRequiredTextAndPublishesMinLength(t *testing.T) {
	tool := WaitTool(&Session{})
	if count := strings.Count(string(tool.Schema), `"minLength": 1`); count < 2 {
		t.Fatalf("schema should publish minLength for for/value fields: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"for":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "'for' is required") {
		t.Fatalf("blank for error = %v, want 'for' is required", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"for":"text","value":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "'value' is required") {
		t.Fatalf("blank text value error = %v, want value is required", err)
	}
}
