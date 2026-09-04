#!/usr/bin/env bash
# Install a pinned Istio (demo profile) + the sample Prometheus addon onto
# the current kubectl context. Does not create a Kind cluster — use the
# existing one (kind-cascade-operator from the scaffold smoke test).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
ISTIO_VERSION="${ISTIO_VERSION:-1.31.0}"
LOCALBIN="${LOCALBIN:-${ROOT}/bin}"
ISTIOCTL="${ISTIOCTL:-${LOCALBIN}/istioctl}"
MESH_NS="${MESH_NS:-default}"
ISTIO_SAMPLES_BASE="https://raw.githubusercontent.com/istio/istio/${ISTIO_VERSION}"

mkdir -p "${LOCALBIN}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}
need kubectl
need curl
need tar

ctx="$(kubectl config current-context)"
echo "kube context: ${ctx}"
if [[ "${ctx}" != kind-* ]]; then
  echo "warning: current context is not a Kind cluster (expected kind-*). continuing anyway." >&2
fi

download_istioctl() {
  local os arch asset
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "${os}" in
    darwin) os="osx" ;;
    linux) os="linux" ;;
    *) echo "unsupported OS: ${os}" >&2; exit 1 ;;
  esac
  case "${arch}" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) echo "unsupported arch: ${arch}" >&2; exit 1 ;;
  esac
  if [[ "${os}" == "osx" && "${arch}" == "amd64" ]]; then
    asset="istioctl-${ISTIO_VERSION}-osx-amd64.tar.gz"
  else
    asset="istioctl-${ISTIO_VERSION}-${os}-${arch}.tar.gz"
  fi
  local url="https://github.com/istio/istio/releases/download/${ISTIO_VERSION}/${asset}"
  local tmp
  tmp="$(mktemp -d)"
  echo "downloading ${url}"
  curl -fsSL -o "${tmp}/${asset}" "${url}"
  tar -xzf "${tmp}/${asset}" -C "${tmp}"
  install -m 0755 "${tmp}/istioctl" "${ISTIOCTL}"
  rm -rf "${tmp}"
}

if [[ ! -x "${ISTIOCTL}" ]] || ! "${ISTIOCTL}" version --remote=false 2>/dev/null | grep -q "${ISTIO_VERSION}"; then
  download_istioctl
fi
echo "istioctl: $("${ISTIOCTL}" version --remote=false --short 2>/dev/null || "${ISTIOCTL}" version --remote=false)"

echo "installing Istio ${ISTIO_VERSION} profile=demo"
"${ISTIOCTL}" install --set profile=demo -y

echo "waiting for istiod"
kubectl -n istio-system rollout status deployment/istiod --timeout=300s
kubectl -n istio-system rollout status deployment/istio-ingressgateway --timeout=300s

echo "installing Prometheus addon (Istio ${ISTIO_VERSION} sample)"
kubectl apply -f "${ISTIO_SAMPLES_BASE}/samples/addons/prometheus.yaml"
kubectl -n istio-system rollout status deployment/prometheus --timeout=300s

echo "enabling sidecar injection on namespace ${MESH_NS}"
kubectl label namespace "${MESH_NS}" istio-injection=enabled --overwrite

echo "Istio ${ISTIO_VERSION} ready on context ${ctx} (injection: ${MESH_NS})"
