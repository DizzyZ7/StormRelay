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
COMPOSE=(docker compose --project-name "$PROJECT_NAME" -f deploy/compose/docker-compose.yml)
OUTPUT_DIR=${STORMRELAY_BENCHMARK_OUTPUT_DIR:-$ROOT_DIR/.benchmark-results/$PROFILE_NAME}
GO_BENCHMARK_PATH="$OUTPUT_DIR/go-benchmark.txt"
K6_SUMMARY_PATH="$OUTPUT_DIR/k6-summary.json"
RESULT_PATH="$OUTPUT_DIR/result.json"
AUTH_KEY=${STORMRELAY_BENCHMARK_API_KEY:-local-development-only-change-me}

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

profile = json.load(open(sys.argv[1], encoding='utf-8'))
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
    'K6_IMAGE': profile['dependencies']['k6_image'],
}
for key, value in values.items():
    print(f'{key}={shlex.quote(str(value))}')
PY
)"

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

echo "Starting isolated StormRelay benchmark profile: $PROFILE_NAME"
"${COMPOSE[@]}" down -v >/dev/null 2>&1 || true
"${COMPOSE[@]}" up --build -d postgres nats server worker
wait_http 'http://localhost:8080/readyz' 'server'
wait_http 'http://localhost:8081/readyz' 'worker'

printf '# StormRelay Go microbenchmarks\n' >"$GO_BENCHMARK_PATH"
go test \
  -run '^$' \
  -bench 'Benchmark(NormalizeGeneric|NormalizeStructuredCloudEvent|Fingerprint|ParsePolicy|EvaluatePolicy)$' \
  -benchmem \
  -benchtime "$GO_BENCHTIME" \
  -count "$GO_BENCH_COUNT" \
  ./internal/events ./internal/policies | tee -a "$GO_BENCHMARK_PATH"

printf '\n# StormRelay PostgreSQL processing benchmarks\n' >>"$GO_BENCHMARK_PATH"
STORMRELAY_TEST_DATABASE_URL='postgres://stormrelay:stormrelay@localhost:5432/stormrelay?sslmode=disable' \
  go test \
    -tags=benchmark \
    -run '^$' \
    -bench '^BenchmarkProcessEvent(NewIncident|Correlated|Duplicate)$' \
    -benchmem \
    -benchtime "$GO_BENCHTIME" \
    -count "$GO_BENCH_COUNT" \
    ./tests/load | tee -a "$GO_BENCHMARK_PATH"

echo "Running k6 image $K6_IMAGE"
docker run --rm \
  --network host \
  --volume "$ROOT_DIR:/work" \
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
  --env STORMRELAY_K6_SUMMARY_PATH="/work/${K6_SUMMARY_PATH#$ROOT_DIR/}" \
  "$K6_IMAGE" run tests/load/k6/event_to_incident.js | tee "$OUTPUT_DIR/k6.log"

python3 tests/load/capture_result.py \
  --profile "$PROFILE_PATH" \
  --k6-summary "$K6_SUMMARY_PATH" \
  --go-benchmark "$GO_BENCHMARK_PATH" \
  --output "$RESULT_PATH" \
  --commit-sha "$(git rev-parse HEAD)" \
  --base-url 'http://localhost:8080'

python3 tests/load/validate_result.py tests/load/result.schema.json "$RESULT_PATH"

echo "Benchmark result: $RESULT_PATH"
