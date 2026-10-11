# Per-mesh Prometheus client, and getting past linkerd-viz's own locked-down AuthorizationPolicy

**Date:** 2026-09-03
**Author:** Claude (solo)
**Type:** feature + infra

## What
- `internal/controller/cascadepolicy_controller.go`: `CascadePolicyReconciler`
  gained `MetricsIstio`/`MetricsLinkerd metrics.Querier` fields and a
  `metricsQuerier(policy)` dispatch method — mirrors `queryBuilder()`/
  `mitigator()`'s existing dispatch-on-`spec.Mesh` shape. Prefers the
  mesh-specific field for a policy's own mesh, falls back to the shared
  `Metrics` field otherwise, so every existing test and single-mesh
  deployment needed zero changes.
- `cmd/main.go`: new `--prometheus-url-istio`/`--prometheus-url-linkerd`
  flags (`PROMETHEUS_URL_ISTIO`/`PROMETHEUS_URL_LINKERD` env vars),
  constructing an independent `metrics.Client` for each and wiring them to
  the new fields above. `--prometheus-url`/`PROMETHEUS_URL` is unchanged
  (still populates the shared fallback field).
- `internal/controller/mesh_dispatch_test.go`: `TestMetricsQuerierDispatch`
  (table-driven, six cases covering both meshes × both-set/one-set/
  neither-set), plus two Reconcile-level tests
  (`TestReconcileIstioPolicyUsesMetricsIstioNotSharedMetrics`,
  `TestReconcileLinkerdPolicyUsesMetricsLinkerdNotSharedMetrics`) that wire
  a healthy shared `Metrics` alongside a tripping mesh-specific field and
  assert the policy still trips — proving dispatch through the real
  `Reconcile` path, not just the method in isolation — and
  `TestReconcileNoMetricsForMeshNeverPolls` locking in the deliberate
  "no Querier for this mesh → never trips, no error" shape. Also simplified
  `linkerdTestPolicy` to take no `mode` parameter (an `unparam` lint finding
  — every call site already passed `PolicyModeMitigate`).
- `hack/deploy-operator.sh`: new step 6 — if `linkerd-viz` is present,
  mesh-inject the operator's own namespace (`linkerd.io/inject: enabled`)
  and grant its ServiceAccount a second `AuthorizationPolicy` on
  `linkerd-viz`'s locked-down `prometheus-admin` `Server` (see below for
  why this is a *second* policy, not an edit). Step 7 (was step 6) now
  wires whichever of `PROMETHEUS_URL_ISTIO`/`PROMETHEUS_URL_LINKERD`
  matches a mesh actually present, instead of a single `PROMETHEUS_URL`.
- `docs/security-threat-model.md`: new trust-boundary item (#6) describing
  the operator's new Linkerd mTLS identity and the additive
  `AuthorizationPolicy` it relies on; resolved the stale "admission webhook
  not deployed to the dev cluster" known gap (closed by the prior slice,
  PR #35, just not yet reflected here).
- `README.md`, `CHANGELOG.md`, `PLAN.md`, `docs/dev-istio.md`,
  `docs/dev-linkerd.md` — updated to describe both the per-mesh-Querier gap
  and the `linkerd-viz` access blocker as closed, not open.

## Why
The immediately preceding slice (PR #35, `docs/worklog/2026-09-03-operator-in-cluster-deploy-and-metrics-scrape.md`)
closed the operator-metrics-scrape gap but surfaced a second, real
limitation while doing it: a single process-wide `PROMETHEUS_URL` cannot
correctly serve both an Istio-mesh and a Linkerd-mesh `CascadePolicy` from
one operator process, since each mesh's proxies are scraped by a different
Prometheus. That PR merged with this documented as an open, deliberately
unattempted gap. Asked directly ("finish everything up... do not stop
until everything is 100%") to close every remaining item that didn't
require the user's own judgment call, this was the one concrete piece of
engineering value left — the rest of the previously-identified list
(Tetragon not in CI, `tetragon_events_total` not disambiguating kprobes,
`hack/deploy-operator.sh` not run start-to-finish, a stale worklog index)
were all deliberate or cosmetic, not real remaining work.

## How
The reconciler change itself is small and additive — see the doc comment
on `MetricsIstio`/`MetricsLinkerd` in `cascadepolicy_controller.go` for the
exact reasoning. `go build`/`go vet`/`go test ./... -race`/`make lint` all
clean before touching the live cluster.

**Live verification, Istio side**: rebuilt the image, `make deploy
IMG=cascade-operator:dev`, a `rollout restart` (the image tag didn't
change, so Kubernetes wouldn't otherwise notice the new bytes were loaded
onto the Kind node), then `kubectl set env` to remove the old
`PROMETHEUS_URL` and set both `PROMETHEUS_URL_ISTIO` and
`PROMETHEUS_URL_LINKERD` at once. Startup logs on both replicas confirmed
`"Istio-mesh Prometheus metrics client configured"` and
`"Linkerd-mesh Prometheus metrics client configured"`.

**Live verification, Linkerd side, first attempt — a real, distinct
blocker**: drove `payments-service.linkerd-demo`'s own `/control/slow`
fault injection plus sustained traffic through `checkout-service`,
confirmed via the *exact* `internal/mesh/linkerd.QueryBuilder` PromQL run
directly against `linkerd-viz`'s Prometheus that both thresholds were
genuinely crossed (p99≈995ms vs. 500ms, error_rate≈0.21 vs. 0.05) — but the
`CascadePolicy` stayed `Normal`. Operator logs showed the real cause:
`"p99 latency query failed ... error: prometheus query: HTTP 403"` — the
fix *was* correctly reaching `linkerd-viz`'s Prometheus now (previously it
would have silently never tried), but that Prometheus rejected the call.
Investigated rather than assumed: `linkerd-viz`'s own `prometheus` Service
names its query port `admin` (`port: 9090`, `name: admin`), which matches
`linkerd-viz`'s own shipped `server.policy.linkerd.io/prometheus-admin`
(`accessPolicy: deny`) and its paired
`authorizationpolicy.policy.linkerd.io/prometheus-admin`
(`requiredAuthenticationRefs: [ServiceAccount linkerd-viz/metrics-api]`) —
a deliberate Linkerd-viz security default, not a bug: only its own
`metrics-api` component, authenticated via mTLS, may query Prometheus
directly. `hack/query-prom.sh`'s own port-forward-based queries had worked
fine all session because a `kubectl port-forward` tunnels straight to the
container port, bypassing the mesh's inbound proxy (and its policy
enforcement) entirely — a real, easy-to-miss distinction between "I can
query it via port-forward" and "the operator's own in-cluster Service call
can reach it."

This is a genuine security-relevant infrastructure decision (mesh-inject
the operator, and/or modify a security policy `linkerd-viz` ships
locked-down on purpose) rather than an ordinary code fix, so it was put to
the user directly via `AskUserQuestion` rather than picked unilaterally:
ship the code fix and document the `linkerd-viz` access gap as still open,
mesh-inject the operator, or relax the policy directly. Chose to mesh-inject.

**Mesh-injecting the operator**: checked the cluster's own
`defaultInboundPolicy` first (`all-unauthenticated`, from
`linkerd-config`'s stored Helm values) — this matters because meshing a
pod subjects its *other* inbound traffic to Linkerd's policy engine too,
and a restrictive cluster default could have silently broken the
already-shipped, already-merged (PR #35) Istio-Prometheus-scrapes-the-
operator's-`/metrics` setup, since istio-system's own Prometheus is not
Linkerd-meshed and would suddenly need to satisfy whatever the new default
required. Confirmed permissive first, then annotated
`cascade-operator-system` with `linkerd.io/inject: enabled` and restarted
the deployment; startup logs confirmed a real mTLS identity
(`cascade-operator-controller-manager.cascade-operator-system.serviceaccount.identity.linkerd.cluster.local`).

**Granting access — one real wrinkle**: the obvious first attempt (append
the operator's ServiceAccount to `prometheus-admin`'s existing
`requiredAuthenticationRefs` list) was rejected by Linkerd's own policy
validator admission webhook: `"only a single ServiceAccount may be set"`.
The correct Linkerd pattern for a second authorized caller is a *second*
`AuthorizationPolicy` targeting the same `Server` — created
`linkerd-viz/prometheus-admin-cascade-operator`, leaving the existing
`metrics-api` grant completely untouched (confirmed via
`kubectl get authorizationpolicy prometheus-admin -o jsonpath=...` showing
only the original entry, unchanged).

**Re-verification, full cycle**: repeated the exact same fault-injection +
sustained-traffic sequence against `linkerd-demo`. This time: `Tripped`/
`LatencyErrorCascade` at t+24s, confidence 1.0, evidence
`p99_ms=1250 error_rate=0.2` (both well past threshold), a real
`patched Service failure-accrual annotations` log line, and — after
healing `payments-service` — a full restore back to `Normal` within a
single ramp tick, `Service` annotations fully cleared. No `403`s anywhere
in the logs afterward. Confirmed the Istio-mesh demo policy was still
`Normal`/healthy throughout (unaffected by any of this), and that both
mesh Prometheus instances still report `up{job="cascade-operator"}=1`.

## Files touched
- `internal/controller/cascadepolicy_controller.go` — `MetricsIstio`/
  `MetricsLinkerd` fields, `metricsQuerier()`, four `Query` call sites and
  the `Reconcile` polling gate switched to use it.
- `internal/controller/mesh_dispatch_test.go` — new tests (see What);
  `linkerdTestPolicy()` simplified.
- `cmd/main.go` — two new flags/env vars, two new `metrics.Client`
  constructions.
- `hack/deploy-operator.sh` — new step 6 (mesh-inject + AuthorizationPolicy,
  both idempotent), step 7 rewritten for per-mesh env wiring, header
  renumbered 7→8 steps.
- `Makefile` — `deploy-operator` help text updated (no more "known
  limitation" caveat).
- `docs/security-threat-model.md` — new trust boundary #6; resolved the
  stale "webhook not deployed" known gap.
- `README.md`, `CHANGELOG.md`, `PLAN.md`, `docs/dev-istio.md`,
  `docs/dev-linkerd.md` — gap descriptions updated from open to closed.
- Applied directly to the live cluster (not committed, cluster-state only):
  the `linkerd.io/inject: enabled` namespace annotation and the
  `prometheus-admin-cascade-operator` `AuthorizationPolicy` — both are also
  created idempotently by `hack/deploy-operator.sh` itself now, so a
  from-scratch run reproduces this without manual steps.

## Testing
- `go build ./...`, `go vet ./...`, `go test ./... -race` — clean.
- `make lint` (project's pinned `golangci-lint`) — clean (fixed one
  `gofmt` alignment issue and one genuine `unparam` finding along the way).
- `bash -n hack/deploy-operator.sh` — clean.
- Live, end-to-end, on both meshes: see How above — real trips, real
  mitigation patches, real full restores, driven by each mesh's own real
  Prometheus, not fake-client unit tests alone.
- Confirmed no regression: the Istio-mesh demo policy and the
  already-shipped operator-metrics scrape (both meshes' `up{job=
  "cascade-operator"}=1`) still work after mesh-injecting the operator.
- Cleaned up every test pod created along the way; healed both demo
  topologies' fault-injected dependencies; confirmed `kubectl get pods -A`
  shows no non-`Running` pods and `docker stats` on the Kind node container
  shows normal, non-pegged resource usage after all of this.

## Follow-ups / known gaps
- `hack/deploy-operator.sh` still hasn't been run start-to-finish as one
  clean invocation against a from-scratch cluster (noted in the prior
  slice's worklog too) — every step across both slices was verified
  individually against this already-partially-set-up cluster. The new
  step 6's idempotency checks were verified in isolation against the
  already-configured live state, not via a genuine fresh run.
- `docs/worklog/README.md`'s index is still missing a few 2026-09-01
  entries predating both of these slices (noted previously, still not
  this scope).
