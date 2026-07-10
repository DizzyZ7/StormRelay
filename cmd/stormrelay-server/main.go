package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/api"
	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
func run() error {
	cfg, err := config.Load("stormrelay-server", version)
	if err != nil {
		return err
	}
	logger := telemetry.NewLogger(cfg.ServiceName, cfg.Version)
	slog.SetDefault(logger)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	tracing, err := telemetry.SetupTracing(ctx, telemetry.TracingConfig{
		ServiceName:   cfg.ServiceName,
		Version:       cfg.Version,
		Endpoint:      cfg.OTLPTraceEndpoint,
		SampleRatio:   cfg.OTelTraceSampleRatio,
		ExportTimeout: cfg.OTelTraceExportTimeout,
	}, logger)
	if err != nil {
		return err
	}
	defer func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		if shutdownErr := tracing.Shutdown(shutdownCtx); shutdownErr != nil {
			logger.Error("flush traces failed", "error", shutdownErr)
		}
	}()
	box, err := cryptox.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	store, err := storage.Open(ctx, cfg.DatabaseURL, box, logger)
	if err != nil {
		return err
	}
	defer store.Close()
	if cfg.AutoMigrate {
		if err := store.Migrate(ctx); err != nil {
			return err
		}
	}
	bus, err := messaging.Connect(cfg.NATSURL, cfg.NATSStream, cfg.NATSSubject, cfg.NATSConsumer, cfg.ServiceName)
	if err != nil {
		return err
	}
	defer bus.Close()
	metrics := &telemetry.Metrics{}
	handler := telemetry.HTTPMiddleware(api.New(cfg, store, bus, metrics, logger).Handler(), nil)
	server := &http.Server{Addr: cfg.HTTPAddress, Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 30 * time.Second, IdleTimeout: 60 * time.Second, MaxHeaderBytes: 32 << 10}
	errCh := make(chan error, 1)
	go func() {
		logger.Info("server listening", "address", cfg.HTTPAddress, "otlp_traces_enabled", cfg.OTLPTraceEndpoint != "")
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}
