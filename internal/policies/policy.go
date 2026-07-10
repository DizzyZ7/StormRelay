package policies

import (
	"fmt"
	"strings"

	"github.com/DizzyZ7/StormRelay/internal/events"
	"gopkg.in/yaml.v3"
)

const SchemaVersion = "v1"

type Document struct {
	APIVersion string   `yaml:"apiVersion" json:"apiVersion"`
	Kind       string   `yaml:"kind" json:"kind"`
	Metadata   Metadata `yaml:"metadata" json:"metadata"`
	Spec       Spec     `yaml:"spec" json:"spec"`
}
type Metadata struct {
	ID      string `yaml:"id" json:"id"`
	Version int    `yaml:"version" json:"version"`
}
type Spec struct {
	Match   Expression `yaml:"match" json:"match"`
	Actions Actions    `yaml:"actions" json:"actions"`
}
type Expression struct {
	All    []Expression `yaml:"all,omitempty" json:"all,omitempty"`
	Any    []Expression `yaml:"any,omitempty" json:"any,omitempty"`
	Not    *Expression  `yaml:"not,omitempty" json:"not,omitempty"`
	Field  string       `yaml:"field,omitempty" json:"field,omitempty"`
	Equals string       `yaml:"equals,omitempty" json:"equals,omitempty"`
	In     []string     `yaml:"in,omitempty" json:"in,omitempty"`
}
type Actions struct {
	Suppress             bool     `yaml:"suppress,omitempty" json:"suppress,omitempty"`
	RequireApproval      bool     `yaml:"requireApproval,omitempty" json:"requireApproval,omitempty"`
	AssignTeam           string   `yaml:"assignTeam,omitempty" json:"assignTeam,omitempty"`
	EscalationPolicy     string   `yaml:"escalationPolicy,omitempty" json:"escalationPolicy,omitempty"`
	NotificationChannels []string `yaml:"notificationChannels,omitempty" json:"notificationChannels,omitempty"`
	Runbook              string   `yaml:"runbook,omitempty" json:"runbook,omitempty"`
}
type Decision struct {
	Matched       bool              `json:"matched"`
	PolicyID      string            `json:"policy_id"`
	PolicyVersion int               `json:"policy_version"`
	Actions       Actions           `json:"actions"`
	Explanation   []string          `json:"explanation"`
	Inputs        map[string]string `json:"inputs"`
}

func Parse(data []byte) (Document, error) {
	var d Document
	dec := yaml.NewDecoder(strings.NewReader(string(data)))
	dec.KnownFields(true)
	if err := dec.Decode(&d); err != nil {
		return Document{}, fmt.Errorf("decode policy: %w", err)
	}
	if err := d.Validate(); err != nil {
		return Document{}, err
	}
	return d, nil
}

func (d Document) Validate() error {
	if d.APIVersion != "stormrelay.io/"+SchemaVersion {
		return fmt.Errorf("apiVersion must be stormrelay.io/%s", SchemaVersion)
	}
	if d.Kind != "Policy" {
		return fmt.Errorf("kind must be Policy")
	}
	if strings.TrimSpace(d.Metadata.ID) == "" || d.Metadata.Version < 1 {
		return fmt.Errorf("metadata.id and positive metadata.version are required")
	}
	if err := validateExpression(d.Spec.Match); err != nil {
		return fmt.Errorf("spec.match: %w", err)
	}
	return nil
}

func validateExpression(e Expression) error {
	count := 0
	if len(e.All) > 0 {
		count++
	}
	if len(e.Any) > 0 {
		count++
	}
	if e.Not != nil {
		count++
	}
	if e.Field != "" {
		count++
	}
	if count != 1 {
		return fmt.Errorf("exactly one of all, any, not or field must be set")
	}
	if len(e.All) > 0 {
		for i, child := range e.All {
			if err := validateExpression(child); err != nil {
				return fmt.Errorf("all[%d]: %w", i, err)
			}
		}
	}
	if len(e.Any) > 0 {
		for i, child := range e.Any {
			if err := validateExpression(child); err != nil {
				return fmt.Errorf("any[%d]: %w", i, err)
			}
		}
	}
	if e.Not != nil {
		return validateExpression(*e.Not)
	}
	if e.Field != "" && isSensitiveField(e.Field) {
		return fmt.Errorf("sensitive fields cannot be used in policy expressions")
	}
	if e.Field != "" && e.Equals == "" && len(e.In) == 0 {
		return fmt.Errorf("field expression requires equals or in")
	}
	if e.Equals != "" && len(e.In) > 0 {
		return fmt.Errorf("equals and in are mutually exclusive")
	}
	return nil
}

func Evaluate(d Document, event events.Event) Decision {
	allInputs := eventInputs(event)
	matched, explanation := eval(d.Spec.Match, allInputs)
	referenced := map[string]struct{}{}
	collectFields(d.Spec.Match, referenced)
	inputs := make(map[string]string, len(referenced))
	for field := range referenced {
		inputs[field] = sanitizeDecisionInput(field, allInputs[field])
	}
	decision := Decision{Matched: matched, PolicyID: d.Metadata.ID, PolicyVersion: d.Metadata.Version, Inputs: inputs, Explanation: explanation}
	if matched {
		decision.Actions = d.Spec.Actions
	}
	return decision
}

func eval(e Expression, inputs map[string]string) (bool, []string) {
	if len(e.All) > 0 {
		exp := []string{}
		for _, child := range e.All {
			ok, childExp := eval(child, inputs)
			exp = append(exp, childExp...)
			if !ok {
				return false, append(exp, "all expression did not match")
			}
		}
		return true, append(exp, "all expression matched")
	}
	if len(e.Any) > 0 {
		exp := []string{}
		for _, child := range e.Any {
			ok, childExp := eval(child, inputs)
			exp = append(exp, childExp...)
			if ok {
				return true, append(exp, "any expression matched")
			}
		}
		return false, append(exp, "any expression did not match")
	}
	if e.Not != nil {
		ok, exp := eval(*e.Not, inputs)
		return !ok, append(exp, fmt.Sprintf("not result=%t", !ok))
	}
	actual := inputs[e.Field]
	if e.Equals != "" {
		ok := actual == e.Equals
		return ok, []string{fmt.Sprintf("%s equals %q: %t", e.Field, e.Equals, ok)}
	}
	for _, allowed := range e.In {
		if actual == allowed {
			return true, []string{fmt.Sprintf("%s in %v: true", e.Field, e.In)}
		}
	}
	return false, []string{fmt.Sprintf("%s in %v: false", e.Field, e.In)}
}

func eventInputs(e events.Event) map[string]string {
	out := map[string]string{"severity": string(e.Severity), "type": e.Type, "source": e.Source, "subject": e.Subject}
	for k, v := range e.Labels {
		out["labels."+k] = v
	}
	if env := e.Labels["environment"]; env != "" {
		out["environment"] = env
	}
	return out
}

func collectFields(e Expression, out map[string]struct{}) {
	if e.Field != "" {
		out[e.Field] = struct{}{}
	}
	for _, child := range e.All {
		collectFields(child, out)
	}
	for _, child := range e.Any {
		collectFields(child, out)
	}
	if e.Not != nil {
		collectFields(*e.Not, out)
	}
}
func sanitizeDecisionInput(field, value string) string {
	lower := strings.ToLower(field)
	for _, sensitive := range []string{"secret", "token", "password", "authorization", "cookie", "signature", "credential"} {
		if strings.Contains(lower, sensitive) {
			return "[REDACTED]"
		}
	}
	if len(value) > 256 {
		return value[:256]
	}
	return value
}

func isSensitiveField(field string) bool {
	lower := strings.ToLower(field)
	for _, sensitive := range []string{"secret", "token", "password", "authorization", "cookie", "signature", "credential"} {
		if strings.Contains(lower, sensitive) {
			return true
		}
	}
	return false
}
