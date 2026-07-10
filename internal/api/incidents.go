package api

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/incidents"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

type incidentCursor struct {
	Time time.Time `json:"time"`
	ID   string    `json:"id"`
}

func (s *Server) listIncidents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	f := storage.IncidentFilter{TenantID: s.cfg.DefaultTenantID, State: r.URL.Query().Get("state"), Severity: r.URL.Query().Get("severity"), Service: r.URL.Query().Get("service"), Limit: limit}
	if cursor := r.URL.Query().Get("cursor"); cursor != "" {
		decoded, err := decodeIncidentCursor(cursor)
		if err != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_cursor", "cursor is invalid", nil)
			return
		}
		f.CursorTime = &decoded.Time
		f.CursorID = decoded.ID
	}
	items, err := s.store.ListIncidents(r.Context(), f)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	next := ""
	if len(items) == f.Limit && len(items) > 0 {
		last := items[len(items)-1]
		next = encodeIncidentCursor(incidentCursor{Time: last.LastEventAt, ID: last.ID})
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "next_cursor": next})
}
func (s *Server) incidentRoute(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/incidents/")
	parts := strings.Split(strings.Trim(rest, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, r, http.StatusNotFound, "not_found", "incident not found", nil)
		return
	}
	incidentID := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		item, err := s.store.GetIncident(r.Context(), s.cfg.DefaultTenantID, incidentID)
		if err != nil {
			mapStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, item)
		return
	}
	if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
		items, err := s.store.ListIncidentEvents(r.Context(), s.cfg.DefaultTenantID, incidentID)
		if err != nil {
			mapStoreError(w, r, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": items})
		return
	}
	if len(parts) == 2 && (parts[1] == "ack" || parts[1] == "resolve") && r.Method == http.MethodPost {
		s.transitionIncident(w, r, incidentID, parts[1])
		return
	}
	writeError(w, r, http.StatusNotFound, "not_found", "incident route not found", nil)
}
func (s *Server) transitionIncident(w http.ResponseWriter, r *http.Request, incidentID, action string) {
	var body struct {
		Version int64  `json:"version"`
		Reason  string `json:"reason"`
	}
	if !decodeJSON(w, r, &body) {
		return
	}
	target := incidents.Acknowledged
	if action == "resolve" {
		target = incidents.Resolved
	}
	item, err := s.store.TransitionIncident(r.Context(), storage.TransitionInput{TenantID: s.cfg.DefaultTenantID, IncidentID: incidentID, To: target, ExpectedVersion: body.Version, ActorType: "api-key", ActorID: "bootstrap-admin", Reason: body.Reason, RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context())})
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, item)
}
func (s *Server) ackByToken(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	if len(token) < 20 {
		writeError(w, r, http.StatusNotFound, "not_found", "acknowledgement token is invalid", nil)
		return
	}
	item, err := s.store.AcknowledgeByToken(r.Context(), token, telemetry.RequestID(r.Context()), telemetry.TraceID(r.Context()))
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"acknowledged": true, "incident": item})
}
func encodeIncidentCursor(c incidentCursor) string {
	data, _ := json.Marshal(c)
	return base64.RawURLEncoding.EncodeToString(data)
}
func decodeIncidentCursor(value string) (incidentCursor, error) {
	data, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return incidentCursor{}, err
	}
	var c incidentCursor
	err = json.Unmarshal(data, &c)
	return c, err
}

func (s *Server) createManualIncident(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Title       string `json:"title"`
		Severity    string `json:"severity"`
		Service     string `json:"service"`
		Environment string `json:"environment"`
		Resource    string `json:"resource"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if strings.TrimSpace(in.Title) == "" {
		writeError(w, r, http.StatusBadRequest, "title_required", "title is required", nil)
		return
	}
	body, _ := json.Marshal(map[string]any{"type": "stormrelay.incident.manual", "subject": in.Title, "severity": in.Severity, "labels": map[string]string{"service": in.Service, "environment": in.Environment, "resource": in.Resource, "alertname": in.Title}})
	e, err := events.Normalize(events.NormalizeInput{TenantID: s.cfg.DefaultTenantID, SourceID: "00000000-0000-4000-8000-000000000020", SourceName: "manual-api", ContentType: "application/json", Body: body, SourceEventID: "manual-" + telemetry.RequestID(r.Context()), IdempotencyKey: r.Header.Get("Idempotency-Key"), TraceParent: r.Header.Get("traceparent"), RequestID: telemetry.RequestID(r.Context()), ReceivedAt: time.Now().UTC()})
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_incident", err.Error(), nil)
		return
	}
	if err := s.bus.PublishEvent(r.Context(), e, e.SourceID+":"+events.DedupeKey(e)); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "event_bus_unavailable", "incident could not be durably accepted", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "event_id": e.ID})
}
