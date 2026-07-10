package runbooks

import (
	"encoding/json"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const APIVersion = "stormrelay.io/v1"

var identifierPattern = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,63}$`)

type Document struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}

type Metadata struct {
	ID          string `yaml:"id" json:"id"`
	Version     int    `yaml:"version" json:"version"`
	Name        string `yaml:"name,omitempty" json:"name,omitempty"`
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

type Spec struct {
	Timeout string `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Steps   []Step `yaml:"steps" json:"steps"`
}

type Step struct {
	ID       string      `yaml:"id" json:"id"`
	Name     string      `yaml:"name,omitempty" json:"name,omitempty"`
	Type     string      `yaml:"type" json:"type"`
	Timeout  string      `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retry    RetryPolicy `yaml:"retry,omitempty" json:"retry,omitempty"`
	When     *Condition  `yaml:"when,omitempty" json:"when,omitempty"`
	With     Input       `yaml:"with,omitempty" json:"with"`
	HTTP     *Input      `yaml:"http,omitempty" json:"-"`
	Wait     *Input      `yaml:"wait,omitempty" json:"-"`
	Approval *Input      `yaml:"approval,omitempty" json:"-"`
	Plugin   *Input      `yaml:"plugin,omitempty" json:"-"`
	Rollback *Action     `yaml:"rollback,omitempty" json:"rollback,omitempty"`
}

type RetryPolicy struct {
	MaxAttempts    int    `yaml:"maxAttempts,omitempty" json:"maxAttempts,omitempty"`
	InitialBackoff string `yaml:"initialBackoff,omitempty" json:"initialBackoff,omitempty"`
	MaxBackoff     string `yaml:"maxBackoff,omitempty" json:"maxBackoff,omitempty"`
}

type Condition struct {
	Field  string   `yaml:"field" json:"field"`
	Equals *string  `yaml:"equals,omitempty" json:"equals,omitempty"`
	In     []string `yaml:"in,omitempty" json:"in,omitempty"`
	Not    bool     `yaml:"not,omitempty" json:"not,omitempty"`
}

type Input struct {
	Method            string            `yaml:"method,omitempty" json:"method,omitempty"`
	URL               string            `yaml:"url,omitempty" json:"url,omitempty"`
	Headers           map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	Body              any               `yaml:"body,omitempty" json:"body,omitempty"`
	IdempotencyHeader string            `yaml:"idempotencyHeader,omitempty" json:"idempotencyHeader,omitempty"`
	Idempotent        bool              `yaml:"idempotent,omitempty" json:"idempotent,omitempty"`
	Duration          string            `yaml:"duration,omitempty" json:"duration,omitempty"`
	Prompt            string            `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	ExpiresIn         string            `yaml:"expiresIn,omitempty" json:"expiresIn,omitempty"`
	ExpiresAfter      string            `yaml:"expiresAfter,omitempty" json:"expiresAfter,omitempty"`
	Plugin            string            `yaml:"plugin,omitempty" json:"plugin,omitempty"`
	Action            string            `yaml:"action,omitempty" json:"action,omitempty"`
	Input             any               `yaml:"input,omitempty" json:"input,omitempty"`
}

type Action struct {
	Type    string      `yaml:"type" json:"type"`
	Timeout string      `yaml:"timeout,omitempty" json:"timeout,omitempty"`
	Retry   RetryPolicy `yaml:"retry,omitempty" json:"retry,omitempty"`
	With    Input       `yaml:"with,omitempty" json:"with"`
	HTTP    *Input      `yaml:"http,omitempty" json:"-"`
	Plugin  *Input      `yaml:"plugin,omitempty" json:"-"`
}

type Snapshot struct {
	RunbookID      string      `json:"runbook_id"`
	RunbookVersion int         `json:"runbook_version"`
	Step           Step        `json:"step"`
	TimeoutSeconds int         `json:"timeout_seconds"`
	Retry          RetryParsed `json:"retry"`
}

type RetryParsed struct {
	MaxAttempts           int `json:"max_attempts"`
	InitialBackoffSeconds int `json:"initial_backoff_seconds"`
	MaxBackoffSeconds     int `json:"max_backoff_seconds"`
}

func Parse(data []byte) (Document, error) {
	var doc Document
	decoder := yaml.NewDecoder(strings.NewReader(string(data)))
	decoder.KnownFields(true)
	if err := decoder.Decode(&doc); err != nil {
		return Document{}, fmt.Errorf("decode runbook: %w", err)
	}
	if err := doc.normalize(); err != nil {
		return Document{}, err
	}
	if err := doc.Validate(); err != nil {
		return Document{}, err
	}
	return doc, nil
}

func (d *Document) normalize() error {
	for index := range d.Spec.Steps {
		step := &d.Spec.Steps[index]
		sections := map[string]*Input{"http": step.HTTP, "wait": step.Wait, "approval": step.Approval, "plugin": step.Plugin}
		selected := sections[step.Type]
		nonNil := 0
		for _, value := range sections {
			if value != nil {
				nonNil++
			}
		}
		if selected != nil {
			if nonNil != 1 || !inputIsZero(step.With) {
				return fmt.Errorf("spec.steps[%d]: exactly one type-specific input section is allowed", index)
			}
			step.With = *selected
		} else if nonNil != 0 {
			return fmt.Errorf("spec.steps[%d]: input section does not match type %q", index, step.Type)
		}
		normalizeInput(&step.With)
		if step.Rollback != nil {
			if err := normalizeAction(step.Rollback); err != nil {
				return fmt.Errorf("spec.steps[%d].rollback: %w", index, err)
			}
		}
	}
	return nil
}

func normalizeAction(action *Action) error {
	sections := map[string]*Input{"http": action.HTTP, "plugin": action.Plugin}
	selected := sections[action.Type]
	nonNil := 0
	for _, value := range sections {
		if value != nil {
			nonNil++
		}
	}
	if selected != nil {
		if nonNil != 1 || !inputIsZero(action.With) {
			return fmt.Errorf("exactly one type-specific input section is allowed")
		}
		action.With = *selected
	} else if nonNil != 0 {
		return fmt.Errorf("input section does not match type %q", action.Type)
	}
	normalizeInput(&action.With)
	return nil
}

func normalizeInput(input *Input) {
	if input.Idempotent && input.IdempotencyHeader == "" {
		input.IdempotencyHeader = "Idempotency-Key"
	}
	if input.ExpiresIn == "" {
		input.ExpiresIn = input.ExpiresAfter
	}
}

func inputIsZero(input Input) bool {
	encoded, _ := json.Marshal(input)
	return string(encoded) == "{}"
}

func (d Document) Validate() error {
	if d.APIVersion != APIVersion {
		return fmt.Errorf("apiVersion must be %s", APIVersion)
	}
	if d.Kind != "Runbook" {
		return fmt.Errorf("kind must be Runbook")
	}
	if !identifierPattern.MatchString(d.Metadata.ID) {
		return fmt.Errorf("metadata.id must match %s", identifierPattern)
	}
	if d.Metadata.Version < 1 {
		return fmt.Errorf("metadata.version must be positive")
	}
	if len(d.Spec.Steps) == 0 || len(d.Spec.Steps) > 100 {
		return fmt.Errorf("spec.steps must contain between 1 and 100 steps")
	}
	if d.Spec.Timeout != "" {
		if _, err := boundedDuration(d.Spec.Timeout, time.Second, 24*time.Hour); err != nil {
			return fmt.Errorf("spec.timeout: %w", err)
		}
	}
	seen := map[string]struct{}{}
	for i, step := range d.Spec.Steps {
		if err := validateStep(step); err != nil {
			return fmt.Errorf("spec.steps[%d]: %w", i, err)
		}
		if _, ok := seen[step.ID]; ok {
			return fmt.Errorf("spec.steps[%d]: duplicate id %q", i, step.ID)
		}
		seen[step.ID] = struct{}{}
	}
	for i, step := range d.Spec.Steps {
		if step.When != nil {
			if err := validateCondition(*step.When, seen); err != nil {
				return fmt.Errorf("spec.steps[%d].when: %w", i, err)
			}
		}
	}
	return nil
}

func validateStep(step Step) error {
	if !identifierPattern.MatchString(step.ID) {
		return fmt.Errorf("id must match %s", identifierPattern)
	}
	if len(step.Name) > 200 {
		return fmt.Errorf("name is too long")
	}
	switch step.Type {
	case "http":
		if err := validateHTTP(step.With); err != nil {
			return err
		}
	case "wait":
		if _, err := boundedDuration(step.With.Duration, time.Second, 7*24*time.Hour); err != nil {
			return fmt.Errorf("wait duration: %w", err)
		}
	case "approval":
		if strings.TrimSpace(step.With.Prompt) == "" || len(step.With.Prompt) > 2000 {
			return fmt.Errorf("approval prompt is required and must be at most 2000 bytes")
		}
		if step.With.ExpiresIn != "" {
			if _, err := boundedDuration(step.With.ExpiresIn, time.Minute, 30*24*time.Hour); err != nil {
				return fmt.Errorf("approval expiresIn: %w", err)
			}
		}
	case "plugin":
		if !identifierPattern.MatchString(step.With.Plugin) || !identifierPattern.MatchString(step.With.Action) {
			return fmt.Errorf("plugin and action must be valid identifiers")
		}
		if encoded, err := json.Marshal(step.With.Input); err != nil || len(encoded) > 256<<10 {
			return fmt.Errorf("plugin input must be JSON-compatible and at most 256 KiB")
		}
	default:
		return fmt.Errorf("unsupported type %q", step.Type)
	}
	if _, err := ParsedRetry(step.Retry); err != nil {
		return fmt.Errorf("retry: %w", err)
	}
	if step.Timeout != "" {
		if _, err := boundedDuration(step.Timeout, time.Second, time.Hour); err != nil {
			return fmt.Errorf("timeout: %w", err)
		}
	}
	if step.Rollback != nil {
		if step.Rollback.Type != "http" && step.Rollback.Type != "plugin" {
			return fmt.Errorf("rollback supports only http or plugin")
		}
		rollbackStep := Step{ID: step.ID + "-rollback", Type: step.Rollback.Type, Timeout: step.Rollback.Timeout, Retry: step.Rollback.Retry, With: step.Rollback.With}
		if err := validateStep(rollbackStep); err != nil {
			return fmt.Errorf("rollback: %w", err)
		}
	}
	return nil
}

func validateHTTP(input Input) error {
	method := strings.ToUpper(strings.TrimSpace(input.Method))
	if method == "" {
		method = "POST"
	}
	switch method {
	case "GET", "POST", "PUT", "PATCH", "DELETE":
	default:
		return fmt.Errorf("unsupported HTTP method %q", method)
	}
	parsed, err := url.Parse(input.URL)
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return fmt.Errorf("http url must be absolute")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("http url scheme must be http or https")
	}
	if parsed.User != nil || parsed.Fragment != "" {
		return fmt.Errorf("http url must not contain userinfo or fragment")
	}
	if encoded, err := json.Marshal(input.Body); err != nil || len(encoded) > 256<<10 {
		return fmt.Errorf("http body must be JSON-compatible and at most 256 KiB")
	}
	for name, value := range input.Headers {
		lower := strings.ToLower(strings.TrimSpace(name))
		if lower == "authorization" || lower == "cookie" || lower == "proxy-authorization" || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
			return fmt.Errorf("sensitive inline header %q is not allowed", name)
		}
		if strings.ContainsAny(name+value, "\r\n") {
			return fmt.Errorf("header %q contains a newline", name)
		}
	}
	return nil
}

func validateCondition(condition Condition, steps map[string]struct{}) error {
	if condition.Field == "" {
		return fmt.Errorf("field is required")
	}
	if (condition.Equals == nil) == (len(condition.In) == 0) {
		return fmt.Errorf("exactly one of equals or in is required")
	}
	allowed := condition.Field == "execution.dry_run" || strings.HasPrefix(condition.Field, "incident.")
	if strings.HasPrefix(condition.Field, "steps.") {
		parts := strings.Split(condition.Field, ".")
		allowed = len(parts) >= 3
		if allowed {
			_, allowed = steps[parts[1]]
		}
	}
	if !allowed || containsSensitiveName(condition.Field) {
		return fmt.Errorf("field %q is not allowed", condition.Field)
	}
	return nil
}

func ParsedRetry(retry RetryPolicy) (RetryParsed, error) {
	maxAttempts := retry.MaxAttempts
	if maxAttempts == 0 {
		maxAttempts = 3
	}
	if maxAttempts < 1 || maxAttempts > 10 {
		return RetryParsed{}, fmt.Errorf("maxAttempts must be between 1 and 10")
	}
	initial := time.Second
	maximum := 30 * time.Second
	var err error
	if retry.InitialBackoff != "" {
		initial, err = boundedDuration(retry.InitialBackoff, 100*time.Millisecond, 5*time.Minute)
		if err != nil {
			return RetryParsed{}, fmt.Errorf("initialBackoff: %w", err)
		}
	}
	if retry.MaxBackoff != "" {
		maximum, err = boundedDuration(retry.MaxBackoff, initial, 30*time.Minute)
		if err != nil {
			return RetryParsed{}, fmt.Errorf("maxBackoff: %w", err)
		}
	}
	if maximum < initial {
		return RetryParsed{}, fmt.Errorf("maxBackoff must be >= initialBackoff")
	}
	return RetryParsed{MaxAttempts: maxAttempts, InitialBackoffSeconds: max(1, int(initial.Seconds())), MaxBackoffSeconds: max(1, int(maximum.Seconds()))}, nil
}

func TimeoutSeconds(step Step) (int, error) {
	if step.Type == "wait" || step.Type == "approval" {
		return 30, nil
	}
	value := step.Timeout
	if value == "" {
		value = "30s"
	}
	d, err := boundedDuration(value, time.Second, time.Hour)
	if err != nil {
		return 0, err
	}
	return int(d.Seconds()), nil
}

func boundedDuration(value string, minimum, maximum time.Duration) (time.Duration, error) {
	if strings.TrimSpace(value) == "" {
		return 0, fmt.Errorf("duration is required")
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid duration %q", value)
	}
	if d < minimum || d > maximum {
		return 0, fmt.Errorf("duration must be between %s and %s", minimum, maximum)
	}
	return d, nil
}

func containsSensitiveName(value string) bool {
	lower := strings.ToLower(value)
	for _, item := range []string{"secret", "token", "password", "authorization", "cookie", "credential", "signature"} {
		if strings.Contains(lower, item) {
			return true
		}
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
