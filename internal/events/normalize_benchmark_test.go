package events

import (
	"bytes"
	"fmt"
	"testing"
	"time"
)

var benchmarkEventSink Event
var benchmarkStringSink string

func BenchmarkNormalizeGeneric(b *testing.B) {
	for _, payloadBytes := range []int{1024, 8192} {
		b.Run(fmt.Sprintf("payload_%d", payloadBytes), func(b *testing.B) {
			body := benchmarkGenericPayload(payloadBytes)
			input := NormalizeInput{
				TenantID:      "00000000-0000-4000-8000-000000000001",
				SourceID:      "00000000-0000-4000-8000-000000000020",
				SourceName:    "benchmark-source",
				ContentType:   "application/json",
				Body:          body,
				SourceEventID: "benchmark-event",
				ReceivedAt:    time.Unix(1_700_000_000, 0).UTC(),
				RequestID:     "benchmark-request",
			}
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				event, err := Normalize(input)
				if err != nil {
					b.Fatal(err)
				}
				benchmarkEventSink = event
			}
		})
	}
}

func BenchmarkNormalizeStructuredCloudEvent(b *testing.B) {
	body := []byte(`{"specversion":"1.0","id":"benchmark-event","source":"urn:benchmark:alertmanager","type":"com.example.alert","subject":"checkout latency","time":"2026-07-11T00:00:00Z","datacontenttype":"application/json","severity":"critical","labels":{"service":"checkout","environment":"production","resource":"api","alertname":"HighLatency"},"data":{"summary":"p95 latency increased"}}`)
	input := NormalizeInput{
		TenantID:    "00000000-0000-4000-8000-000000000001",
		SourceID:    "00000000-0000-4000-8000-000000000020",
		SourceName:  "benchmark-source",
		ContentType: "application/cloudevents+json",
		Body:        body,
		ReceivedAt:  time.Unix(1_700_000_000, 0).UTC(),
		RequestID:   "benchmark-request",
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		event, err := Normalize(input)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkEventSink = event
	}
}

func BenchmarkFingerprint(b *testing.B) {
	labels := map[string]string{
		"service":     "checkout",
		"environment": "production",
		"resource":    "api",
		"alertname":   "HighLatency",
		"region":      "eu-west-1",
		"team":        "payments",
	}
	raw := benchmarkGenericPayload(8192)
	b.ReportAllocs()
	b.SetBytes(int64(len(raw)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchmarkStringSink = Fingerprint("benchmark-source", "com.example.alert", "checkout latency", labels, raw)
	}
}

func benchmarkGenericPayload(targetBytes int) []byte {
	prefix := []byte(`{"type":"com.example.alert","title":"checkout latency","severity":"critical","service":"checkout","environment":"production","resource":"api","alertname":"HighLatency","labels":{"region":"eu-west-1","team":"payments"},"padding":"`)
	suffix := []byte(`"}`)
	padding := targetBytes - len(prefix) - len(suffix)
	if padding < 0 {
		padding = 0
	}
	body := make([]byte, 0, len(prefix)+padding+len(suffix))
	body = append(body, prefix...)
	body = append(body, bytes.Repeat([]byte{'x'}, padding)...)
	body = append(body, suffix...)
	return body
}
