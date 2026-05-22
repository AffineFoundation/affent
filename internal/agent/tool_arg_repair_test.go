package agent

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRepairToolCallArgsForDispatch(t *testing.T) {
	cases := []struct {
		name         string
		raw          string
		want         string
		wantRepaired bool
	}{
		{
			name: "strict json",
			raw:  `{"path":"a.txt","max_bytes":12}`,
			want: `{"path":"a.txt","max_bytes":12}`,
		},
		{
			name:         "empty",
			raw:          "",
			want:         `{}`,
			wantRepaired: true,
		},
		{
			name:         "trailing comma",
			raw:          `{"path":"a.txt",}`,
			want:         `{"path":"a.txt"}`,
			wantRepaired: true,
		},
		{
			name:         "missing closer",
			raw:          `{"path":"a.txt","opts":["x"`,
			want:         `{"path":"a.txt","opts":["x"]}`,
			wantRepaired: true,
		},
		{
			name:         "double encoded object",
			raw:          `"{\"path\":\"a.txt\"}"`,
			want:         `{"path":"a.txt"}`,
			wantRepaired: false,
		},
		{
			name:         "irreparable fallback",
			raw:          `not json`,
			want:         `{}`,
			wantRepaired: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, repaired, err := repairToolCallArgsForDispatch(c.raw)
			if err != nil {
				t.Fatalf("repair returned error: %v", err)
			}
			if repaired != c.wantRepaired {
				t.Fatalf("repaired=%v, want %v", repaired, c.wantRepaired)
			}
			if string(got) != c.want {
				t.Fatalf("got %s, want %s", got, c.want)
			}
			var v any
			if err := json.Unmarshal(got, &v); err != nil {
				t.Fatalf("result must be valid json: %v", err)
			}
		})
	}
}

func TestRepairToolCallArgsForDispatch_RejectsOversize(t *testing.T) {
	_, _, err := repairToolCallArgsForDispatch(strings.Repeat("x", maxRepairableToolArgBytes+1))
	if err == nil {
		t.Fatal("expected oversize repair error")
	}
}

func TestRepairToolArgsWithSchema_RenamesAndCoercesFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"},
			"max_bytes":{"type":"integer"},
			"replace_all":{"type":"boolean"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{
		"file_path":"README.md",
		"maxBytes":"2048",
		"replaceAll":"true",
		"noise":"drop me"
	}`), schema)
	if !repaired {
		t.Fatal("expected schema repair")
	}
	want := `{"max_bytes":2048,"path":"README.md","replace_all":true}`
	if string(got) != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	if len(notes) == 0 {
		t.Fatal("expected repair notes")
	}
}

func TestToolErrorHelpUsesSchema(t *testing.T) {
	tl := &Tool{
		Name:   "read_file",
		Schema: json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"max_bytes":{"type":"integer"}}}`),
	}
	got := toolErrorHelp(tl, json.RawMessage(`{}`))
	for _, want := range []string{"Expected:", "required path", "Allowed:", `"path":"relative/path.txt"`, "Received: {}", "Next:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool error help missing %q:\n%s", want, got)
		}
	}
}
