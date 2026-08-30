#!/usr/bin/env bash
# Build, load, and deploy the §2.7 demo topology (checkout ->
# {payments, inventory}) into the current Kind cluster. This is the
# fan-out evidence-gathering workload — not the operator itself, and not
# the sleep/httpbin validation pair from the Kind+Istio dev-env slice.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DEMO_DIR="${ROOT}/demo"
KIND_CLUSTER_NAME="${KIND_CLUSTER_NAME:-cascade-operator}"
MESH_NS="${MESH_NS:-default}"
TAG="${DEMO_TAG:-dev}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}
need docker
need kind
need kubectl

kubectl get namespace "${MESH_NS}" >/dev/null
if ! kubectl get namespace "${MESH_NS}" -o jsonpath='{.metadata.labels.istio-injection}' | grep -q enabled; then
  echo "namespace ${MESH_NS} is not labeled istio-injection=enabled" >&2
  echo "run: make istio-install" >&2
  exit 1
fi

for svc in payments inventory checkout; do
  echo "building cascade-demo-${svc}:${TAG}"
  docker build -t "cascade-demo-${svc}:${TAG}" -f "${DEMO_DIR}/${svc}/Dockerfile" "${DEMO_DIR}"
done

for svc in payments inventory checkout; do
  echo "loading cascade-demo-${svc}:${TAG} into kind cluster ${KIND_CLUSTER_NAME}"
  kind load docker-image "cascade-demo-${svc}:${TAG}" --name "${KIND_CLUSTER_NAME}"
done

echo "applying demo manifests to namespace ${MESH_NS}"
kubectl apply -n "${MESH_NS}" -f "${DEMO_DIR}/k8s/"

echo "waiting for rollout (READY 2/2 once Istio sidecars inject)"
kubectl -n "${MESH_NS}" rollout status deployment/payments-service --timeout=180s
kubectl -n "${MESH_NS}" rollout status deployment/inventory-service --timeout=180s
kubectl -n "${MESH_NS}" rollout status deployment/checkout-service --timeout=180s

kubectl -n "${MESH_NS}" get pods -l 'app in (checkout-service,payments-service,inventory-service)'
