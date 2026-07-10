package events

import (
	"errors"
	"testing"
	"time"
)

func TestNormalizeStructuredCloudEventPreservesRawPayload(t *testing.T) {
	body := []byte(`{"specversion":"1.0","id":"evt-1","source":"urn:test","type":"alert","data":{"x":1},"severity":"critical","labels":{"service":"api"}}`)
	e, err := Normalize(NormalizeInput{TenantID: "t1", SourceID: "s1", Body: body, ContentType: "application/cloudevents+json", ReceivedAt: time.Unix(1, 0).UTC()})
	if err != nil {
		t.Fatal(err)
	}
	if string(e.RawPayload) != string(body) {
		t.Fatal("raw payload changed")
	}
	if e.ID != "evt-1" || e.Severity != SeverityCritical || e.Labels["service"] != "api" {
		t.Fatalf("unexpected event: %#v", e)
	}
}

func TestNormalizeRejectsInvalidCloudEvent(t *testing.T) {
	_, err := Normalize(NormalizeInput{TenantID: "t1", SourceID: "s1", Body: []byte(`{"specversion":"1.0"}`), ContentType: "application/cloudevents+json"})
	if !errors.Is(err, ErrInvalidCloudEvent) {
		t.Fatalf("got %v", err)
	}
}

func TestDedupeKeyPriority(t *testing.T) {
	e := Event{IdempotencyKey: "i", SourceEventID: "s", Fingerprint: "f"}
	if got := DedupeKey(e); got != "idem:i" {
		t.Fatalf("got %s", got)
	}
}

func FuzzNormalizeGeneric(f *testing.F) {
	f.Add([]byte(`{"service":"api"}`))
	f.Fuzz(func(t *testing.T, body []byte) {
		_, _ = Normalize(NormalizeInput{TenantID: "t", SourceID: "s", Body: body, ContentType: "application/json", ReceivedAt: time.Now().UTC()})
	})
}
