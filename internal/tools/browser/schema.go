package browser

import (
	"strings"

	browserdomain "github.com/xymorphic/morph/internal/browser"
)

func inputSchema() map[string]any {
	actions := browserdomain.SupportedActions()
	actionNames := make([]string, 0, len(actions))
	properties := map[string]any{}
	for _, action := range actions {
		actionNames = append(actionNames, string(action))
		spec := requestSpecs[action]
		for _, field := range spec.allowed {
			if field == "action" {
				continue
			}
			if _, exists := properties[field]; exists {
				continue
			}
			properties[field] = schemaForField(field)
		}
	}
	properties["action"] = map[string]any{
		"type": "string", "enum": actionNames, "description": getActionFieldDescription(actions),
	}
	return map[string]any{
		"type": "object", "properties": properties, "required": []string{"action"}, "additionalProperties": false,
	}
}

func getActionFieldDescription(actions []browserdomain.Action) string {
	var description strings.Builder
	description.WriteString("Action fields, with optional fields marked by ?: ")
	for index, action := range actions {
		if index > 0 {
			description.WriteString("; ")
		}
		description.WriteString(string(action))
		description.WriteByte('[')
		spec := requestSpecs[action]
		required := make(map[string]struct{}, len(spec.required))
		for _, field := range spec.required {
			required[field] = struct{}{}
		}
		fieldIndex := 0
		for _, field := range spec.allowed {
			if field == "action" {
				continue
			}
			if fieldIndex > 0 {
				description.WriteString(", ")
			}
			description.WriteString(field)
			if _, ok := required[field]; !ok {
				description.WriteByte('?')
			}
			fieldIndex++
		}
		description.WriteByte(']')
	}
	description.WriteString(". Set unused fields to null.")
	return description.String()
}

func schemaForField(name string) map[string]any {
	switch name {
	case "replace", "full_page":
		return map[string]any{"type": "boolean"}
	case "limit", "x", "y", "timeout_ms":
		return map[string]any{"type": "integer"}
	default:
		return map[string]any{"type": "string"}
	}
}
