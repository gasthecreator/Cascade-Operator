#!/usr/bin/env bash
# Emits a Role + RoleBinding pair (config/rbac-namespaced/role.yaml.tmpl)
# for each namespace in NAMESPACES, to stdout — the concrete
# implementation of docs/security-threat-model.md's namespace-scoped-RBAC
# hardening step, which config/rbac/role.yaml's cluster-wide ClusterRole
# was flagged as broader than strictly necessary for.
#
# Usage:
#   NAMESPACES=default,linkerd-demo hack/generate-namespaced-rbac.sh | kubectl apply -f -
#
# Pairs with cmd/main.go's --watch-namespaces/WATCH_NAMESPACES flag, which
# must name the exact same namespace set — a namespace named to one but
# not the other either grants RBAC nobody uses (named here, not watched)
# or restricts a watch to a namespace with no matching RBAC (named to
# --watch-namespaces, not here), the latter failing with a real Forbidden
# error on every reconcile attempt in that namespace.
#
# Does not touch config/rbac/role.yaml's cluster-wide ClusterRole/Binding —
# using both cluster-wide and namespaced RBAC at once is redundant (the
# cluster-wide grant already covers everything the namespaced one would),
# not incorrect, but defeats the point. To actually run in namespace-
# scoped mode rather than merely alongside it, delete the cluster-wide
# `cascade-operator-manager-rolebinding` ClusterRoleBinding after applying
# these (see docs/dev-istio.md/docs/dev-linkerd.md's own "namespace-scoped
# RBAC" section for the full live-verified sequence).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TEMPLATE="${ROOT}/config/rbac-namespaced/role.yaml.tmpl"

if [[ -z "${NAMESPACES:-}" ]]; then
  echo "usage: NAMESPACES=ns1,ns2 $0" >&2
  exit 1
fi

IFS=',' read -ra ns_list <<<"${NAMESPACES}"
for ns in "${ns_list[@]}"; do
  ns="$(echo "${ns}" | xargs)" # trim whitespace
  if [[ -z "${ns}" ]]; then
    continue
  fi
  sed "s/__NAMESPACE__/${ns}/g" "${TEMPLATE}"
  echo "---"
done
