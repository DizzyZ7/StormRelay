package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/auth"
	"github.com/DizzyZ7/StormRelay/internal/oidcauth"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

type createOIDCProviderRequest struct {
	Name     string `json:"name"`
	Issuer   string `json:"issuer"`
	Audience string `json:"audience"`
}

type updateOIDCProviderRequest struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	Version int64  `json:"version"`
}

type createOIDCIdentityRequest struct {
	Subject     string      `json:"subject"`
	Email       string      `json:"email"`
	DisplayName string      `json:"display_name"`
	Roles       []auth.Role `json:"roles"`
}

type updateOIDCIdentityRequest struct {
	Enabled bool        `json:"enabled"`
	Roles   []auth.Role `json:"roles"`
	Version int64       `json:"version"`
}

func (s *Server) listOIDCProviders(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListOIDCProviders(r.Context(), tenantID(r))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) createOIDCProvider(w http.ResponseWriter, r *http.Request) {
	var request createOIDCProviderRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	discovery, err := oidcauth.Discover(ctx, request.Issuer)
	if err != nil {
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_oidc_provider", "OIDC discovery validation failed", map[string]any{"reason": err.Error()})
		return
	}
	provider, err := s.store.CreateOIDCProvider(r.Context(), storage.CreateOIDCProviderInput{
		TenantID: tenantID(r), Name: request.Name, Issuer: discovery.Issuer, Audience: request.Audience,
		JWKSURI: discovery.JWKSURI, SupportedSigningAlgs: discovery.SupportedSigningAlgs,
		ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()),
	})
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, provider)
}

func (s *Server) oidcProviderRoute(w http.ResponseWriter, r *http.Request) {
	remainder := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/oidc/providers/"), "/")
	parts := strings.Split(remainder, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	providerID := parts[0]
	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			provider, err := s.store.GetOIDCProvider(r.Context(), tenantID(r), providerID)
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, provider)
		case http.MethodPut:
			var request updateOIDCProviderRequest
			if !decodeJSON(w, r, &request) {
				return
			}
			provider, err := s.store.UpdateOIDCProvider(r.Context(), storage.UpdateOIDCProviderInput{
				TenantID: tenantID(r), ID: providerID, Name: request.Name, Enabled: request.Enabled,
				ExpectedVersion: request.Version, ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()),
			})
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, provider)
		default:
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		}
		return
	}
	if len(parts) == 2 && parts[1] == "identities" {
		switch r.Method {
		case http.MethodGet:
			items, err := s.store.ListOIDCIdentities(r.Context(), tenantID(r), providerID)
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusOK, map[string]any{"items": items})
		case http.MethodPost:
			var request createOIDCIdentityRequest
			if !decodeJSON(w, r, &request) {
				return
			}
			identity, err := s.store.CreateOIDCIdentity(r.Context(), storage.CreateOIDCIdentityInput{
				TenantID: tenantID(r), ProviderID: providerID, Subject: request.Subject, Email: request.Email,
				DisplayName: request.DisplayName, Roles: request.Roles, ActorID: actorID(r),
				RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()),
			})
			if err != nil {
				mapStoreError(w, r, err)
				return
			}
			writeJSON(w, http.StatusCreated, identity)
		default:
			writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
		}
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
}

func (s *Server) oidcIdentityRoute(w http.ResponseWriter, r *http.Request) {
	identityID := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/oidc/identities/"), "/")
	if identityID == "" || strings.Contains(identityID, "/") || r.Method != http.MethodPut {
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
		return
	}
	var request updateOIDCIdentityRequest
	if !decodeJSON(w, r, &request) {
		return
	}
	identity, err := s.store.UpdateOIDCIdentity(r.Context(), storage.UpdateOIDCIdentityInput{
		TenantID: tenantID(r), ID: identityID, Enabled: request.Enabled, Roles: request.Roles,
		ExpectedVersion: request.Version, ActorID: actorID(r), RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()),
	})
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, identity)
}
