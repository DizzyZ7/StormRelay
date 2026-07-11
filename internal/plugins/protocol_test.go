package plugins

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
)

type testClientGuard struct {
	client *http.Client
}

func (g testClientGuard) Client(_ context.Context, rawURL string, timeout time.Duration) (*http.Client, *url.URL, error) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	client := *g.client
	client.Timeout = timeout
	return &client, parsed, nil
}

func actionRequest() ActionRequest {
	return ActionRequest{
		ProtocolVersion: ProtocolVersion,
		ExecutionID:     "execution-1",
		StepID:          "step-1",
		RequestID:       "request-1",
		Deadline:        time.Now().Add(time.Minute),
		IdempotencyKey:  "idempotency-1",
		Input:           []byte(`{"operation":"test"}`),
	}
}

func TestPluginCallTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"protocol_version":"stormrelay.plugin/v1","status":"succeeded","idempotency_key":"idempotency-1","output":{}}`)
	}))
	defer server.Close()

	client := &Client{guard: testClientGuard{client: server.Client()}}
	_, err := client.Call(context.Background(), server.URL, "echo", "", actionRequest(), 20*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "plugin action request failed") {
		t.Fatalf("error=%v, want bounded plugin request failure", err)
	}
}

func TestPluginCallRejectsMalformedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"protocol_version":`)
	}))
	defer server.Close()

	client := &Client{guard: testClientGuard{client: server.Client()}}
	_, err := client.Call(context.Background(), server.URL, "echo", "", actionRequest(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "malformed plugin response") {
		t.Fatalf("error=%v, want malformed plugin response", err)
	}
}

func TestPluginCallRejectsIdempotencyMismatch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"protocol_version":"stormrelay.plugin/v1","status":"succeeded","idempotency_key":"different","output":{}}`)
	}))
	defer server.Close()

	client := &Client{guard: testClientGuard{client: server.Client()}}
	_, err := client.Call(context.Background(), server.URL, "echo", "", actionRequest(), time.Second)
	if err == nil || !strings.Contains(err.Error(), "did not echo the idempotency key") {
		t.Fatalf("error=%v, want idempotency mismatch", err)
	}
}

func TestPluginCallPropagatesW3CContextInHeaderAndContract(t *testing.T) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithRemoteSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled, Remote: true,
	}))

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("traceparent")
		if !strings.HasPrefix(header, "00-4bf92f3577b34da6a3ce929d0e0e4736-") {
			t.Errorf("traceparent header=%q", header)
		}
		var request ActionRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		if request.TraceParent != header {
			t.Errorf("contract traceparent=%q header=%q", request.TraceParent, header)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"protocol_version":"stormrelay.plugin/v1","status":"succeeded","idempotency_key":"idempotency-1","output":{}}`)
	}))
	defer server.Close()

	client := &Client{guard: testClientGuard{client: server.Client()}}
	if _, err := client.Call(ctx, server.URL, "echo", "", actionRequest(), time.Second); err != nil {
		t.Fatal(err)
	}
}
