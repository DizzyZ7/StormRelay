package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
)

const ProtocolVersion = "stormrelay.plugin/v1"

const (
	MaxManifestBytes = 256 << 10
	MaxRequestBytes  = 1 << 20
	MaxResponseBytes = 1 << 20
	MaxErrorBytes    = 1000
)

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

type Manifest struct {
	PluginID        string   `json:"plugin_id"`
	Version         string   `json:"version"`
	ProtocolVersion string   `json:"protocol_version"`
	Actions         []Action `json:"actions"`
}

type Action struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Permissions []string `json:"permissions"`
	Handler     Handler  `json:"-"`
}

type Request struct {
	ProtocolVersion string          `json:"protocol_version"`
	ExecutionID     string          `json:"execution_id"`
	StepID          string          `json:"step_id"`
	RequestID       string          `json:"request_id"`
	TraceParent     string          `json:"traceparent,omitempty"`
	Deadline        time.Time       `json:"deadline"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Input           json.RawMessage `json:"input"`
}

type Response struct {
	ProtocolVersion string          `json:"protocol_version"`
	Status          string          `json:"status"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Output          json.RawMessage `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
}

type Handler func(context.Context, Request) (any, error)

type ActionError struct {
	Message string
}

func (e *ActionError) Error() string { return e.Message }

func Fail(message string) error {
	message = strings.TrimSpace(message)
	if message == "" {
		message = "plugin action failed"
	}
	return &ActionError{Message: truncate(message, MaxErrorBytes)}
}

func ValidateManifest(manifest Manifest) error {
	if manifest.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("protocol_version must be %q", ProtocolVersion)
	}
	if !identifierPattern.MatchString(manifest.PluginID) {
		return errors.New("plugin_id must be a lowercase protocol identifier")
	}
	if strings.TrimSpace(manifest.Version) == "" || len(manifest.Version) > 100 {
		return errors.New("version must contain 1 to 100 bytes")
	}
	if len(manifest.Actions) == 0 || len(manifest.Actions) > 100 {
		return errors.New("manifest must declare between 1 and 100 actions")
	}
	seen := map[string]struct{}{}
	for _, action := range manifest.Actions {
		if !identifierPattern.MatchString(action.Name) {
			return fmt.Errorf("invalid action name %q", action.Name)
		}
		if _, ok := seen[action.Name]; ok {
			return fmt.Errorf("duplicate action name %q", action.Name)
		}
		seen[action.Name] = struct{}{}
		if action.Handler == nil {
			return fmt.Errorf("action %q has no handler", action.Name)
		}
		if len(action.Description) > 1000 {
			return fmt.Errorf("action %q description exceeds 1000 bytes", action.Name)
		}
		if len(action.Permissions) > 100 {
			return fmt.Errorf("action %q declares too many permissions", action.Name)
		}
		permissionSeen := map[string]struct{}{}
		for _, permission := range action.Permissions {
			if !identifierPattern.MatchString(permission) {
				return fmt.Errorf("action %q has invalid permission %q", action.Name, permission)
			}
			if _, ok := permissionSeen[permission]; ok {
				return fmt.Errorf("action %q has duplicate permission %q", action.Name, permission)
			}
			permissionSeen[permission] = struct{}{}
		}
	}
	return nil
}

func validateRequest(request Request, now time.Time) error {
	if request.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("protocol_version must be %q", ProtocolVersion)
	}
	for name, value := range map[string]string{
		"execution_id": request.ExecutionID,
		"step_id": request.StepID,
		"request_id": request.RequestID,
		"idempotency_key": request.IdempotencyKey,
	} {
		value = strings.TrimSpace(value)
		if value == "" || len(value) > 500 {
			return fmt.Errorf("%s must contain 1 to 500 bytes", name)
		}
	}
	if request.Deadline.IsZero() {
		return errors.New("deadline is required")
	}
	if !request.Deadline.After(now) {
		return errors.New("deadline has expired")
	}
	if request.Deadline.After(now.Add(24 * time.Hour)) {
		return errors.New("deadline must be within 24 hours")
	}
	if len(request.TraceParent) > 200 {
		return errors.New("traceparent exceeds 200 bytes")
	}
	if len(request.Input) == 0 || !json.Valid(request.Input) {
		return errors.New("input must be valid JSON")
	}
	return nil
}

func truncate(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	return value[:limit]
}
