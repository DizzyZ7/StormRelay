package config

import (
	"encoding/base64"
	"fmt"
	"math"
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
	EventMaxDeliveries          int
	EventRetryBaseDelay         time.Duration
	EventRetryMaxDelay          time.Duration
	RunbookConcurrency          int
	AutoMigrate                 bool
	TelegramToken               string
	AllowUnauthenticatedSources bool
	RunbookHTTPAllowedHosts     []string
	PluginAllowedHosts          []string
	OTLPTraceEndpoint           string
	OTelTraceSampleRatio        float64
	OTelTraceExportTimeout      time.Duration
}

func Load(serviceName, version string) (Config, error) {
	master, err := decodeMasterKey(os.Getenv("STORMRELAY_MASTER_KEY"))
	if err != nil {
		return Config{}, err
	}
	traceSampleRatio, err := envFloatStrict("STORMRELAY_OTEL_TRACE_SAMPLE_RATIO", 0.10)
	if err != nil {
		return Config{}, err
	}
	traceExportTimeout, err := envDurationStrict("STORMRELAY_OTEL_EXPORT_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	eventRetryBaseDelay, err := envDurationStrict("STORMRELAY_EVENT_RETRY_BASE_DELAY", time.Second)
	if err != nil {
		return Config{}, err
	}
	eventRetryMaxDelay, err := envDurationStrict("STORMRELAY_EVENT_RETRY_MAX_DELAY", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxPayloadBytes, err := envIntStrict("STORMRELAY_MAX_PAYLOAD_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	replayWindow, err := envDurationStrict("STORMRELAY_REPLAY_WINDOW", 5*time.Minute)
	if err != nil {
		return Config{}, err
	}
	dedupeWindow, err := envDurationStrict("STORMRELAY_DEDUPE_WINDOW", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	correlationWindow, err := envDurationStrict("STORMRELAY_CORRELATION_WINDOW", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	workerConcurrency, err := envIntStrict("STORMRELAY_WORKER_CONCURRENCY", 8)
	if err != nil {
		return Config{}, err
	}
	eventMaxDeliveries, err := envIntStrict("STORMRELAY_EVENT_MAX_DELIVERIES", 5)
	if err != nil {
		return Config{}, err
	}
	runbookConcurrency, err := envIntStrict("STORMRELAY_RUNBOOK_CONCURRENCY", 4)
	if err != nil {
		return Config{}, err
	}
	autoMigrate, err := envBoolStrict("STORMRELAY_AUTO_MIGRATE", true)
	if err != nil {
		return Config{}, err
	}
	allowUnauthenticatedSources, err := envBoolStrict("STORMRELAY_ALLOW_UNAUTHENTICATED_SOURCES", false)
	if err != nil {
		return Config{}, err
	}
	traceEndpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"))
	if traceEndpoint == "" {
		traceEndpoint = strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
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
		MaxPayloadBytes:             int64(maxPayloadBytes),
		ReplayWindow:                replayWindow,
		DedupeWindow:                dedupeWindow,
		CorrelationWindow:           correlationWindow,
		WorkerConcurrency:           workerConcurrency,
		EventMaxDeliveries:          eventMaxDeliveries,
		EventRetryBaseDelay:         eventRetryBaseDelay,
		EventRetryMaxDelay:          eventRetryMaxDelay,
		RunbookConcurrency:          runbookConcurrency,
		AutoMigrate:                 autoMigrate,
		TelegramToken:               os.Getenv("STORMRELAY_TELEGRAM_BOT_TOKEN"),
		AllowUnauthenticatedSources: allowUnauthenticatedSources,
		RunbookHTTPAllowedHosts:     envCSV("STORMRELAY_RUNBOOK_HTTP_ALLOWED_HOSTS"),
		PluginAllowedHosts:          envCSV("STORMRELAY_PLUGIN_ALLOWED_HOSTS"),
		OTLPTraceEndpoint:           traceEndpoint,
		OTelTraceSampleRatio:        traceSampleRatio,
		OTelTraceExportTimeout:      traceExportTimeout,
	}
	if cfg.BootstrapAPIKey == "" {
		return Config{}, fmt.Errorf("STORMRELAY_BOOTSTRAP_API_KEY is required")
	}
	if cfg.MaxPayloadBytes < 1 || cfg.MaxPayloadBytes > 64<<20 {
		return Config{}, fmt.Errorf("STORMRELAY_MAX_PAYLOAD_BYTES must be between 1 and 67108864")
	}
	if cfg.ReplayWindow <= 0 || cfg.DedupeWindow <= 0 || cfg.CorrelationWindow <= 0 {
		return Config{}, fmt.Errorf("replay, dedupe, and correlation windows must be positive")
	}
	if cfg.WorkerConcurrency < 1 || cfg.WorkerConcurrency > 128 {
		return Config{}, fmt.Errorf("worker concurrency must be between 1 and 128")
	}
	if cfg.EventMaxDeliveries < 2 || cfg.EventMaxDeliveries > 100 {
		return Config{}, fmt.Errorf("STORMRELAY_EVENT_MAX_DELIVERIES must be between 2 and 100")
	}
	if cfg.EventRetryBaseDelay <= 0 || cfg.EventRetryBaseDelay > time.Minute {
		return Config{}, fmt.Errorf("STORMRELAY_EVENT_RETRY_BASE_DELAY must be greater than zero and at most one minute")
	}
	if cfg.EventRetryMaxDelay < cfg.EventRetryBaseDelay || cfg.EventRetryMaxDelay > 15*time.Minute {
		return Config{}, fmt.Errorf("STORMRELAY_EVENT_RETRY_MAX_DELAY must be at least the base delay and at most fifteen minutes")
	}
	if cfg.RunbookConcurrency < 1 || cfg.RunbookConcurrency > 64 {
		return Config{}, fmt.Errorf("runbook concurrency must be between 1 and 64")
	}
	if cfg.OTelTraceSampleRatio < 0 || cfg.OTelTraceSampleRatio > 1 {
		return Config{}, fmt.Errorf("STORMRELAY_OTEL_TRACE_SAMPLE_RATIO must be between 0 and 1")
	}
	if cfg.OTelTraceExportTimeout <= 0 || cfg.OTelTraceExportTimeout > time.Minute {
		return Config{}, fmt.Errorf("STORMRELAY_OTEL_EXPORT_TIMEOUT must be greater than zero and at most one minute")
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
func envIntStrict(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", name, err)
	}
	return parsed, nil
}

func envFloatStrict(name string, fallback float64) (float64, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", name, err)
	}
	if math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return 0, fmt.Errorf("%s must be a finite number", name)
	}
	return parsed, nil
}
func envBoolStrict(name string, fallback bool) (bool, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return false, fmt.Errorf("%s must be a boolean: %w", name, err)
	}
	return parsed, nil
}

func envDurationStrict(name string, fallback time.Duration) (time.Duration, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a Go duration: %w", name, err)
	}
	return parsed, nil
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
