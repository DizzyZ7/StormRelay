package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DizzyZ7/StormRelay/internal/config"
)

func TestDecodeJSONAcceptsSingleBoundedDocument(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{name: "single object", body: " \n{\"value\":\"ok\"}\t "},
		{name: "exact size limit", body: "{\"value\":\"" + strings.Repeat("x", int(maxControlJSONBytes)-len("{\"value\":\"\"}")) + "\"}"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/example", strings.NewReader(tc.body))
			recorder := httptest.NewRecorder()
			var value struct{ Value string }
			if !decodeJSON(recorder, req, &value) {
				t.Fatalf("valid JSON rejected: status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if value.Value == "" {
				t.Fatal("decoded value is empty")
			}
			if recorder.Body.Len() > 0 {
				t.Fatalf("successful decode wrote response: %s", recorder.Body.String())
			}
		})
	}
}

func TestDecodeJSONRejectsInvalidAndOversizedDocuments(t *testing.T) {
	oversized := "{\"value\":\"" + strings.Repeat("x", int(maxControlJSONBytes)) + "\"}"
	tests := []struct {
		name          string
		body          string
		unknownLength bool
		wantCode      int
		wantError     string
	}{
		{name: "empty", body: "", wantCode: http.StatusBadRequest, wantError: "invalid_json"},
		{name: "null", body: " \n null \n ", wantCode: http.StatusBadRequest, wantError: "invalid_json"},
		{name: "syntax error", body: "{\"value\":", wantCode: http.StatusBadRequest, wantError: "invalid_json"},
		{name: "unknown field", body: "{\"value\":\"ok\",\"unexpected\":true}", wantCode: http.StatusBadRequest, wantError: "invalid_json"},
		{name: "two JSON objects", body: "{\"value\":\"ok\"}{\"value\":\"other\"}", wantCode: http.StatusBadRequest, wantError: "invalid_json"},
		{name: "trailing garbage", body: "{\"value\":\"ok\"} not-json", wantCode: http.StatusBadRequest, wantError: "invalid_json"},
		{name: "known size over limit", body: oversized, wantCode: http.StatusRequestEntityTooLarge, wantError: "payload_too_large"},
		{name: "chunked size over limit", body: oversized, unknownLength: true, wantCode: http.StatusRequestEntityTooLarge, wantError: "payload_too_large"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/api/v1/example", strings.NewReader(tc.body))
			if tc.unknownLength {
				req.ContentLength = -1
			}
			recorder := httptest.NewRecorder()
			var value struct{ Value string }
			if decodeJSON(recorder, req, &value) {
				t.Fatal("invalid JSON request was accepted")
			}
			if recorder.Code != tc.wantCode {
				t.Fatalf("status=%d, want %d: %s", recorder.Code, tc.wantCode, recorder.Body.String())
			}
			var out struct {
				Error struct {
					Code string
				}
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &out); err != nil {
				t.Fatalf("invalid error envelope: %v", err)
			}
			if out.Error.Code != tc.wantError {
				t.Fatalf("error code=%q want %q", out.Error.Code, tc.wantError)
			}
		})
	}
}

func TestMiddlewareBoundsUntrustedRequestID(t *testing.T) {
	s := New(config.Config{BootstrapAPIKey: "request-id-test"}, nil, nil, nil, slog.Default())
	valid := strings.Repeat("v", maxClientRequestIDBytes)
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "valid UUID", input: "1d2b3a4f-91e0-4c18-9ff8-37c1dfea1200", want: "1d2b3a4f-91e0-4c18-9ff8-37c1dfea1200"},
		{name: "valid at bound", input: valid, want: valid},
		{name: "trim surrounding space", input: " trace-id:45 ", want: "trace-id:45"},
		{name: "empty", input: ""},
		{name: "too long", input: strings.Repeat("a", maxClientRequestIDBytes+1)},
		{name: "unicode", input: "trace-☃"},
		{name: "control byte", input: "trace-\x01-id"},
		{name: "JSON/log separator", input: "trace\"bad"},
		{name: "HTML", input: "<script>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
			if tc.input != "" {
				req.Header.Set("X-Request-ID", tc.input)
			}
			recorder := httptest.NewRecorder()
			s.Handler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("health response=%d: %s", recorder.Code, recorder.Body.String())
			}
			got := recorder.Header().Get("X-Request-ID")
			if tc.want != "" {
				if got != tc.want {
					t.Fatalf("request ID=%q want %q", got, tc.want)
				}
			} else if got == tc.input || len(got) != 36 || boundedRequestID(got) != got {
				t.Fatalf("invalid client request ID was reflected: input=%q output=%q", tc.input, got)
			}
		})
	}
}
