package api

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

func (s *Server) createSource(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name               string             `json:"name"`
		Kind               string             `json:"kind"`
		AuthMode           ingestion.AuthMode `json:"auth_mode"`
		RateLimitPerSecond int                `json:"rate_limit_per_second"`
		RateLimitBurst     int                `json:"rate_limit_burst"`
	}
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.AuthMode == ingestion.AuthNone && !s.cfg.AllowUnauthenticatedSources {
		writeError(w, r, http.StatusBadRequest, "unauthenticated_sources_disabled", "auth_mode none requires STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES=true", nil)
		return
	}
	result, err := s.store.CreateSource(r.Context(), storage.CreateSourceInput{TenantID: s.cfg.DefaultTenantID, Name: in.Name, Kind: in.Kind, AuthMode: in.AuthMode, RateLimitPerSecond: in.RateLimitPerSecond, RateLimitBurst: in.RateLimitBurst})
	if err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_source", "event source could not be created", nil)
		return
	}
	if err := s.store.RecordAudit(r.Context(), storage.AuditInput{TenantID: s.cfg.DefaultTenantID, ActorType: "api-key", ActorID: "bootstrap-admin", Action: "source.created", ResourceType: "source", ResourceID: result.Source.ID, RequestID: telemetry.RequestID(r.Context()), TraceID: telemetry.TraceID(r.Context()), After: result.Source, Metadata: map[string]any{"kind": result.Source.Kind, "auth_mode": result.Source.AuthMode}}); err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, result)
}
func (s *Server) listSources(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ListSources(r.Context(), s.cfg.DefaultTenantID, 100)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}
func (s *Server) testSource(w http.ResponseWriter, r *http.Request) {
	path := strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/v1/sources/"), "/test")
	creds, err := s.store.GetSourceCredentials(r.Context(), path)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	body := []byte(`{"type":"stormrelay.source.test","subject":"Source connectivity test","severity":"info","labels":{"service":"stormrelay","environment":"test"}}`)
	e, err := events.Normalize(events.NormalizeInput{TenantID: creds.Source.TenantID, SourceID: creds.Source.ID, SourceName: creds.Source.Name, ContentType: "application/json", Body: body, SourceEventID: "source-test-" + telemetry.RequestID(r.Context()), TraceParent: r.Header.Get("traceparent"), RequestID: telemetry.RequestID(r.Context()), ReceivedAt: time.Now().UTC()})
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "normalization_failed", "failed to build test event", nil)
		return
	}
	if err := s.bus.PublishEvent(r.Context(), e, creds.Source.ID+":"+events.DedupeKey(e)); err != nil {
		writeError(w, r, http.StatusServiceUnavailable, "nats_unavailable", "event bus unavailable", nil)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "event_id": e.ID})
}

func (s *Server) ingestWebhook(w http.ResponseWriter, r *http.Request) {
	sourceID := r.PathValue("sourceID")
	creds, err := s.store.GetSourceCredentials(r.Context(), sourceID)
	if err != nil {
		mapStoreError(w, r, err)
		return
	}
	if creds.Source.AuthMode == ingestion.AuthNone && !s.cfg.AllowUnauthenticatedSources {
		writeError(w, r, http.StatusForbidden, "unauthenticated_source_disabled", "unauthenticated webhook ingestion is disabled", nil)
		return
	}
	if !creds.Source.Enabled {
		writeError(w, r, http.StatusGone, "source_disabled", "event source is disabled", nil)
		return
	}
	if !s.sourceLimiter(creds.Source).Allow(sourceID, time.Now()) {
		w.Header().Set("Retry-After", "1")
		s.metrics.RejectedEvents.Add(1)
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "source rate limit exceeded", nil)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.cfg.MaxPayloadBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			s.metrics.RejectedEvents.Add(1)
			writeError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "payload exceeds configured limit", map[string]any{"max_bytes": s.cfg.MaxPayloadBytes})
			return
		}
		writeError(w, r, http.StatusBadRequest, "read_failed", "unable to read request body", nil)
		return
	}
	verify := ingestion.VerifyInput{Credentials: ingestion.Credentials{Mode: creds.Source.AuthMode, HMACSecret: creds.HMACSecret, BearerHash: creds.BearerHash}, Body: body, Signature: r.Header.Get("X-StormRelay-Signature"), Authorization: r.Header.Get("Authorization"), Timestamp: r.Header.Get("X-StormRelay-Timestamp"), Now: time.Now().UTC(), ReplayWindow: s.cfg.ReplayWindow}
	if err := ingestion.Verify(verify); err != nil {
		s.metrics.RejectedEvents.Add(1)
		status := http.StatusUnauthorized
		code := "signature_invalid"
		if errors.Is(err, ingestion.ErrReplay) {
			status = http.StatusConflict
			code = "replay_rejected"
		}
		writeError(w, r, status, code, err.Error(), nil)
		return
	}

	replayKey := ""
	replayCommitted := false
	if creds.Source.AuthMode == ingestion.AuthHMAC {
		replayKey = sourceID + ":" + r.Header.Get("X-StormRelay-Timestamp") + ":" + r.Header.Get("X-StormRelay-Signature")
		if !s.replay.Accept(replayKey, time.Now().Add(s.cfg.ReplayWindow), time.Now()) {
			s.metrics.RejectedEvents.Add(1)
			writeError(w, r, http.StatusConflict, "replay_rejected", "request was already accepted", nil)
			return
		}
		defer func() {
			if !replayCommitted {
				s.replay.Release(replayKey)
			}
		}()
	}
	if creds.Source.AuthMode == ingestion.AuthBearer && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		writeError(w, r, http.StatusBadRequest, "idempotency_key_required", "bearer-authenticated sources require Idempotency-Key for replay protection", nil)
		return
	}
	contentType := r.Header.Get("Content-Type")
	if creds.Source.Kind == "cloudevents" && !strings.Contains(strings.ToLower(contentType), "application/cloudevents+json") {
		writeError(w, r, http.StatusUnsupportedMediaType, "content_type_required", "CloudEvents source requires application/cloudevents+json", nil)
		return
	}
	input := events.NormalizeInput{TenantID: creds.Source.TenantID, SourceID: sourceID, SourceName: creds.Source.Name, ContentType: contentType, Body: body, SourceEventID: r.Header.Get("X-Event-ID"), IdempotencyKey: r.Header.Get("Idempotency-Key"), TraceParent: r.Header.Get("traceparent"), RequestID: telemetry.RequestID(r.Context()), ReceivedAt: time.Now().UTC()}
	event, err := events.Normalize(input)
	if err != nil {
		s.metrics.RejectedEvents.Add(1)
		writeError(w, r, http.StatusUnprocessableEntity, "invalid_event", err.Error(), nil)
		return
	}
	msgID := sourceID + ":" + events.DedupeKey(event)
	ctx, cancel := contextWithTimeout(r, 5*time.Second)
	defer cancel()
	if err := s.bus.PublishEvent(ctx, event, msgID); err != nil {
		s.metrics.RejectedEvents.Add(1)
		writeError(w, r, http.StatusServiceUnavailable, "event_bus_unavailable", "event could not be durably accepted", nil)
		return
	}
	replayCommitted = true
	s.metrics.IngressEvents.Add(1)
	writeJSON(w, http.StatusAccepted, map[string]any{"accepted": true, "event_id": event.ID, "request_id": telemetry.RequestID(r.Context())})
}

func (s *Server) sourceLimiter(source storage.Source) *ingestion.Limiter {
	s.limiterMu.Lock()
	defer s.limiterMu.Unlock()
	l := s.limiters[source.ID]
	if l == nil {
		l = ingestion.NewLimiter(float64(source.RateLimitPerSecond), source.RateLimitBurst)
		s.limiters[source.ID] = l
	}
	return l
}
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
