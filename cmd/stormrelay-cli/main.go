package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var version = "dev"

type cliConfig struct {
	URL    string `json:"url"`
	APIKey string `json:"api_key"`
}
type client struct {
	cfg  cliConfig
	http *http.Client
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
func run(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "login":
		return login(args[1:])
	case "version":
		fmt.Println(version)
		return nil
	}
	cfg, err := loadConfig()
	if err != nil {
		return err
	}
	c := client{cfg: cfg, http: &http.Client{Timeout: 15 * time.Second}}
	switch args[0] {
	case "server":
		return c.server(args[1:])
	case "doctor":
		return c.doctor()
	case "sources":
		return c.sources(args[1:])
	case "incidents":
		return c.incidents(args[1:])
	case "policies":
		return c.policies(args[1:])
	case "audit":
		return c.audit(args[1:])
	default:
		return usage()
	}
}
func usage() error {
	fmt.Print(`stormrelay - StormRelay command line client

Commands:
  login --url URL --api-key KEY
  server version
  doctor
  sources list
  sources create --name NAME [--kind generic] [--auth hmac-sha256]
  sources test SOURCE_ID
  incidents create --title TITLE [--severity warning] [--service NAME] [--environment NAME]
  incidents list [--state detected]
  incidents show INCIDENT_ID
  incidents ack INCIDENT_ID --version N [--reason TEXT]
  incidents resolve INCIDENT_ID --version N [--reason TEXT]
  policies list
  policies validate FILE
  policies apply FILE
  audit export [--after RFC3339] [--output FILE]
  version
`)
	return nil
}
func login(args []string) error {
	fs := flag.NewFlagSet("login", flag.ContinueOnError)
	url := fs.String("url", "http://localhost:8080", "server URL")
	key := fs.String("api-key", "", "API key")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *key == "" {
		return errors.New("--api-key is required")
	}
	cfg := cliConfig{URL: strings.TrimRight(*url, "/"), APIKey: *key}
	path, err := configPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(path, data, 0600); err != nil {
		return err
	}
	fmt.Println("configuration saved to", path)
	return nil
}
func loadConfig() (cliConfig, error) {
	path, err := configPath()
	if err != nil {
		return cliConfig{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cliConfig{}, fmt.Errorf("read config (run stormrelay login): %w", err)
	}
	var cfg cliConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return cliConfig{}, err
	}
	if cfg.URL == "" || cfg.APIKey == "" {
		return cliConfig{}, errors.New("invalid CLI config")
	}
	return cfg, nil
}
func configPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "stormrelay", "config.json"), nil
}
func (c client) request(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.cfg.URL+path, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(data)))
	}
	return data, nil
}
func printJSON(data []byte) error {
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return err
	}
	pretty, _ := json.MarshalIndent(value, "", "  ")
	fmt.Println(string(pretty))
	return nil
}
func (c client) server(args []string) error {
	if len(args) != 1 || args[0] != "version" {
		return usage()
	}
	data, err := c.request(context.Background(), http.MethodGet, "/api/v1/version", "", nil)
	if err != nil {
		return err
	}
	return printJSON(data)
}
func (c client) doctor() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	data, err := c.request(ctx, http.MethodGet, "/readyz", "", nil)
	if err != nil {
		return fmt.Errorf("API/readiness check failed: %w", err)
	}
	fmt.Println("API connectivity: ok")
	if err := printJSON(data); err != nil {
		return err
	}
	versionData, err := c.request(ctx, http.MethodGet, "/api/v1/version", "", nil)
	if err != nil {
		return err
	}
	fmt.Println("Version compatibility:")
	return printJSON(versionData)
}
func (c client) sources(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "list":
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/sources", "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "test":
		if len(args) != 2 {
			return usage()
		}
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/sources/"+args[1]+"/test", "application/json", []byte(`{}`))
		if err != nil {
			return err
		}
		return printJSON(data)
	case "create":
		fs := flag.NewFlagSet("sources create", flag.ContinueOnError)
		name := fs.String("name", "", "source name")
		kind := fs.String("kind", "generic", "source kind")
		auth := fs.String("auth", "hmac-sha256", "auth mode")
		rate := fs.Int("rate", 20, "events per second")
		burst := fs.Int("burst", 40, "burst size")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *name == "" {
			return errors.New("--name is required")
		}
		body, _ := json.Marshal(map[string]any{"name": *name, "kind": *kind, "auth_mode": *auth, "rate_limit_per_second": *rate, "rate_limit_burst": *burst})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/sources", "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	default:
		return usage()
	}
}
func (c client) incidents(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "create":
		fs := flag.NewFlagSet("incidents create", flag.ContinueOnError)
		title := fs.String("title", "", "incident title")
		severity := fs.String("severity", "warning", "severity")
		service := fs.String("service", "", "service")
		environment := fs.String("environment", "", "environment")
		resource := fs.String("resource", "", "resource")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *title == "" {
			return errors.New("--title is required")
		}
		body, _ := json.Marshal(map[string]any{"title": *title, "severity": *severity, "service": *service, "environment": *environment, "resource": *resource})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/incidents", "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "list":
		fs := flag.NewFlagSet("incidents list", flag.ContinueOnError)
		state := fs.String("state", "", "state filter")
		severity := fs.String("severity", "", "severity filter")
		service := fs.String("service", "", "service filter")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		path := fmt.Sprintf("/api/v1/incidents?state=%s&severity=%s&service=%s", *state, *severity, *service)
		data, err := c.request(context.Background(), http.MethodGet, path, "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "show":
		if len(args) != 2 {
			return usage()
		}
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/incidents/"+args[1], "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "ack", "resolve":
		if len(args) < 2 {
			return usage()
		}
		fs := flag.NewFlagSet("incidents "+args[0], flag.ContinueOnError)
		ver := fs.Int64("version", 0, "expected incident version")
		reason := fs.String("reason", "", "transition reason")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *ver < 1 {
			return errors.New("--version is required")
		}
		body, _ := json.Marshal(map[string]any{"version": *ver, "reason": *reason})
		data, err := c.request(context.Background(), http.MethodPost, "/api/v1/incidents/"+args[1]+"/"+args[0], "application/json", body)
		if err != nil {
			return err
		}
		return printJSON(data)
	default:
		return usage()
	}
}
func (c client) policies(args []string) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "list":
		data, err := c.request(context.Background(), http.MethodGet, "/api/v1/policies", "", nil)
		if err != nil {
			return err
		}
		return printJSON(data)
	case "validate", "apply":
		if len(args) != 2 {
			return usage()
		}
		data, err := os.ReadFile(args[1])
		if err != nil {
			return err
		}
		path := "/api/v1/policies/validate"
		if args[0] == "apply" {
			path = "/api/v1/policies"
		}
		response, err := c.request(context.Background(), http.MethodPost, path, "application/yaml", data)
		if err != nil {
			return err
		}
		return printJSON(response)
	default:
		return usage()
	}
}
func (c client) audit(args []string) error {
	if len(args) == 0 || args[0] != "export" {
		return usage()
	}
	fs := flag.NewFlagSet("audit export", flag.ContinueOnError)
	after := fs.String("after", "", "RFC3339 lower bound")
	output := fs.String("output", "", "output file (default stdout)")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	data, err := c.request(context.Background(), http.MethodGet, "/api/v1/audit/export?after="+*after, "", nil)
	if err != nil {
		return err
	}
	if *output == "" {
		_, err = os.Stdout.Write(data)
		return err
	}
	return os.WriteFile(*output, data, 0600)
}
