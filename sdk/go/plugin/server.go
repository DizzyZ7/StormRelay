package plugin

import (
	"bytes"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type Server struct {
	manifest Manifest
	actions  map[string]Action
	bearer   string
	logger   *slog.Logger
	now      func() time.Time
}

type ServerOption func(*Server) error

func WithBearerToken(token string) ServerOption {
	return func(server *Server) error {
		token = strings.TrimSpace(token)
		if token == "" || strings.ContainsAny(token, "\r\n") || len(token) > 4096 {
			return errors.New("bearer token must contain 1 to 4096 safe bytes")
		}
		server.bearer = token
		return nil
	}
}

func WithLogger(logger *slog.Logger) ServerOption {
	return func(server *Server) error {
		if logger == nil {
			return errors.New("logger must not be nil")
		}
		server.logger = logger
		return nil
	}
}

func NewServer(pluginID, version string, actions []Action, options ...ServerOption) (*Server, error) {
	manifest := Manifest{
		PluginID: pluginID, Version: version,
		ProtocolVersion: ProtocolVersion, Actions: append([]Action(nil), actions...),
	}
	if err := ValidateManifest(manifest); err != nil {
		return nil, err
	}
	server := &Server{
		manifest: manifest,
		actions:  make(map[string]Action, len(actions)),
		logger:   slog.Default(),
		now:      time.Now,
	}
	for _, action := range actions {
		server.actions[action.Name] = action
	}
	for _, option := range options {
		if option != nil {
			if err := option(server); err != nil {
				return nil, err
			}
		}
	}
	return server, nil
}

func (s *Server) Manifest() Manifest {
	manifest := s.manifest
	manifest.Actions = append([]Action(nil), s.manifest.Actions...)
	return manifest
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /stormrelay/plugin/v1/manifest", s.handleManifest)
	mux.HandleFunc("POST /stormrelay/plugin/v1/actions/{action}", s.handleAction)
	return s.recoverPanics(s.authenticate(mux))
}

func (s *Server) ListenAndServe(address string) error {
	server := &http.Server{
		Addr:              address,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	return server.ListenAndServe()
}

func (s *Server) handleManifest(w http.ResponseWriter, _ *http.Request) {
	manifest := s.manifest
	manifest.Actions = make([]Action, len(s.manifest.Actions))
	for index, action := range s.manifest.Actions {
		manifest.Actions[index] = Action{
			Name: action.Name, Description: action.Description,
			Permissions: append([]string(nil), action.Permissions...),
		}
	}
	writeJSON(w, http.StatusOK, manifest)
}

func (s *Server) handleAction(w http.ResponseWriter, r *http.Request) {
	actionName := r.PathValue("action")
	action, ok := s.actions[actionName]
	if !ok {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown action"})
		return
	}
	if contentType := r.Header.Get("Content-Type"); !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		writeJSON(w, http.StatusUnsupportedMediaType, map[string]string{"error": "content type must be application/json"})
		return
	}
	if r.ContentLength > MaxRequestBytes {
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"error": "request body exceeds limit"})
		return
	}
	body, err := readBounded(r.Body, MaxRequestBytes)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	var request Request
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid request body"})
		return
	}
	if err := ensureEOF(decoder); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "request body must contain one JSON object"})
		return
	}
	if err := validateRequest(request, s.now().UTC()); err != nil {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": truncate(err.Error(), MaxErrorBytes)})
		return
	}
	ctx, cancel := context.WithDeadline(r.Context(), request.Deadline)
	defer cancel()
	output, actionErr := action.Handler(ctx, request)
	response := Response{
		ProtocolVersion: ProtocolVersion,
		IdempotencyKey:  request.IdempotencyKey,
		Status:          "succeeded",
	}
	if actionErr != nil {
		response.Status = "failed"
		response.Error = sanitizeActionError(actionErr)
		writeJSON(w, http.StatusOK, response)
		return
	}
	if output == nil {
		output = map[string]any{}
	}
	encoded, err := json.Marshal(output)
	if err != nil || len(encoded) > MaxResponseBytes {
		response.Status = "failed"
		response.Error = "plugin output is not valid bounded JSON"
		writeJSON(w, http.StatusOK, response)
		return
	}
	response.Output = encoded
	writeJSON(w, http.StatusOK, response)
}

func (s *Server) authenticate(next http.Handler) http.Handler {
	if s.bearer == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(header, "Bearer ") || !constantTimeEqual(strings.TrimSpace(strings.TrimPrefix(header, "Bearer ")), s.bearer) {
			w.Header().Set("WWW-Authenticate", `Bearer realm="stormrelay-plugin"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "valid bearer token required"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) recoverPanics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				s.logger.Error("plugin handler panic", "action", r.PathValue("action"), "error", fmt.Sprint(recovered))
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal plugin error"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func sanitizeActionError(err error) string {
	var actionError *ActionError
	if errors.As(err, &actionError) {
		return truncate(strings.TrimSpace(actionError.Message), MaxErrorBytes)
	}
	return "plugin action failed"
}

func constantTimeEqual(left, right string) bool {
	const limit = 4096
	if len(left) > limit || len(right) > limit {
		return false
	}
	var a, b [limit]byte
	copy(a[:], left)
	copy(b[:], right)
	equal := subtle.ConstantTimeCompare(a[:], b[:])
	equal &= subtle.ConstantTimeEq(int32(len(left)), int32(len(right)))
	return equal == 1
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, fmt.Errorf("body exceeds %d bytes", limit)
	}
	return body, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return errors.New("extra JSON value")
		}
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	body, err := json.Marshal(value)
	if err != nil {
		status = http.StatusInternalServerError
		body = []byte(`{"error":"encode response"}`)
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}
