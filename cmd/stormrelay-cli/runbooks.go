package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"net/http"
	"os"
)

func (c client) runbooks(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "list":
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/runbooks", "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "validate", "apply":
		if len(args) != 2 {
			return usage()
		}
		body, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		path := "/api/v1/runbooks/validate"
		if args[0] == "apply" {
			path = "/api/v1/runbooks"
		}
		data, err := c.request(context.Background(), http.MethodPost, path, "application/yaml", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "run":
		if len(args) < 2 {
			return usage()
		}
		fs := flag.NewFlagSet("runbooks run", flag.ContinueOnError)
		incidentID := fs.String("incident", "", "incident ID")
		dryRun := fs.Bool("dry-run", false, "do not perform external actions")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		body, _ := json.Marshal(map[string]any{"incident_id": *incidentID, "dry_run": *dryRun})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/runbooks/"+args[1]+"/run", "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	default:
		return usage()
	}
}

func (c client) executions(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "list":
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/executions", "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "show":
		if len(args) != 2 {
			return usage()
		}
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/executions/"+args[1], "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "pause", "resume", "cancel", "rollback":
		if len(args) != 2 {
			return usage()
		}
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/executions/"+args[1]+"/"+args[0], "application/json", []byte(`{}`))
		if err != nil {
			return err
		}
		return printJSON(data)
	case "retry":
		if len(args) < 2 {
			return usage()
		}
		fs := flag.NewFlagSet("executions retry", flag.ContinueOnError)
		step := fs.String("step", "", "execution step ID")
		force := fs.Bool("force", false, "retry an ambiguous non-idempotent step")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *step == "" {
			return errors.New("--step is required")
		}
		body, _ := json.Marshal(map[string]any{"force": *force})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/executions/"+args[1]+"/steps/"+*step+"/retry", "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	default:
		return usage()
	}
}

func (c client) approvals(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	if args[0] == "list" {
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/approvals", "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	}
	if args[0] != "approve" && args[0] != "reject" {
		return usage()
	}
	if len(args) < 2 {
		return usage()
	}
	fs := flag.NewFlagSet("approvals "+args[0], flag.ContinueOnError)
	reason := fs.String("reason", "", "decision reason")
	if err := fs.Parse(args[2:]); err != nil {
		return err
	}
	body, _ := json.Marshal(map[string]any{"reason": *reason})
	data, err := c.request(context.Background(), http.MethodPost, "/api/v1/approvals/"+args[1]+"/"+args[0], "application/json", body)
	if err != nil {
		return err
	}
	return printJSON(data)
}

func (c client) plugins(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "list":
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/plugins", "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "test":
		if len(args) < 2 {
			return usage()
		}
		fs := flag.NewFlagSet("plugins test", flag.ContinueOnError)
		action := fs.String("action", "", "plugin action (defaults to first declared action)")
		input := fs.String("input", "{}", "JSON action input")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		var raw json.RawMessage = json.RawMessage(*input)
		if !json.Valid(raw) {
			return errors.New("--input must be valid JSON")
		}
		body, _ := json.Marshal(map[string]any{"action": *action, "input": raw})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/plugins/"+args[1]+"/test", "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "register":
		fs := flag.NewFlagSet("plugins register", flag.ContinueOnError)
		key := fs.String("key", "", "plugin key")
		endpoint := fs.String("endpoint", "", "plugin base URL")
		token := fs.String("token", "", "optional bearer token")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *key == "" || *endpoint == "" {
			return errors.New("--key and --endpoint are required")
		}
		authMode := "none"
		if *token != "" {
			authMode = "bearer"
		}
		body, _ := json.Marshal(map[string]any{"key": *key, "endpoint": *endpoint, "auth_mode": authMode, "bearer_token": *token})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/plugins", "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	default:
		return usage()
	}
}
