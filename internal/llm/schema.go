package llm

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ValidateJSONSchema checks that text is JSON conforming to a minimal subset of
// JSON Schema: type, required, properties (object), items (array). No new deps.
func ValidateJSONSchema(schema json.RawMessage, text string) error {
	text = strings.TrimSpace(text)
	if text == "" {
		return fmt.Errorf("empty response")
	}
	// Strip optional markdown fences some models emit.
	if strings.HasPrefix(text, "```") {
		text = stripFence(text)
	}
	var schemaObj any
	if err := json.Unmarshal(schema, &schemaObj); err != nil {
		return fmt.Errorf("invalid json schema: %w", err)
	}
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		return fmt.Errorf("response is not json: %w", err)
	}
	return validateNode(schemaObj, value, "$")
}

func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.LastIndex(s, "```"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

func validateNode(schema any, value any, path string) error {
	sm, ok := schema.(map[string]any)
	if !ok {
		return nil
	}
	if t, ok := sm["type"].(string); ok {
		if err := checkType(t, value, path); err != nil {
			return err
		}
	}
	switch value := value.(type) {
	case map[string]any:
		if req, ok := sm["required"].([]any); ok {
			for _, r := range req {
				key, _ := r.(string)
				if key == "" {
					continue
				}
				if _, exists := value[key]; !exists {
					return fmt.Errorf("%s: missing required property %q", path, key)
				}
			}
		}
		if props, ok := sm["properties"].(map[string]any); ok {
			for k, child := range props {
				if v, exists := value[k]; exists {
					if err := validateNode(child, v, path+"."+k); err != nil {
						return err
					}
				}
			}
		}
	case []any:
		if items, ok := sm["items"]; ok {
			for i, el := range value {
				if err := validateNode(items, el, fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func checkType(want string, value any, path string) error {
	switch want {
	case "object":
		if _, ok := value.(map[string]any); !ok {
			return fmt.Errorf("%s: want object", path)
		}
	case "array":
		if _, ok := value.([]any); !ok {
			return fmt.Errorf("%s: want array", path)
		}
	case "string":
		if _, ok := value.(string); !ok {
			return fmt.Errorf("%s: want string", path)
		}
	case "number":
		switch value.(type) {
		case float64, json.Number:
		default:
			return fmt.Errorf("%s: want number", path)
		}
	case "integer":
		switch v := value.(type) {
		case float64:
			if v != float64(int64(v)) {
				return fmt.Errorf("%s: want integer", path)
			}
		case json.Number:
		default:
			return fmt.Errorf("%s: want integer", path)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return fmt.Errorf("%s: want boolean", path)
		}
	case "null":
		if value != nil {
			return fmt.Errorf("%s: want null", path)
		}
	}
	return nil
}
