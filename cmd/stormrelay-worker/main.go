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

	"github.com/DizzyZ7/StormRelay/internal/config"
	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/notifications"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/DizzyZ7/StormRelay/internal/telemetry"
	"github.com/DizzyZ7/StormRelay/internal/worker"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("worker stopped", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load("stormrelay-worker", version)
	if err != nil {
		return err
	}
	logger := telemetry.NewLogger(cfg.ServiceName, cfg.Version)
	slog.SetDefault(logger)
	box, err := cryptox.NewBox(cfg.MasterKey)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
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
	notifier := notifications.New(logger, cfg.TelegramToken)
	w := worker.New(cfg, store, bus, notifier, metrics, logger)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(rw http.ResponseWriter, _ *http.Request) {
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /readyz", func(rw http.ResponseWriter, req *http.Request) {
		checkCtx, checkCancel := context.WithTimeout(req.Context(), 2*time.Second)
		defer checkCancel()
		if err := store.Ping(checkCtx); err != nil {
			http.Error(rw, `{"ready":false}`, http.StatusServiceUnavailable)
			return
		}
		if migration, migrationErr := store.MigrationVersion(checkCtx); migrationErr != nil || migration != storage.ExpectedMigrationVersion {
			http.Error(rw, `{"ready":false}`, http.StatusServiceUnavailable)
			return
		}
		if err := bus.Ready(); err != nil {
			http.Error(rw, `{"ready":false}`, http.StatusServiceUnavailable)
			return
		}
		rw.Header().Set("Content-Type", "application/json")
		_, _ = rw.Write([]byte(`{"ready":true}`))
	})
	mux.HandleFunc("GET /metrics", func(rw http.ResponseWriter, req *http.Request) {
		acquired, _, max := store.PoolStats()
		metrics.DBPoolAcquired.Store(int64(acquired))
		metrics.DBPoolMax.Store(int64(max))
		metrics.JetStreamConsumerLag.Store(bus.ConsumerLag())
		if count, countErr := store.CountOpenIncidents(req.Context(), cfg.DefaultTenantID); countErr == nil {
			metrics.OpenIncidents.Store(count)
		}
		rw.Header().Set("Content-Type", "text/plain; version=0.0.4")
		metrics.WritePrometheus(rw)
	})
	healthServer := &http.Server{Addr: cfg.WorkerHTTPAddress, Handler: mux, ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second}
	healthErr := make(chan error, 1)
	go func() {
		logger.Info("worker health server listening", "address", cfg.WorkerHTTPAddress)
		healthErr <- healthServer.ListenAndServe()
	}()
	workerErr := make(chan error, 1)
	go func() { workerErr <- w.Run(ctx) }()
	select {
	case <-ctx.Done():
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = healthServer.Shutdown(shutdownCtx)
		return <-workerErr
	case err := <-healthErr:
		cancel()
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-workerErr:
		cancel()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer shutdownCancel()
		_ = healthServer.Shutdown(shutdownCtx)
		return err
	}
}
