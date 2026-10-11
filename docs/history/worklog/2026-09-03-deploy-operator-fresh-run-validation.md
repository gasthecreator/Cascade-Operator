# `hack/deploy-operator.sh` validated as a genuine from-scratch run

**Date:** 2026-09-03
**Author:** Claude (solo)
**Type:** validation only — no runtime source changed

## What
Ran `hack/deploy-operator.sh` as one clean invocation against a reset
state, rather than the piecemeal, individually-verified-step approach both
prior slices (`2026-09-03-operator-in-cluster-deploy-and-metrics-scrape.md`,
`2026-09-03-per-mesh-prometheus-client-and-linkerd-viz-authz.md`) explicitly
flagged as a follow-up gap. No code changed — this closes a verification
gap, not an implementation one.

## Why
Both prior slices' own "Follow-ups / known gaps" sections stated the
script had never been run start-to-finish as a single invocation against a
from-scratch state — every step across both PRs was verified individually
against an already-partially-set-up cluster. Asked directly whether that
was worth doing given the risk (another resource-exhaustion cycle, the
kind this session already fought through once), and given an explicit
go-ahead to do it.

## How
Reset scope was deliberately narrower than "delete the whole cluster and
reinstall everything": Istio, Linkerd, Tetragon, and both demo topologies
were left running untouched, since their own install-from-scratch path is
already continuously exercised by CI's own "Kind + Istio + Linkerd"
integration job on every PR — re-proving that here would have added risk
(rebuilding two full mesh installs on an already resource-tight host) for
zero new information. What *hadn't* been proven was `hack/deploy-operator.sh`
itself running end-to-end against a state where none of its own
deliverables existed yet:

- `make undeploy ignore-not-found=true` — removed the operator Deployment,
  Service, RBAC, webhook config, cert-manager `Certificate`/`Issuer`
  objects, and (unexpectedly, since `config/default` bundles `config/crd`)
  the `CascadePolicy` CRD itself — which cascade-deleted both demo
  `CascadePolicy` CRs. Backed both up via `kubectl get -o yaml` before
  touching anything, reapplied after.
- Deleted the two `cascade-operator-metrics-reader-*`
  `ClusterRoleBinding`s and the `prometheus-admin-cascade-operator`
  `AuthorizationPolicy` directly, so their create-paths (not skip-paths)
  would run.
- Left cert-manager itself installed, and did **not** revert the
  scrape-config additions on either mesh's Prometheus ConfigMap — both
  attempts (`kubectl delete -f <cert-manager release URL>`, and a
  ConfigMap-patch-plus-`rollout restart` to remove the scrape job) were
  blocked by the auto-mode permission classifier as shared-infrastructure
  modification, and — unlike an earlier blocked `kubectl apply` in the
  prior slice — did not go through even after the user explicitly
  approved the specific command. Reported this plainly rather than
  finding a workaround, and scoped the run accordingly: those two steps
  would exercise their already-proven skip-paths instead of their
  create-paths this round, which is an honest, smaller claim than "every
  single line of this script was freshly exercised."

**Result**: `bash hack/deploy-operator.sh` completed with exit code 0, all
8 steps in one run:
1. cert-manager: skip (as expected, not reset this round)
2. build + load image: succeeded
3. `make install` + `make deploy`: genuinely fresh — CRD, namespace, every
   RBAC object, both Services, the Deployment, both cert-manager
   `Certificate`s, and the `ValidatingWebhookConfiguration` all reported
   `created`, not `unchanged`
4. rollout wait: succeeded
5. RBAC metrics-reader bindings: both `created`
6. Linkerd mesh-injection + `AuthorizationPolicy`: namespace annotated,
   deployment restarted, `AuthorizationPolicy` `created` — the specific,
   previously least-proven path (this exact sequence had only ever been
   run as individual manual commands before, never as this script)
7. Per-mesh `PROMETHEUS_URL_ISTIO`/`PROMETHEUS_URL_LINKERD`: applied fresh
8. scrape config: skip (as expected, not reset this round)

Reapplied both backed-up `CascadePolicy` CRs, confirmed both back to
`Normal`, confirmed both operator replicas' startup logs showed both
per-mesh Prometheus clients configured, then re-ran the same live-fire
trip/mitigate/restore sequence used to verify the prior two slices — on
**both meshes at once** this time, driven entirely by the fresh deploy:
Istio-mesh policy tripped (`LatencyErrorCascade`) at t+24s, Linkerd-mesh
policy tripped (`LatencyErrorCascade`) at t+30s, both healed and fully
restored to `Normal` within one ramp cycle.

## Files touched
None (validation only) besides this worklog entry and its index line.

## Testing
The entire "Testing" section of this entry *is* the test — see How above.
Additionally confirmed no leftover test pods (`kubectl get pods -A | grep
curl-` empty) and overall cluster health (`kubectl get pods -A | grep -v
Running` empty, `docker stats` showing normal, non-pegged CPU/memory)
after the full cycle.

## Follow-ups / known gaps
- The cert-manager-delete and scrape-config-revert steps still haven't
  had their create-paths exercised within a single script run — blocked
  by the auto-mode permission classifier both times, the second time even
  after explicit user approval of the specific command (approval doesn't
  lift this particular gate; the tool's own guidance says it needs a Bash
  permission rule change in the user's own Claude Code settings). Their
  create-paths were each proven once before, individually, in the prior
  two slices — this is a smaller, honestly-scoped residual gap, not a
  new one.
