package api

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DizzyZ7/StormRelay/internal/config"
)

// A nil store ensures GET never reaches the state-changing token lookup.
func TestAcknowledgementGETIsReadOnly(t *testing.T) {
	s := New(config.Config{BootstrapAPIKey: "ack-test-key"}, nil, nil, nil, slog.Default())
	token := strings.Repeat("A", 32)
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			req := httptest.NewRequest(method, "/api/v1/ack/"+token, nil)
			recorder := httptest.NewRecorder()
			s.Handler().ServeHTTP(recorder, req)
			if recorder.Code != http.StatusOK {
				t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
			}
			if recorder.Header().Get("Referrer-Policy") != "no-referrer" {
				t.Fatal("confirmation page may leak the token via Referer")
			}
			if recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("acknowledgement token response must not be cached")
			}
			if recorder.Header().Get("X-Frame-Options") != "DENY" {
				t.Fatal("confirmation page must not be framed")
			}
			if !strings.Contains(recorder.Header().Get("Content-Security-Policy"), "form-action 'self'") {
				t.Fatal("confirmation form must only post to this origin")
			}
			if method == http.MethodGet {
				body := recorder.Body.String()
				if !strings.Contains(body, `method="post"`) || !strings.Contains(body, "/api/v1/ack/"+token) {
					t.Fatalf("GET did not show the expected POST-only confirmation form: %s", body)
				}
			}
		})
	}
}

func TestAcknowledgementGETRejectsShortTokens(t *testing.T) {
	s := New(config.Config{BootstrapAPIKey: "ack-test-key"}, nil, nil, nil, slog.Default())
	req := httptest.NewRequest(http.MethodGet, "/api/v1/ack/short", nil)
	recorder := httptest.NewRecorder()
	s.Handler().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("short-token status=%d, want 404", recorder.Code)
	}
}
