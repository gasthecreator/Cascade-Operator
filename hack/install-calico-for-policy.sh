#!/usr/bin/env bash
# Installs Calico in "policy-only" mode on top of Kind's default CNI
# (kindnet) — needed to actually *enforce* NetworkPolicy objects, since
# kindnet itself has no NetworkPolicy controller and silently no-ops any
# NetworkPolicy applied without it. This is what
# config/network-policy-egress/restrict-egress.yaml needs to have any real
# effect on this project's own dev cluster.
#
# Includes a real, confirmed fix for a genuine bug in Calico's own
# upstream manifest, found live on this exact cluster (v3.32.2 against
# Kubernetes v1.37.0): the calico-cni-plugin ClusterRole never grants
# access to clusterinformations.crd.projectcalico.org, which its own CNI
# binary needs at pod-sandbox-teardown time. Without this fix, EVERY pod
# sandbox teardown across the whole cluster fails and retries forever
# ("SandboxChanged" storms in kubelet's own logs), which looks exactly
# like generic resource exhaustion but isn't — confirmed by checking
# `journalctl -u kubelet` for "cannot get resource \"clusterinformations\""
# before concluding it was anything else. Reported upstream? Not yet — a
# reasonable follow-up, not required to use Calico here.
#
# Usage: hack/install-calico-for-policy.sh
set -euo pipefail

CALICO_VERSION="${CALICO_VERSION:-v3.32.2}"

echo "== installing Calico ${CALICO_VERSION} (policy-only mode) =="
kubectl apply -f "https://raw.githubusercontent.com/projectcalico/calico/${CALICO_VERSION}/manifests/calico-policy-only.yaml"

echo "== waiting for calico-node to roll out =="
kubectl -n kube-system rollout status daemonset/calico-node --timeout=180s

echo "== patching the calico-cni-plugin ClusterRole (upstream RBAC gap, see this script's own header) =="
cat <<EOF | kubectl apply -f -
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: calico-cni-plugin
rules:
  - apiGroups: [""]
    resources:
      - pods
      - nodes
      - namespaces
    verbs:
      - get
  - apiGroups: [""]
    resources:
      - pods/status
    verbs:
      - patch
  - apiGroups: ["crd.projectcalico.org"]
    resources:
      - clusterinformations
    verbs:
      - get
      - list
      - watch
EOF

echo "== done =="
echo "Confirm NetworkPolicy is actually enforced with a quick test pod, e.g.:"
echo "  kubectl run test-egress --image=curlimages/curl -n cascade-operator-system \\"
echo "    --labels='control-plane=controller-manager,app.kubernetes.io/name=cascade-operator' \\"
echo "    --restart=Never --command -- sh -c 'curl -sm5 http://example.com || echo BLOCKED'"
