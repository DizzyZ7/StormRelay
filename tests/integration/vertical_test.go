//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/DizzyZ7/StormRelay/internal/cryptox"
	"github.com/DizzyZ7/StormRelay/internal/events"
	"github.com/DizzyZ7/StormRelay/internal/ingestion"
	"github.com/DizzyZ7/StormRelay/internal/messaging"
	"github.com/DizzyZ7/StormRelay/internal/storage"
	"github.com/nats-io/nats.go"
)

const tenantID = "00000000-0000-4000-8000-000000000001"

func databaseURL() string {
	if v := os.Getenv("STORMRELAY_TEST_DATABASE_URL"); v != "" {
		return v
	}
	return "postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable"
}
func natsURL() string {
	if v := os.Getenv("STORMRELAY_TEST_NATS_URL"); v != "" {
		return v
	}
	return "nats://localhost:4222"
}
func openStore(t *testing.T) *storage.Store {
	t.Helper()
	box, err := cryptox.NewBox(make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	s, err := storage.Open(context.Background(), databaseURL(), box, slog.Default())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return s
}

func TestMigrationsAreIdempotent(t *testing.T) {
	s := openStore(t)
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	version, err := s.MigrationVersion(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if version != storage.ExpectedMigrationVersion {
		t.Fatalf("migration version=%d", version)
	}
}

func TestConcurrentDuplicateProcessingCreatesOneCanonicalEvent(t *testing.T) {
	s := openStore(t)
	source, err := s.CreateSource(context.Background(), storage.CreateSourceInput{TenantID: tenantID, Name: fmt.Sprintf("concurrency-%d", time.Now().UnixNano()), Kind: "generic", AuthMode: ingestion.AuthNone})
	if err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"type":"alert","subject":"database latency","severity":"critical","labels":{"service":"checkout","environment":"production","resource":"db-primary"}}`)
	event, err := events.Normalize(events.NormalizeInput{TenantID: tenantID, SourceID: source.Source.ID, SourceName: source.Source.Name, ContentType: "application/json", Body: body, SourceEventID: "same-upstream-id", ReceivedAt: time.Now().UTC(), RequestID: "test"})
	if err != nil {
		t.Fatal(err)
	}
	const workers = 8
	results := make(chan storage.ProcessResult, workers)
	errs := make(chan error, workers)
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, processErr := s.ProcessEvent(context.Background(), event, storage.ProcessOptions{DedupeWindow: 15 * time.Minute, CorrelationWindow: 30 * time.Minute, PublicBaseURL: "http://localhost"})
			results <- result
			errs <- processErr
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	canonical := 0
	duplicates := 0
	eventID := ""
	for result := range results {
		if result.Duplicate {
			duplicates++
		} else {
			canonical++
			eventID = result.EventID
			if result.Incident.ID == "" {
				t.Fatal("canonical event did not create incident")
			}
		}
	}
	if canonical != 1 || duplicates != workers-1 {
		t.Fatalf("canonical=%d duplicates=%d", canonical, duplicates)
	}
	count, err := s.DuplicateCountForEvent(context.Background(), eventID)
	if err != nil {
		t.Fatal(err)
	}
	if count != workers-1 {
		t.Fatalf("stored duplicate count=%d", count)
	}
}

func TestJetStreamPublishAndConsume(t *testing.T) {
	suffix := fmt.Sprint(time.Now().UnixNano())
	bus, err := messaging.Connect(natsURL(), "TEST_"+suffix, "stormrelay.test."+suffix, "consumer_"+suffix, "integration-test")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(bus.Close)
	sub, err := bus.Subscription()
	if err != nil {
		t.Fatal(err)
	}
	event := events.Event{ID: "ce-id", TenantID: tenantID, SourceID: "00000000-0000-4000-8000-000000000020", Source: "urn:test", Type: "test", Time: time.Now().UTC(), ReceivedAt: time.Now().UTC(), DataContentType: "application/json", SchemaVersion: events.SchemaVersion, Severity: events.SeverityInfo, RawPayload: json.RawMessage(`{"ok":true}`), Fingerprint: "fp"}
	if err := bus.PublishEvent(context.Background(), event, "integration-msg"); err != nil {
		t.Fatal(err)
	}
	messages, err := sub.Fetch(1, nats.MaxWait(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	var got events.Event
	if err := json.Unmarshal(messages[0].Data, &got); err != nil {
		t.Fatal(err)
	}
	if got.ID != event.ID {
		t.Fatalf("got %q", got.ID)
	}
	if err := messages[0].AckSync(); err != nil {
		t.Fatal(err)
	}
}
