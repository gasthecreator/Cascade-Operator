#!/usr/bin/env bash
# Install Istio's sample Grafana addon (already-running Prometheus is its
# datasource — see hack/install-istio.sh) and import the operator's own
# dashboard (config/observability/grafana-dashboard.json). Idempotent:
# re-running re-imports the dashboard so edits show up without a reinstall.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ISTIO_VERSION="${ISTIO_VERSION:-1.31.0}"
ISTIO_SAMPLES_BASE="https://raw.githubusercontent.com/istio/istio/${ISTIO_VERSION}"
LOCAL_PORT="${GRAFANA_LOCAL_PORT:-13000}"
DASHBOARD_JSON="${ROOT}/config/observability/grafana-dashboard.json"

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "required command not found: $1" >&2; exit 1; }
}
need kubectl
need curl
need python3

if [[ ! -f "${DASHBOARD_JSON}" ]]; then
  echo "dashboard JSON not found: ${DASHBOARD_JSON}" >&2
  exit 1
fi

echo "installing Grafana (Istio ${ISTIO_VERSION} sample addon)"
kubectl apply -f "${ISTIO_SAMPLES_BASE}/samples/addons/grafana.yaml"
kubectl -n istio-system rollout status deployment/grafana --timeout=300s

echo "importing dashboard via a temporary port-forward"
kubectl -n istio-system port-forward svc/grafana "${LOCAL_PORT}:3000" >/tmp/cascade-grafana-pf.log 2>&1 &
PF_PID=$!
cleanup() { kill "${PF_PID}" >/dev/null 2>&1 || true; }
trap cleanup EXIT

for _ in $(seq 1 40); do
  if curl -fsS "http://127.0.0.1:${LOCAL_PORT}/api/health" >/dev/null 2>&1; then
    break
  fi
  sleep 0.5
done

# Grafana's dashboard-import API wants {"dashboard": <model>, "overwrite": true},
# not the bare model this file stores (that bare form is what dashboard-provider
# provisioning and "import via file" both expect instead).
python3 -c "
import json
with open('${DASHBOARD_JSON}') as f:
    dashboard = json.load(f)
print(json.dumps({'dashboard': dashboard, 'overwrite': True, 'inputs': [
    {'name': 'DS_PROMETHEUS', 'type': 'datasource', 'pluginId': 'prometheus', 'value': 'Prometheus'}
]}))
" > /tmp/cascade-grafana-import.json

curl -fsS -X POST "http://127.0.0.1:${LOCAL_PORT}/api/dashboards/db" \
  -H "Content-Type: application/json" \
  -d @/tmp/cascade-grafana-import.json
echo
rm -f /tmp/cascade-grafana-import.json

echo "Grafana ready: kubectl -n istio-system port-forward svc/grafana 3000:3000, then http://localhost:3000/d/cascade-operator"
