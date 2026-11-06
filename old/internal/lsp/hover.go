package lsp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizeHover flattens the LSP Hover.contents union (MarkupContent, a plain
// string, a MarkedString or a MarkedString[]) into one plain text block. Null
// or empty hovers normalize to "".
func NormalizeHover(raw json.RawMessage) (string, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", nil
	}
	var value any
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", fmt.Errorf("invalid hover contents: %w", err)
	}
	return normalizeHoverValue(value)
}

// normalizeHoverValue handles one level of the LSP hover contents union.
func normalizeHoverValue(value any) (string, error) {
	switch typed := value.(type) {
	case nil:
		return "", nil
	case string:
		return strings.TrimSpace(typed), nil
	case map[string]any:
		if text, ok := typed["value"].(string); ok {
			return strings.TrimSpace(text), nil
		}
		return "", fmt.Errorf("unsupported hover contents object")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, entry := range typed {
			text, err := normalizeHoverValue(entry)
			if err != nil {
				return "", err
			}
			if text != "" {
				parts = append(parts, text)
			}
		}
		return strings.TrimSpace(strings.Join(parts, "\n\n")), nil
	default:
		return "", fmt.Errorf("unsupported hover contents type %T", value)
	}
}
