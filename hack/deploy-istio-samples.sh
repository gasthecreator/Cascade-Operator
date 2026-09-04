#!/usr/bin/env bash
# Deploy Istio's sleep + httpbin samples into the injected namespace.
# This is a *validation* workload for PromQL / response_flags evidence,
# not the §2.7 checkout→{payments,inventory} demo topology.
set -euo pipefail

ISTIO_VERSION="${ISTIO_VERSION:-1.31.0}"
MESH_NS="${MESH_NS:-default}"
ISTIO_SAMPLES_BASE="https://raw.githubusercontent.com/istio/istio/${ISTIO_VERSION}"

kubectl get namespace "${MESH_NS}" >/dev/null
if ! kubectl get namespace "${MESH_NS}" -o jsonpath='{.metadata.labels.istio-injection}' | grep -q enabled; then
  echo "namespace ${MESH_NS} is not labeled istio-injection=enabled" >&2
  echo "run: make istio-install" >&2
  exit 1
fi

echo "deploying sleep + httpbin from Istio ${ISTIO_VERSION} samples"
kubectl apply -n "${MESH_NS}" -f "${ISTIO_SAMPLES_BASE}/samples/sleep/sleep.yaml"
kubectl apply -n "${MESH_NS}" -f "${ISTIO_SAMPLES_BASE}/samples/httpbin/httpbin.yaml"

echo "waiting for sidecars (READY 2/2)"
kubectl -n "${MESH_NS}" rollout status deployment/sleep --timeout=180s
kubectl -n "${MESH_NS}" rollout status deployment/httpbin --timeout=180s

echo "sleep + httpbin ready in ${MESH_NS}"
kubectl -n "${MESH_NS}" get pods -l 'app in (sleep,httpbin)'
