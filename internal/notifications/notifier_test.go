package notifications

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/DizzyZ7/StormRelay/internal/storage"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return fn(request)
}

func telegramDelivery() storage.Delivery {
	return storage.Delivery{
		ID:         "delivery-1",
		IncidentID: "incident-1",
		Kind:       "telegram",
		Config:     []byte(`{"chat_id":"12345"}`),
		Payload:    []byte(`{"title":"Database latency","severity":"critical","state":"detected","ack_url":"https://stormrelay.example/ack/redacted"}`),
	}
}

func TestTelegramProviderHTTP500IsRetryableFailure(t *testing.T) {
	const token = "provider-secret-token"
	notifier := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), token)
	notifier.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if !strings.Contains(request.URL.Path, "/bot"+token+"/sendMessage") {
			t.Fatalf("unexpected Telegram request path %q", request.URL.Path)
		}
		return &http.Response{
			StatusCode: http.StatusInternalServerError,
			Body:       io.NopCloser(strings.NewReader(`{"ok":false}`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	_, err := notifier.Deliver(context.Background(), telegramDelivery())
	if err == nil || !strings.Contains(err.Error(), "telegram returned HTTP 500") {
		t.Fatalf("error=%v, want provider HTTP 500", err)
	}
	if strings.Contains(err.Error(), token) {
		t.Fatal("provider token leaked into delivery error")
	}
}

func TestTelegramMalformedSuccessResponseIsFailure(t *testing.T) {
	notifier := New(slog.New(slog.NewJSONHandler(io.Discard, nil)), "provider-secret-token")
	notifier.client = &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       io.NopCloser(strings.NewReader(`{"ok":`)),
			Header:     make(http.Header),
			Request:    request,
		}, nil
	})}

	_, err := notifier.Deliver(context.Background(), telegramDelivery())
	if err == nil || !strings.Contains(err.Error(), "telegram returned malformed response") {
		t.Fatalf("error=%v, want malformed provider response", err)
	}
}
