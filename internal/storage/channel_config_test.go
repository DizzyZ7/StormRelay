package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestNormalizeChannelConfig(t *testing.T) {
	tests := []struct {
		name string
		kind string
		raw  string
		want string
		bad  bool
	}{
		{name: "default mock", kind: "mock", raw: "", want: "{}"},
		{name: "empty mock", kind: "mock", raw: "{}", want: "{}"},
		{name: "mock rejects arbitrary fields", kind: "mock", raw: `{"api_token":"secret"}`, bad: true},
		{name: "telegram canonicalization", kind: "telegram", raw: `{"chat_id":"  -123456  ","disable_preview":true}`, want: `{"chat_id":"-123456","disable_preview":true}`},
		{name: "telegram minimum", kind: "telegram", raw: `{"chat_id":"@channel"}`, want: `{"chat_id":"@channel"}`},
		{name: "telegram unknown secret", kind: "telegram", raw: `{"chat_id":"123","bot_token":"secret"}`, bad: true},
		{name: "telegram null flag", kind: "telegram", raw: `{"chat_id":"123","disable_preview":null}`, bad: true},
		{name: "telegram wrong flag type", kind: "telegram", raw: `{"chat_id":"123","disable_preview":"true"}`, bad: true},
		{name: "telegram missing chat", kind: "telegram", raw: `{"disable_preview":true}`, bad: true},
		{name: "telegram excessive chat", kind: "telegram", raw: `{"chat_id":"` + strings.Repeat("x", 129) + `"}`, bad: true},
		{name: "non-object", kind: "telegram", raw: "[]", bad: true},
		{name: "invalid JSON", kind: "telegram", raw: "{", bad: true},
		{name: "unsupported kind", kind: "webhook", raw: "{}", bad: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeChannelConfig(tc.kind, json.RawMessage(tc.raw))
			if tc.bad {
				if !errors.Is(err, ErrInvalidCreateInput) {
					t.Fatalf("invalid config error=%v; want ErrInvalidCreateInput", err)
				}
				if len(got) > 0 {
					t.Fatalf("invalid config returned normalized fields: %s", got)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Fatalf("normalized config=%s, want %s", got, tc.want)
			}
		})
	}
}
