---
status: accepted
date: 2026-09-04
decision-makers: "@gasthecreator"
---

# 0009. Namespace-scoped RBAC and an egress NetworkPolicy as opt-in hardening

## Context and Problem Statement

kubebuilder's default grants the manager a cluster-wide `ClusterRole`. The
threat model (2026-08-31) listed that, and unrestricted egress, as known
gaps.

## Decision Drivers

- Least privilege without breaking the default install, which tests and CI
  assume is cluster-wide.
- Enforcement must be proven, not assumed: Kind's default CNI does not
  enforce NetworkPolicy.

## Considered Options

- Keep the cluster-wide `ClusterRole` and unrestricted egress
- Make namespace-scoped RBAC the only mode
- Ship both as opt-in: a `--watch-namespaces` flag with generated
  `Role`/`RoleBinding` pairs, and an egress `NetworkPolicy` limited to DNS,
  the API server, each mesh's Prometheus and Linkerd's control plane

## Decision Outcome

Chosen option: "opt-in hardening". `hack/switch-to-namespaced-rbac.sh` applies
the namespaced RBAC, removes the `ClusterRoleBinding` and sets
`WATCH_NAMESPACES`. The default stays cluster-wide.

### Consequences

- Good: a documented path to least privilege with verified behavior; live
  testing found real bugs, including an API-server rule that never
  matched because the CNI evaluates egress against the pre-DNAT ClusterIP
  (fixed with an `ipBlock`), and the operator's own Linkerd sidecar
  intercepting API-server calls (fixed with `skip-outbound-ports`).
- Bad: a `CascadePolicy` in an unwatched namespace is silently invisible once
  restricted; `make deploy` re-creates the cluster-wide binding.

### Confirmation

`kubectl auth can-i list cascadepolicies` returns `no` cluster-wide and `yes`
only in watched namespaces; allow/deny test from an unmeshed pod.

## Pros and Cons of the Options

### Cluster-wide only

- Good, because it is the scaffold default and simplest.
- Bad, because the operator could patch mesh objects in any namespace.

### Namespaced only

- Good, because it is the tightest default.
- Bad, because it changes behavior for every existing install and test.

### Opt-in hardening

- Good, because it is verifiable and reversible.
- Bad, because security depends on operators choosing it.

## More Information

`docs/security-threat-model.md` (Known gaps);
`docs/history/worklog/2026-09-04-security-hardening-and-a-real-calico-bug.md`,
`docs/history/worklog/2026-09-04-live-verification-completed.md`.
