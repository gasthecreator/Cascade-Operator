#!/usr/bin/env bash
# Phase 9 (PLAN.md §5): quantified resilience benchmark. Runs each of
# demo/k6/*.js's cascade scenarios twice against the live dev Kind cluster —
# once with the CascadePolicy in DetectOnly (the real, not hypothetical,
# baseline: metrics recorded, nothing patched) and once in Mitigate — and
# measures, from the CascadePolicy's own status transitions and Prometheus,
# how much difference the operator's mitigation actually makes:
#
#   - time-to-detect: wall-clock seconds from this script starting the k6
#     Job to status.phase first reaching Tripped.
#   - blast-radius: total requests and 5xx count to the affected host across
#     the whole run window (Prometheus increase(), not a local counter).
#   - time-to-restore: wall-clock seconds from Tripped to status.phase
#     returning to Normal (DetectOnly never patches, so this is however long
#     the induced condition's own PromQL window takes to roll off after the
#     script's own heal() call — the true unassisted baseline; Mitigate's
#     number is the operator's own gradual restoration ramp).
#
# Prerequisites: same as demo/k6/README.md's "Running a scenario end to
# end" — Kind + Istio + Prometheus up (`make istio-install`), demo topology
# deployed (`make demo-deploy`), k6 not required on the host (runs as an
# in-cluster Job via hack/run-k6-demo.sh).
#
# This script starts its own Prometheus port-forward and its own `go run`
# of the operator, and tears both down on exit — it does not require the
# four-terminal manual setup, only that nothing else is already bound to
# the ports it uses.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
NS="${MESH_NS:-default}"
POLICY="${POLICY_NAME:-checkout-service}"
PROM_PORT="${PROM_LOCAL_PORT:-19090}"
RESULTS_MD="${ROOT}/docs/benchmark-results.md"
RESULTS_TSV="$(mktemp)"

SCENARIOS=(latency-error-cascade retry-storm fanout-amplification)

# host_for maps scenario -> affected dependency host. A plain case, not an
# associative array: the only bash available on this machine is macOS's
# stock 3.2.57 (no Homebrew bash installed), which predates `declare -A`.
host_for() {
  case "$1" in
    latency-error-cascade) echo "payments-service.default.svc.cluster.local" ;;
    retry-storm)           echo "inventory-service.default.svc.cluster.local" ;;
    fanout-amplification)  echo "payments-service.default.svc.cluster.local" ;;
    *) echo "unknown scenario: $1" >&2; exit 1 ;;
  esac
}

PROM_PF_PID=""
OPERATOR_PID=""
cleanup() {
  echo "cleaning up: operator + prometheus port-forward"
  [[ -n "${OPERATOR_PID}" ]] && kill "${OPERATOR_PID}" >/dev/null 2>&1 || true
  [[ -n "${PROM_PF_PID}" ]] && kill "${PROM_PF_PID}" >/dev/null 2>&1 || true
  kubectl -n "${NS}" patch cascadepolicy "${POLICY}" --type=merge -p '{"spec":{"mode":"Mitigate"}}' >/dev/null 2>&1 || true
}
trap cleanup EXIT

echo "== starting Prometheus port-forward =="
kubectl -n istio-system port-forward svc/prometheus "${PROM_PORT}:9090" >/tmp/cascade-bench-prom-pf.log 2>&1 &
PROM_PF_PID=$!
for _ in $(seq 1 20); do
  curl -fsS "http://127.0.0.1:${PROM_PORT}/-/ready" >/dev/null 2>&1 && break
  sleep 0.25
done

echo "== starting operator (go run ./cmd/main.go --prometheus-url=http://127.0.0.1:${PROM_PORT}) =="
# ENABLE_WEBHOOKS=false: no cert-manager (or any cert at all) is installed
# on this dev cluster, and the manager only ever tries to load a webhook
# TLS cert if something actually registers a webhook handler on it — so
# skipping registration here is sufficient, not just a workaround (see
# docs/worklog/2026-08-31-phase9-resilience-benchmark.md for the full
# account of chasing this down).
(cd "${ROOT}" && ENABLE_WEBHOOKS=false go run ./cmd/main.go --prometheus-url="http://127.0.0.1:${PROM_PORT}" --health-probe-bind-address=:18091) \
  >/tmp/cascade-bench-operator.log 2>&1 &
OPERATOR_PID=$!
sleep 3
if ! kill -0 "${OPERATOR_PID}" 2>/dev/null; then
  echo "operator failed to start; see /tmp/cascade-bench-operator.log" >&2
  cat /tmp/cascade-bench-operator.log >&2
  exit 1
fi

query_prom() {
  local q="$1"
  curl -fsSG "http://127.0.0.1:${PROM_PORT}/api/v1/query" --data-urlencode "query=${q}" \
    | python3 -c '
import json,sys
d = json.load(sys.stdin)
r = d.get("data", {}).get("result", [])
print(r[0]["value"][1] if r else "0")
'
}

set_mode() {
  kubectl -n "${NS}" patch cascadepolicy "${POLICY}" --type=merge -p "{\"spec\":{\"mode\":\"$1\"}}" >/dev/null
}

wait_for_normal() {
  for _ in $(seq 1 60); do
    phase=$(kubectl -n "${NS}" get cascadepolicy "${POLICY}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    [[ "${phase}" == "Normal" || -z "${phase}" ]] && return 0
    sleep 2
  done
  return 1
}

# force_heal_all: each k6 scenario's own heal() call is a single, unretried
# HTTP request — and demo/internal/depsvc's own fault injection (1-in-5
# requests fail during "slow" mode) applies uniformly to every request that
# service receives, *including* its own /control/heal endpoint. Bad luck can
# make that one request land on the 1-in-5 failure path, silently leaving
# the fault permanently un-healed for every scenario run afterward (found
# live running this benchmark — see the worklog). Calling /control/heal
# directly here, after every scenario, is a safety net this script controls
# rather than depending on a single unretried in-script call succeeding.
force_heal_all() {
  kubectl -n "${NS}" exec deploy/sleep -c sleep -- curl -s http://payments-service.default.svc.cluster.local/control/heal >/dev/null 2>&1 || true
  kubectl -n "${NS}" exec deploy/sleep -c sleep -- curl -s http://inventory-service.default.svc.cluster.local/control/heal >/dev/null 2>&1 || true
}

run_one() {
  local scenario="$1" mode="$2"
  local host
  host="$(host_for "${scenario}")"

  set_mode "${mode}"
  wait_for_normal || echo "warning: policy did not settle to Normal before starting ${scenario}/${mode}" >&2

  "${ROOT}/hack/run-k6-demo.sh" "${scenario}" >"/tmp/cascade-bench-${scenario}-${mode}.log" 2>&1 &
  local k6_pid=$!

  local start_ts tripped_ts restored_ts extra_wait
  start_ts=$(date +%s)
  tripped_ts=""
  restored_ts=""
  extra_wait=0
  while kill -0 "${k6_pid}" 2>/dev/null || { [[ "${mode}" == "Mitigate" && -z "${restored_ts}" ]] && [[ ${extra_wait} -lt 90 ]]; }; do
    phase=$(kubectl -n "${NS}" get cascadepolicy "${POLICY}" -o jsonpath='{.status.phase}' 2>/dev/null || echo "")
    if [[ -z "${tripped_ts}" && "${phase}" == "Tripped" ]]; then
      tripped_ts=$(date +%s)
    fi
    if [[ -n "${tripped_ts}" && -z "${restored_ts}" && "${phase}" == "Normal" ]]; then
      restored_ts=$(date +%s)
    fi
    if ! kill -0 "${k6_pid}" 2>/dev/null; then
      extra_wait=$((extra_wait + 2))
    fi
    sleep 2
  done
  wait "${k6_pid}" 2>/dev/null || true
  force_heal_all
  local end_ts
  end_ts=$(date +%s)

  local ttd="n/a" ttr="n/a"
  [[ -n "${tripped_ts}" ]] && ttd=$((tripped_ts - start_ts))
  [[ -n "${restored_ts}" && -n "${tripped_ts}" ]] && ttr=$((restored_ts - tripped_ts))

  local window=$((end_ts - start_ts))
  local total_reqs err_reqs
  total_reqs=$(query_prom "sum(increase(istio_requests_total{destination_service=\"${host}\",reporter=\"destination\"}[${window}s]))")
  err_reqs=$(query_prom "sum(increase(istio_requests_total{destination_service=\"${host}\",reporter=\"destination\",response_code=~\"5..\"}[${window}s]))")

  echo -e "${scenario}\t${mode}\t${ttd}\t${ttr}\t${total_reqs}\t${err_reqs}" | tee -a "${RESULTS_TSV}" >&2
}

echo -e "scenario\tmode\ttime_to_detect_s\ttime_to_restore_s\ttotal_requests\terror_requests" > "${RESULTS_TSV}"

for scenario in "${SCENARIOS[@]}"; do
  for mode in DetectOnly Mitigate; do
    echo "== running ${scenario} in ${mode} mode =="
    run_one "${scenario}" "${mode}"
  done
done

echo "== writing ${RESULTS_MD} =="
{
  echo "# Resilience benchmark results"
  echo
  echo "**Generated:** $(date -u +%Y-%m-%dT%H:%M:%SZ) via \`make benchmark\` (\`hack/run-benchmark.sh\`)"
  echo
  echo "Each of \`demo/k6/*.js\`'s cascade scenarios run twice against the live"
  echo "dev Kind cluster: once with \`checkout-service\`'s CascadePolicy in"
  echo "\`DetectOnly\` mode (the real baseline — signatures detected and"
  echo "recorded, nothing patched) and once in \`Mitigate\`. Numbers are wall-"
  echo "clock seconds from this script's own timestamps and Prometheus"
  echo "\`increase()\` queries over the run window — not simulated."
  echo
  echo "| Scenario | Mode | Time to detect (s) | Time to restore (s) | Total requests | 5xx requests | Error rate |"
  echo "|---|---|---|---|---|---|---|"
  tail -n +2 "${RESULTS_TSV}" | while IFS=$'\t' read -r scenario mode ttd ttr total err; do
    rate="n/a"
    if [[ "${total}" != "0" && "${total}" != "n/a" ]]; then
      rate=$(python3 -c "print(f'{float(${err})/float(${total})*100:.1f}%')" 2>/dev/null || echo "n/a")
    fi
    echo "| ${scenario} | ${mode} | ${ttd} | ${ttr} | ${total} | ${err} | ${rate} |"
  done
} > "${RESULTS_MD}"

echo "== done — see ${RESULTS_MD} =="
