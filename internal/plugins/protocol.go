package plugins

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/networkguard"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"go.opentelemetry.io/otel/trace"
)

const ProtocolVersion = "stormrelay.plugin/v1"

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
}

type ActionRequest struct {
	ProtocolVersion string          `json:"protocol_version"`
	ExecutionID     string          `json:"execution_id"`
	StepID          string          `json:"step_id"`
	RequestID       string          `json:"request_id"`
	TraceParent     string          `json:"traceparent,omitempty"`
	Deadline        time.Time       `json:"deadline"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Input           json.RawMessage `json:"input"`
}

type ActionResponse struct {
	ProtocolVersion string          `json:"protocol_version"`
	Status          string          `json:"status"`
	IdempotencyKey  string          `json:"idempotency_key"`
	Output          json.RawMessage `json:"output,omitempty"`
	Error           string          `json:"error,omitempty"`
}

type clientGuard interface {
	Client(context.Context, string, time.Duration) (*http.Client, *url.URL, error)
}

type Client struct {
	guard clientGuard
}

func NewClient(allowedHosts []string) *Client { return &Client{guard: networkguard.New(allowedHosts)} }

func (c *Client) Discover(ctx context.Context, endpoint, bearer string, timeout time.Duration) (manifest Manifest, err error) {
	discoveryCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.plugin.discover", trace.SpanKindClient,
		telemetry.StringAttribute("stormrelay.plugin.protocol", ProtocolVersion),
	)
	defer func() {
		if err != nil {
			telemetry.MarkSpanError(span)
			telemetry.SetSpanOutcome(span, "failed")
		} else {
			telemetry.SetSpanOutcome(span, "succeeded")
			span.End()
	}()

	urlValue := strings.TrimRight(endpoint, "/") + "/stormrelay/plugin/v1/manifest"
	client, parsed, err := c.guard.Client(discoveryCtx, urlValue, timeout)
	if err != nil {
		return Manifest{}, err
	}
	req, err := http.NewRequestWithContext(discoveryCtx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return Manifest{}, err
	}
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	telemetry.InjectHTTPTrace(discoveryCtx, req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return Manifest{}, fmt.Errorf("plugin manifest request failed: %w", err)
	}
	defer resp.Body.Close()
	span.SetAttributes(telemetry.IntAttribute("http.response.status_code", resp.StatusCode))
	body, err := readBounded(resp.Body, 256<<10)
	if err != nil {
		return Manifest{}, err
	}
	if resp.StatusCode/100 != 2 {
		return Manifest{}, fmt.Errorf("plugin manifest returned HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return Manifest{}, fmt.Errorf("malformed plugin manifest: %w", err)
	}
	if err := ValidateManifest(manifest); err != nil {
		return Manifest{}, err
	}
	span.SetAttributes(telemetry.IntAttribute("stormrelay.plugin.action_count", len(manifest.Actions)))
	return manifest, nil
}

func (c *Client) Call(ctx context.Context, endpoint, action, bearer string, request ActionRequest, timeout time.Duration) (result ActionResponse, err error) {
	callCtx, span := telemetry.StartOperationSpan(ctx, "stormrelay.plugin.call", trace.SpanKindClient,
		telemetry.StringAttribute("stormrelay.plugin.protocol", ProtocolVersion),
		telemetry.StringAttribute("stormrelay.plugin.action", action),
	)
	defer func() {
		if err != nil {
			telemetry.MarkSpanError(span)
			telemetry.SetSpanOutcome(span, "failed")
		} else {
			telemetry.SetSpanOutcome(span, "succeeded")
		}
		span.End()
	}()

	if !identifierPattern.MatchString(action) {
		return ActionResponse{}, fmt.Errorf("invalid plugin action %q", action)
	}
	urlValue := strings.TrimRight(endpoint, "/") + "/stormrelay/plugin/v1/actions/" + action
	client, parsed, err := c.guard.Client(callCtx, urlValue, timeout)
	if err != nil {
		return ActionResponse{}, err
	}
	request.TraceParent = telemetry.TraceParentFromContext(callCtx)
	body, err := json.Marshal(request)
	if err != nil {
		return ActionResponse{}, err
	}
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost, parsed.String(), bytes.NewReader(body))
	if err != nil {
		return ActionResponse{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	telemetry.InjectHTTPTrace(callCtx, req.Header)
	resp, err := client.Do(req)
	if err != nil {
		return ActionResponse{}, fmt.Errorf("plugin action request failed: %w", err)
	}
	defer resp.Body.Close()
	span.SetAttributes(telemetry.IntAttribute("http.response.status_code", resp.StatusCode))
	responseBody, err := readBounded(resp.Body, 1<<20)
	if err != nil {
		return ActionResponse{}, err
	}
	if resp.StatusCode/100 != 2 {
		return ActionResponse{}, fmt.Errorf("plugin action returned HTTP %d", resp.StatusCode)
	}
	decoder := json.NewDecoder(bytes.NewReader(responseBody))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		return ActionResponse{}, fmt.Errorf("malformed plugin response: %w", err)
	}
	if result.ProtocolVersion != ProtocolVersion {
		return ActionResponse{}, fmt.Errorf("plugin response protocol %q is incompatible", result.ProtocolVersion)
	}
	if result.IdempotencyKey != request.IdempotencyKey {
		return ActionResponse{}, fmt.Errorf("plugin did not echo the idempotency key")
	}
	if result.Status != "succeeded" && result.Status != "failed" {
		return ActionResponse{}, fmt.Errorf("plugin returned invalid status %q", result.Status)
	}
	if len(result.Output) > 1<<20 || len(result.Error) > 1000 {
		return ActionResponse{}, fmt.Errorf("plugin response exceeds limits")
	}
	if result.Status == "failed" {
		return result, fmt.Errorf("plugin action failed: %s", sanitize(result.Error))
	}
	return result, nil
}

func ValidateManifest(manifest Manifest) error {
	if manifest.ProtocolVersion != ProtocolVersion {
		return fmt.Errorf("plugin protocol %q is incompatible; expected %q", manifest.ProtocolVersion, ProtocolVersion)
	}
	if strings.TrimSpace(manifest.PluginID) == "" || strings.TrimSpace(manifest.Version) == "" {
		return fmt.Errorf("plugin_id and version are required")
	}
	if len(manifest.Actions) == 0 || len(manifest.Actions) > 100 {
		return fmt.Errorf("plugin must declare between 1 and 100 actions")
	}
	seen := map[string]bool{}
	for _, action := range manifest.Actions {
		if !identifierPattern.MatchString(action.Name) || seen[action.Name] {
			return fmt.Errorf("plugin action names must be valid identifiers and unique")
		}
		seen[action.Name] = true
	}
	return nil
}

func ManifestHasAction(manifest Manifest, action string) bool {
	for _, item := range manifest.Actions {
		if item.Name == action {
			return true
		}
	}
	return false
}
func sanitize(value string) string {
	if len(value) > 1000 {
		return value[:1000]
	}
	return value
}

func readBounded(r io.Reader, max int64) ([]byte, error) {
	limited := &io.LimitedReader{R: r, N: max + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > max {
		return nil, fmt.Errorf("plugin response exceeds %d bytes", max)
	}
	return data, nil
}
