#!/usr/bin/env bash
# Instant-query Prometheus in istio-system via a temporary port-forward.
# Usage: hack/query-prom.sh 'histogram_quantile(0.99, ...)'
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <promql>" >&2
  exit 1
fi

QUERY="$1"
LOCAL_PORT="${PROM_LOCAL_PORT:-19090}"

kubectl -n istio-system get svc prometheus >/dev/null

kubectl -n istio-system port-forward svc/prometheus "${LOCAL_PORT}:9090" >/tmp/cascade-prom-pf.log 2>&1 &
PF_PID=$!
cleanup() { kill "${PF_PID}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

for _ in $(seq 1 20); do
  if curl -fsS "http://127.0.0.1:${LOCAL_PORT}/-/ready" >/dev/null 2>&1; then
    break
  fi
  sleep 0.25
done

echo "GET /api/v1/query  query=${QUERY}"
curl -fsSG "http://127.0.0.1:${LOCAL_PORT}/api/v1/query" --data-urlencode "query=${QUERY}"
echo
