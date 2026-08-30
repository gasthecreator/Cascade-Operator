#!/usr/bin/env bash
# Port-forwards the demo topology's three services plus Prometheus to fixed
# local ports, for running k6 (demo/k6/*.js) and the operator (`make run`)
# from the host against the Kind cluster. Traffic still passes through each
# pod's Istio sidecar (interception is at the pod network namespace, not at
# "how the connection arrived") so this does not change what the operator's
# PromQL queries see — see demo/k6/README.md for the fuller explanation.
#
# Foreground; Ctrl-C stops all four port-forwards.
set -euo pipefail

MESH_NS="${MESH_NS:-default}"
CHECKOUT_PORT="${CHECKOUT_PORT:-18080}"
PAYMENTS_PORT="${PAYMENTS_PORT:-18081}"
INVENTORY_PORT="${INVENTORY_PORT:-18082}"
PROM_PORT="${PROM_LOCAL_PORT:-19090}"

pids=()
cleanup() {
  echo "stopping port-forwards"
  for pid in "${pids[@]}"; do
    kill "$pid" >/dev/null 2>&1 || true
  done
}
trap cleanup EXIT INT TERM

kubectl -n "${MESH_NS}" port-forward svc/checkout-service "${CHECKOUT_PORT}:80" >/tmp/cascade-pf-checkout.log 2>&1 &
pids+=($!)
kubectl -n "${MESH_NS}" port-forward svc/payments-service "${PAYMENTS_PORT}:80" >/tmp/cascade-pf-payments.log 2>&1 &
pids+=($!)
kubectl -n "${MESH_NS}" port-forward svc/inventory-service "${INVENTORY_PORT}:80" >/tmp/cascade-pf-inventory.log 2>&1 &
pids+=($!)
kubectl -n istio-system port-forward svc/prometheus "${PROM_PORT}:9090" >/tmp/cascade-pf-prometheus.log 2>&1 &
pids+=($!)

echo "checkout-service  -> http://127.0.0.1:${CHECKOUT_PORT}"
echo "payments-service  -> http://127.0.0.1:${PAYMENTS_PORT}"
echo "inventory-service -> http://127.0.0.1:${INVENTORY_PORT}"
echo "prometheus        -> http://127.0.0.1:${PROM_PORT}"
echo "(Ctrl-C to stop all four)"

wait
