package main

import (
	"encoding/json"
	"testing"
)

func TestDecodeInput(t *testing.T) {
	value, err := decodeInput(`{"count":9007199254740993,"enabled":true}`)
	if err != nil {
		t.Fatal(err)
	}
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("value=%T", value)
	}
	if number, ok := object["count"].(json.Number); !ok || number.String() != "9007199254740993" {
		t.Fatalf("count=%#v", object["count"])
	}
}

func TestDecodeInputRejectsTrailingAndInvalidJSON(t *testing.T) {
	for _, value := range []string{
		``,
		`{"ok":true} {"extra":true}`,
		`{"unterminated":`,
	} {
		if _, err := decodeInput(value); err == nil {
			t.Fatalf("accepted %q", value)
		}
	}
}
