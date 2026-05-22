package agent

import (
	"bytes"
	"encoding/json"
	"fmt"
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
	Type any   `json:"type"`
	Enum []any `json:"enum"`
}

func repairToolArgsWithSchema(args json.RawMessage, schema json.RawMessage) (json.RawMessage, bool, []string) {
	var s toolSchema
	if len(schema) == 0 || json.Unmarshal(schema, &s) != nil || len(s.Properties) == 0 {
		return args, false, nil
	}
	var obj map[string]any
	if json.Unmarshal(args, &obj) != nil {
		return args, false, nil
	}

	changed := false
	var notes []string
	out := make(map[string]any, len(obj))
	used := map[string]bool{}
	for key, value := range obj {
		prop, ok := s.Properties[key]
		target := key
		if !ok {
			if alias, found := schemaPropertyAlias(key, s.Properties); found {
				target = alias
				prop = s.Properties[alias]
				ok = true
				changed = true
				notes = append(notes, fmt.Sprintf("renamed field %s to %s", key, target))
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
		coerced, didCoerce := coerceSchemaValue(value, prop)
		if didCoerce {
			changed = true
			notes = append(notes, fmt.Sprintf("coerced field %s to %s", target, strings.Join(schemaPropertyTypes(prop), "|")))
		}
		out[target] = coerced
		used[target] = true
	}

	for _, req := range s.Required {
		if used[req] || out[req] != nil {
			continue
		}
		if value, ok := recoverRequiredSchemaValue(req, obj, s.Properties[req]); ok {
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

func schemaPropertyAlias(key string, props map[string]toolSchemaProperty) (string, bool) {
	norm := normalizeToolIdentifier(key)
	for name := range props {
		if normalizeToolIdentifier(name) == norm {
			return name, true
		}
	}
	for name, aliases := range commonSchemaFieldAliases {
		for _, alias := range aliases {
			if normalizeToolIdentifier(alias) == norm {
				if _, ok := props[name]; ok {
					return name, true
				}
			}
		}
	}
	return "", false
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
	"top_k":           {"topK", "limit"},
	"max_per_session": {"maxPerSession", "per_session"},
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
	types := schemaPropertyTypes(prop)
	for _, typ := range types {
		switch typ {
		case "integer":
			switch x := v.(type) {
			case string:
				n, err := strconv.Atoi(strings.TrimSpace(x))
				if err == nil {
					return n, true
				}
			case float64:
				if x == float64(int(x)) {
					return int(x), true
				}
			}
		case "boolean":
			if s, ok := v.(string); ok {
				b, err := strconv.ParseBool(strings.TrimSpace(s))
				if err == nil {
					return b, true
				}
			}
		case "string":
			switch x := v.(type) {
			case float64:
				return strconv.FormatFloat(x, 'f', -1, 64), true
			case bool:
				return strconv.FormatBool(x), true
			}
		}
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

func recoverRequiredSchemaValue(name string, obj map[string]any, prop toolSchemaProperty) (any, bool) {
	aliases := commonSchemaFieldAliases[name]
	for _, alias := range aliases {
		if v, ok := obj[alias]; ok {
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
	for name := range s.Properties {
		fields = append(fields, name)
	}
	sort.Strings(fields)
	return required, fields
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

func exampleValueForProperty(name string, prop toolSchemaProperty) any {
	if len(prop.Enum) > 0 {
		return prop.Enum[0]
	}
	for _, typ := range schemaPropertyTypes(prop) {
		switch typ {
		case "integer":
			return 1
		case "boolean":
			return false
		case "array":
			return []any{}
		case "object":
			return map[string]any{}
		}
	}
	switch name {
	case "path":
		return "relative/path.txt"
	case "command":
		return "go test ./..."
	case "query":
		return "keywords"
	default:
		return "value"
	}
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
