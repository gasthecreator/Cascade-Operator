#!/usr/bin/env bash
# Phase 11 (PLAN.md §5): installs Cilium's Tetragon (eBPF observability
# DaemonSet) on the current kube context, this project's own two
# TracingPolicies (tcp_retransmit_skb and tcp_send_active_reset — see
# demo/tetragon/*.yaml), and a static Prometheus scrape job for Tetragon's
# own /metrics endpoint on whichever mesh Prometheus instance(s) are
# already installed — a fourth, independent, corroborating input
# alongside the existing Envoy/Linkerd-proxy-metric-based detection
# (internal/controller/kernel_corroboration.go). Corroboration only:
# detection works identically with Tetragon absent.
#
# Requires Helm (not otherwise used by this project — no raw-manifest
# kubectl-apply install path exists upstream for Tetragon on Kubernetes,
# only Helm or a Docker Compose-only quickstart for non-k8s use):
#   brew install helm
#
# Verified live on this exact dev environment (2026-08-31): Docker Desktop
# 29.7.2, kernel 7.0.12-linuxkit (arm64), BTF confirmed present at
# /sys/kernel/btf/vmlinux — Tetragon's base sensor and both kprobes loaded
# successfully. 2026-09-01: confirmed real tcp_send_active_reset events
# fire during an actual induced incident (demo/internal/depsvc's
# /control/reset), that Tetragon's own tetragon_events_total counter
# (Prometheus-format, at :2112/metrics) reflects them once scraped, and
# that a real Reconcile() call against a real Prometheus scraping both
# Linkerd and Tetragon produces a corroborated Verdict end-to-end — see
# docs/worklog/2026-09-01-phase11-kernel-corroboration.md.
#
# tcp_retransmit_skb remains genuinely unexercised (no current
# fault-injection mode produces real packet loss) — its policy is still
# applied, since the sensor itself is real and harmless to have loaded,
# but only tcp_send_active_reset has ever actually fired during an
# induced incident on this project's own demo topology.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

if ! command -v helm >/dev/null 2>&1; then
  echo "helm is required: brew install helm" >&2
  exit 1
fi

helm repo add cilium https://helm.cilium.io >/dev/null 2>&1 || true
helm repo update cilium >/dev/null

if helm status tetragon -n kube-system >/dev/null 2>&1; then
  echo "tetragon already installed in kube-system"
else
  helm install tetragon cilium/tetragon -n kube-system
fi

echo "waiting for the Tetragon DaemonSet pod(s) to be ready..."
kubectl -n kube-system wait --for=condition=Ready pod -l app.kubernetes.io/name=tetragon --timeout=120s

kubectl apply -f "${ROOT}/demo/tetragon/tcp-retransmit-policy.yaml"
kubectl apply -f "${ROOT}/demo/tetragon/tcp-reset-policy.yaml"

# add_tetragon_scrape_job patches namespace/configmap's prometheus.yml
# (data key key) with a static scrape job for Tetragon's own /metrics
# endpoint, then restarts deployment/prometheus in that namespace to pick
# it up — best-effort per mesh: only runs if that mesh's Prometheus
# ConfigMap actually exists, so installing Tetragon never requires both
# meshes to be present.
add_tetragon_scrape_job() {
  local namespace="$1" configmap="$2" key="$3"
  # jsonpath treats an unescaped "." as a field separator, not a literal
  # character — "prometheus.yml" as a data key needs its dot backslash-
  # escaped in the expression itself, confirmed live: the unescaped and
  # bracket-notation forms both silently return empty rather than erroring.
  local key_jsonpath="${key//./\\.}"
  if ! kubectl -n "${namespace}" get configmap "${configmap}" >/dev/null 2>&1; then
    echo "skipping ${namespace}/${configmap}: not installed"
    return
  fi
  if kubectl -n "${namespace}" get configmap "${configmap}" -o jsonpath="{.data.${key_jsonpath}}" | grep -q "job_name: tetragon"; then
    echo "${namespace}/${configmap} already scrapes tetragon"
    return
  fi
  echo "adding a tetragon scrape job to ${namespace}/${configmap}"
  local tmp_yaml tmp_cm
  tmp_yaml="$(mktemp -t tetragon-scrape.XXXXXX.yaml)"
  tmp_cm="$(mktemp -t tetragon-scrape-cm.XXXXXX.json)"
  kubectl -n "${namespace}" get configmap "${configmap}" -o jsonpath="{.data.${key_jsonpath}}" >"${tmp_yaml}.orig"
  python3 -c "
import sys, yaml
with open(sys.argv[1]) as f:
    d = yaml.safe_load(f)
d['scrape_configs'].append({
    'job_name': 'tetragon',
    'static_configs': [{'targets': ['tetragon.kube-system.svc.cluster.local:2112']}],
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

if ! command -v python3 >/dev/null 2>&1; then
  echo "python3 is required to patch Prometheus scrape config" >&2
  exit 1
fi

add_tetragon_scrape_job istio-system prometheus prometheus.yml
add_tetragon_scrape_job linkerd-viz prometheus-config prometheus.yml

echo "== Tetragon installed, both TracingPolicies applied, Tetragon scraped by whichever mesh Prometheus is present =="
echo "Tail raw events with:"
echo "  kubectl -n kube-system logs -l app.kubernetes.io/name=tetragon -c export-stdout -f"
echo "Query the corroboration signal directly with:"
echo "  make query-prom QUERY='sum(increase(tetragon_events_total{type=\"PROCESS_KPROBE\"}[30s])) by (namespace,workload)'"
