# Operator deployed in-cluster for real, and its own metrics genuinely scraped

**Date:** 2026-09-03
**Author:** Claude (solo)
**Type:** infra

## What
- `hack/deploy-operator.sh` (new) + `make deploy-operator`: installs
  cert-manager (if not already present), builds/loads the operator image,
  runs `make install`/`make deploy`, binds the kubebuilder-scaffolded
  `cascade-operator-metrics-reader` `ClusterRole` to whichever mesh
  Prometheus ServiceAccount(s) are present, sets `PROMETHEUS_URL` on the
  deployment, and adds a static scrape job for the operator's `/metrics`
  to whichever mesh Prometheus ConfigMap(s) are present (same technique
  `hack/install-tetragon.sh` already used for Tetragon).
- `README.md`, `CHANGELOG.md`, `PLAN.md`, `docs/dev-istio.md`,
  `docs/dev-linkerd.md`: updated to describe the gap as closed, and to
  document the new limitation found while closing it (below).

## Why
A prior slice's audit (`docs/worklog/2026-09-01-docs-dev-linkerd-and-audit-followup.md`)
scoped this gap accurately but deliberately didn't attempt the fix without
a direct decision, given cert-manager is a new, persistent, cluster-wide
dependency on a dev cluster this project's own worklogs have repeatedly
found resource-constrained. Asked directly whether to build it, given the
real (not hypothetical) scope; user said yes.

## How
Cert-manager v1.21.1 installed via its own static manifest (already the
version this cluster runs, confirmed via `kubectl -n cert-manager get
deploy cert-manager -o jsonpath='{.spec.template.spec.containers[0].image}'`
from an earlier session slice that had already installed it live before
this one started — `hack/deploy-operator.sh` detects and skips a re-install
the same way `hack/install-linkerd.sh` detects an existing control plane).
`make deploy` then came up cleanly with the webhook's TLS satisfied.

RBAC: the kubebuilder scaffold already ships a `metrics-reader`
`ClusterRole` (`config/rbac/metrics_reader_role.yaml`, deployed as
`cascade-operator-metrics-reader`) with `nonResourceURLs: ["/metrics"]` —
but nothing binds it by default. Bound it to the `prometheus`
ServiceAccount in both `istio-system` and `linkerd-viz` (best-effort per
mesh, mirroring `install-tetragon.sh`'s own "only patch a mesh's
Prometheus if that mesh is actually installed" pattern).

**A real, previously-hidden bug found live, not assumed:** after the first
deploy, curling the operator's own secured `/metrics` endpoint (bearer
token from the `prometheus` ServiceAccount, confirming the RBAC binding
itself worked — HTTP 200) returned zero `cascade_*` series even after
driving real threshold-crossing traffic through the demo topology
(`payments-service`'s own `/control/slow` fault-injection endpoint, p99
confirmed at 995ms and error rate at 33% via the *exact* PromQL
`internal/mesh/istio.QueryBuilder` uses, both well past the demo
`CascadePolicy`'s 500ms/0.05 thresholds). The `CascadePolicy` stayed
`Normal` throughout. Root cause, found by reading `cmd/main.go`: the
`--prometheus-url`/`PROMETHEUS_URL` flag was never set on this
deployment (an oversight from the original in-cluster deploy, not a
pre-existing bug in the operator's own code), so `reconciler.Metrics`
was `nil` — `cmd/main.go`'s own `else` branch only logs "Prometheus URL
not set; metrics polling disabled" at startup, easy to miss, and the
reconciler otherwise runs and reports `Normal` forever with no error.
Fixed by setting `PROMETHEUS_URL=http://prometheus.istio-system.svc.cluster.local:9090`
on the deployment; re-ran the same load and got a genuine `RetryStorm`
trip within 18 seconds, real `cascade_signatures_detected_total`/
`cascade_mitigation_patches_applied_total`/`cascade_restorations_completed_total`
series appeared on the leader replica's `/metrics` (curling through the
`Service` intermittently hit the non-leader replica instead, which has
zero series for an un-incremented `CounterVec` — Prometheus's own
`client_golang` omits a metric family entirely until at least one label
combination has been touched, so this reads as "endpoint broken" rather
than "just quiet" if you don't check both replicas).

Scrape config: `bearer_token_file:
/var/run/secrets/kubernetes.io/serviceaccount/token`, `scheme: https`,
`tls_config: {insecure_skip_verify: true}` (the operator's metrics-server
cert is cluster-internal, not from a CA either mesh's Prometheus trusts),
static target
`cascade-operator-controller-manager-metrics-service.cascade-operator-system.svc.cluster.local:8443`
— patched into both `istio-system/prometheus`'s and
`linkerd-viz/prometheus-config`'s own `prometheus.yml` ConfigMap key via
the same Python-based YAML patch + `rollout restart` technique
`install-tetragon.sh` already established.

## A new limitation surfaced, not fixed here
`PROMETHEUS_URL` is one URL for the whole operator process — one
`metrics.Client`, `CascadePolicyReconciler.Metrics` — but `spec.mesh` can
be `Istio` or `Linkerd`, and this cluster has one `CascadePolicy` of each
(`default/checkout-service` mesh=Istio, `linkerd-demo/checkout-service`
mesh=Linkerd) reconciled by the *same* operator deployment. Confirmed
live: with `PROMETHEUS_URL` pointed at `istio-system`'s Prometheus, the
Istio-mesh policy detects and trips for real (see above), but the
Linkerd-mesh policy — confirmed still actively reconciling every ~10s via
its own log lines — has been `Normal` for its entire 2+ day existence and
necessarily always will be while pointed at the wrong mesh's Prometheus,
since Istio's Prometheus has no Linkerd proxy metrics to return regardless
of query correctness. This is silent, not an error: no log line flags a
mesh mismatch, because the reconciler has no way to know which
Prometheus's data it's actually getting. Documented in
`hack/deploy-operator.sh`'s own header, `README.md`'s Project status
section, and `CHANGELOG.md`'s Known gaps rather than fixed — the real fix
is a per-mesh `Querier` (or one operator deployment per mesh), a design
change out of scope for a scrape-config slice.

## Files touched
- `hack/deploy-operator.sh` — new, full deploy script (see What).
- `Makefile` — `deploy-operator` target.
- `README.md` — Project status section: gap closed, new limitation noted.
- `CHANGELOG.md` — Known gaps section revised: old entry closed, new
  per-mesh-Querier limitation added, "Closed this slice" entry added.
- `PLAN.md` — Phase 3's "not yet deployed" note resolved; Phase 4's
  scrape→Grafana pipeline note resolved; new checklist item after Phase 11
  for this slice.
- `docs/dev-istio.md`, `docs/dev-linkerd.md` — "Grafana / operator
  metrics" sections updated to point at `make deploy-operator` and state
  the new limitation instead of the old, now-resolved gap.

## Testing
All against the live dev Kind cluster, not simulated:
- `kubectl get clusterrolebinding | grep metrics-reader` — both bindings
  present (they'd already been applied by a prior session slice; this
  slice's script recreates them idempotently for anyone re-running it).
- Curled the operator's secured `/metrics` directly with the `prometheus`
  ServiceAccount's own token — HTTP 200 both before and after the
  `PROMETHEUS_URL` fix; zero `cascade_*` lines before, real series after
  (and only when hitting the actual leader replica, not the `Service`,
  see above).
- Drove a real trip via `payments-service`'s `/control/slow`
  fault-injection endpoint and sustained `checkout-service` traffic;
  confirmed via the exact `ErrorRateQuery`/`LatencyP99Query` PromQL that
  both thresholds were genuinely crossed (p99=995ms, error_rate≈0.33) before
  concluding the `Normal` phase was a real bug, not a timing artifact.
  `CascadePolicy` phase flipped to `Tripped`/`RetryStorm` within 18s of the
  fix.
- `up{job="cascade-operator"}` queried through *both* `istio-system`'s and
  `linkerd-viz`'s Prometheus — `1` on both — and
  `cascade_signatures_detected_total` queried through Prometheus itself
  (not curled directly) returned the same real counts the direct curl
  showed.
- Healed `payments-service` (`/control/heal`) and deleted every test pod
  created along the way; confirmed `checkout-service` back to `payments=200
  inventory=200` and no leftover pods.
- `bash -n hack/deploy-operator.sh` — clean. The script itself was not run
  start-to-finish as a single invocation in this slice (each step was run
  live, individually, against the already-partially-deployed cluster from
  an earlier session slice) — a fresh from-scratch run of the full script
  against a clean cluster is a follow-up worth doing before relying on it
  in, e.g., a from-scratch onboarding doc.

## Follow-ups / known gaps
- The per-mesh-`Querier` limitation above — needs a real design decision,
  not attempted here.
- `hack/deploy-operator.sh` has not been run start-to-finish as one
  invocation against a from-scratch cluster (see Testing) — each step was
  verified individually against this already-partially-set-up cluster.
- `docs/worklog/README.md`'s own index was found to be missing several
  entries from 2026-09-01 (the Linkerd/Phase 11 slices) predating this
  one — not this slice's scope, noted here so it isn't lost.
