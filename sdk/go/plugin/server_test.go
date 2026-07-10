package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestManifestAndSuccessfulAction(t *testing.T) {
	server, err := NewServer("echo-go", "1.0.0", []Action{{
		Name: "echo", Description: "Echoes input", Permissions: []string{},
		Handler: func(_ context.Context, request Request) (any, error) {
			var input any
			if err := json.Unmarshal(request.Input, &input); err != nil {
				return nil, err
			}
			return map[string]any{"echo": input}, nil
		},
	}}, WithBearerToken("secret-token"))
	if err != nil {
		t.Fatal(err)
	}

	manifestRequest := httptest.NewRequest(http.MethodGet, "/stormrelay/plugin/v1/manifest", nil)
	manifestRequest.Header.Set("Authorization", "Bearer secret-token")
	manifestResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(manifestResponse, manifestRequest)
	if manifestResponse.Code != http.StatusOK {
		t.Fatalf("manifest status=%d body=%s", manifestResponse.Code, manifestResponse.Body)
	}
	var manifest Manifest
	if err := json.Unmarshal(manifestResponse.Body.Bytes(), &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.PluginID != "echo-go" || manifest.ProtocolVersion != ProtocolVersion || len(manifest.Actions) != 1 {
		t.Fatalf("manifest=%+v", manifest)
	}

	request := validRequest()
	request.Input = json.RawMessage(`{"hello":"world"}`)
	body, _ := json.Marshal(request)
	actionRequest := httptest.NewRequest(http.MethodPost, "/stormrelay/plugin/v1/actions/echo", bytes.NewReader(body))
	actionRequest.Header.Set("Authorization", "Bearer secret-token")
	actionRequest.Header.Set("Content-Type", "application/json")
	actionResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(actionResponse, actionRequest)
	if actionResponse.Code != http.StatusOK {
		t.Fatalf("action status=%d body=%s", actionResponse.Code, actionResponse.Body)
	}
	var response Response
	if err := json.Unmarshal(actionResponse.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response.Status != "succeeded" || response.IdempotencyKey != request.IdempotencyKey || !bytes.Contains(response.Output, []byte("world")) {
		t.Fatalf("response=%+v", response)
	}
}

func TestAuthenticationAndStrictJSON(t *testing.T) {
	server := mustServer(t, func(context.Context, Request) (any, error) { return map[string]any{}, nil }, WithBearerToken("correct"))

	unauthorized := httptest.NewRecorder()
	server.Handler().ServeHTTP(unauthorized, httptest.NewRequest(http.MethodGet, "/stormrelay/plugin/v1/manifest", nil))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d", unauthorized.Code)
	}

	body := []byte(`{"protocol_version":"stormrelay.plugin/v1","execution_id":"e","step_id":"s","request_id":"r","deadline":"2099-01-01T00:00:00Z","idempotency_key":"k","input":{},"unexpected":true}`)
	request := httptest.NewRequest(http.MethodPost, "/stormrelay/plugin/v1/actions/test", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer correct")
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("status=%d body=%s", response.Code, response.Body)
	}
}

func TestDeadlineAndFailureSanitization(t *testing.T) {
	server := mustServer(t, func(context.Context, Request) (any, error) { return nil, errors.New("secret backend detail") })
	server.now = func() time.Time { return time.Unix(100, 0).UTC() }

	expired := validRequest()
	expired.Deadline = time.Unix(99, 0).UTC()
	expiredBody, _ := json.Marshal(expired)
	expiredRequest := httptest.NewRequest(http.MethodPost, "/stormrelay/plugin/v1/actions/test", bytes.NewReader(expiredBody))
	expiredRequest.Header.Set("Content-Type", "application/json")
	expiredResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(expiredResponse, expiredRequest)
	if expiredResponse.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status=%d body=%s", expiredResponse.Code, expiredResponse.Body)
	}

	server.now = time.Now
	requestBody, _ := json.Marshal(validRequest())
	request := httptest.NewRequest(http.MethodPost, "/stormrelay/plugin/v1/actions/test", bytes.NewReader(requestBody))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	var result Response
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Status != "failed" || result.Error != "plugin action failed" || strings.Contains(result.Error, "secret") {
		t.Fatalf("result=%+v", result)
	}
}

func TestExplicitActionFailureAndOutputLimit(t *testing.T) {
	failure := mustServer(t, func(context.Context, Request) (any, error) { return nil, Fail("safe operator message") })
	result := callAction(t, failure, validRequest())
	if result.Status != "failed" || result.Error != "safe operator message" {
		t.Fatalf("result=%+v", result)
	}

	large := mustServer(t, func(context.Context, Request) (any, error) { return strings.Repeat("x", MaxResponseBytes+1), nil })
	result = callAction(t, large, validRequest())
	if result.Status != "failed" || result.Error == "" {
		t.Fatalf("result=%+v", result)
	}
}

func TestManifestValidation(t *testing.T) {
	if _, err := NewServer("Bad ID", "1", []Action{{Name: "test", Handler: func(context.Context, Request) (any, error) { return nil, nil }}}); err == nil {
		t.Fatal("expected invalid plugin id")
	}
	if _, err := NewServer("valid", "1", []Action{{Name: "dup", Handler: func(context.Context, Request) (any, error) { return nil, nil }}, {Name: "dup", Handler: func(context.Context, Request) (any, error) { return nil, nil }}}); err == nil {
		t.Fatal("expected duplicate action")
	}
}

func validRequest() Request {
	return Request{
		ProtocolVersion: ProtocolVersion,
		ExecutionID: "execution", StepID: "step", RequestID: "request",
		Deadline: time.Now().Add(time.Hour).UTC(), IdempotencyKey: "idempotency",
		Input: json.RawMessage(`{}`),
	}
}

func mustServer(t *testing.T, handler Handler, options ...ServerOption) *Server {
	t.Helper()
	server, err := NewServer("test-plugin", "1.0.0", []Action{{Name: "test", Handler: handler}}, options...)
	if err != nil {
		t.Fatal(err)
	}
	return server
}

func callAction(t *testing.T, server *Server, request Request) Response {
	t.Helper()
	body, _ := json.Marshal(request)
	httpRequest := httptest.NewRequest(http.MethodPost, "/stormrelay/plugin/v1/actions/test", bytes.NewReader(body))
	httpRequest.Header.Set("Content-Type", "application/json")
	httpResponse := httptest.NewRecorder()
	server.Handler().ServeHTTP(httpResponse, httpRequest)
	var result Response
	if err := json.Unmarshal(httpResponse.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}
