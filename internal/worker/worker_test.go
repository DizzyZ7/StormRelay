package worker

import (
	"testing"
	"time"
)

func TestRetryDelayIsDeterministicAndBounded(t *testing.T) {
	payload := []byte(`{"event":"poison"}`)
	first := retryDelay(payload, 3, 100*time.Millisecond, 2*time.Second)
	second := retryDelay(payload, 3, 100*time.Millisecond, 2*time.Second)
	if first != second {
		t.Fatalf("retry delay is not deterministic: %s != %s", first, second)
	}
	if first < 320*time.Millisecond || first > 480*time.Millisecond {
		t.Fatalf("retry delay %s is outside the expected 20%% jitter window", first)
	}
}

func TestRetryDelayCapsAtMaximum(t *testing.T) {
	delay := retryDelay([]byte("same-event"), 50, time.Second, 3*time.Second)
	if delay <= 0 || delay > 3*time.Second {
		t.Fatalf("retry delay %s was not capped", delay)
	}
}

func TestRetryDelaySeparatesDifferentPayloads(t *testing.T) {
	left := retryDelay([]byte("event-a"), 2, time.Second, 30*time.Second)
	right := retryDelay([]byte("event-b"), 2, time.Second, 30*time.Second)
	if left == right {
		t.Fatalf("different payloads unexpectedly received identical jitter: %s", left)
	}
}
