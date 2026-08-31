#!/usr/bin/env bash
# Phase 11 (PLAN.md §5): installs Cilium's Tetragon (eBPF observability
# DaemonSet) on the current kube context, plus this project's own
# TracingPolicy watching tcp_retransmit_skb (demo/tetragon/tcp-retransmit-
# policy.yaml) — a fourth, independent, corroborating input alongside the
# existing Envoy-metric-based detection. Corroboration only: detection
# works identically with Tetragon absent.
#
# Requires Helm (not otherwise used by this project — no raw-manifest
# kubectl-apply install path exists upstream for Tetragon on Kubernetes,
# only Helm or a Docker Compose-only quickstart for non-k8s use):
#   brew install helm
#
# Verified live on this exact dev environment (2026-08-31): Docker Desktop
# 29.7.2, kernel 7.0.12-linuxkit (arm64), BTF confirmed present at
# /sys/kernel/btf/vmlinux — Tetragon's base sensor and the
# tcp_retransmit_skb kprobe both loaded successfully, and it captured real
# process_exec events for the demo topology's own pods.
#
# Known gap (see the worklog): the demo topology's fault injection
# (demo/internal/depsvc's /control/fail /control/slow) is HTTP-layer only
# — it returns 500s and adds sleep latency, but never actually drops a TCP
# connection or forces a retransmit. The tcp_retransmit_skb sensor is
# genuinely running and would fire on real network-layer disruption, but
# no current k6 scenario induces that, so end-to-end corroboration during
# an actual cascade incident has not been demonstrated — only that the
# sensor itself loads and captures real (unrelated) kernel events.
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

echo "== Tetragon installed and cascade-tcp-retransmit policy applied =="
echo "Tail raw events with:"
echo "  kubectl -n kube-system logs -l app.kubernetes.io/name=tetragon -c export-stdout -f"
