#!/usr/bin/env bash
# Install a pinned Linkerd control plane + the linkerd-viz extension (for
# its bundled Prometheus) onto the current kubectl context, alongside
# whatever mesh is already there — PLAN.md §5 Phase 6.6's Linkerd copy of
# hack/install-istio.sh. Does not create a Kind cluster, and does not
# apply the demo/k8s-linkerd/ topology (see demo/k8s-linkerd/namespace.yaml
# — that namespace already carries its own linkerd.io/inject annotation,
# unlike Istio's namespace-label injection, so there is nothing here for
# this script to label).
#
# Every step below was live-verified working, in this exact order, against
# the dev Kind cluster during PLAN.md §5 Phase 6.6's own spike — see
# docs/worklog/2026-08-31-phase6.6-linkerd-query-builder.md for the full
# accounting, including two real mistakes this script's ordering exists to
# avoid repeating:
#   - Linkerd 2.16+ requires the Gateway API CRDs to already exist before
#     `linkerd install` will proceed at all (confirmed live: install fails
#     outright otherwise) — installed first, below.
#   - `linkerd install --crds`/`linkerd install`/`linkerd viz install` must
#     be captured via stdout *only*. Redirecting stderr into the same
#     stream (`2>&1`) interleaves the CLI's human-readable progress text
#     into the YAML document stream (confirmed live: this produced
#     `error converting YAML to JSON` failures at seemingly-arbitrary line
#     numbers, traced back to exactly this cause) — every `linkerd ...`
#     capture below redirects only stdout.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
LINKERD_VERSION="${LINKERD_VERSION:-edge-26.6.3}"
GATEWAY_API_VERSION="${GATEWAY_API_VERSION:-v1.2.1}"
LOCALBIN="${LOCALBIN:-${ROOT}/bin}"
LINKERD="${LINKERD:-${LOCALBIN}/linkerd}"

mkdir -p "${LOCALBIN}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "required command not found: $1" >&2
    exit 1
  }
}
need kubectl
need curl

ctx="$(kubectl config current-context)"
echo "kube context: ${ctx}"
if [[ "${ctx}" != kind-* ]]; then
  echo "warning: current context is not a Kind cluster (expected kind-*). continuing anyway." >&2
fi

download_linkerd() {
  local os arch asset
  os="$(uname -s | tr '[:upper:]' '[:lower:]')"
  arch="$(uname -m)"
  case "${arch}" in
    x86_64|amd64) arch="amd64" ;;
    arm64|aarch64) arch="arm64" ;;
    *) echo "unsupported arch: ${arch}" >&2; exit 1 ;;
  esac
  case "${os}" in
    linux) asset="linkerd2-cli-${LINKERD_VERSION}-linux-${arch}" ;;
    darwin)
      if [[ "${arch}" == "arm64" ]]; then
        asset="linkerd2-cli-${LINKERD_VERSION}-darwin-arm64"
      else
        asset="linkerd2-cli-${LINKERD_VERSION}-darwin"
      fi
      ;;
    *) echo "unsupported OS: ${os}" >&2; exit 1 ;;
  esac
  local url="https://github.com/linkerd/linkerd2/releases/download/${LINKERD_VERSION}/${asset}"
  local tmp
  tmp="$(mktemp -d)"
  echo "downloading ${url}"
  curl -fsSL -o "${tmp}/${asset}" "${url}"
  install -m 0755 "${tmp}/${asset}" "${LINKERD}"
  rm -rf "${tmp}"
}

if [[ ! -x "${LINKERD}" ]] || ! "${LINKERD}" version --client --short 2>/dev/null | grep -q "${LINKERD_VERSION}"; then
  download_linkerd
fi
echo "linkerd: $("${LINKERD}" version --client --short 2>/dev/null)"

echo "installing Gateway API ${GATEWAY_API_VERSION} CRDs (Linkerd 2.16+ prerequisite)"
kubectl apply --server-side -f "https://github.com/kubernetes-sigs/gateway-api/releases/download/${GATEWAY_API_VERSION}/standard-install.yaml"

crds_yaml="$(mktemp -t linkerd-crds.XXXXXX.yaml)"
install_yaml="$(mktemp -t linkerd-install.XXXXXX.yaml)"
viz_yaml="$(mktemp -t linkerd-viz.XXXXXX.yaml)"
cleanup() { rm -f "${crds_yaml}" "${install_yaml}" "${viz_yaml}"; }
trap cleanup EXIT

# `linkerd install`/`install --crds` refuse outright — a real,
# live-confirmed failure, not a guess — when a control plane already
# exists ("Can't install the Linkerd control plane in the 'linkerd'
# namespace. Reason: ConfigMap/linkerd-config already exists. Run the
# command `linkerd upgrade`..."), unlike istioctl's own install command
# (hack/install-istio.sh's own single `istioctl install` call), which
# reconciles an existing install in place instead of refusing. `linkerd
# upgrade`/`upgrade --crds` are the CLI's own suggested next step and
# render the equivalent manifests for an already-installed control
# plane, so this script picks whichever pair actually applies rather
# than only supporting a from-scratch cluster.
linkerd_subcommand="install"
if kubectl get configmap linkerd-config -n linkerd >/dev/null 2>&1; then
  echo "existing Linkerd control plane detected (linkerd-config ConfigMap present); using 'linkerd upgrade' instead of 'linkerd install'"
  linkerd_subcommand="upgrade"
fi

echo "rendering Linkerd ${LINKERD_VERSION} CRDs"
"${LINKERD}" "${linkerd_subcommand}" --crds >"${crds_yaml}"
kubectl apply -f "${crds_yaml}"

echo "rendering Linkerd ${LINKERD_VERSION} control plane"
"${LINKERD}" "${linkerd_subcommand}" >"${install_yaml}"
kubectl apply -f "${install_yaml}"

echo "waiting for the control plane"
kubectl -n linkerd rollout status deployment/linkerd-destination --timeout=300s
kubectl -n linkerd rollout status deployment/linkerd-identity --timeout=300s
kubectl -n linkerd rollout status deployment/linkerd-proxy-injector --timeout=300s

echo "rendering linkerd-viz (for its bundled Prometheus)"
"${LINKERD}" viz install >"${viz_yaml}"
kubectl apply -f "${viz_yaml}"

echo "waiting for linkerd-viz's Prometheus (the only linkerd-viz component"
echo "internal/mesh/linkerd's QueryBuilder actually depends on — metrics-api/"
echo "tap/web are only used by the linkerd/linkerd-viz CLI's own dashboard"
echo "commands, and are not waited on here: a resource-starved dev cluster"
echo "can leave them crash-looping without affecting detection at all, per"
echo "this session's own live finding)"
kubectl -n linkerd-viz rollout status deployment/prometheus --timeout=300s

echo "Linkerd ${LINKERD_VERSION} + linkerd-viz ready on context ${ctx}"
