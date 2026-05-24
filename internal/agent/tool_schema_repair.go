package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

type toolSchema struct {
	Required             []string                      `json:"required"`
	Properties           map[string]toolSchemaProperty `json:"properties"`
	AdditionalProperties any                           `json:"additionalProperties"`
}

type toolSchemaProperty struct {
	Type      any   `json:"type"`
	Enum      []any `json:"enum"`
	Minimum   any   `json:"minimum"`
	Maximum   any   `json:"maximum"`
	MinLength int   `json:"minLength"`
	MaxLength int   `json:"maxLength"`
	Default   any   `json:"default"`
	Items     *struct {
		Type any `json:"type"`
	} `json:"items"`
}

func repairToolArgsWithSchema(args json.RawMessage, schema json.RawMessage) (json.RawMessage, bool, []string) {
	var s toolSchema
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return args, false, nil
	}
	var obj map[string]any
	if err := json.Unmarshal(args, &obj); err != nil || obj == nil {
		if wrapped, ok := wrapSingleRequiredValueArgs(args, s); ok {
			return wrapped, true, []string{"wrapped arguments as " + s.Required[0]}
		}
		return args, false, nil
	}

	changed := false
	var notes []string
	if inner, key, ok := unwrapSingleToolArgsWrapper(obj, s.Properties); ok {
		obj = inner
		changed = true
		notes = append(notes, "unwrapped field "+key)
	}
	if wrapped, key, ok := wrapSingleRequiredValueWrapper(obj, s); ok {
		notes = append(notes, "unwrapped field "+key)
		notes = append(notes, "wrapped arguments as "+s.Required[0])
		return wrapped, true, notes
	}
	if wrapped, ok := wrapSingleRequiredObjectArgs(obj, s); ok {
		notes = append(notes, "wrapped object arguments as "+s.Required[0])
		return wrapped, true, notes
	}
	out := make(map[string]any, len(obj))
	used := map[string]bool{}
	for key, value := range obj {
		prop, ok := s.Properties[key]
		target := key
		if !ok {
			if alias, found := schemaPropertyAlias(key, s.Properties, s.Required); found {
				aliasProp := s.Properties[alias]
				if shouldRenameSchemaField(key, alias, value, aliasProp, s.Required, obj) {
					target = alias
					prop = aliasProp
					ok = true
					changed = true
					notes = append(notes, fmt.Sprintf("renamed field %s to %s", key, target))
				}
			}
		}
		if !ok {
			if shouldDropUnknownSchemaField(s) {
				changed = true
				notes = append(notes, fmt.Sprintf("dropped unknown field %s", key))
				continue
			}
			out[key] = value
			continue
		}
		coerced, didCoerce, coerceKind := coerceSchemaValueWithKind(value, prop)
		if didCoerce {
			changed = true
			if coerceKind == "enum" {
				notes = append(notes, fmt.Sprintf("normalized enum field %s", target))
			} else {
				notes = append(notes, fmt.Sprintf("coerced field %s to %s", target, strings.Join(schemaPropertyTypes(prop), "|")))
			}
		}
		out[target] = coerced
		used[target] = true
	}

	for _, req := range s.Required {
		if used[req] || out[req] != nil {
			continue
		}
		if value, ok := recoverRequiredSchemaValue(req, obj, s.Properties, s.Required); ok {
			out[req] = value
			changed = true
			notes = append(notes, fmt.Sprintf("filled required field %s from alias", req))
		}
	}
	if !changed {
		return args, false, nil
	}
	raw, err := json.Marshal(out)
	if err != nil {
		return args, false, nil
	}
	return json.RawMessage(raw), true, notes
}

var wrapperFieldNames = map[string]bool{
	"args":       true,
	"arguments":  true,
	"input":      true,
	"parameters": true,
}

func unwrapSingleToolArgsWrapper(obj map[string]any, props map[string]toolSchemaProperty) (map[string]any, string, bool) {
	if len(obj) != 1 {
		return nil, "", false
	}
	for key, value := range obj {
		if _, isSchemaField := props[key]; isSchemaField {
			return nil, "", false
		}
		if wrapperFieldNames[normalizeToolIdentifier(key)] {
			if inner, ok := value.(map[string]any); ok {
				return inner, key, true
			}
			if s, ok := value.(string); ok {
				if inner, ok := parseObjectString(s); ok {
					return inner, key, true
				}
			}
		}
	}
	return nil, "", false
}

func parseObjectString(s string) (map[string]any, bool) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return nil, false
	}
	var obj map[string]any
	if err := json.Unmarshal([]byte(s), &obj); err != nil || obj == nil {
		return nil, false
	}
	return obj, true
}

func wrapSingleRequiredValueArgs(args json.RawMessage, s toolSchema) (json.RawMessage, bool) {
	var value any
	if err := json.Unmarshal(args, &value); err != nil {
		return nil, false
	}
	return wrapSingleRequiredValue(value, s)
}

func wrapSingleRequiredValue(value any, s toolSchema) (json.RawMessage, bool) {
	if len(s.Required) != 1 {
		return nil, false
	}
	name := s.Required[0]
	prop, ok := s.Properties[name]
	if !ok {
		return nil, false
	}
	switch value.(type) {
	case map[string]any:
		return nil, false
	}
	if !schemaValueMatchesOrCanCoerce(value, prop) {
		return nil, false
	}
	value = coerceSchemaValueOrOriginal(value, prop)
	raw, err := json.Marshal(map[string]any{name: value})
	if err != nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}

func wrapSingleRequiredValueWrapper(obj map[string]any, s toolSchema) (json.RawMessage, string, bool) {
	if len(obj) != 1 {
		return nil, "", false
	}
	for key, value := range obj {
		if _, isSchemaField := s.Properties[key]; isSchemaField {
			return nil, "", false
		}
		if !wrapperFieldNames[normalizeToolIdentifier(key)] {
			return nil, "", false
		}
		if wrapped, ok := wrapSingleRequiredValue(value, s); ok {
			return wrapped, key, true
		}
	}
	return nil, "", false
}

func wrapSingleRequiredObjectArgs(obj map[string]any, s toolSchema) (json.RawMessage, bool) {
	if len(obj) == 0 || len(s.Required) != 1 || len(s.Properties) != 1 {
		return nil, false
	}
	name := s.Required[0]
	if _, exists := obj[name]; exists {
		return nil, false
	}
	prop, ok := s.Properties[name]
	if !ok || !schemaPropertyIncludesType(prop, "object") {
		return nil, false
	}
	raw, err := json.Marshal(map[string]any{name: obj})
	if err != nil {
		return nil, false
	}
	return json.RawMessage(raw), true
}

func shouldRenameSchemaField(key, target string, value any, prop toolSchemaProperty, required []string, obj map[string]any) bool {
	if _, exists := obj[target]; exists {
		return false
	}
	if normalizeToolIdentifier(key) == normalizeToolIdentifier(target) {
		return true
	}
	for _, req := range required {
		if req == target {
			return schemaValueMatchesOrCanCoerce(value, prop)
		}
	}
	return schemaValueMatchesOrCanCoerce(value, prop)
}

func schemaValueMatchesOrCanCoerce(v any, prop toolSchemaProperty) bool {
	if coerced, ok := coerceSchemaValue(v, prop); ok {
		return schemaValueWithinBounds(coerced, prop)
	}
	if !schemaValueMatchesTypes(v, schemaPropertyTypes(prop)) {
		return false
	}
	return schemaValueWithinBounds(v, prop)
}

func schemaValueWithinBounds(v any, prop toolSchemaProperty) bool {
	return schemaValueWithinNumericBounds(v, prop) && schemaValueWithinStringBounds(v, prop)
}

func schemaValueWithinNumericBounds(v any, prop toolSchemaProperty) bool {
	n, ok := schemaNumericValue(v)
	if !ok {
		return true
	}
	if min, ok := schemaBoundaryNumber(prop.Minimum); ok && n < min {
		return false
	}
	if max, ok := schemaBoundaryNumber(prop.Maximum); ok && n > max {
		return false
	}
	return true
}

func schemaValueWithinStringBounds(v any, prop toolSchemaProperty) bool {
	if prop.MinLength <= 0 && prop.MaxLength <= 0 {
		return true
	}
	s, ok := v.(string)
	if !ok {
		return true
	}
	if prop.MinLength > 0 && len(strings.TrimSpace(s)) < prop.MinLength {
		return false
	}
	if prop.MaxLength > 0 && len(s) > prop.MaxLength {
		return false
	}
	return true
}

func schemaNumericValue(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	default:
		return 0, false
	}
}

func schemaBoundaryNumber(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return 0, false
		}
		return x, true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return 0, false
		}
		return f, true
	case string:
		return parseNumberString(x)
	default:
		return 0, false
	}
}

func schemaValueMatchesTypes(v any, types []string) bool {
	for _, typ := range types {
		switch typ {
		case "null":
			if v == nil {
				return true
			}
		case "string":
			if _, ok := v.(string); ok {
				return true
			}
		case "integer":
			if f, ok := v.(float64); ok && isIntegralFloat64(f) {
				return true
			}
		case "number":
			if _, ok := v.(float64); ok {
				return true
			}
		case "boolean":
			if _, ok := v.(bool); ok {
				return true
			}
		case "array":
			if _, ok := v.([]any); ok {
				return true
			}
		case "object":
			if _, ok := v.(map[string]any); ok {
				return true
			}
		}
	}
	return false
}

func schemaPropertyIncludesType(prop toolSchemaProperty, want string) bool {
	for _, typ := range schemaPropertyTypes(prop) {
		if typ == want {
			return true
		}
	}
	return false
}

func schemaPropertyAlias(key string, props map[string]toolSchemaProperty, required []string) (string, bool) {
	norm := normalizeToolIdentifier(key)
	for name := range props {
		if normalizeToolIdentifier(name) == norm {
			return name, true
		}
	}
	var candidates []string
	for name, aliases := range commonSchemaFieldAliases {
		if _, ok := props[name]; !ok {
			continue
		}
		for _, alias := range aliases {
			if normalizeToolIdentifier(alias) == norm {
				candidates = append(candidates, name)
				break
			}
		}
	}
	if len(candidates) == 0 {
		return "", false
	}
	sort.Strings(candidates)
	candidates = compactStrings(candidates)
	if len(candidates) == 1 {
		return candidates[0], true
	}
	if requiredAlias, ok := uniqueRequiredCandidate(candidates, required); ok {
		return requiredAlias, true
	}
	return "", false
}

func compactStrings(in []string) []string {
	if len(in) < 2 {
		return in
	}
	out := in[:1]
	for _, s := range in[1:] {
		if s != out[len(out)-1] {
			out = append(out, s)
		}
	}
	return out
}

func uniqueRequiredCandidate(candidates []string, required []string) (string, bool) {
	requiredSet := map[string]bool{}
	for _, req := range required {
		requiredSet[req] = true
	}
	found := ""
	for _, candidate := range candidates {
		if !requiredSet[candidate] {
			continue
		}
		if found != "" {
			return "", false
		}
		found = candidate
	}
	return found, found != ""
}

var commonSchemaFieldAliases = map[string][]string{
	"path":            {"file", "filename", "file_name", "file_path", "filepath"},
	"command":         {"cmd", "shell_command"},
	"cwd":             {"dir", "directory", "working_dir", "workdir"},
	"timeout_sec":     {"timeout", "timeout_seconds", "timeoutSeconds"},
	"max_bytes":       {"maxBytes", "bytes", "limit"},
	"max_entries":     {"maxEntries", "limit"},
	"old":             {"old_text", "oldText", "old_string", "find", "search"},
	"new":             {"new_text", "newText", "new_string", "replacement", "replace"},
	"replace_all":     {"replaceAll", "all"},
	"content":         {"body", "text"},
	"query":           {"q", "search", "keywords"},
	"url":             {"uri", "link", "href"},
	"ref":             {"ref_id", "refId", "element", "element_id", "elementId", "element_ref", "elementRef", "id"},
	"text":            {"input", "input_text", "inputText", "value"},
	"value":           {"expected_text", "expectedText", "text"},
	"direction":       {"dir"},
	"amount":          {"pixels", "delta", "scroll_amount", "scrollAmount"},
	"wait_until":      {"waitUntil", "wait_for", "waitFor"},
	"timeout_ms":      {"timeout", "timeoutMS", "timeoutMs", "timeout_millis", "timeoutMilliseconds"},
	"num_results":     {"n", "count", "limit", "max_results", "maxResults"},
	"top_k":           {"topK", "limit"},
	"max_per_session": {"maxPerSession", "per_session"},
	"save_path":       {"savePath", "path", "file", "filename", "file_path", "filepath"},
	"full_page":       {"fullPage", "full", "capture_full_page", "captureFullPage"},
	"task":            {"prompt", "instruction", "instructions", "query"},
	"mode":            {"type", "kind"},
	"max_turns":       {"maxTurns", "turns", "limit", "budget"},
}

func shouldDropUnknownSchemaField(s toolSchema) bool {
	if len(s.Properties) == 0 {
		return false
	}
	if b, ok := s.AdditionalProperties.(bool); ok && b {
		return false
	}
	return true
}

func coerceSchemaValue(v any, prop toolSchemaProperty) (any, bool) {
	coerced, ok, _ := coerceSchemaValueWithKind(v, prop)
	return coerced, ok
}

func coerceSchemaValueWithKind(v any, prop toolSchemaProperty) (any, bool, string) {
	if coerced, ok := coerceEnumValue(v, prop); ok {
		if schemaValueWithinBounds(coerced, prop) {
			return coerced, true, "enum"
		}
	}
	types := schemaPropertyTypes(prop)
	if schemaValueMatchesTypes(v, types) {
		return v, false, ""
	}
	for _, typ := range types {
		switch typ {
		case "integer":
			switch x := v.(type) {
			case string:
				if n, ok := parseIntegerString(x); ok {
					if schemaValueWithinBounds(n, prop) {
						return n, true, "type"
					}
				}
			case float64:
				if isIntegralFloat64(x) {
					n := int(x)
					if schemaValueWithinBounds(n, prop) {
						return n, true, "type"
					}
				}
			}
		case "number":
			switch x := v.(type) {
			case string:
				if n, ok := parseNumberString(x); ok {
					if schemaValueWithinBounds(n, prop) {
						return n, true, "type"
					}
				}
			}
		case "boolean":
			switch x := v.(type) {
			case string:
				b, err := strconv.ParseBool(strings.TrimSpace(x))
				if err == nil {
					return b, true, "type"
				}
			case float64:
				if x == 1 {
					return true, true, "type"
				}
				if x == 0 {
					return false, true, "type"
				}
			}
		case "array":
			if x, ok := v.(string); ok {
				item := strings.TrimSpace(x)
				if item != "" && schemaArrayCanAcceptScalarItem(prop) {
					return []any{item}, true, "type"
				}
			}
		case "string":
			switch x := v.(type) {
			case float64:
				s := strconv.FormatFloat(x, 'f', -1, 64)
				if schemaValueWithinBounds(s, prop) {
					return s, true, "type"
				}
			case bool:
				s := strconv.FormatBool(x)
				if schemaValueWithinBounds(s, prop) {
					return s, true, "type"
				}
			}
		}
	}
	return v, false, ""
}

func schemaArrayCanAcceptScalarItem(prop toolSchemaProperty) bool {
	if prop.Items == nil {
		return true
	}
	itemTypes := schemaPropertyTypes(toolSchemaProperty{Type: prop.Items.Type})
	if len(itemTypes) == 0 {
		return true
	}
	for _, typ := range itemTypes {
		if typ == "string" {
			return true
		}
	}
	return false
}

func parseIntegerString(s string) (int, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	if n, err := strconv.Atoi(s); err == nil {
		return n, true
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || !isIntegralFloat64(f) {
		return 0, false
	}
	return int(f), true
}

func parseNumberString(s string) (float64, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
}

func isIntegralFloat64(f float64) bool {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return false
	}
	const maxExactIntegerFloat64 = 1<<53 - 1
	if f < -maxExactIntegerFloat64 || f > maxExactIntegerFloat64 {
		return false
	}
	if f < float64(math.MinInt) || f > float64(math.MaxInt) {
		return false
	}
	return f == math.Trunc(f)
}

func coerceEnumValue(v any, prop toolSchemaProperty) (any, bool) {
	if len(prop.Enum) == 0 {
		return v, false
	}
	s, ok := v.(string)
	if !ok {
		return v, false
	}
	trimmed := strings.TrimSpace(s)
	var relaxedMatches []string
	for _, enumValue := range prop.Enum {
		candidate, ok := enumValue.(string)
		if !ok {
			continue
		}
		if trimmed == candidate {
			if s == candidate {
				return v, false
			}
			return candidate, true
		}
		if strings.EqualFold(trimmed, candidate) || normalizeToolIdentifier(trimmed) == normalizeToolIdentifier(candidate) {
			relaxedMatches = append(relaxedMatches, candidate)
		}
	}
	sort.Strings(relaxedMatches)
	relaxedMatches = compactStrings(relaxedMatches)
	if len(relaxedMatches) == 1 {
		return relaxedMatches[0], true
	}
	return v, false
}

func schemaPropertyTypes(prop toolSchemaProperty) []string {
	switch t := prop.Type.(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	default:
		return nil
	}
}

func recoverRequiredSchemaValue(name string, obj map[string]any, props map[string]toolSchemaProperty, required []string) (any, bool) {
	prop, ok := props[name]
	if !ok {
		return nil, false
	}
	aliases := commonSchemaFieldAliases[name]
	for _, alias := range aliases {
		if v, ok := obj[alias]; ok {
			target, ok := schemaPropertyAlias(alias, props, required)
			if !ok || target != name {
				continue
			}
			if !schemaValueMatchesOrCanCoerce(v, prop) {
				continue
			}
			return coerceSchemaValueOrOriginal(v, prop), true
		}
	}
	return nil, false
}

func coerceSchemaValueOrOriginal(v any, prop toolSchemaProperty) any {
	if coerced, ok := coerceSchemaValue(v, prop); ok {
		return coerced
	}
	return v
}

func toolErrorHelp(t *Tool, args json.RawMessage) string {
	if t == nil {
		return ""
	}
	required, fields := summarizeToolSchema(t.Schema)
	if len(required) == 0 && len(fields) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString("\nExpected: ")
	if len(required) > 0 {
		b.WriteString("required ")
		b.WriteString(strings.Join(required, ", "))
	} else {
		b.WriteString("no required fields")
	}
	if len(fields) > 0 {
		b.WriteString("\nAllowed: ")
		b.WriteString(strings.Join(fields, ", "))
	}
	if example := toolArgsExample(t.Schema); example != "" {
		b.WriteString("\nExample: ")
		b.WriteString(example)
	}
	if len(args) > 0 {
		b.WriteString("\nReceived: ")
		b.WriteString(previewN(string(args), 240))
	}
	b.WriteString("\nNext: fix the arguments and retry once, or use a different tool if the same error repeats.")
	return b.String()
}

func summarizeToolSchema(schema json.RawMessage) ([]string, []string) {
	var s toolSchema
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil {
		return nil, nil
	}
	required := append([]string{}, s.Required...)
	sort.Strings(required)
	fields := make([]string, 0, len(s.Properties))
	for name, prop := range s.Properties {
		fields = append(fields, schemaFieldSummary(name, prop))
	}
	sort.Strings(fields)
	return required, fields
}

func schemaFieldSummary(name string, prop toolSchemaProperty) string {
	var parts []string
	if typ := schemaTypeSummary(prop); typ != "" {
		parts = append(parts, "type="+typ)
	}
	if itemTyp := schemaArrayItemTypeSummary(prop); itemTyp != "" {
		parts = append(parts, "items="+itemTyp)
	}
	if enum := stringEnumSummary(prop.Enum); enum != "" {
		parts = append(parts, "enum="+enum)
	}
	if min := schemaLiteral(prop.Minimum); min != "" {
		parts = append(parts, "min="+min)
	}
	if max := schemaLiteral(prop.Maximum); max != "" {
		parts = append(parts, "max="+max)
	}
	if prop.MinLength > 0 {
		parts = append(parts, "minLength="+strconv.Itoa(prop.MinLength))
	}
	if prop.MaxLength > 0 {
		parts = append(parts, "maxLength="+strconv.Itoa(prop.MaxLength))
	}
	if def := schemaLiteral(prop.Default); def != "" {
		parts = append(parts, "default="+def)
	}
	if len(parts) == 0 {
		return name
	}
	return name + " (" + strings.Join(parts, ", ") + ")"
}

func schemaTypeSummary(prop toolSchemaProperty) string {
	types := schemaPropertyTypes(prop)
	if len(types) == 0 {
		return ""
	}
	return strings.Join(types, "|")
}

func schemaArrayItemTypeSummary(prop toolSchemaProperty) string {
	if prop.Items == nil || !schemaPropertyIncludesType(prop, "array") {
		return ""
	}
	types := schemaPropertyTypes(toolSchemaProperty{Type: prop.Items.Type})
	if len(types) == 0 {
		return ""
	}
	return strings.Join(types, "|")
}

func stringEnumSummary(values []any) string {
	if len(values) == 0 {
		return ""
	}
	out := make([]string, 0, len(values))
	for _, v := range values {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	if len(out) == 0 {
		return ""
	}
	sort.Strings(out)
	return strings.Join(out, "|")
}

func schemaLiteral(v any) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		raw, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(raw)
	}
}

func toolArgsExample(schema json.RawMessage) string {
	var s toolSchema
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return ""
	}
	ex := map[string]any{}
	if len(s.Required) > 0 {
		for _, name := range s.Required {
			ex[name] = exampleValueForProperty(name, s.Properties[name])
		}
	} else {
		names := make([]string, 0, len(s.Properties))
		for name := range s.Properties {
			names = append(names, name)
		}
		sort.Strings(names)
		for i, name := range names {
			if i >= 2 {
				break
			}
			ex[name] = exampleValueForProperty(name, s.Properties[name])
		}
	}
	raw, err := json.Marshal(ex)
	if err != nil {
		return ""
	}
	var compact bytes.Buffer
	if json.Compact(&compact, raw) != nil {
		return string(raw)
	}
	return compact.String()
}

var typeExampleValues = map[string]any{
	"integer": 1,
	"boolean": false,
	"array":   []any{},
	"object":  map[string]any{},
}

var propertyNameExamples = map[string]string{
	"path":    "relative/path.txt",
	"command": "go test ./...",
	"query":   "keywords",
}

func exampleValueForProperty(name string, prop toolSchemaProperty) any {
	if len(prop.Enum) > 0 {
		return prop.Enum[0]
	}
	for _, typ := range schemaPropertyTypes(prop) {
		if v, ok := typeExampleValues[typ]; ok {
			return v
		}
	}
	if v, ok := propertyNameExamples[name]; ok {
		return v
	}
	return "value"
}

func formatRepairDebug(tool string, canonicalChanged, argsChanged bool) string {
	switch {
	case canonicalChanged && argsChanged:
		return fmt.Sprintf("canonicalized tool name and repaired args for %s", tool)
	case canonicalChanged:
		return fmt.Sprintf("canonicalized tool name to %s", tool)
	case argsChanged:
		return fmt.Sprintf("repaired args for %s", tool)
	default:
		return ""
	}
}
