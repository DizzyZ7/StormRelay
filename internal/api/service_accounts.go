package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

func (s *Server) listRoles(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"items": auth.Roles()})
}

func (s *Server) listServiceAccounts(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListServiceAccounts(r.Context(), tenantID(r))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createServiceAccount(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name  string      `json:"name"`
		Roles []auth.Role `json:"roles"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	out, err := s.store.CreateServiceAccount(r.Context(), storage.CreateServiceAccountInput{TenantID: tenantID(r), Name: body.Name, Roles: body.Roles, ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_service_account", err.Error(), nil)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) serviceAccountRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/service-accounts/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			out, err := s.store.GetServiceAccount(r.Context(), tenantID(r), parts[0])
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		case http.MethodPut:
			var body struct {
				Name    string      `json:"name"`
				Enabled bool        `json:"enabled"`
				Roles   []auth.Role `json:"roles"`
				Version int64       `json:"version"`
			}
			if !decodeJSON(w, r, &body) {
				return
			}
			out, err := s.store.UpdateServiceAccount(r.Context(), storage.UpdateServiceAccountInput{TenantID: tenantID(r), ID: parts[0], Name: body.Name, Enabled: body.Enabled, Roles: body.Roles, ExpectedVersion: body.Version, ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, out)
			return
		}
	}
	if len(parts) == 2 && parts[1] == "keys" {
		switch r.Method {
		case http.MethodGet:
			items, err := s.store.ListServiceAccountKeys(r.Context(), tenantID(r), parts[0])
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
			return
		case http.MethodPost:
			var body struct {
				ExpiresAt *time.Time `json:"expires_at"`
			}
			if r.ContentLength != 0 && !decodeJSON(w, r, &body) {
				return
			}
			out, err := s.store.CreateServiceAccountKey(r.Context(), storage.CreateServiceAccountKeyInput{TenantID: tenantID(r), ServiceAccountID: parts[0], ExpiresAt: body.ExpiresAt, ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
			if err != nil {
				writeError(w, r, http.StatusUnprocessableEntity, "invalid_service_account_key", err.Error(), nil)
				return
			}
			writeJSON(w, http.StatusCreated, out)
			return
		}
	}
	writeError(w, r, http.StatusNotFound, "not_found", "service account route not found", nil)
}

func (s *Server) serviceAccountKeyRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/service-account-keys/"), "/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[1] != "revoke" || r.Method != http.MethodPost {
		writeError(w, r, http.StatusNotFound, "not_found", "service account key route not found", nil)
		return
	}
	out, err := s.store.RevokeServiceAccountKey(r.Context(), tenantID(r), parts[0], actorID(r), telemetry.RequestID(r.Context()), telemetry.TraceID(r.Context()))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, out)
}
