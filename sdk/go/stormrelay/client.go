package stormrelay

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const maxResponseBytes = 8 << 20

type Client struct {
	baseURL    *url.URL
	apiKey     string
	httpClient *http.Client
	userAgent  string
}

type Option func(*Client) error

func WithHTTPClient(client *http.Client) Option {
	return func(c *Client) error {
		if client == nil {
			return errors.New("HTTP client must not be nil")
		}
		c.httpClient = client
		return nil
	}
}

func WithUserAgent(value string) Option {
	return func(c *Client) error {
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, "\r\n") {
			return errors.New("user agent is invalid")
		}
		c.userAgent = value
		return nil
	}
}

func NewClient(baseURL, apiKey string, options ...Option) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("base URL must be an absolute HTTP(S) URL")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("base URL scheme must be HTTP or HTTPS")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, errors.New("base URL must not contain userinfo, query, or fragment")
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || strings.ContainsAny(apiKey, "\r\n") {
		return nil, errors.New("API key is required")
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	client := &Client{baseURL: parsed, apiKey: apiKey, httpClient: &http.Client{Timeout: 30 * time.Second}, userAgent: "stormrelay-go/dev"}
	for _, option := range options {
		if option != nil {
			if err := option(client); err != nil {
				return nil, err
			}
		}
	}
	return client, nil
}

type APIError struct {
	StatusCode int
	Code       string
	Message    string
	RequestID  string
	Details    json.RawMessage
}

func (e *APIError) Error() string {
	if e.RequestID != "" {
		return fmt.Sprintf("stormrelay API %d %s: %s (request_id=%s)", e.StatusCode, e.Code, e.Message, e.RequestID)
	}
	return fmt.Sprintf("stormrelay API %d %s: %s", e.StatusCode, e.Code, e.Message)
}

func (c *Client) Version(ctx context.Context) (Version, error) {
	var out Version
	return out, c.do(ctx, http.MethodGet, "/api/v1/version", "", nil, &out)
}

func (c *Client) ListIncidents(ctx context.Context, state, severity, service, cursor string, limit int) (IncidentList, error) {
	query := url.Values{"state": []string{state}, "severity": []string{severity}, "service": []string{service}, "cursor": []string{cursor}}
	if limit > 0 {
		query.Set("limit", strconv.Itoa(limit))
	}
	var out IncidentList
	return out, c.do(ctx, http.MethodGet, "/api/v1/incidents?"+query.Encode(), "", nil, &out)
}

func (c *Client) GetIncident(ctx context.Context, incidentID string) (Incident, error) {
	var out Incident
	return out, c.do(ctx, http.MethodGet, "/api/v1/incidents/"+url.PathEscape(incidentID), "", nil, &out)
}

func (c *Client) TransitionIncident(ctx context.Context, incidentID, action string, version int64, reason string) (Incident, error) {
	if action != "ack" && action != "resolve" {
		return Incident{}, errors.New("incident action must be ack or resolve")
	}
	var out Incident
	return out, c.do(ctx, http.MethodPost, "/api/v1/incidents/"+url.PathEscape(incidentID)+"/"+action, "application/json", map[string]any{"version": version, "reason": reason}, &out)
}

func (c *Client) ApplyRunbook(ctx context.Context, document []byte) (Runbook, error) {
	var out Runbook
	return out, c.do(ctx, http.MethodPost, "/api/v1/runbooks", "application/yaml", document, &out)
}

func (c *Client) StartExecution(ctx context.Context, runbookKey, incidentID string, dryRun bool, parameters map[string]any) (Execution, error) {
	var out Execution
	return out, c.do(ctx, http.MethodPost, "/api/v1/runbooks/"+url.PathEscape(runbookKey)+"/run", "application/json", map[string]any{"incident_id": incidentID, "dry_run": dryRun, "parameters": parameters}, &out)
}

func (c *Client) GetExecution(ctx context.Context, executionID string) (Execution, error) {
	var out Execution
	return out, c.do(ctx, http.MethodGet, "/api/v1/executions/"+url.PathEscape(executionID), "", nil, &out)
}

func (c *Client) ListApprovals(ctx context.Context, executionID, status string) ([]Approval, error) {
	query := url.Values{"execution_id": []string{executionID}, "status": []string{status}}
	var out struct {
		Items []Approval `json:"items"`
	}
	err := c.do(ctx, http.MethodGet, "/api/v1/approvals?"+query.Encode(), "", nil, &out)
	return out.Items, err
}

func (c *Client) DecideApproval(ctx context.Context, approvalID, decision, reason string) (Execution, error) {
	if decision != "approve" && decision != "reject" {
		return Execution{}, errors.New("approval decision must be approve or reject")
	}
	var out Execution
	return out, c.do(ctx, http.MethodPost, "/api/v1/approvals/"+url.PathEscape(approvalID)+"/"+decision, "application/json", map[string]string{"reason": reason}, &out)
}

func (c *Client) CreateServiceAccount(ctx context.Context, name string, roles []string) (ServiceAccount, error) {
	var out ServiceAccount
	return out, c.do(ctx, http.MethodPost, "/api/v1/service-accounts", "application/json", map[string]any{"name": name, "roles": roles}, &out)
}

func (c *Client) CreateServiceAccountKey(ctx context.Context, accountID string, expiresAt *time.Time) (CreatedServiceAccountKey, error) {
	var out CreatedServiceAccountKey
	return out, c.do(ctx, http.MethodPost, "/api/v1/service-accounts/"+url.PathEscape(accountID)+"/keys", "application/json", map[string]any{"expires_at": expiresAt}, &out)
}

func (c *Client) RevokeServiceAccountKey(ctx context.Context, keyID string) (ServiceAccountKey, error) {
	var out ServiceAccountKey
	return out, c.do(ctx, http.MethodPost, "/api/v1/service-account-keys/"+url.PathEscape(keyID)+"/revoke", "application/json", map[string]any{}, &out)
}

func (c *Client) do(ctx context.Context, method, path, contentType string, body, out any) error {
	if ctx == nil {
		return errors.New("context must not be nil")
	}
	requestURL := *c.baseURL
	requestURL.Path = strings.TrimRight(c.baseURL.Path, "/") + strings.SplitN(path, "?", 2)[0]
	if index := strings.IndexByte(path, '?'); index >= 0 {
		requestURL.RawQuery = path[index+1:]
	}
	payload, err := encodeBody(body, contentType)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL.String(), bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	responseBody, err := readResponse(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return decodeAPIError(resp.StatusCode, responseBody)
	}
	if out == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, out); err != nil {
		return fmt.Errorf("decode stormrelay response: %w", err)
	}
	return nil
}

func encodeBody(body any, contentType string) ([]byte, error) {
	if body == nil {
		return nil, nil
	}
	if raw, ok := body.([]byte); ok {
		return raw, nil
	}
	if contentType == "application/yaml" {
		return nil, errors.New("YAML request body must be []byte")
	}
	return json.Marshal(body)
}

func readResponse(reader io.Reader) ([]byte, error) {
	limited := &io.LimitedReader{R: reader, N: maxResponseBytes + 1}
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if len(body) > maxResponseBytes {
		return nil, fmt.Errorf("stormrelay response exceeds %d bytes", maxResponseBytes)
	}
	return body, nil
}

func decodeAPIError(status int, body []byte) error {
	var envelope struct {
		Error struct {
			Code      string          `json:"code"`
			Message   string          `json:"message"`
			RequestID string          `json:"request_id"`
			Details   json.RawMessage `json:"details"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error.Message != "" {
		return &APIError{StatusCode: status, Code: envelope.Error.Code, Message: envelope.Error.Message, RequestID: envelope.Error.RequestID, Details: envelope.Error.Details}
	}
	message := strings.TrimSpace(string(body))
	if message == "" {
		message = http.StatusText(status)
	}
	return &APIError{StatusCode: status, Code: "http_error", Message: message}
}
