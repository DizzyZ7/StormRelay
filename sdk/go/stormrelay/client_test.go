package stormrelay

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClientSendsBearerAndDecodesVersion(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/version" {
			t.Fatalf("path=%q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer secret" {
			t.Fatalf("authorization=%q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"version":"dev","api":"v1","event_schema":"1.0"}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	version, err := client.Version(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version.API != "v1" || version.EventSchema != "1.0" {
		t.Fatalf("version=%+v", version)
	}
}

func TestClientDecodesErrorEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"error":{"code":"forbidden","message":"permission denied","request_id":"req-1","details":{"required_permission":"incidents:write"}}}`))
	}))
	defer server.Close()
	client, err := NewClient(server.URL, "secret")
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.GetIncident(context.Background(), "incident")
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("error=%T %v", err, err)
	}
	if apiErr.StatusCode != http.StatusForbidden || apiErr.Code != "forbidden" || apiErr.RequestID != "req-1" {
		t.Fatalf("api error=%+v", apiErr)
	}
}

func TestClientRejectsUnsafeConfiguration(t *testing.T) {
	for _, test := range []struct {
		url string
		key string
	}{
		{"localhost:8080", "secret"},
		{"ftp://example.com", "secret"},
		{"https://user:pass@example.com", "secret"},
		{"https://example.com", ""},
		{"https://example.com", "bad\nkey"},
	} {
		if _, err := NewClient(test.url, test.key); err == nil {
			t.Fatalf("expected rejection for url=%q", test.url)
		}
	}
}
