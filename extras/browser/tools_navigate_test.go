package browser

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	agent "github.com/affinefoundation/affent/internal/agent"
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
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"minLength": 1`) {
		t.Fatalf("schema should publish url minLength: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maxLength": 4096`) {
		t.Fatalf("schema should publish url maxLength: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"default": "load"`) {
		t.Fatalf("schema should publish wait_until default: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"url":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "url is required") {
		t.Fatalf("blank URL error = %v, want url is required", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("blank URL error should include Next step, got %v", err)
	}
	longURL := "https://example.com/" + strings.Repeat("x", maxBrowserURLBytes-len("https://example.com/")+1)
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"`+longURL+`"}`))
	if err == nil || !strings.Contains(err.Error(), "browser_navigate supports URLs up to") {
		t.Fatalf("oversized URL error = %v, want URL length error", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("oversized URL error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "url must start with") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("scheme error = %v, want Next guidance", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"url":"https://example.com","query":"ignored"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "query") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v, want Next guidance", err)
	}
}

func TestWaitToolRejectsBlankRequiredTextAndPublishesMinLength(t *testing.T) {
	tool := WaitTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	if count := strings.Count(string(tool.Schema), `"minLength": 1`); count < 2 {
		t.Fatalf("schema should publish minLength for for/value fields: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maxLength": 2048`) {
		t.Fatalf("schema should publish value maxLength: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"default": 10000`) {
		t.Fatalf("schema should publish timeout_ms default: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"for":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "'for' is required") {
		t.Fatalf("blank for error = %v, want 'for' is required", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("blank for error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"for":"text","value":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "'value' is required") {
		t.Fatalf("blank text value error = %v, want value is required", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("blank text value error should include Next step, got %v", err)
	}
	longValue := strings.Repeat("x", maxBrowserWaitTextBytes+1)
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"for":"text","value":"`+longValue+`"}`))
	if err == nil || !strings.Contains(err.Error(), "browser_wait text supports values up to") {
		t.Fatalf("oversized text value error = %v, want value length error", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("oversized text value error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"for":"load","timeout_ms":1}`))
	if err == nil || !strings.Contains(err.Error(), "timeout_ms must be between") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("invalid timeout error = %v, want Next guidance", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"for":"load","url":"https://example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "url") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v, want Next guidance", err)
	}
}

func TestScrollToolRejectsBlankDirectionBeforePageCheck(t *testing.T) {
	tool := ScrollTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"minLength": 1`) {
		t.Fatalf("schema should publish direction minLength: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maximum": 5000`) {
		t.Fatalf("schema should publish amount maximum: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"default": 600`) {
		t.Fatalf("schema should publish amount default: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"direction":"   "}`))
	if err == nil || !strings.Contains(err.Error(), "'direction' is required") {
		t.Fatalf("blank direction error = %v, want direction is required", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("blank direction error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"direction":"sideways"}`))
	if err == nil || !strings.Contains(err.Error(), `unknown direction "sideways"`) {
		t.Fatalf("unknown direction error = %v, want unknown direction", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown direction error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"direction":"down","amount":5001}`))
	if err == nil || !strings.Contains(err.Error(), "amount must be between 1 and 5000") {
		t.Fatalf("oversized amount error = %v, want amount maximum", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("oversized amount error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"direction":"down","ref":1}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "ref") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v, want Next guidance", err)
	}
}

func TestTypeToolRejectsOversizedTextBeforePageCheck(t *testing.T) {
	tool := TypeTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	if !strings.Contains(string(tool.Schema), `"maxLength": 4096`) {
		t.Fatalf("schema should publish text maxLength: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"ref":0,"text":"hi"}`))
	if err == nil || !strings.Contains(err.Error(), "ref must be a positive integer") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("invalid ref error = %v, want Next guidance", err)
	}
	text := strings.Repeat("x", maxBrowserTypeTextBytes+1)
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"ref":1,"text":"`+text+`"}`))
	if err == nil || !strings.Contains(err.Error(), "browser_type supports text up to") {
		t.Fatalf("oversized text error = %v, want text length error", err)
	}
	if !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("oversized text error should include Next step, got %v", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"ref":1,"text":"hi","url":"https://example.com"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "url") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v, want Next guidance", err)
	}
}

func TestClickToolRejectsInvalidRefBeforePageCheck(t *testing.T) {
	tool := ClickTool(&Session{})
	if !strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
		t.Fatalf("schema should reject unknown args: %s", tool.Schema)
	}
	_, err := tool.Execute(context.Background(), json.RawMessage(`{"ref":0}`))
	if err == nil || !strings.Contains(err.Error(), "ref must be a positive integer") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("invalid ref error = %v, want Next guidance", err)
	}
	_, err = tool.Execute(context.Background(), json.RawMessage(`{"ref":1,"text":"ignored"}`))
	if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "text") || !strings.Contains(err.Error(), "Next:") {
		t.Fatalf("unknown arg error = %v, want Next guidance", err)
	}
}

func TestNoArgBrowserToolsRejectUnknownArgs(t *testing.T) {
	for _, tool := range []*agent.Tool{
		BackTool(&Session{}),
		SnapshotTool(&Session{}),
	} {
		if !strings.Contains(string(tool.Schema), `"additionalProperties":false`) &&
			!strings.Contains(string(tool.Schema), `"additionalProperties": false`) {
			t.Fatalf("%s schema should reject unknown args: %s", tool.Name, tool.Schema)
		}
		_, err := tool.Execute(context.Background(), json.RawMessage(`{"ref":1}`))
		if err == nil || !strings.Contains(err.Error(), "unknown field") || !strings.Contains(err.Error(), "ref") || !strings.Contains(err.Error(), "Next:") {
			t.Fatalf("%s unknown arg error = %v, want Next guidance", tool.Name, err)
		}
	}
}
