import crypto from 'k6/crypto';
import http from 'k6/http';
import { Counter, Rate, Trend } from 'k6/metrics';
import { sleep } from 'k6';

const BASE_URL = __ENV.STORMRELAY_BASE_URL || 'http://localhost:8080';
const API_KEY = __ENV.STORMRELAY_API_KEY || 'local-development-only-change-me';
const SOURCE_COUNT = numberEnv('STORMRELAY_SOURCE_COUNT', 1);
const PAYLOAD_BYTES = numberEnv('STORMRELAY_PAYLOAD_BYTES', 1024);
const DUPLICATE_RATIO = numberEnv('STORMRELAY_DUPLICATE_RATIO', 0.2);
const VIRTUAL_USERS = numberEnv('STORMRELAY_VUS', 1);
const WARMUP_SECONDS = numberEnv('STORMRELAY_WARMUP_SECONDS', 2);
const DURATION_SECONDS = numberEnv('STORMRELAY_DURATION_SECONDS', 8);
const INCIDENT_TIMEOUT_SECONDS = numberEnv('STORMRELAY_INCIDENT_TIMEOUT_SECONDS', 10);
const POLL_INTERVAL_MS = numberEnv('STORMRELAY_POLL_INTERVAL_MS', 100);
const SUMMARY_PATH = __ENV.STORMRELAY_K6_SUMMARY_PATH || '/tmp/stormrelay-k6-summary.json';

const httpAcceptance = new Trend('stormrelay_http_acceptance_ms', true);
const eventToIncident = new Trend('stormrelay_event_to_incident_ms', true);
const errors = new Rate('stormrelay_errors');
const acceptedEvents = new Counter('stormrelay_accepted_events');
const durableEvents = new Counter('stormrelay_durable_events');
const duplicateRequests = new Counter('stormrelay_duplicate_requests');

export const options = {
  discardResponseBodies: false,
  summaryTrendStats: ['avg', 'min', 'med', 'p(95)', 'p(99)', 'max', 'count'],
  scenarios: {
    warmup: {
      executor: 'constant-vus',
      vus: VIRTUAL_USERS,
      duration: `${WARMUP_SECONDS}s`,
      exec: 'warmup',
      gracefulStop: '0s',
    },
    measurement: {
      executor: 'constant-vus',
      vus: VIRTUAL_USERS,
      duration: `${DURATION_SECONDS}s`,
      startTime: `${WARMUP_SECONDS}s`,
      exec: 'measurement',
      gracefulStop: `${Math.max(1, INCIDENT_TIMEOUT_SECONDS)}s`,
    },
  },
  thresholds: {
    stormrelay_errors: ['rate==0'],
    stormrelay_accepted_events: ['count>0'],
    stormrelay_durable_events: ['count>0'],
  },
};

let warmupLastCanonical = null;
let measurementLastCanonical = null;

export function setup() {
  const runID = `${Date.now()}-${Math.floor(Math.random() * 1000000)}`;
  const sources = [];
  for (let index = 0; index < SOURCE_COUNT; index += 1) {
    const response = http.post(
      `${BASE_URL}/api/v1/sources`,
      JSON.stringify({
        name: `benchmark-${runID}-${index}`,
        kind: 'generic',
        auth_mode: 'hmac-sha256',
        rate_limit_per_second: 10000,
        rate_limit_burst: 10000,
      }),
      {
        headers: {
          Authorization: `Bearer ${API_KEY}`,
          'Content-Type': 'application/json',
        },
        tags: { operation: 'benchmark_source_create' },
      },
    );
    if (response.status !== 201) {
      throw new Error(`source creation returned HTTP ${response.status}: ${bounded(response.body)}`);
    }
    const created = response.json();
    if (!created.source || !created.source.id || !created.credential) {
      throw new Error('source creation response did not contain source.id and credential');
    }
    sources.push({ id: created.source.id, secret: created.credential });
  }
  return { runID, sources };
}

export function warmup(data) {
  warmupLastCanonical = executeEvent(data, false, warmupLastCanonical, 'warmup');
}

export function measurement(data) {
  measurementLastCanonical = executeEvent(
    data,
    true,
    measurementLastCanonical,
    'measurement',
  );
}

function executeEvent(data, record, previousCanonical, phase) {
  const source = data.sources[(__VU - 1) % data.sources.length];
  const duplicate = previousCanonical !== null && deterministicDuplicate(__VU, __ITER);
  const canonical = duplicate
    ? previousCanonical
    : buildCanonical(data.runID, phase, source.id, __VU, __ITER);
  const body = buildPayload(canonical);
  const timestamp = `${Math.floor(Date.now() / 1000)}`;
  const signature = crypto.hmac('sha256', source.secret, `${timestamp}.${body}`, 'hex');
  const startedAt = Date.now();
  const response = http.post(`${BASE_URL}/api/v1/webhooks/${source.id}`, body, {
    headers: {
      'Content-Type': 'application/json',
      'X-StormRelay-Timestamp': timestamp,
      'X-StormRelay-Signature': `sha256=${signature}`,
      'X-Event-ID': canonical.eventID,
    },
    tags: { operation: 'webhook_acceptance', phase, duplicate: `${duplicate}` },
  });

  let failed = response.status !== 202;
  if (record) {
    httpAcceptance.add(response.timings.duration);
    if (response.status === 202) {
      acceptedEvents.add(1);
    }
    if (duplicate) {
      duplicateRequests.add(1);
    }
  }

  if (!failed && !duplicate) {
    const durable = waitForIncident(canonical.service, startedAt, phase);
    failed = !durable;
    if (record && durable) {
      durableEvents.add(1);
      eventToIncident.add(Date.now() - startedAt);
    }
  }

  if (record) {
    errors.add(failed);
  }
  return duplicate ? previousCanonical : canonical;
}

function waitForIncident(service, startedAt, phase) {
  const deadline = startedAt + INCIDENT_TIMEOUT_SECONDS * 1000;
  while (Date.now() < deadline) {
    const response = http.get(
      `${BASE_URL}/api/v1/incidents?service=${encodeURIComponent(service)}&limit=1`,
      {
        headers: { Authorization: `Bearer ${API_KEY}` },
        tags: { operation: 'incident_visibility', phase },
      },
    );
    if (response.status === 200) {
      const decoded = response.json();
      if (decoded.items && decoded.items.length > 0) {
        return true;
      }
    }
    sleep(POLL_INTERVAL_MS / 1000);
  }
  return false;
}

function buildCanonical(runID, phase, sourceID, vu, iteration) {
  const suffix = `${runID}-${phase}-${sourceID}-${vu}-${iteration}`;
  return {
    eventID: `benchmark-event-${suffix}`,
    service: `benchmark-service-${suffix}`,
    resource: `benchmark-resource-${suffix}`,
  };
}

function buildPayload(canonical) {
  const payload = {
    type: 'com.stormrelay.benchmark',
    title: 'StormRelay benchmark alert',
    severity: 'critical',
    service: canonical.service,
    environment: 'benchmark',
    resource: canonical.resource,
    alertname: 'BenchmarkAlert',
    labels: { benchmark: 'true' },
    padding: '',
  };
  const empty = JSON.stringify(payload);
  payload.padding = 'x'.repeat(Math.max(0, PAYLOAD_BYTES - empty.length));
  return JSON.stringify(payload);
}

function deterministicDuplicate(vu, iteration) {
  if (DUPLICATE_RATIO <= 0) {
    return false;
  }
  if (DUPLICATE_RATIO >= 1) {
    return true;
  }
  const bucket = ((iteration + 1) * 1103515245 + vu * 12345) % 10000;
  return bucket < Math.floor(DUPLICATE_RATIO * 10000);
}

function numberEnv(name, fallback) {
  const raw = __ENV[name];
  if (raw === undefined || raw === '') {
    return fallback;
  }
  const parsed = Number(raw);
  if (!Number.isFinite(parsed)) {
    throw new Error(`${name} must be numeric`);
  }
  return parsed;
}

function bounded(value) {
  const text = `${value || ''}`;
  return text.length > 500 ? `${text.slice(0, 500)}…` : text;
}

export function handleSummary(data) {
  const output = {};
  output[SUMMARY_PATH] = `${JSON.stringify(data, null, 2)}\n`;
  output.stdout = `${JSON.stringify({
    http_acceptance: data.metrics.stormrelay_http_acceptance_ms.values,
    event_to_incident: data.metrics.stormrelay_event_to_incident_ms.values,
    error_rate: data.metrics.stormrelay_errors.values.rate,
  })}\n`;
  return output;
}
