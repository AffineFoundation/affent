package agent

import (
	"encoding/json"
	"reflect"
	"sort"
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

func TestRepairToolArgsWithSchema_RenamesBrowserAndWebAliases(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["url","ref","text"],
		"properties":{
			"url":{"type":"string","minLength":1},
			"ref":{"type":"integer","minimum":1},
			"text":{"type":"string","minLength":1},
			"num_results":{"type":"integer","minimum":1,"maximum":20},
			"save_path":{"type":"string","maxLength":64},
			"full_page":{"type":"boolean"},
			"timeout_ms":{"type":"integer","minimum":100,"maximum":60000}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{
		"href":"https://example.com",
		"ref_id":"3",
		"value":"hello",
		"n":"5",
		"savePath":"shots/page.png",
		"fullPage":"true",
		"timeoutMs":"1000"
	}`), schema)
	if !repaired {
		t.Fatal("expected browser/web aliases to repair")
	}
	want := `{"full_page":true,"num_results":5,"ref":3,"save_path":"shots/page.png","text":"hello","timeout_ms":1000,"url":"https://example.com"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s; notes=%v", got, want, notes)
	}
}

func TestRepairToolArgsWithSchema_RenamesSubagentAliases(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["task"],
		"properties":{
			"task":{"type":"string","minLength":1},
			"mode":{"type":"string","enum":["explore","review"]},
			"max_turns":{"type":"integer","minimum":1,"maximum":12}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{
		"instructions":"inspect docs",
		"type":"Review",
		"turns":"6"
	}`), schema)
	if !repaired {
		t.Fatal("expected subagent aliases to repair")
	}
	want := `{"max_turns":6,"mode":"review","task":"inspect docs"}`
	if string(got) != want {
		t.Fatalf("got %s, want %s; notes=%v", got, want, notes)
	}
}

func TestRepairToolArgsWithSchema_WrapsScalarForSingleRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`"README.md"`), schema)
	if !repaired {
		t.Fatal("expected scalar path to be wrapped")
	}
	if string(got) != `{"path":"README.md"}` {
		t.Fatalf("got %s, want wrapped path; notes=%v", got, notes)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "wrapped arguments") {
		t.Fatalf("missing scalar wrap note: %v", notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapScalarAboveMaxLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","maxLength":5}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`"too long"`), schema)
	if repaired {
		t.Fatalf("overlong string should not be wrapped; got %s notes=%v", got, notes)
	}
	if string(got) != `"too long"` {
		t.Fatalf("got %s, want original overlong scalar", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapScalarBelowMinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","minLength":1}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`""`), schema)
	if repaired {
		t.Fatalf("empty string should not be wrapped for minLength schema; got %s notes=%v", got, notes)
	}
	if string(got) != `""` {
		t.Fatalf("got %s, want original empty scalar", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapWhitespaceScalarBelowMinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","minLength":1}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`"   "`), schema)
	if repaired {
		t.Fatalf("blank string should not be wrapped for minLength schema; got %s notes=%v", got, notes)
	}
	if string(got) != `"   "` {
		t.Fatalf("got %s, want original blank scalar", got)
	}
}

func TestRepairToolArgsWithSchema_WrapsScalarAndNormalizesEnum(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["action"],
		"properties":{
			"action":{"type":"string","enum":["list","read"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`" READ "`), schema)
	if !repaired {
		t.Fatal("expected scalar enum action to be wrapped")
	}
	if string(got) != `{"action":"read"}` {
		t.Fatalf("got %s, want normalized wrapped action; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_WrapsIntegerScalarForSingleRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_entries"],
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`10`), schema)
	if !repaired {
		t.Fatal("expected scalar integer to be wrapped")
	}
	if string(got) != `{"max_entries":10}` {
		t.Fatalf("got %s, want wrapped integer; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapFractionalScalarForIntegerField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_entries"],
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`10.5`), schema)
	if repaired {
		t.Fatalf("fractional number should not be wrapped for integer schema; got %s notes=%v", got, notes)
	}
	if string(got) != `10.5` {
		t.Fatalf("got %s, want original fractional scalar", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapUnsafeScalarForIntegerField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_entries"],
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`9223372036854775808`), schema)
	if repaired {
		t.Fatalf("out-of-range number should not be wrapped for integer schema; got %s notes=%v", got, notes)
	}
	if string(got) != `9223372036854775808` {
		t.Fatalf("got %s, want original out-of-range scalar", got)
	}
}

func TestRepairToolArgsWithSchema_WrapsNumberScalarForSingleRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["timeout_sec"],
		"properties":{
			"timeout_sec":{"type":"number"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`1.5`), schema)
	if !repaired {
		t.Fatal("expected scalar number to be wrapped")
	}
	if string(got) != `{"timeout_sec":1.5}` {
		t.Fatalf("got %s, want wrapped number; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_WrapsNumberScalarForNullableRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["timeout_sec"],
		"properties":{
			"timeout_sec":{"type":["null","number"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`1.5`), schema)
	if !repaired {
		t.Fatal("expected scalar number to be wrapped for nullable number schema")
	}
	if string(got) != `{"timeout_sec":1.5}` {
		t.Fatalf("got %s, want wrapped nullable number; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_WrapsNullForNullableRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["timeout_sec"],
		"properties":{
			"timeout_sec":{"type":["number","null"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`null`), schema)
	if !repaired {
		t.Fatal("expected null to be wrapped for nullable required field")
	}
	if string(got) != `{"timeout_sec":null}` {
		t.Fatalf("got %s, want wrapped null; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapNullForNonNullableField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["timeout_sec"],
		"properties":{
			"timeout_sec":{"type":"number"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`null`), schema)
	if repaired {
		t.Fatalf("non-nullable schema should not wrap null; got %s notes=%v", got, notes)
	}
	if string(got) != `null` {
		t.Fatalf("got %s, want original null", got)
	}
}

func TestRepairToolArgsWithSchema_WrapsArrayForSingleRequiredField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["items"],
		"properties":{
			"items":{"type":"array"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`["a","b"]`), schema)
	if !repaired {
		t.Fatal("expected array to be wrapped for single required array field")
	}
	if string(got) != `{"items":["a","b"]}` {
		t.Fatalf("got %s, want wrapped array; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapArrayForStringField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`["README.md"]`), schema)
	if repaired {
		t.Fatalf("array should not be wrapped for string schema; got %s notes=%v", got, notes)
	}
	if string(got) != `["README.md"]` {
		t.Fatalf("got %s, want original array", got)
	}
}

func TestRepairToolArgsWithSchema_WrapsObjectForSingleRequiredObjectField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["input"],
		"properties":{
			"input":{"type":"object"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"query":"memory search","top_k":3}`), schema)
	if !repaired {
		t.Fatal("expected object to be wrapped for single required object field")
	}
	if string(got) != `{"input":{"query":"memory search","top_k":3}}` {
		t.Fatalf("got %s, want wrapped object; notes=%v", got, notes)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, "\n"), "wrapped object arguments as input") {
		t.Fatalf("missing object wrap note: %v", notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapObjectWhenSchemaHasSiblingField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["input"],
		"properties":{
			"input":{"type":"object"},
			"mode":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"query":"memory search"}`), schema)
	if !repaired {
		t.Fatal("expected unknown field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("object should not be wrapped when schema has sibling fields; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "wrapped object arguments") {
			t.Fatalf("object should not be wrapped with sibling fields; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapEmptyObjectForRequiredObjectField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["input"],
		"properties":{
			"input":{"type":"object"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{}`), schema)
	if repaired {
		t.Fatalf("empty object should not be wrapped; got %s notes=%v", got, notes)
	}
	if string(got) != `{}` {
		t.Fatalf("got %s, want original empty object", got)
	}
}

func TestRepairToolArgsWithSchema_UnwrapsSingleArgumentsObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"},
			"max_bytes":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"arguments":{"file_path":"README.md","maxBytes":"2048"}}`), schema)
	if !repaired {
		t.Fatal("expected arguments wrapper to be unwrapped")
	}
	if string(got) != `{"max_bytes":2048,"path":"README.md"}` {
		t.Fatalf("got %s, want unwrapped repaired object; notes=%v", got, notes)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, "\n"), "unwrapped field arguments") {
		t.Fatalf("missing unwrap note: %v", notes)
	}
}

func TestRepairToolArgsWithSchema_UnwrapsSingleArgumentsJSONStringObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"},
			"max_bytes":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"arguments":"{\"file_path\":\"README.md\",\"maxBytes\":\"2048\"}"}`), schema)
	if !repaired {
		t.Fatal("expected JSON-string arguments wrapper to be unwrapped")
	}
	if string(got) != `{"max_bytes":2048,"path":"README.md"}` {
		t.Fatalf("got %s, want unwrapped repaired object; notes=%v", got, notes)
	}
	if len(notes) == 0 || !strings.Contains(strings.Join(notes, "\n"), "unwrapped field arguments") {
		t.Fatalf("missing unwrap note: %v", notes)
	}
}

func TestRepairToolArgsWithSchema_UnwrapsSingleArgumentsPlainString(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"arguments":"README.md"}`), schema)
	if !repaired {
		t.Fatal("expected plain wrapper string to be repaired")
	}
	if string(got) != `{"path":"README.md"}` {
		t.Fatalf("got %s, want wrapped path; notes=%v", got, notes)
	}
	if joined := strings.Join(notes, "\n"); !strings.Contains(joined, "unwrapped field arguments") || !strings.Contains(joined, "wrapped arguments as path") {
		t.Fatalf("missing wrapper scalar repair notes: %v", notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotUnwrapWrapperPlainStringBelowMinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","minLength":1}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"arguments":"   "}`), schema)
	if !repaired {
		t.Fatal("expected invalid wrapper field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("blank wrapper value should not be wrapped; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "wrapped arguments") {
			t.Fatalf("blank wrapper value should not report scalar wrap; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotUnwrapWrapperPlainStringForMultipleRequiredFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path","content"],
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"arguments":"README.md"}`), schema)
	if !repaired {
		t.Fatal("expected ambiguous wrapper field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("multi-required plain wrapper should not be guessed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "wrapped arguments") {
			t.Fatalf("multi-required wrapper should not report scalar wrap; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_UnwrapsSingleInputObject(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"input":{"query":"memory search"}}`), schema)
	if !repaired {
		t.Fatal("expected input wrapper to be unwrapped")
	}
	if string(got) != `{"query":"memory search"}` {
		t.Fatalf("got %s, want unwrapped input; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotUnwrapWhenWrapperIsNotSoleField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path"],
		"properties":{
			"path":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"input":{"path":"README.md"},"path":"actual.md"}`), schema)
	if !repaired {
		t.Fatal("expected unknown wrapper field to be dropped")
	}
	if string(got) != `{"path":"actual.md"}` {
		t.Fatalf("got %s, want real field preserved; notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "unwrapped field") {
			t.Fatalf("mixed wrapper and real fields should not unwrap; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotUnwrapDeclaredInputField(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["input"],
		"properties":{
			"input":{"type":"object"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"input":{"query":"keep me"}}`), schema)
	if repaired {
		t.Fatalf("declared input field must not be treated as wrapper; got %s notes=%v", got, notes)
	}
	if string(got) != `{"input":{"query":"keep me"}}` {
		t.Fatalf("got %s, want original input object", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapScalarForMultipleRequiredFields(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["path","content"],
		"properties":{
			"path":{"type":"string"},
			"content":{"type":"string"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`"README.md"`), schema)
	if repaired {
		t.Fatalf("multi-required schema should not wrap scalar; got %s notes=%v", got, notes)
	}
	if string(got) != `"README.md"` {
		t.Fatalf("got %s, want original scalar", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotGuessAmbiguousAlias(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_bytes":{"type":"integer"},
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"limit":"10"}`), schema)
	if !repaired {
		t.Fatal("expected ambiguous unknown field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("ambiguous limit alias should not be guessed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "renamed field limit") {
			t.Fatalf("ambiguous limit alias should not be renamed; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_UsesUniqueRequiredAliasCandidate(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_entries"],
		"properties":{
			"max_bytes":{"type":"integer"},
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"limit":"10"}`), schema)
	if !repaired {
		t.Fatal("expected required alias candidate to repair")
	}
	if string(got) != `{"max_entries":10}` {
		t.Fatalf("got %s, want max_entries repair; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotRenameRequiredAliasWithUnsafeInteger(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_entries"],
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"limit":9223372036854775808}`), schema)
	if !repaired {
		t.Fatal("expected unsafe unknown alias field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("unsafe required alias should not be renamed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "renamed field limit") || strings.Contains(note, "filled required field") {
			t.Fatalf("unsafe required alias should not be renamed or filled; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotRenameRequiredAliasAboveMaximum(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_entries"],
		"properties":{
			"max_entries":{"type":"integer","minimum":1,"maximum":20}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"limit":"999"}`), schema)
	if !repaired {
		t.Fatal("expected out-of-bounds unknown alias field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("out-of-bounds alias should not be renamed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "renamed field limit") || strings.Contains(note, "filled required field") {
			t.Fatalf("out-of-bounds alias should not be renamed or filled; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotRenameRequiredAliasAboveMaxLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","maxLength":5}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"q":"too long"}`), schema)
	if !repaired {
		t.Fatal("expected overlong unknown alias field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("overlong alias should not be renamed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "renamed field q") || strings.Contains(note, "filled required field") {
			t.Fatalf("overlong alias should not be renamed or filled; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotRenameRequiredAliasBelowMinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","minLength":1}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"q":""}`), schema)
	if !repaired {
		t.Fatal("expected empty unknown alias field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("empty alias should not be renamed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "renamed field q") || strings.Contains(note, "filled required field") {
			t.Fatalf("empty alias should not be renamed or filled; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotRenameRequiredAliasWithWhitespaceBelowMinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["query"],
		"properties":{
			"query":{"type":"string","minLength":1}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"q":"   "}`), schema)
	if !repaired {
		t.Fatal("expected blank unknown alias field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("blank alias should not be renamed; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "renamed field q") || strings.Contains(note, "filled required field") {
			t.Fatalf("blank alias should not be renamed or filled; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_DoesNotFillMultipleRequiredFieldsFromAmbiguousAlias(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["max_bytes","max_entries"],
		"properties":{
			"max_bytes":{"type":"integer"},
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"limit":"10"}`), schema)
	if !repaired {
		t.Fatal("expected ambiguous unknown field to be dropped")
	}
	if string(got) != `{}` {
		t.Fatalf("ambiguous limit alias should not fill multiple required fields; got %s notes=%v", got, notes)
	}
	for _, note := range notes {
		if strings.Contains(note, "filled required field") {
			t.Fatalf("ambiguous limit alias should not fill required fields; notes=%v", notes)
		}
	}
}

func TestRepairToolArgsWithSchema_NormalizesStringEnums(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"required":["action","mode"],
		"properties":{
			"action":{"type":"string","enum":["list","read"]},
			"mode":{"type":"string","enum":["explore","review"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"action":" READ ","mode":"Review"}`), schema)
	if !repaired {
		t.Fatal("expected enum normalization repair")
	}
	if string(got) != `{"action":"read","mode":"review"}` {
		t.Fatalf("got %s, want normalized enums; notes=%v", got, notes)
	}
	sort.Strings(notes)
	if !reflect.DeepEqual(notes, []string{"normalized enum field action", "normalized enum field mode"}) {
		t.Fatalf("notes = %v, want enum normalization notes", notes)
	}
}

func TestRepairToolArgsWithSchema_CoercesIntegralDecimalStringToInteger(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"max_entries":"10.0"}`), schema)
	if !repaired {
		t.Fatal("expected integral decimal string to repair")
	}
	if string(got) != `{"max_entries":10}` {
		t.Fatalf("got %s, want integer repair; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_CoercesStringToNumber(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"timeout_sec":{"type":"number"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"timeout_sec":"1.5"}`), schema)
	if !repaired {
		t.Fatal("expected numeric string to repair")
	}
	if string(got) != `{"timeout_sec":1.5}` {
		t.Fatalf("got %s, want number repair; notes=%v", got, notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceStringIntegerAboveMaximum(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_entries":{"type":"integer","minimum":1,"maximum":20}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"max_entries":"999"}`), schema)
	if repaired {
		t.Fatalf("out-of-bounds numeric string should not be coerced; got %s notes=%v", got, notes)
	}
	if string(got) != `{"max_entries":"999"}` {
		t.Fatalf("got %s, want original out-of-bounds string", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceStringNumberBelowMinimum(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"timeout_sec":{"type":"number","minimum":0.5,"maximum":10}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"timeout_sec":"0.1"}`), schema)
	if repaired {
		t.Fatalf("below-minimum numeric string should not be coerced; got %s notes=%v", got, notes)
	}
	if string(got) != `{"timeout_sec":"0.1"}` {
		t.Fatalf("got %s, want original below-minimum string", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceToStringAboveMaxLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{"type":"string","maxLength":3}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"value":12345}`), schema)
	if repaired {
		t.Fatalf("number should not be coerced to overlong string; got %s notes=%v", got, notes)
	}
	if string(got) != `{"value":12345}` {
		t.Fatalf("got %s, want original number", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceToStringBelowMinLength(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{"type":"string","minLength":2}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"value":1}`), schema)
	if repaired {
		t.Fatalf("number should not be coerced to too-short string; got %s notes=%v", got, notes)
	}
	if string(got) != `{"value":1}` {
		t.Fatalf("got %s, want original number", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceAlreadyValidUnionInteger(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{"type":["string","integer"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"value":10}`), schema)
	if repaired {
		t.Fatalf("integer already matches union schema and should not be coerced; got %s notes=%v", got, notes)
	}
	if string(got) != `{"value":10}` {
		t.Fatalf("got %s, want original integer value", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceAlreadyValidUnionString(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{"type":["integer","string"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"value":"10"}`), schema)
	if repaired {
		t.Fatalf("string already matches union schema and should not be coerced; got %s notes=%v", got, notes)
	}
	if string(got) != `{"value":"10"}` {
		t.Fatalf("got %s, want original string value", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceUnionIntegerToBoolean(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"value":{"type":["boolean","integer"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"value":1}`), schema)
	if repaired {
		t.Fatalf("integer already matches union schema and should not become boolean; got %s notes=%v", got, notes)
	}
	if string(got) != `{"value":1}` {
		t.Fatalf("got %s, want original integer value", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceBadStringToNumber(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"timeout_sec":{"type":"number"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"timeout_sec":"soon"}`), schema)
	if repaired {
		t.Fatalf("nonnumeric string should not be coerced to number; got %s notes=%v", got, notes)
	}
	if string(got) != `{"timeout_sec":"soon"}` {
		t.Fatalf("got %s, want original string value", got)
	}
}

func TestRepairToolArgsWithSchema_CoercesNumericBoolean(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"replace_all":{"type":"boolean"}
		}
	}`)
	for _, tc := range []struct {
		name string
		args string
		want string
	}{
		{name: "one", args: `{"replace_all":1}`, want: `{"replace_all":true}`},
		{name: "zero", args: `{"replace_all":0}`, want: `{"replace_all":false}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(tc.args), schema)
			if !repaired {
				t.Fatal("expected numeric boolean repair")
			}
			if string(got) != tc.want {
				t.Fatalf("got %s, want %s; notes=%v", got, tc.want, notes)
			}
		})
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceOtherNumbersToBoolean(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"replace_all":{"type":"boolean"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"replace_all":2}`), schema)
	if repaired {
		t.Fatalf("non-0/1 number should not be coerced to boolean; got %s notes=%v", got, notes)
	}
	if string(got) != `{"replace_all":2}` {
		t.Fatalf("got %s, want original numeric value", got)
	}
}

func TestRepairToolArgsWithSchema_WrapsStringAsSingleItemArray(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"evidence":{"type":"array","items":{"type":"string"}}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"evidence":"go test ./internal/agent"}`), schema)
	if !repaired {
		t.Fatal("expected string array repair")
	}
	if string(got) != `{"evidence":["go test ./internal/agent"]}` {
		t.Fatalf("got %s, want single-item array; notes=%v", got, notes)
	}
	if len(notes) == 0 || !strings.Contains(notes[0], "coerced field evidence to array") {
		t.Fatalf("missing array repair note: %v", notes)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapBlankStringAsArray(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"triggers":{"type":"array","items":{"type":"string"}}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"triggers":"   "}`), schema)
	if repaired {
		t.Fatalf("blank string should not become array; got %s notes=%v", got, notes)
	}
	if string(got) != `{"triggers":"   "}` {
		t.Fatalf("got %s, want original blank string", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotWrapStringForNonStringArray(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"values":{"type":"array","items":{"type":"integer"}}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"values":"10"}`), schema)
	if repaired {
		t.Fatalf("string should not become integer array; got %s notes=%v", got, notes)
	}
	if string(got) != `{"values":"10"}` {
		t.Fatalf("got %s, want original string", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceFractionalStringToInteger(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"max_entries":"10.5"}`), schema)
	if repaired {
		t.Fatalf("fractional string should not be coerced to integer; got %s notes=%v", got, notes)
	}
	if string(got) != `{"max_entries":"10.5"}` {
		t.Fatalf("got %s, want original fractional string", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotCoerceOutOfRangeStringToInteger(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"max_entries":{"type":"integer"}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"max_entries":"9223372036854775808"}`), schema)
	if repaired {
		t.Fatalf("out-of-range string should not be coerced to integer; got %s notes=%v", got, notes)
	}
	if string(got) != `{"max_entries":"9223372036854775808"}` {
		t.Fatalf("got %s, want original out-of-range string", got)
	}
}

func TestRepairToolArgsWithSchema_LeavesUnknownEnumValueForToolValidation(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"action":{"type":"string","enum":["list","read"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"action":"delete"}`), schema)
	if repaired {
		t.Fatalf("unknown enum should not be rewritten; got %s notes=%v", got, notes)
	}
	if string(got) != `{"action":"delete"}` {
		t.Fatalf("got %s, want original unknown enum value", got)
	}
}

func TestRepairToolArgsWithSchema_DoesNotGuessAmbiguousEnumNormalization(t *testing.T) {
	schema := json.RawMessage(`{
		"type":"object",
		"properties":{
			"mode":{"type":"string","enum":["safe-mode","safe_mode"]}
		}
	}`)
	got, repaired, notes := repairToolArgsWithSchema(json.RawMessage(`{"mode":"Safe Mode"}`), schema)
	if repaired {
		t.Fatalf("ambiguous enum normalization should not be rewritten; got %s notes=%v", got, notes)
	}
	if string(got) != `{"mode":"Safe Mode"}` {
		t.Fatalf("got %s, want original ambiguous enum value", got)
	}
}

func TestToolErrorHelpUsesSchema(t *testing.T) {
	tl := &Tool{
		Name:   "read_file",
		Schema: json.RawMessage(`{"type":"object","required":["path"],"properties":{"path":{"type":"string"},"max_bytes":{"type":"integer"}}}`),
	}
	got := toolErrorHelp(tl, json.RawMessage(`{}`))
	for _, want := range []string{"Expected:", "required path", "Allowed:", "path (type=string)", "max_bytes (type=integer)", `"path":"relative/path.txt"`, "Received: {}", "Next:"} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool error help missing %q:\n%s", want, got)
		}
	}
}

func TestToolErrorHelpShowsArrayItemType(t *testing.T) {
	tl := &Tool{
		Name:   "plan",
		Schema: json.RawMessage(`{"type":"object","properties":{"evidence":{"type":"array","items":{"type":"string"}}}}`),
	}
	got := toolErrorHelp(tl, json.RawMessage(`{"evidence":"go test"}`))
	if !strings.Contains(got, "evidence (type=array, items=string)") {
		t.Fatalf("array item type missing:\n%s", got)
	}
	if !strings.Contains(got, "Received:") || !strings.Contains(got, `"evidence":"go test"`) {
		t.Fatalf("received args missing:\n%s", got)
	}
}

func TestToolErrorHelpSurfacesSchemaConstraints(t *testing.T) {
	tl := &Tool{
		Name: "skill",
		Schema: json.RawMessage(`{
			"type":"object",
			"required":["action"],
			"properties":{
				"action":{"type":"string","enum":["list","read"]},
				"max_entries":{"type":"integer","minimum":1,"maximum":20,"default":5},
				"query":{"type":"string","minLength":1,"maxLength":64},
				"replace_all":{"type":"boolean","default":false}
			}
		}`),
	}
	got := toolErrorHelp(tl, json.RawMessage(`{"action":"delete","max_entries":100}`))
	for _, want := range []string{
		"action (type=string, enum=list|read)",
		"max_entries (type=integer, min=1, max=20, default=5)",
		"query (type=string, minLength=1, maxLength=64)",
		"replace_all (type=boolean, default=false)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("tool error help missing %q:\n%s", want, got)
		}
	}
}
