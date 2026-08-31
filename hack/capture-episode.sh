#!/usr/bin/env bash
# Phase 7 (PLAN.md §5): capture one real trip->mitigate->restore episode for
# a single k6 scenario as a JSON trace, for demo/replay/index.html to
# animate. Polls the CascadePolicy's own status plus the affected Istio
# object's raw spec every ${POLL_INTERVAL}s while the scenario's k6 Job
# runs (same 170s timeline as every demo/k6/*.js script: 20s baseline,
# induce at 20s, heal at 80s, 90s more load to watch the restore ramp).
#
# Unlike hack/run-benchmark.sh (which only needs status.phase transitions),
# this script also needs the raw object spec at each tick — read via
# `kubectl get -o json`, the same raw-JSON-not-typed-struct discipline
# test/integration/ uses, since the whole point of the replay page is
# showing the actual patch, not a typed Go struct's view of it.
#
# Usage: hack/capture-episode.sh <latency-error-cascade|retry-storm|fanout-amplification>
# Requires the same live setup as hack/run-benchmark.sh (Kind+Istio+demo
# topology up); starts its own Prometheus port-forward + operator process.
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <latency-error-cascade|retry-storm|fanout-amplification>" >&2
  exit 1
fi

SCENARIO="$1"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NS="${MESH_NS:-default}"
POLICY="${POLICY_NAME:-checkout-service}"
PROM_PORT="${PROM_LOCAL_PORT:-19090}"
POLL_INTERVAL="${POLL_INTERVAL:-2}"
OUT="${ROOT}/demo/replay/traces/${SCENARIO}.json"

case "${SCENARIO}" in
  latency-error-cascade)
    OBJ_KIND="destinationrule"; OBJ_NAME="payments-service"; METRIC_LABEL="p99_latency_ms"
    METRIC_QUERY='histogram_quantile(0.99, sum by (le) (rate(istio_request_duration_milliseconds_bucket{destination_service="payments-service.default.svc.cluster.local",reporter="source"}[30s])))'
    ;;
  retry-storm)
    OBJ_KIND="virtualservice"; OBJ_NAME="inventory-service"; METRIC_LABEL="dest_source_ratio"
    METRIC_QUERY='sum(rate(istio_requests_total{destination_service="inventory-service.default.svc.cluster.local",reporter="destination"}[30s])) / sum(rate(istio_requests_total{destination_service="inventory-service.default.svc.cluster.local",reporter="source"}[30s]))'
    ;;
  fanout-amplification)
    OBJ_KIND="destinationrule"; OBJ_NAME="payments-service"; METRIC_LABEL="dependency_caller_ratio"
    METRIC_QUERY='sum(rate(istio_requests_total{destination_service="payments-service.default.svc.cluster.local",reporter="destination"}[30s])) / sum(rate(istio_requests_total{destination_service="checkout-service.default.svc.cluster.local",reporter="destination"}[30s]))'
    ;;
  *)
    echo "unknown scenario: ${SCENARIO}" >&2
    exit 1
    ;;
esac

mkdir -p "$(dirname "${OUT}")"

PROM_PF_PID=""
OPERATOR_PID=""
cleanup() {
  echo "cleaning up: operator + prometheus port-forward"
  [[ -n "${OPERATOR_PID}" ]] && kill "${OPERATOR_PID}" >/dev/null 2>&1 || true
  [[ -n "${PROM_PF_PID}" ]] && kill "${PROM_PF_PID}" >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== starting Prometheus port-forward =="
kubectl -n istio-system port-forward svc/prometheus "${PROM_PORT}:9090" >/tmp/cascade-capture-prom-pf.log 2>&1 &
PROM_PF_PID=$!
for _ in $(seq 1 20); do
  curl -fsS "http://127.0.0.1:${PROM_PORT}/-/ready" >/dev/null 2>&1 && break
  sleep 0.25
done

echo "== starting operator =="
# ENABLE_WEBHOOKS=false — see hack/run-benchmark.sh's identical comment:
# no cert exists on this dev cluster, and skipping registration is
# sufficient to avoid the manager ever trying to load one.
(cd "${ROOT}" && ENABLE_WEBHOOKS=false go run ./cmd/main.go --prometheus-url="http://127.0.0.1:${PROM_PORT}" --health-probe-bind-address=:18092) \
  >/tmp/cascade-capture-operator.log 2>&1 &
OPERATOR_PID=$!
sleep 3
if ! kill -0 "${OPERATOR_PID}" 2>/dev/null; then
  echo "operator failed to start; see /tmp/cascade-capture-operator.log" >&2
  cat /tmp/cascade-capture-operator.log >&2
  exit 1
fi

kubectl -n "${NS}" patch cascadepolicy "${POLICY}" --type=merge -p '{"spec":{"mode":"Mitigate"}}' >/dev/null

query_metric() {
  curl -fsSG "http://127.0.0.1:${PROM_PORT}/api/v1/query" --data-urlencode "query=${METRIC_QUERY}" \
    | python3 -c '
import json,sys
d = json.load(sys.stdin)
r = d.get("data", {}).get("result", [])
print(r[0]["value"][1] if r else "null")
' 2>/dev/null || echo "null"
}

echo "== launching k6 job: ${SCENARIO} =="
"${ROOT}/hack/run-k6-demo.sh" "${SCENARIO}" >"/tmp/cascade-capture-${SCENARIO}.log" 2>&1 &
K6_PID=$!

start_ts=$(date +%s)
echo "[" > "${OUT}.tmp"
first=1
while kill -0 "${K6_PID}" 2>/dev/null; do
  now=$(date +%s)
  elapsed=$((now - start_ts))

  phase=$(kubectl -n "${NS}" get cascadepolicy "${POLICY}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
  step=$(kubectl -n "${NS}" get cascadepolicy "${POLICY}" -o jsonpath='{.status.restoreStep}' 2>/dev/null || echo "0")
  sig=$(kubectl -n "${NS}" get cascadepolicy "${POLICY}" -o jsonpath='{.status.lastSignature}' 2>/dev/null || echo "")
  metric_val=$(query_metric)

  # Pass everything through env vars into Python, not string-interpolated
  # into the script text — the object spec is arbitrary JSON and must never
  # be spliced into source code as text.
  entry=$(
    kubectl -n "${NS}" get "${OBJ_KIND}" "${OBJ_NAME}" -o json 2>/dev/null \
      | CAP_ELAPSED="${elapsed}" CAP_PHASE="${phase}" CAP_STEP="${step:-0}" CAP_SIG="${sig}" \
        CAP_METRIC_LABEL="${METRIC_LABEL}" CAP_METRIC_VAL="${metric_val}" CAP_OBJ_KIND="${OBJ_KIND}" \
        python3 -c '
import json, math, os, sys
try:
    obj = json.load(sys.stdin)
    spec = obj.get("spec", {})
except Exception:
    spec = {}
mv = os.environ["CAP_METRIC_VAL"]
metric_value = None
if mv not in ("null", ""):
    try:
        f = float(mv)
        # Exclude NaN (f != f) and +/-Inf (Prometheus returns both for a
        # momentary zero-denominator ratio query) — none of these are
        # valid JSON tokens, even though the json module here tolerates them.
        if f == f and not math.isinf(f):
            metric_value = f
    except ValueError:
        pass
print(json.dumps({
    "elapsedSeconds": int(os.environ["CAP_ELAPSED"]),
    "phase": os.environ["CAP_PHASE"],
    "restoreStep": int(os.environ["CAP_STEP"] or 0),
    "lastSignature": os.environ["CAP_SIG"],
    "metricLabel": os.environ["CAP_METRIC_LABEL"],
    "metricValue": metric_value,
    "objectKind": os.environ["CAP_OBJ_KIND"],
    "objectSpec": spec,
}))
'
  )

  if [[ ${first} -eq 0 ]]; then echo "," >> "${OUT}.tmp"; fi
  first=0
  echo -n "${entry}" >> "${OUT}.tmp"
  echo "t=${elapsed}s phase=${phase} step=${step} sig=${sig} ${METRIC_LABEL}=${metric_val}"

  sleep "${POLL_INTERVAL}"
done
wait "${K6_PID}" 2>/dev/null || true

# Safety net, not this script's own heal step: the k6 scenario's single
# unretried /control/heal call can itself land on the fault's own "1-in-5
# requests fail" injection and silently fail — found live running
# hack/run-benchmark.sh (see its identical comment). Leaves the cluster
# genuinely healthy after a capture, regardless of that race.
kubectl -n "${NS}" exec deploy/sleep -c sleep -- curl -s http://payments-service.default.svc.cluster.local/control/heal >/dev/null 2>&1 || true
kubectl -n "${NS}" exec deploy/sleep -c sleep -- curl -s http://inventory-service.default.svc.cluster.local/control/heal >/dev/null 2>&1 || true

echo "]" >> "${OUT}.tmp"

CAP_TMP="${OUT}.tmp" CAP_OUT="${OUT}" CAP_SCENARIO="${SCENARIO}" CAP_CAPTURED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)" python3 -c '
import json, os
with open(os.environ["CAP_TMP"]) as f:
    data = json.load(f)
with open(os.environ["CAP_OUT"], "w") as f:
    json.dump({
        "scenario": os.environ["CAP_SCENARIO"],
        "capturedAt": os.environ["CAP_CAPTURED_AT"],
        "points": data,
    }, f, indent=2)
out_path = os.environ["CAP_OUT"]
print(f"== captured {len(data)} points to {out_path} ==")
'
rm -f "${OUT}.tmp"
