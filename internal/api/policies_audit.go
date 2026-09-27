package api

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/policies"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

func (s *Server) validatePolicy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "read_failed", "unable to read policy", nil)
		return
	}
	doc, err := policies.Parse(body)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_policy", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "policy_id": doc.Metadata.ID, "version": doc.Metadata.Version})
}
func (s *Server) exportAudit(w http.ResponseWriter, r *http.Request) {
	after := time.Unix(0, 0).UTC()
	if value := r.URL.Query().Get("after"); value != "" {
		parsed, err := time.Parse(time.RFC3339, value)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_after", "after must be RFC3339", nil)
			return
		}
		after = parsed
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Content-Disposition", "attachment; filename=stormrelay-audit.jsonl")
	if err := s.store.ExportAuditJSONL(r.Context(), tenantID(r), after, w); err != nil {
		mapStoreError(w, r, err)
	}
}

func (s *Server) applyPolicy(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 256<<10))
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "read_failed", "unable to read policy", nil)
		return
	}
	out, err := s.store.ApplyPolicy(r.Context(), storage.ApplyPolicyInput{TenantID: tenantID(r), DocumentYAML: string(body), ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_policy", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) listPolicies(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListPolicies(r.Context(), tenantID(r))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) createNotificationChannel(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key    string          `json:"key"`
		Kind   string          `json:"kind"`
		Config json.RawMessage `json:"config"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Kind != "mock" && in.Kind != "telegram" {
		writeError(w, r, http.StatusBadRequest, "unsupported_channel", "Milestone 1 supports mock and telegram channels", nil)
		return
	}
	out, err := s.store.CreateNotificationChannelWithAudit(r.Context(), tenantID(r), in.Key, in.Kind, in.Config, storage.AuditInput{
		TenantID: tenantID(r), ActorType: actorType(r), ActorID: actorID(r),
		RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()),
	})
	if err != nil {
		s.logger.Error("notification channel transaction failed", "request_id", telemetry.RequestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "channel_creation_failed", "notification channel could not be created", nil)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}
func (s *Server) listNotificationChannels(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListNotificationChannels(r.Context(), tenantID(r))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
