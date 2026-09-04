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
#   6. If linkerd-viz is present, mesh-inject the operator itself (annotate
#      cascade-operator-system with linkerd.io/inject: enabled, restart)
#      and grant its ServiceAccount a second, additive AuthorizationPolicy
#      on linkerd-viz's own locked-down `prometheus-admin` Server. Without
#      this, an unmeshed caller (no mTLS identity) gets a clean HTTP 403
#      from linkerd-viz's Prometheus regardless of RBAC or query
#      correctness — confirmed live this slice: this is Linkerd-viz's own
#      deliberate default-deny policy on its Prometheus's query port, not a
#      bug, and not something this project's own RBAC (step 7) can satisfy
#      on its own. Cluster-wide defaultInboundPolicy is all-unauthenticated
#      on this project's dev install (checked live before doing this —
#      meshing the operator does not newly restrict any of its *other*
#      inbound traffic, e.g. istio-system's unmeshed Prometheus scraping
#      the operator's own /metrics in step 8 keeps working unauthenticated).
#   7. Set PROMETHEUS_URL_ISTIO/PROMETHEUS_URL_LINKERD on the deployment
#      (one per mesh actually present) so `reconciler.MetricsIstio`/
#      `MetricsLinkerd` are non-nil — confirmed live that omitting either
#      one silently disables detection for that mesh alone: no error, that
#      mesh's policies just reconcile forever without ever seeing real
#      data. cmd/main.go's own per-mesh flags exist specifically because a
#      single, mesh-agnostic PROMETHEUS_URL cannot correctly serve both an
#      Istio-mesh and a Linkerd-mesh CascadePolicy from one operator
#      process at once — each mesh's proxies are only scraped by that
#      mesh's own Prometheus (see internal/controller/cascadepolicy_controller.go's
#      metricsQuerier(), and docs/worklog/2026-09-03-operator-in-cluster-deploy-and-metrics-scrape.md
#      for the live reproduction of the bug this fixes).
#   8. Add a static scrape job for the operator's `/metrics` (bearer token
#      from the Prometheus pod's own mounted ServiceAccount, scheme https,
#      insecure_skip_verify since the cert is cluster-internal) to whichever
#      mesh Prometheus ConfigMap(s) are present.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CERT_MANAGER_VERSION="${CERT_MANAGER_VERSION:-v1.21.1}"
IMG="${IMG:-cascade-operator:dev}"
KIND_CLUSTER="${KIND_CLUSTER:-kind-cascade-operator}"
PROMETHEUS_URL_ISTIO="${PROMETHEUS_URL_ISTIO:-http://prometheus.istio-system.svc.cluster.local:9090}"
PROMETHEUS_URL_LINKERD="${PROMETHEUS_URL_LINKERD:-http://prometheus.linkerd-viz.svc.cluster.local:9090}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}
need kubectl
need docker
need python3

echo "== [1/8] cert-manager =="
if kubectl get deployment cert-manager -n cert-manager >/dev/null 2>&1; then
  echo "cert-manager already installed, skipping"
else
  kubectl apply -f "https://github.com/cert-manager/cert-manager/releases/download/${CERT_MANAGER_VERSION}/cert-manager.yaml"
  kubectl -n cert-manager wait --for=condition=Available deployment --all --timeout=180s
  kubectl -n cert-manager wait --for=condition=Ready pod --all --timeout=180s
fi

echo "== [2/8] build + load operator image =="
cd "${ROOT}"
docker build -t "${IMG}" .
kind load docker-image "${IMG}" --name "${KIND_CLUSTER#kind-}"

echo "== [3/8] make install + make deploy =="
make install
make deploy IMG="${IMG}"
git -C "${ROOT}" checkout -- config/manager/kustomization.yaml 2>/dev/null || true

echo "== [4/8] wait for rollout =="
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

echo "== [5/8] RBAC: bind metrics-reader to each mesh's Prometheus ServiceAccount =="
bind_metrics_reader istio-system prometheus cascade-operator-metrics-reader-istio-prometheus
bind_metrics_reader linkerd-viz prometheus cascade-operator-metrics-reader-linkerd-viz-prometheus

OPERATOR_SA="cascade-operator-controller-manager"
OPERATOR_NS="cascade-operator-system"

echo "== [6/8] Linkerd: mesh-inject the operator + grant it access to linkerd-viz's Prometheus =="
if kubectl get namespace linkerd-viz >/dev/null 2>&1; then
  if kubectl get namespace "${OPERATOR_NS}" -o jsonpath='{.metadata.annotations.linkerd\.io/inject}' | grep -q enabled; then
    echo "${OPERATOR_NS} already annotated for Linkerd injection, skipping annotate+restart"
  else
    kubectl annotate namespace "${OPERATOR_NS}" linkerd.io/inject=enabled --overwrite
    kubectl -n "${OPERATOR_NS}" rollout restart deployment/cascade-operator-controller-manager
    kubectl -n "${OPERATOR_NS}" rollout status deployment/cascade-operator-controller-manager --timeout=180s
  fi

  if kubectl -n linkerd-viz get authorizationpolicy prometheus-admin-cascade-operator >/dev/null 2>&1; then
    echo "prometheus-admin-cascade-operator AuthorizationPolicy already present, skipping"
  else
    # A second, additive AuthorizationPolicy on the same Server, not an edit
    # to the existing prometheus-admin policy: Linkerd's own policy
    # validator webhook rejects more than one ServiceAccount per
    # AuthorizationPolicy ("only a single ServiceAccount may be set"),
    # confirmed live — the correct pattern for a second authorized caller is
    # a second policy, leaving linkerd-viz's own metrics-api grant
    # untouched.
    cat <<EOF | kubectl apply -f -
apiVersion: policy.linkerd.io/v1alpha1
kind: AuthorizationPolicy
metadata:
  name: prometheus-admin-cascade-operator
  namespace: linkerd-viz
  labels:
    linkerd.io/extension: viz
spec:
  targetRef:
    group: policy.linkerd.io
    kind: Server
    name: prometheus-admin
  requiredAuthenticationRefs:
    - kind: ServiceAccount
      name: ${OPERATOR_SA}
      namespace: ${OPERATOR_NS}
EOF
  fi
else
  echo "skipping: linkerd-viz not present"
fi

echo "== [7/8] wire a Prometheus URL per mesh actually present =="
prometheus_env_args=()
if kubectl get namespace istio-system >/dev/null 2>&1 && kubectl -n istio-system get svc prometheus >/dev/null 2>&1; then
  prometheus_env_args+=("PROMETHEUS_URL_ISTIO=${PROMETHEUS_URL_ISTIO}")
else
  echo "skipping PROMETHEUS_URL_ISTIO: istio-system/prometheus not present"
fi
if kubectl get namespace linkerd-viz >/dev/null 2>&1 && kubectl -n linkerd-viz get svc prometheus >/dev/null 2>&1; then
  prometheus_env_args+=("PROMETHEUS_URL_LINKERD=${PROMETHEUS_URL_LINKERD}")
else
  echo "skipping PROMETHEUS_URL_LINKERD: linkerd-viz/prometheus not present"
fi
if [ "${#prometheus_env_args[@]}" -eq 0 ]; then
  echo "no mesh Prometheus found; leaving metrics polling disabled for every policy" >&2
else
  kubectl -n cascade-operator-system set env deployment/cascade-operator-controller-manager \
    "${prometheus_env_args[@]}"
  kubectl -n cascade-operator-system rollout status deployment/cascade-operator-controller-manager --timeout=120s
fi

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

echo "== [8/8] scrape config: add the operator as a static target on each mesh Prometheus =="
add_operator_scrape_job istio-system prometheus prometheus.yml
add_operator_scrape_job linkerd-viz prometheus-config prometheus.yml

echo "== done =="
echo "Confirm scraping with:"
echo "  make query-prom QUERY='up{job=\"cascade-operator\"}'"
echo "  make query-prom QUERY='cascade_signatures_detected_total'"
