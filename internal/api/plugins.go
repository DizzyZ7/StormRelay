package api

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	pluginprotocol "github.com/DizzyZ7/StormRelay/internal/plugins"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

func (s *Server) listPlugins(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPlugins(r.Context(), tenantID(r))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) registerPlugin(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key            string          `json:"key"`
		Endpoint       string          `json:"endpoint"`
		AuthMode       string          `json:"auth_mode"`
		BearerToken    string          `json:"bearer_token"`
		TimeoutSeconds int             `json:"timeout_seconds"`
		RetryPolicy    json.RawMessage `json:"retry_policy"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Key) == "" {
		writeError(w, r, http.StatusBadRequest, "invalid_plugin", "plugin key is required", nil)
		return
	}
	manifest, err := s.pluginClient.Discover(r.Context(), in.Endpoint, in.BearerToken, 10*time.Second)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "plugin_discovery_failed", err.Error(), nil)
		return
	}
	out, err := s.store.RegisterPlugin(r.Context(), storage.RegisterPluginInput{TenantID: tenantID(r), PluginKey: in.Key, Endpoint: in.Endpoint, AuthMode: in.AuthMode, BearerToken: in.BearerToken, TimeoutSeconds: in.TimeoutSeconds, RetryPolicy: in.RetryPolicy, Manifest: manifest, ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_plugin", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) testPlugin(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/plugins/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "test" || r.Method != http.MethodPost {
		writeError(w, r, http.StatusNotFound, "not_found", "plugin route not found", nil)
		return
	}
	plugin, err := s.store.GetPlugin(r.Context(), tenantID(r), parts[0])
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	var body struct {
		Action string          `json:"action"`
		Input  json.RawMessage `json:"input"`
	}
	if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
		return
	}
	if body.Action == "" {
		var actions []pluginprotocol.Action
		if err := json.Unmarshal(plugin.Actions, &actions); err != nil || len(actions) == 0 {
			writeError(w, r, http.StatusConflict, "plugin_manifest_invalid", "plugin has no declared actions", nil)
			return
		}
		body.Action = actions[0].Name
	}
	if len(body.Input) == 0 {
		body.Input = json.RawMessage(`{}`)
	}
	plugin, bearer, err := s.store.PluginCredentialForAction(r.Context(), tenantID(r), parts[0], body.Action)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	response, err := s.pluginClient.Call(r.Context(), plugin.Endpoint, body.Action, bearer, pluginprotocol.ActionRequest{
		ProtocolVersion: pluginprotocol.ProtocolVersion,
		ExecutionID:     "plugin-test",
		StepID:          "plugin-test",
		RequestID:       telemetry.RequestID(r.Context()),
		Deadline:        time.Now().Add(time.Duration(plugin.TimeoutSeconds) * time.Second),
		IdempotencyKey:  "plugin-test-" + telemetry.RequestID(r.Context()),
		Input:           body.Input,
	}, time.Duration(plugin.TimeoutSeconds)*time.Second)
	if err != nil {
		writeError(w, r, http.StatusBadGateway, "plugin_test_failed", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, response)
}
