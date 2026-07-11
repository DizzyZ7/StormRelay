//go:build benchmark

package load

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/storage"
)

const benchmarkTenantID = "00000000-0000-4000-8000-000000000001"

var benchmarkProcessResultSink storage.ProcessResult

func BenchmarkProcessEventNewIncident(b *testing.B) {
	store, sourceID := benchmarkStoreAndSource(b, "new-incident")
	base := time.Now().UTC().Truncate(15 * time.Minute).Add(time.Minute)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := benchmarkEvent(sourceID, "new-"+strconv.Itoa(i), "resource-"+strconv.Itoa(i), base.Add(time.Duration(i)*time.Millisecond))
		result, err := store.ProcessEvent(context.Background(), event, benchmarkProcessOptions())
		if err != nil {
			b.Fatal(err)
		}
		benchmarkProcessResultSink = result
	}
}

func BenchmarkProcessEventCorrelated(b *testing.B) {
	store, sourceID := benchmarkStoreAndSource(b, "correlated")
	base := time.Now().UTC().Truncate(15 * time.Minute).Add(time.Minute)
	first := benchmarkEvent(sourceID, "correlated-seed", "shared-resource", base)
	if _, err := store.ProcessEvent(context.Background(), first, benchmarkProcessOptions()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event := benchmarkEvent(sourceID, "correlated-"+strconv.Itoa(i), "shared-resource", base.Add(time.Duration(i+1)*time.Millisecond))
		result, err := store.ProcessEvent(context.Background(), event, benchmarkProcessOptions())
		if err != nil {
			b.Fatal(err)
		}
		benchmarkProcessResultSink = result
	}
}

func BenchmarkProcessEventDuplicate(b *testing.B) {
	store, sourceID := benchmarkStoreAndSource(b, "duplicate")
	base := time.Now().UTC().Truncate(15 * time.Minute).Add(time.Minute)
	canonical := benchmarkEvent(sourceID, "duplicate-source-event", "duplicate-resource", base)
	if _, err := store.ProcessEvent(context.Background(), canonical, benchmarkProcessOptions()); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		duplicate := canonical
		duplicate.ID = "duplicate-delivery-" + strconv.Itoa(i)
		duplicate.RequestID = "benchmark-duplicate-" + strconv.Itoa(i)
		result, err := store.ProcessEvent(context.Background(), duplicate, benchmarkProcessOptions())
		if err != nil {
			b.Fatal(err)
		}
		if !result.Duplicate {
			b.Fatal("expected duplicate result")
		}
		benchmarkProcessResultSink = result
	}
}

func benchmarkStoreAndSource(b *testing.B, suffix string) (*storage.Store, string) {
	b.Helper()
	box, err := cryptox.NewBox(make([]byte, 32))
	if err != nil {
		b.Fatal(err)
	}
	store, err := storage.Open(context.Background(), benchmarkDatabaseURL(), box, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		b.Fatal(err)
	}
	b.Cleanup(store.Close)
	if err := store.Migrate(context.Background()); err != nil {
		b.Fatal(err)
	}
	created, err := store.CreateSource(context.Background(), storage.CreateSourceInput{
		TenantID: benchmarkTenantID,
		Name:     fmt.Sprintf("benchmark-%s-%d", suffix, time.Now().UnixNano()),
		Kind:     "generic",
		AuthMode: ingestion.AuthNone,
	})
	if err != nil {
		b.Fatal(err)
	}
	return store, created.Source.ID
}

func benchmarkDatabaseURL() string {
	if value := os.Getenv("STORMRELAY_TEST_DATABASE_URL"); value != "" {
		return value
	}
	return "postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable"
}

func benchmarkProcessOptions() storage.ProcessOptions {
	return storage.ProcessOptions{
		DedupeWindow:      15 * time.Minute,
		CorrelationWindow: 30 * time.Minute,
		ActorID:           "benchmark-worker",
		PublicBaseURL:     "http://localhost:8080",
	}
}

func benchmarkEvent(sourceID, sourceEventID, resource string, receivedAt time.Time) events.Event {
	payload := json.RawMessage(`{"type":"com.stormrelay.benchmark","title":"benchmark alert","severity":"critical","service":"benchmark-service","environment":"benchmark","resource":"` + resource + `","alertname":"BenchmarkAlert"}`)
	labels := map[string]string{
		"service":     "benchmark-service",
		"environment": "benchmark",
		"resource":    resource,
		"alertname":   "BenchmarkAlert",
	}
	return events.Event{
		ID:              "benchmark-event-" + sourceEventID,
		Source:          "urn:stormrelay:source:" + sourceID,
		Type:            "com.stormrelay.benchmark",
		Subject:         "benchmark alert",
		Time:            receivedAt,
		DataContentType: "application/json",
		SchemaVersion:   events.SchemaVersion,
		TenantID:        benchmarkTenantID,
		SourceID:        sourceID,
		Labels:          labels,
		Severity:        events.SeverityCritical,
		RawPayload:      payload,
		SourceEventID:   sourceEventID,
		Fingerprint:     events.Fingerprint("benchmark-source", "com.stormrelay.benchmark", "benchmark alert", labels, payload),
		ReceivedAt:      receivedAt,
		RequestID:       "benchmark-request-" + sourceEventID,
	}
}
