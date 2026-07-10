package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	pluginprotocol "github.com/DizzyZ7/StormRelay/internal/plugins"
)

var version = "dev"

type report struct {
	Endpoint        string          `json:"endpoint"`
	PluginID        string          `json:"plugin_id"`
	PluginVersion   string          `json:"plugin_version"`
	ProtocolVersion string          `json:"protocol_version"`
	Action          string          `json:"action"`
	Status          string          `json:"status"`
	Output          json.RawMessage `json:"output,omitempty"`
}

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "conformance failed:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	flags := flag.NewFlagSet("stormrelay-plugin-conformance", flag.ContinueOnError)
	endpoint := flags.String("endpoint", "", "absolute plugin base URL")
	action := flags.String("action", "", "declared action to invoke; defaults to first action")
	inputText := flags.String("input", `{}`, "JSON action input")
	bearer := flags.String("bearer-token", "", "optional plugin bearer token")
	timeout := flags.Duration("timeout", 15*time.Second, "overall conformance timeout")
	showVersion := flags.Bool("version", false, "print runner version")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println(version)
		return nil
	}
	parsed, err := url.Parse(strings.TrimSpace(*endpoint))
	if err != nil || parsed.Scheme == "" || parsed.Hostname() == "" {
		return errors.New("--endpoint must be an absolute HTTP(S) URL")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return errors.New("--endpoint must not contain userinfo, query, or fragment")
	}
	if *timeout <= 0 || *timeout > 2*time.Minute {
		return errors.New("--timeout must be greater than zero and at most two minutes")
	}
	var input any
	decoder := json.NewDecoder(strings.NewReader(*inputText))
	decoder.UseNumber()
	if err := decoder.Decode(&input); err != nil {
		return fmt.Errorf("decode --input: %w", err)
	}
	if decoder.More() {
		return errors.New("--input must contain one JSON value")
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	client := pluginprotocol.NewClient([]string{parsed.Hostname()})
	manifest, err := client.Discover(ctx, parsed.String(), strings.TrimSpace(*bearer))
	if err != nil {
		return fmt.Errorf("manifest discovery: %w", err)
	}
	selectedAction := strings.TrimSpace(*action)
	if selectedAction == "" {
		if len(manifest.Actions) == 0 {
			return errors.New("manifest declares no actions")
		}
		selectedAction = manifest.Actions[0].Name
	}
	declared := false
	for _, item := range manifest.Actions {
		if item.Name == selectedAction {
			declared = true
			break
		}
	}
	if !declared {
		return fmt.Errorf("action %q is not declared by the manifest", selectedAction)
	}
	requestID := fmt.Sprintf("conformance-%d", time.Now().UnixNano())
	idempotencyKey := requestID + ":idempotency"
	response, err := client.Call(ctx, parsed.String(), strings.TrimSpace(*bearer), selectedAction, pluginprotocol.Request{
		ProtocolVersion: pluginprotocol.ProtocolVersion,
		ExecutionID:     requestID + ":execution",
		StepID:          requestID + ":step",
		RequestID:       requestID,
		Deadline:        time.Now().Add(*timeout).UTC(),
		IdempotencyKey:  idempotencyKey,
		Input:           input,
	})
	if err != nil {
		return fmt.Errorf("action invocation: %w", err)
	}
	encoded, err := json.MarshalIndent(report{
		Endpoint: parsed.String(), PluginID: manifest.PluginID, PluginVersion: manifest.Version,
		ProtocolVersion: manifest.ProtocolVersion, Action: selectedAction,
		Status: response.Status, Output: response.Output,
	}, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(encoded))
	return nil
}
