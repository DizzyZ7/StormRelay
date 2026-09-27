package storage

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

// normalizeChannelConfig keeps the current supported channel types restricted
// to known provider fields, rather than persisting arbitrary secrets in JSONB.
func normalizeChannelConfig(kind string, config json.RawMessage) (json.RawMessage, error) {
	if len(bytes.TrimSpace(config)) == 0 {
		config = json.RawMessage(`{}`)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(config, &fields); err != nil || fields == nil {
		return nil, fmt.Errorf("%w: channel config must be a JSON object", ErrInvalidCreateInput)
	}
	switch kind {
	case "mock":
		if len(fields) != 0 {
			return nil, fmt.Errorf("%w: mock channel does not accept configuration fields", ErrInvalidCreateInput)
		}
		return json.RawMessage(`{}`), nil
	case "telegram":
		for field := range fields {
			if field != "chat_id" && field != "disable_preview" {
				return nil, fmt.Errorf("%w: unsupported telegram configuration field %q", ErrInvalidCreateInput, field)
			}
		}
		var parsed struct {
			ChatID         string `json:"chat_id"`
			DisablePreview bool   `json:"disable_preview,omitempty"`
		}
		if err := json.Unmarshal(config, &parsed); err != nil {
			return nil, fmt.Errorf("%w: malformed telegram configuration: %v", ErrInvalidCreateInput, err)
		}
		parsed.ChatID = strings.TrimSpace(parsed.ChatID)
		if parsed.ChatID == "" || len(parsed.ChatID) > 128 {
			return nil, fmt.Errorf("%w: telegram chat_id is required and limited to 128 bytes", ErrInvalidCreateInput)
		}
		if bytes.Equal(bytes.TrimSpace(fields["disable_preview"]), []byte("null")) {
			return nil, fmt.Errorf("%w: disable_preview must be a boolean", ErrInvalidCreateInput)
		}
		normalized, err := json.Marshal(parsed)
		if err != nil {
			return nil, err
		}
		return normalized, nil
	default:
		return nil, fmt.Errorf("%w: unsupported notification channel kind", ErrInvalidCreateInput)
	}
}
