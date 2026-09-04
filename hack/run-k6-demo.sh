#!/usr/bin/env bash
# Runs one of demo/k6/*.js inside the Kind cluster as a Job, so checkout-
# service's inbound requests arrive over the pod network and get properly
# intercepted/reported by its own Istio sidecar (see fanout-amplification.js's
# comment: `kubectl port-forward` bypasses that interception, which silently
# breaks the fan-out ratio's denominator). The k6 pod itself is *not*
# sidecar-injected (sidecar.istio.io/inject: "false") — it doesn't need to
# be a mesh member to generate real destination-side telemetry on the
# services it calls, and an injected istio-proxy container would never exit,
# so the Job would never reach Completed.
#
# Usage: hack/run-k6-demo.sh <fanout-amplification|latency-error-cascade|retry-storm|tetragon-reset>
set -euo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: $0 <fanout-amplification|latency-error-cascade|retry-storm|tetragon-reset>" >&2
  exit 1
fi

SCRIPT_NAME="$1"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SCRIPT_PATH="${ROOT}/demo/k6/${SCRIPT_NAME}.js"
NS="${MESH_NS:-default}"
JOB_NAME="k6-${SCRIPT_NAME}"
CM_NAME="k6-${SCRIPT_NAME}-script"

if [[ ! -f "${SCRIPT_PATH}" ]]; then
  echo "no such script: ${SCRIPT_PATH}" >&2
  exit 1
fi

cleanup() {
  kubectl -n "${NS}" delete job "${JOB_NAME}" --ignore-not-found >/dev/null
  kubectl -n "${NS}" delete configmap "${CM_NAME}" --ignore-not-found >/dev/null
}
trap cleanup EXIT

cleanup # remove any leftover Job/ConfigMap from a prior interrupted run

kubectl -n "${NS}" create configmap "${CM_NAME}" --from-file="${SCRIPT_NAME}.js=${SCRIPT_PATH}"

# Passed through only if set in the caller's environment, so each script's
# own in-cluster-DNS defaults (baked into the .js files) are untouched for
# the common case — this exists for overriding against a non-default
# namespace/service naming scheme, not for routine runs.
env_overrides() {
  for name in CHECKOUT_URL PAYMENTS_URL INVENTORY_URL; do
    if [[ -n "${!name:-}" ]]; then
      printf '            - name: %s\n              value: %q\n' "${name}" "${!name}"
    fi
  done
}

cat <<EOF | kubectl apply -f -
apiVersion: batch/v1
kind: Job
metadata:
  name: ${JOB_NAME}
  namespace: ${NS}
spec:
  backoffLimit: 0
  template:
    metadata:
      annotations:
        sidecar.istio.io/inject: "false"
    spec:
      restartPolicy: Never
      containers:
        - name: k6
          image: grafana/k6:latest
          args: ["run", "/scripts/${SCRIPT_NAME}.js"]
          env:
$(env_overrides)
          volumeMounts:
            - name: script
              mountPath: /scripts
      volumes:
        - name: script
          configMap:
            name: ${CM_NAME}
EOF

echo "waiting for k6 pod to be created..."
for _ in $(seq 1 30); do
  if [[ -n "$(kubectl -n "${NS}" get pods -l "job-name=${JOB_NAME}" -o name 2>/dev/null)" ]]; then
    break
  fi
  sleep 1
done
kubectl -n "${NS}" wait --for=condition=Ready pod -l "job-name=${JOB_NAME}" --timeout=60s
kubectl -n "${NS}" logs -f "job/${JOB_NAME}"

# k6's own scenarios run ~170s (see each script's timeline comment); give the
# Job generous headroom beyond that before deciding it actually hung.
kubectl -n "${NS}" wait --for=condition=Complete "job/${JOB_NAME}" --timeout=300s
