package config

import (
	"encoding/base64"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	ServiceName                 string
	Version                     string
	HTTPAddress                 string
	WorkerHTTPAddress           string
	DatabaseURL                 string
	NATSURL                     string
	NATSStream                  string
	NATSSubject                 string
	NATSConsumer                string
	MasterKey                   []byte
	BootstrapAPIKey             string
	PublicBaseURL               string
	DefaultTenantID             string
	MaxPayloadBytes             int64
	ReplayWindow                time.Duration
	DedupeWindow                time.Duration
	CorrelationWindow           time.Duration
	WorkerConcurrency           int
	RunbookConcurrency          int
	AutoMigrate                 bool
	TelegramToken               string
	AllowUnauthenticatedSources bool
	RunbookHTTPAllowedHosts     []string
	PluginAllowedHosts          []string
}

func Load(serviceName, version string) (Config, error) {
	master, err := decodeMasterKey(os.Getenv("STORMRELAY_MASTER_KEY"))
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		ServiceName:                 serviceName,
		Version:                     version,
		HTTPAddress:                 env("STORMRELAY_HTTP_ADDRESS", ":8080"),
		WorkerHTTPAddress:           env("STORMRELAY_WORKER_HTTP_ADDRESS", ":8081"),
		DatabaseURL:                 env("STORMRELAY_DATABASE_URL", "postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable"),
		NATSURL:                     env("STORMRELAY_NATS_URL", "nats://localhost:4222"),
		NATSStream:                  env("STORMRELAY_NATS_STREAM", "STORMRELAY_EVENTS"),
		NATSSubject:                 env("STORMRELAY_NATS_SUBJECT", "stormrelay.events.v1"),
		NATSConsumer:                env("STORMRELAY_NATS_CONSUMER", "stormrelay-worker-v1"),
		MasterKey:                   master,
		BootstrapAPIKey:             os.Getenv("STORMRELAY_BOOTSTRAP_API_KEY"),
		PublicBaseURL:               env("STORMRELAY_PUBLIC_BASE_URL", "http://localhost:8080"),
		DefaultTenantID:             env("STORMRELAY_DEFAULT_TENANT_ID", "00000000-0000-4000-8000-000000000001"),
		MaxPayloadBytes:             int64(envInt("STORMRELAY_MAX_PAYLOAD_BYTES", 1<<20)),
		ReplayWindow:                envDuration("STORMRELAY_REPLAY_WINDOW", 5*time.Minute),
		DedupeWindow:                envDuration("STORMRELAY_DEDUPE_WINDOW", 15*time.Minute),
		CorrelationWindow:           envDuration("STORMRELAY_CORRELATION_WINDOW", 30*time.Minute),
		WorkerConcurrency:           envInt("STORMRELAY_WORKER_CONCURRENCY", 8),
		RunbookConcurrency:          envInt("STORMRELAY_RUNBOOK_CONCURRENCY", 4),
		AutoMigrate:                 envBool("STORMRELAY_AUTO_MIGRATE", true),
		TelegramToken:               os.Getenv("STORMRELAY_TELEGRAM_BOT_TOKEN"),
		AllowUnauthenticatedSources: envBool("STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES", false),
		RunbookHTTPAllowedHosts:     envCSV("STORMRELAY_RUNBOOK_HTTP_ALLOWED_HOSTS"),
		PluginAllowedHosts:          envCSV("STORMRELAY_PLUGIN_ALLOWED_HOSTS"),
	}
	if cfg.BootstrapAPIKey == "" {
		return Config{}, fmt.Errorf("STORMRELAY_BOOTSTRAP_API_KEY is required")
	}
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 128 {
		return Config{}, fmt.Errorf("worker concurrency must be between 1 and 128")
	}
	if cfg.RunbookConcurrency < 1 || cfg.RunbookConcurrency > 64 {
		return Config{}, fmt.Errorf("runbook concurrency must be between 1 and 64")
	}
	return cfg, nil
}

func decodeMasterKey(value string) ([]byte, error) {
	if value == "" {
		return nil, fmt.Errorf("STORMRELAY_MASTER_KEY is required (base64 encoded 32 bytes)")
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("STORMRELAY_MASTER_KEY must be base64 encoded 32 bytes")
	}
	return key, nil
}
func env(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}
func envInt(name string, fallback int) int {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
func envBool(name string, fallback bool) bool {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return fallback
	}
	return b
}
func envDuration(name string, fallback time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(name))
	if v == "" {
		return fallback
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return fallback
	}
	return d
}
func envCSV(name string) []string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.ToLower(strings.TrimSpace(part))
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
