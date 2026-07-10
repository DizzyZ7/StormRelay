package api

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"
	"sync"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/id"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

type Server struct {
	cfg           config.Config
	store         *storage.Store
	bus           *messaging.Bus
	metrics       *telemetry.Metrics
	logger        *slog.Logger
	bootstrapHash [32]byte
	limiterMu     sync.Mutex
	limiters      map[string]*ingestion.Limiter
	replay        *ingestion.ReplayCache
}

func New(cfg config.Config, store *storage.Store, bus *messaging.Bus, metrics *telemetry.Metrics, logger *slog.Logger) *Server {
	return &Server{cfg: cfg, store: store, bus: bus, metrics: metrics, logger: logger, bootstrapHash: sha256.Sum256([]byte(cfg.BootstrapAPIKey)), limiters: map[string]*ingestion.Limiter{}, replay: ingestion.NewReplayCache(100000)}
}
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /readyz", s.ready)
	mux.HandleFunc("GET /metrics", s.metricsHandler)
	mux.HandleFunc("GET /api/v1/ack/{token}", s.ackByToken)
	mux.HandleFunc("POST /api/v1/ack/{token}", s.ackByToken)
	mux.HandleFunc("POST /api/v1/webhooks/{sourceID}", s.ingestWebhook)
	mux.Handle("/api/v1/", s.requireAdmin(http.HandlerFunc(s.apiRoutes)))
	return s.middleware(mux)
}

func (s *Server) apiRoutes(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/v1/version":
		s.version(w, r)
	case r.URL.Path == "/api/v1/sources" && r.Method == http.MethodGet:
		s.listSources(w, r)
	case r.URL.Path == "/api/v1/sources" && r.Method == http.MethodPost:
		s.createSource(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/sources/") && strings.HasSuffix(r.URL.Path, "/test") && r.Method == http.MethodPost:
		s.testSource(w, r)
	case r.URL.Path == "/api/v1/incidents" && r.Method == http.MethodGet:
		s.listIncidents(w, r)
	case r.URL.Path == "/api/v1/incidents" && r.Method == http.MethodPost:
		s.createManualIncident(w, r)
	case strings.HasPrefix(r.URL.Path, "/api/v1/incidents/"):
		s.incidentRoute(w, r)
	case r.URL.Path == "/api/v1/policies/validate" && r.Method == http.MethodPost:
		s.validatePolicy(w, r)
	case r.URL.Path == "/api/v1/policies" && r.Method == http.MethodGet:
		s.listPolicies(w, r)
	case r.URL.Path == "/api/v1/policies" && r.Method == http.MethodPost:
		s.applyPolicy(w, r)
	case r.URL.Path == "/api/v1/notification-channels" && r.Method == http.MethodGet:
		s.listNotificationChannels(w, r)
	case r.URL.Path == "/api/v1/notification-channels" && r.Method == http.MethodPost:
		s.createNotificationChannel(w, r)
	case r.URL.Path == "/api/v1/audit/export" && r.Method == http.MethodGet:
		s.exportAudit(w, r)
	default:
		writeError(w, r, http.StatusNotFound, "not_found", "route not found", nil)
	}
}

func (s *Server) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		requestID := strings.TrimSpace(r.Header.Get("X-Request-ID"))
		if requestID == "" {
			requestID, _ = id.New()
		}
		ctx := telemetry.WithRequestID(r.Context(), requestID)
		traceParent := r.Header.Get("traceparent")
		traceID, ok := telemetry.ParseTraceParent(traceParent)
		if !ok {
			traceParent, traceID = telemetry.NewTraceParent()
			r.Header.Set("traceparent", traceParent)
		}
		ctx = telemetry.WithTraceID(ctx, traceID)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-ID", requestID)
		w.Header().Set("traceparent", traceParent)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-store")
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("panic recovered", "request_id", requestID, "trace_id", traceID, "error", fmt.Sprint(recovered), "stack", string(debug.Stack()))
				writeError(w, r, http.StatusInternalServerError, "internal_error", "internal server error", nil)
			}
			s.logger.Info("http request", "request_id", requestID, "trace_id", traceID, "method", r.Method, "path", safePath(r.URL.Path), "duration_ms", time.Since(start).Milliseconds())
		}()
		next.ServeHTTP(w, r)
	})
}
func (s *Server) requireAdmin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		value := strings.TrimSpace(strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer "))
		hash := sha256.Sum256([]byte(value))
		if subtle.ConstantTimeCompare(hash[:], s.bootstrapHash[:]) != 1 {
			writeError(w, r, http.StatusUnauthorized, "unauthorized", "valid API key required", nil)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "service": s.cfg.ServiceName, "version": s.cfg.Version})
}
func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	checks := map[string]string{"postgres": "ok", "nats": "ok", "migrations": "ok"}
	ready := true
	if err := s.store.Ping(ctx); err != nil {
		checks["postgres"] = "unavailable"
		ready = false
	}
	if version, err := s.store.MigrationVersion(ctx); err != nil || version != storage.ExpectedMigrationVersion {
		checks["migrations"] = "unavailable"
		ready = false
	}
	if err := s.bus.Ready(); err != nil {
		checks["nats"] = "unavailable"
		ready = false
	}
	status := http.StatusOK
	if !ready {
		status = http.StatusServiceUnavailable
	}
	writeJSON(w, status, map[string]any{"ready": ready, "checks": checks})
}
func (s *Server) metricsHandler(w http.ResponseWriter, r *http.Request) {
	if count, err := s.store.CountOpenIncidents(r.Context(), s.cfg.DefaultTenantID); err == nil {
		s.metrics.OpenIncidents.Store(count)
	}
	acquired, _, max := s.store.PoolStats()
	s.metrics.DBPoolAcquired.Store(int64(acquired))
	s.metrics.DBPoolMax.Store(int64(max))
	s.metrics.JetStreamConsumerLag.Store(s.bus.ConsumerLag())
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	s.metrics.WritePrometheus(w)
}
func (s *Server) version(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"version": s.cfg.Version, "api": "v1", "event_schema": "1.0"})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

type errorEnvelope struct {
	Error struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		RequestID string `json:"request_id"`
		Details   any    `json:"details,omitempty"`
	} `json:"error"`
}

func writeError(w http.ResponseWriter, r *http.Request, status int, code, message string, details any) {
	var out errorEnvelope
	out.Error.Code = code
	out.Error.Message = message
	out.Error.RequestID = telemetry.RequestID(r.Context())
	out.Error.Details = details
	writeJSON(w, status, out)
}
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_json", "request body is invalid", nil)
		return false
	}
	return true
}
func safePath(path string) string {
	if strings.HasPrefix(path, "/api/v1/ack/") {
		return "/api/v1/ack/[REDACTED]"
	}
	return path
}
func mapStoreError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case storage.IsNoRows(err):
		writeError(w, r, http.StatusNotFound, "not_found", "resource not found", nil)
	case strings.Contains(err.Error(), "version conflict"):
		writeError(w, r, http.StatusConflict, "version_conflict", err.Error(), nil)
	case strings.Contains(err.Error(), "invalid incident transition") || strings.Contains(err.Error(), "already"):
		writeError(w, r, http.StatusConflict, "invalid_transition", err.Error(), nil)
	default:
		slog.Error("storage operation failed", "request_id", telemetry.RequestID(r.Context()), "error", err)
		writeError(w, r, http.StatusInternalServerError, "storage_error", "storage operation failed", nil)
	}
}
