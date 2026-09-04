#!/usr/bin/env bash
# Deploy the operator itself in-cluster (never done anywhere in this
# project's history before this slice — every prior live-verification pass
# ran it via `go run ./cmd/main.go` from the host instead), and wire it up
# so its own `/metrics` endpoint is genuinely scraped by a mesh Prometheus,
# not just curlable directly. This closes the "operator metrics not
# scraped" gap CHANGELOG.md's [Unreleased] "Known gaps" section previously
# described (docs/worklog/2026-09-01-docs-dev-linkerd-and-audit-followup.md
# scoped it: needs cert-manager for the webhook's TLS *and* a scrape-config
# addition, not just the latter).
#
# Steps, each idempotent (safe to re-run):
#   1. Install cert-manager (the admission webhook's config/certmanager
#      kustomize overlay needs it for TLS — Phase 3's own long-standing
#      follow-up).
#   2. Build the operator image and load it onto the Kind node.
#   3. `make install` (CRDs) + `make deploy` (controller, webhook, RBAC).
#   4. Wait for cert-manager's Certificate to actually issue and the
#      operator to roll out — `make deploy`'s own webhook can otherwise
#      race a still-issuing cert.
#   5. Bind the existing `cascade-operator-metrics-reader` ClusterRole
#      (kubebuilder-scaffolded, `config/rbac/metrics_reader_role.yaml`, but
#      nothing binds it by default) to whichever mesh Prometheus
#      ServiceAccount(s) are present — best-effort per mesh, same pattern
#      as install-tetragon.sh's own scrape-job function.
#   6. Set PROMETHEUS_URL on the deployment so `reconciler.Metrics` is
#      actually non-nil — confirmed live this slice that omitting this
#      silently disables all detection (Normal forever, no error logged);
#      cmd/main.go's own `else` branch only logs "metrics polling disabled"
#      at startup, easy to miss.
#   7. Add a static scrape job for the operator's `/metrics` (bearer token
#      from the Prometheus pod's own mounted ServiceAccount, scheme https,
#      insecure_skip_verify since the cert is cluster-internal) to whichever
#      mesh Prometheus ConfigMap(s) are present.
#
# KNOWN LIMITATION, not fixed by this script: PROMETHEUS_URL is one URL for
# the whole operator process (cmd/main.go constructs a single
# metrics.Client, `CascadePolicyReconciler.Metrics`), but a CascadePolicy's
# `spec.mesh` can be Istio *or* Linkerd, and each mesh's proxies are only
# scraped by that mesh's own Prometheus. Confirmed live this slice: with
# PROMETHEUS_URL pointed at istio-system, an Istio-mesh policy detects and
# trips correctly, but a Linkerd-mesh policy on the same operator process
# reconciles forever without ever seeing real data (istio-system's
# Prometheus has no Linkerd proxy metrics to return) — silent, not an
# error. Running both meshes' CascadePolicies against one operator
# deployment needs a real design change (e.g. a per-mesh Querier map) that
# is out of scope here; this script defaults PROMETHEUS_URL_ISTIO only and
# documents rather than silently masks the gap. Override PROMETHEUS_URL_*
# below only once that design work lands.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.21.1}"
IMG="${IMG:-cascade-operator:dev}"
KIND_CLUSTER="${KIND_CLUSTER:-kind-cascade-operator}"
PROMETHEUS_URL_ISTIO="${PROMETHEUS_URL_ISTIO:-http://prometheus.istio-system.svc.cluster.local:9090}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}
need kubectl
need docker
need python3

echo "== [1/7] cert-manager =="
if kubectl get deployment cert-manager -n cert-manager >/dev/null 2>&1; then
  echo "cert-manager already installed, skipping"
else
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
  kubectl -n cert-manager wait --for=condition=Available deployment --all --timeout=180s
  kubectl -n cert-manager wait --for=condition=Ready pod --all --timeout=180s
fi

echo "== [2/7] build + load operator image =="
cd "${ROOT}"
docker build -t "${IMG}" .
kind load docker-image "${IMG}" --name "${KIND_CLUSTER#kind-}"

echo "== [3/7] make install + make deploy =="
make install
make deploy IMG="${IMG}"
git -C "${ROOT}" checkout -- config/manager/kustomization.yaml 2>/dev/null || true

echo "== [4/7] wait for rollout =="
kubectl -n cascade-operator-system wait --for=condition=Available deployment/cascade-operator-controller-manager --timeout=180s
kubectl -n cascade-operator-system rollout status deployment/cascade-operator-controller-manager --timeout=180s

bind_metrics_reader() {
  local mesh_namespace="$1" prom_sa="$2" binding_name="$3"
  if ! kubectl get namespace "${mesh_namespace}" >/dev/null 2>&1; then
    echo "skipping metrics-reader binding for ${mesh_namespace}: namespace not present"
    return
  fi
  cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: ${binding_name}
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: cascade-operator-metrics-reader
subjects:
  - kind: ServiceAccount
    name: ${prom_sa}
    namespace: ${mesh_namespace}
EOF
}

echo "== [5/7] RBAC: bind metrics-reader to each mesh's Prometheus ServiceAccount =="
bind_metrics_reader istio-system prometheus cascade-operator-metrics-reader-istio-prometheus
bind_metrics_reader linkerd-viz prometheus cascade-operator-metrics-reader-linkerd-viz-prometheus

echo "== [6/7] wire PROMETHEUS_URL (see the KNOWN LIMITATION note at the top of this script) =="
kubectl -n cascade-operator-system set env deployment/cascade-operator-controller-manager \
  "PROMETHEUS_URL=${PROMETHEUS_URL_ISTIO}"
kubectl -n cascade-operator-system rollout status deployment/cascade-operator-controller-manager --timeout=120s

add_operator_scrape_job() {
  local namespace="$1" configmap="$2" key="$3"
  local key_jsonpath="${key//./\\.}"
  if ! kubectl -n "${namespace}" get configmap "${configmap}" >/dev/null 2>&1; then
    echo "skipping ${namespace}/${configmap}: not installed"
    return
  fi
  if kubectl -n "${namespace}" get configmap "${configmap}" -o jsonpath="{.data.${key_jsonpath}}" | grep -q "job_name: cascade-operator"; then
    echo "${namespace}/${configmap} already scrapes cascade-operator"
    return
  fi
  echo "adding a cascade-operator scrape job to ${namespace}/${configmap}"
  local tmp_yaml tmp_cm
  tmp_yaml="$(mktemp -t operator-scrape.XXXXXX.yaml)"
  tmp_cm="$(mktemp -t operator-scrape-cm.XXXXXX.json)"
  kubectl -n "${namespace}" get configmap "${configmap}" -o jsonpath="{.data.${key_jsonpath}}" >"${tmp_yaml}.orig"
  python3 -c "
import sys, yaml
with open(sys.argv[1]) as f:
    d = yaml.safe_load(f)
d['scrape_configs'].append({
    'job_name': 'cascade-operator',
    'scheme': 'https',
    'tls_config': {'insecure_skip_verify': True},
    'bearer_token_file': '/var/run/secrets/kubernetes.io/serviceaccount/token',
    'static_configs': [{'targets': ['cascade-operator-controller-manager-metrics-service.cascade-operator-system.svc.cluster.local:8443']}],
})
with open(sys.argv[2], 'w') as f:
    yaml.safe_dump(d, f, default_flow_style=False, sort_keys=False)
" "${tmp_yaml}.orig" "${tmp_yaml}"
  kubectl -n "${namespace}" get configmap "${configmap}" -o json >"${tmp_cm}"
  python3 -c "
import json, sys
with open(sys.argv[1]) as f:
    cm = json.load(f)
with open(sys.argv[2]) as f:
    cm['data'][sys.argv[3]] = f.read()
for k in ('resourceVersion', 'uid', 'creationTimestamp'):
    cm['metadata'].pop(k, None)
cm['metadata'].pop('annotations', None)
with open(sys.argv[1], 'w') as f:
    json.dump(cm, f)
" "${tmp_cm}" "${tmp_yaml}" "${key}"
  kubectl apply -f "${tmp_cm}"
  rm -f "${tmp_yaml}" "${tmp_yaml}.orig" "${tmp_cm}"
  kubectl -n "${namespace}" rollout restart deployment/prometheus
  kubectl -n "${namespace}" rollout status deployment/prometheus --timeout=120s
}

echo "== [7/7] scrape config: add the operator as a static target on each mesh Prometheus =="
add_operator_scrape_job istio-system prometheus prometheus.yml
add_operator_scrape_job linkerd-viz prometheus-config prometheus.yml

echo "== done =="
echo "Confirm scraping with:"
echo "  make query-prom QUERY='up{job=\"cascade-operator\"}'"
echo "  make query-prom QUERY='cascade_signatures_detected_total'"
