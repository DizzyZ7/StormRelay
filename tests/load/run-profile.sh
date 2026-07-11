#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)
cd "$ROOT_DIR"

PROFILE_NAME=${1:-correctness}
PROFILE_PATH="$ROOT_DIR/tests/load/profiles/${PROFILE_NAME}.json"
if [[ ! -f "$PROFILE_PATH" ]]; then
  echo "unknown benchmark profile: $PROFILE_NAME" >&2
  exit 2
fi

PROJECT_NAME=${STORMRELAY_BENCHMARK_PROJECT:-stormrelay-benchmark-${PROFILE_NAME}}
POSTGRES_PORT=${STORMRELAY_BENCHMARK_POSTGRES_PORT:-55432}
export STORMRELAY_BENCHMARK_POSTGRES_PORT="$POSTGRES_PORT"
COMPOSE=(
  docker compose
  --project-name "$PROJECT_NAME"
  -f deploy/compose/docker-compose.yml
  -f tests/load/docker-compose.yml
)
OUTPUT_DIR=${STORMRELAY_BENCHMARK_OUTPUT_DIR:-$ROOT_DIR/.benchmark-results/$PROFILE_NAME}
GO_BENCHMARK_PATH="$OUTPUT_DIR/go-benchmark.txt"
K6_SUMMARY_PATH="$OUTPUT_DIR/k6-summary.json"
RESULT_PATH="$OUTPUT_DIR/result.json"
AUTH_KEY=${STORMRELAY_BENCHMARK_API_KEY:-local-development-only-change-me}
DATABASE_URL="postgres://stormrelay:stormrelay@localhost:${POSTGRES_PORT}/stormrelay?sslmode=disable"

cleanup() {
  status=$?
  if [[ $status -ne 0 ]]; then
    echo '--- benchmark compose state ---' >&2
    "${COMPOSE[@]}" ps >&2 || true
    echo '--- benchmark server/worker dependency logs ---' >&2
    "${COMPOSE[@]}" logs --no-color server worker postgres nats >&2 || true
  fi
  "${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
  exit "$status"
}
trap cleanup EXIT INT TERM

for command in docker go python3 curl git; do
  if ! command -v "$command" >/dev/null 2>&1; then
    echo "required command is unavailable: $command" >&2
    exit 2
  fi
done

# Profiles are repository-controlled JSON. shlex.quote keeps the generated shell
# assignments safe even if descriptive fields later contain whitespace.
eval "$(python3 - "$PROFILE_PATH" <<'PY'
import json
import shlex
import sys

with open(sys.argv[1], encoding='utf-8') as handle:
    profile = json.load(handle)
values = {
    'SOURCE_COUNT': profile['dataset']['source_count'],
    'PAYLOAD_BYTES': profile['dataset']['payload_bytes'],
    'DUPLICATE_RATIO': profile['dataset']['duplicate_ratio'],
    'VIRTUAL_USERS': profile['concurrency']['virtual_users'],
    'WARMUP_SECONDS': profile['timing']['warmup_seconds'],
    'DURATION_SECONDS': profile['timing']['duration_seconds'],
    'INCIDENT_TIMEOUT_SECONDS': profile['timing']['incident_timeout_seconds'],
    'POLL_INTERVAL_MS': profile['timing']['poll_interval_milliseconds'],
    'GO_BENCHTIME': profile['go_benchmark']['benchtime'],
    'GO_BENCH_COUNT': profile['go_benchmark']['count'],
    'POSTGRES_IMAGE': profile['dependencies']['postgres_image'],
    'POSTGRES_MAX_CONNECTIONS': profile['dependencies']['postgres_max_connections'],
    'NATS_IMAGE': profile['dependencies']['nats_image'],
    'JETSTREAM_STORAGE': profile['dependencies']['jetstream_storage'],
    'K6_IMAGE': profile['dependencies']['k6_image'],
}
for key, value in values.items():
    print(f'{key}={shlex.quote(str(value))}')
PY
)"

if [[ "$JETSTREAM_STORAGE" != "file" ]]; then
  echo "unsupported benchmark JetStream storage mode: $JETSTREAM_STORAGE" >&2
  exit 2
fi
export STORMRELAY_BENCHMARK_POSTGRES_IMAGE="$POSTGRES_IMAGE"
export STORMRELAY_BENCHMARK_POSTGRES_MAX_CONNECTIONS="$POSTGRES_MAX_CONNECTIONS"
export STORMRELAY_BENCHMARK_NATS_IMAGE="$NATS_IMAGE"

configured_images=$("${COMPOSE[@]}" config --images)
for required_image in "$POSTGRES_IMAGE" "$NATS_IMAGE"; do
  if ! grep -Fqx "$required_image" <<<"$configured_images"; then
    echo "benchmark profile image was not applied to Compose: $required_image" >&2
    exit 2
  fi
done

rm -rf "$OUTPUT_DIR"
mkdir -p "$OUTPUT_DIR"

wait_http() {
  local url=$1
  local label=$2
  for ((attempt = 1; attempt <= 90; attempt++)); do
    if curl -fsS "$url" >/dev/null; then
      return 0
    fi
    sleep 1
  done
  echo "$label did not become ready: $url" >&2
  return 1
}

assert_postgres_profile() {
  local actual
  actual=$("${COMPOSE[@]}" exec -T postgres \
    psql -U stormrelay -d stormrelay -Atqc 'SHOW max_connections')
  if [[ "$actual" != "$POSTGRES_MAX_CONNECTIONS" ]]; then
    echo "PostgreSQL max_connections mismatch: expected $POSTGRES_MAX_CONNECTIONS, got $actual" >&2
    return 1
  fi
}

echo "Running StormRelay benchmark profile: $PROFILE_NAME"
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true

printf '# StormRelay Go microbenchmarks\n' >"$GO_BENCHMARK_PATH"
go test \
  -run '^$' \
  -bench 'Benchmark(NormalizeGeneric|NormalizeStructuredCloudEvent|Fingerprint|ParsePolicy|EvaluatePolicy)$' \
  -benchmem \
  -benchtime "$GO_BENCHTIME" \
  -count "$GO_BENCH_COUNT" \
  ./internal/events ./internal/policies | tee -a "$GO_BENCHMARK_PATH"

# Storage benchmarks receive a dedicated fresh PostgreSQL volume. Their rows are
# intentionally discarded before the end-to-end phase so k6 never measures a
# database polluted by the benchmark that preceded it.
echo 'Starting isolated PostgreSQL transaction benchmark phase'
"${COMPOSE[@]}" up -d --wait --wait-timeout 60 postgres
assert_postgres_profile
printf '\n# StormRelay PostgreSQL processing benchmarks\n' >>"$GO_BENCHMARK_PATH"
STORMRELAY_TEST_DATABASE_URL="$DATABASE_URL" \
  go test \
    -tags=benchmark \
    -run '^$' \
    -bench '^BenchmarkProcessEvent(NewIncident|Correlated|Duplicate)$' \
    -benchmem \
    -benchtime "$GO_BENCHTIME" \
    -count "$GO_BENCH_COUNT" \
    ./tests/load | tee -a "$GO_BENCHMARK_PATH"
"${COMPOSE[@]}" down -v

# The k6 phase starts from another new database and JetStream volume. Only the
# warm-up scenario populates caches before measurement custom metrics begin.
echo 'Starting clean end-to-end benchmark phase'
"${COMPOSE[@]}" up --build -d postgres nats server worker
wait_http 'http://localhost:8080/readyz' 'server'
wait_http 'http://localhost:8081/readyz' 'worker'
assert_postgres_profile

echo "Inspecting k6 scenario with image $K6_IMAGE"
docker run --rm \
  --volume "$ROOT_DIR:/work:ro" \
  --workdir /work \
  "$K6_IMAGE" inspect tests/load/k6/event_to_incident.js 2>&1 | tee "$OUTPUT_DIR/k6-inspect.log"

echo "Running k6 image $K6_IMAGE"
docker run --rm \
  --user "$(id -u):$(id -g)" \
  --network host \
  --volume "$ROOT_DIR:/work:ro" \
  --volume "$OUTPUT_DIR:/results" \
  --workdir /work \
  --env STORMRELAY_BASE_URL='http://localhost:8080' \
  --env STORMRELAY_API_KEY="$AUTH_KEY" \
  --env STORMRELAY_SOURCE_COUNT="$SOURCE_COUNT" \
  --env STORMRELAY_PAYLOAD_BYTES="$PAYLOAD_BYTES" \
  --env STORMRELAY_DUPLICATE_RATIO="$DUPLICATE_RATIO" \
  --env STORMRELAY_VUS="$VIRTUAL_USERS" \
  --env STORMRELAY_WARMUP_SECONDS="$WARMUP_SECONDS" \
  --env STORMRELAY_DURATION_SECONDS="$DURATION_SECONDS" \
  --env STORMRELAY_INCIDENT_TIMEOUT_SECONDS="$INCIDENT_TIMEOUT_SECONDS" \
  --env STORMRELAY_POLL_INTERVAL_MS="$POLL_INTERVAL_MS" \
  --env STORMRELAY_K6_SUMMARY_PATH='/results/k6-summary.json' \
  "$K6_IMAGE" run tests/load/k6/event_to_incident.js 2>&1 | tee "$OUTPUT_DIR/k6.log"

python3 tests/load/capture_result.py \
  --profile "$PROFILE_PATH" \
  --k6-summary "$K6_SUMMARY_PATH" \
  --go-benchmark "$GO_BENCHMARK_PATH" \
  --output "$RESULT_PATH" \
  --commit-sha "$(git rev-parse HEAD)" \
  --base-url 'http://localhost:8080' \
  --database-target "postgresql://localhost:${POSTGRES_PORT}/stormrelay" \
  --nats-target 'nats://localhost:4222'

python3 tests/load/validate_result.py tests/load/result.schema.json "$RESULT_PATH"

echo "Benchmark result: $RESULT_PATH"
