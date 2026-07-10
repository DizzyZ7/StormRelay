package telemetry

import (
	"fmt"
	"io"
	"sync/atomic"
	"time"
)

type Metrics struct {
	IngressEvents         atomic.Uint64
	RejectedEvents        atomic.Uint64
	DuplicateEvents       atomic.Uint64
	RunbookFailures       atomic.Uint64
	RunbookDurationCount  atomic.Uint64
	RunbookDurationMicros atomic.Uint64
	PluginFailures        atomic.Uint64
	NotificationFailures  atomic.Uint64
	OpenIncidents         atomic.Int64
	EventLatencyCount     atomic.Uint64
	EventLatencyMicros    atomic.Uint64
	DBPoolAcquired        atomic.Int64
	DBPoolMax             atomic.Int64
	JetStreamConsumerLag  atomic.Uint64
}

func (m *Metrics) ObserveEventLatency(d time.Duration) {
	m.EventLatencyCount.Add(1)
	m.EventLatencyMicros.Add(uint64(maxInt64(d.Microseconds(), 0)))
}
func (m *Metrics) ObserveRunbookDuration(d time.Duration) {
	m.RunbookDurationCount.Add(1)
	m.RunbookDurationMicros.Add(uint64(maxInt64(d.Microseconds(), 0)))
}
func (m *Metrics) WritePrometheus(w io.Writer) {
	counter(w, "stormrelay_ingress_events_total", m.IngressEvents.Load())
	counter(w, "stormrelay_rejected_events_total", m.RejectedEvents.Load())
	counter(w, "stormrelay_duplicate_events_total", m.DuplicateEvents.Load())
	counter(w, "stormrelay_runbook_failures_total", m.RunbookFailures.Load())
	_, _ = fmt.Fprintf(w, "# TYPE stormrelay_runbook_duration_seconds summary\nstormrelay_runbook_duration_seconds_count %d\nstormrelay_runbook_duration_seconds_sum %.6f\n", m.RunbookDurationCount.Load(), float64(m.RunbookDurationMicros.Load())/1e6)
	counter(w, "stormrelay_plugin_failures_total", m.PluginFailures.Load())
	counter(w, "stormrelay_notification_delivery_failures_total", m.NotificationFailures.Load())
	_, _ = fmt.Fprintf(w, "# TYPE stormrelay_open_incidents gauge\nstormrelay_open_incidents %d\n", m.OpenIncidents.Load())
	_, _ = fmt.Fprintf(w, "# TYPE stormrelay_database_pool_acquired gauge\nstormrelay_database_pool_acquired %d\n", m.DBPoolAcquired.Load())
	_, _ = fmt.Fprintf(w, "# TYPE stormrelay_database_pool_max gauge\nstormrelay_database_pool_max %d\n", m.DBPoolMax.Load())
	_, _ = fmt.Fprintf(w, "# TYPE stormrelay_jetstream_consumer_lag gauge\nstormrelay_jetstream_consumer_lag %d\n", m.JetStreamConsumerLag.Load())
	count := m.EventLatencyCount.Load()
	total := m.EventLatencyMicros.Load()
	_, _ = fmt.Fprintf(w, "# TYPE stormrelay_event_processing_latency_seconds summary\nstormrelay_event_processing_latency_seconds_count %d\nstormrelay_event_processing_latency_seconds_sum %.6f\n", count, float64(total)/1e6)
}
func counter(w io.Writer, name string, value uint64) {
	_, _ = fmt.Fprintf(w, "# TYPE %s counter\n%s %d\n", name, name, value)
}

func maxInt64(a, b int64) int64 {
	if a > b {
		return a
	}
	return b
}
