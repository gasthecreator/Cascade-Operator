# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once tagged releases begin. Until then, `[Unreleased]` tracks `main`.

## [Unreleased]

Everything in PLAN.md §5's Production-Readiness & Multi-Mesh Initiative
(Phases 0–11) — full reasoning, live-verification evidence, and the real
bugs found along the way for every item below live in
[`docs/worklog/`](docs/worklog/README.md) and are tracked to completion in
[`PLAN.md`](PLAN.md)'s own checklist, the source of truth for scope.

### Added

- **Repo hygiene** (Phase 0): LICENSE, CONTRIBUTING, CODE_OF_CONDUCT,
  SECURITY, CODEOWNERS, CHANGELOG, `.editorconfig`, GitHub issue/PR
  templates, Dependabot.
- **CI** (Phase 1): Kind+Istio integration workflow running
  `make test-integration` in Actions, govulncheck, CodeQL.
- **Admission webhook** (Phase 3): rejects self-dependency, duplicate
  `dependsOn` entries, and malformed Service FQDNs on `CascadePolicy`.
- **Observability** (Phase 4): Grafana dashboard for the operator's own
  `cascade_*` metrics plus controller-runtime reconcile health; optional
  trip/restore webhook notifier (`internal/notify`, `--notify-webhook-url`).
- **Production hardening** (Phase 5): HA (`replicas: 2` + preferred
  pod anti-affinity), per-edge `spec.thresholdOverrides` (additive,
  every field a pointer so unset vs. explicit-zero is distinguishable),
  and a security threat-model doc (`docs/security-threat-model.md`).
- **Multi-mesh support** (Phase 6): a shared `internal/mesh.QueryBuilder`/
  `internal/mesh.Mitigator` interface, with `internal/mesh/istio` as the
  reference implementation (moved from `internal/controller`/
  `internal/mitigation`) and `internal/mesh/linkerd` as a second,
  equally first-class one — failure-accrual `Service` annotations for
  latency/error-cascade, `ServiceProfile.spec.retryBudget` for retry
  storm (via a hand-written, minimal typed client rather than the
  upstream `linkerd2` monorepo), and honest detect-only for fan-out
  (Linkerd has no connection-pool primitive). Additive `spec.mesh`
  field (`Istio | Linkerd`, default `Istio`) picks which one a policy
  uses. `hack/install-linkerd.sh` and Linkerd integration test coverage
  (`test/integration/linkerd_*.go`), plus CI now installing and
  exercising both meshes together.
- **Visual cascade replay** (Phase 7): `demo/replay/index.html`
  (self-contained, no build step) animating a captured trip→mitigate→
  restore trace; `hack/capture-episode.sh` to capture one from the live
  cluster.
- **Postmortem generator** (Phase 8): `cmd/postmortem` renders a real
  incident postmortem from a `CascadePolicy`'s live status and
  Prometheus history at the trip timestamp.
- **Resilience benchmark** (Phase 9): `make benchmark`
  (`hack/run-benchmark.sh`) runs each signature's k6 scenario in
  `DetectOnly` vs. `Mitigate` against the live cluster and writes
  `docs/benchmark-results.md`.
- **Property-based verification** (Phase 10): `pgregory.net/rapid`
  generators drive the real `Reconcile` path and the retry-storm patch
  builders through random sequences, checking state-machine and
  zero-value invariants across the input space, not just fixed
  fixtures.
- **eBPF kernel-signal corroboration** (Phase 11): a real TCP-layer
  fault-injection mode (`demo/internal/depsvc`'s `/control/reset`, a
  genuine `SO_LINGER 0` reset) captured by a Cilium Tetragon
  `TracingPolicy` and corroborated into an already-tripped verdict's
  confidence/evidence (`internal/signatures.ApplyKernelCorroboration`) —
  additive only, detection is unchanged with Tetragon absent.

### Fixed

Every "Fixed" entry below was a real, live-reproduced bug, not a
hypothetical — see the linked worklog convention for the exact
reproduction and confirmation for each:

- Retry-storm restore-completion's own zero-value `omitempty` hole
  (distinct from, and found after, the original trip-path fix below).
- Admission webhook validated only in isolation until Phase 3's real
  envtest-wired-webhook pass caught gaps a unit test alone couldn't.
- A Linkerd `kubectl apply -f <dir>` namespace-creation race
  (`namespace.yaml` alongside namespaced objects in the same batch,
  processed out of dependency order) — invisible on a long-lived local
  dev cluster, exposed by CI's always-fresh one.
- `linkerd install`/`install --crds` refuse outright when a control
  plane already exists (unlike `istioctl install`'s reconcile-in-place)
  — `hack/install-linkerd.sh` now detects this and uses `linkerd
  upgrade` instead.
- An unescaped `.` in a `kubectl jsonpath` expression
  (`hack/install-tetragon.sh`) silently returned empty instead of
  erroring, breaking the Prometheus scrape-config patch.

### Documentation

- `docs/dev-linkerd.md` — a Linkerd-equivalent of `docs/dev-istio.md`
  (install, verify sidecars, generate traffic and query linkerd-viz's
  Prometheus, the retry-storm `ServiceProfile` fixture, the Tetragon
  TCP-reset disruption).
- `hack/query-prom.sh` generalized to query any Prometheus Service
  (`PROM_NAMESPACE`/`PROM_SERVICE`, defaulting to Istio's unchanged) —
  needed so `docs/dev-linkerd.md` could point at linkerd-viz's Prometheus
  with the same script `docs/dev-istio.md` already used, rather than a
  second copy.

### Known gaps (documented, not silently assumed fixed)

- Tetragon is not installed or exercised in CI (`hack/install-tetragon.sh`
  is a local dev-environment tool only) — deliberate, not an oversight.
- `tetragon_events_total` (the metric kernel corroboration queries)
  doesn't disambiguate which kprobe fired — accurate for this project's
  current state, since `/control/reset` is the only mechanism that has
  ever produced a real event, but would need refining if a real
  packet-loss mechanism is added later.
### Closed this slice

- **Operator metrics now genuinely scraped in-cluster**
  (`hack/deploy-operator.sh`, `make deploy-operator`): the operator is
  deployed in-cluster for the first time in this project's history (every
  prior live check ran it via `go run` from the host) — cert-manager
  installed for the admission webhook's TLS (Phase 3's own long-standing
  follow-up), `PROMETHEUS_URL` wired so `reconciler.Metrics` is non-nil
  (confirmed live: omitting it silently disables all detection — `Normal`
  forever, no error, just a startup log line easy to miss), RBAC bound so
  a mesh's Prometheus can read the operator's secured `/metrics`, and a
  static scrape job added to that Prometheus (same technique
  `hack/install-tetragon.sh` already used for Tetragon). Verified live
  end-to-end, not just deployed: triggered a real trip via the demo
  topology's fault-injection endpoints, confirmed `cascade_*` counters
  incremented on the operator's own `/metrics`, and confirmed the *same*
  numbers come back through a real PromQL query against Prometheus itself
  (`cascade_signatures_detected_total`), not just a direct curl.
- **Per-mesh Prometheus client, closing the gap surfaced while verifying
  the item above**: the operator's Prometheus client was one process-wide
  `PROMETHEUS_URL` (`cmd/main.go`), but `CascadePolicy.spec.mesh` can be
  Istio *or* Linkerd, each scraped by a different mesh's own Prometheus.
  Confirmed live: with `PROMETHEUS_URL` pointed at Istio's Prometheus, an
  Istio-mesh policy detected and tripped correctly, but a Linkerd-mesh
  policy on the same operator process reconciled forever without ever
  seeing real data — silently, not as an error. Fixed with
  `MetricsIstio`/`MetricsLinkerd` (`internal/controller/cascadepolicy_controller.go`'s
  `metricsQuerier()`) and matching `--prometheus-url-istio`/
  `--prometheus-url-linkerd` flags, each an independent `metrics.Client`;
  `metricsQuerier()` picks the mesh-specific one for a policy's own
  `spec.mesh`, falling back to the shared field so single-mesh deployments
  need no change. A second, real blocker turned up live-verifying this
  against `linkerd-viz`'s actual Prometheus (not just a fake-client unit
  test): `linkerd-viz` ships its Prometheus locked down by a deny-by-default
  `AuthorizationPolicy` on its query port (only its own `metrics-api`
  ServiceAccount, authenticated via mTLS, is allowed in) — an unmeshed
  operator pod got a clean HTTP 403 regardless of RBAC or query
  correctness. Closed by mesh-injecting the operator itself
  (`linkerd.io/inject: enabled`) and adding a second, additive
  `AuthorizationPolicy` granting its own ServiceAccount identity access to
  the same `Server` (Linkerd's own policy validator rejects more than one
  ServiceAccount per `AuthorizationPolicy`, so this is a new policy
  alongside `metrics-api`'s existing one, not an edit to it) — confirmed
  this cluster's `defaultInboundPolicy` is `all-unauthenticated` first, so
  meshing the operator doesn't newly restrict its other inbound traffic
  (istio-system's own unmeshed Prometheus scrape of the operator's
  `/metrics` keeps working unauthenticated). Live-verified end-to-end on
  both meshes at once: a real trip on the Istio-mesh demo policy and a
  separate real trip on the previously-broken Linkerd-mesh demo policy,
  both against their own mesh's real Prometheus, both mitigating and fully
  restoring.
- **Security hardening from `docs/security-threat-model.md`'s own Known
  gaps section** — all three built and now live-verified end-to-end (a
  fourth wave of dev-cluster instability interrupted the first attempt;
  completed once the cluster recovered):
  - **Image signing/provenance**: `.github/workflows/publish-image.yml`
    builds and publishes the manager image to GHCR, signs it keylessly
    with `cosign` via the workflow's own OIDC identity (no private key to
    manage), and attaches SLSA provenance + an SBOM via
    `docker/build-push-action`'s own support. Triggered for the first
    time via `workflow_dispatch`; the first real run failed with a real
    bug (`invalid tag ...: repository name must be lowercase` —
    `github.repository` preserves this repo's real mixed-case name
    verbatim), fixed by lowercasing it.
  - **Egress `NetworkPolicy`** —
    `config/network-policy-egress/restrict-egress.yaml` restricts the
    operator to DNS, the Kubernetes API server, each mesh's Prometheus,
    and Linkerd's own control plane. Enforcement genuinely confirmed via
    Calico-for-policy (Kind's default CNI doesn't enforce `NetworkPolicy`
    on its own) — which surfaced and fixed a real upstream Calico RBAC
    bug, then a real gap in the policy's own first draft, then two more
    real bugs while finishing the deliberate allow/deny pass/fail test: a
    broken API-server rule (a `podSelector` that never matched real
    traffic — this CNI evaluates egress against the pre-DNAT ClusterIP,
    not the post-DNAT pod; fixed with an `ipBlock`), and a separate issue
    that looked identical but wasn't this `NetworkPolicy` at all — the
    operator's own Linkerd sidecar intercepting its API-server calls,
    fixed via a `config.linkerd.io/skip-outbound-ports` annotation. Final
    test: DNS/both meshes' Prometheus allowed, external internet and two
    same-pod-wrong-port cases genuinely blocked.
  - **Namespace-scoped RBAC**: `cmd/main.go`'s `--watch-namespaces`/
    `WATCH_NAMESPACES` flag, `config/rbac-namespaced/role.yaml.tmpl` +
    `hack/generate-namespaced-rbac.sh`, and
    `hack/switch-to-namespaced-rbac.sh` (`make switch-to-namespaced-rbac`).
    Live-verified via `kubectl auth can-i`: cluster-wide access correctly
    revoked, both watched namespaces correctly granted, an unwatched
    namespace correctly denied, reconciliation unaffected.

  See `docs/worklog/2026-09-04-security-hardening-and-a-real-calico-bug.md`
  and `docs/worklog/2026-09-04-live-verification-completed.md` for the
  full accounting of all four bugs found and fixed via live testing.

## [0.1.0] - 2026-08-31

First portfolio-complete milestone: three cascade signatures detectable from
Prometheus, mitigatable via Istio patches, and restorable through a shared
ramp — plus demo topology, k6 scripts, and Kind-based integration coverage
for retry storm wire-format.

### Added

- **Project foundation:** Kubebuilder scaffold, `CascadePolicy` v1alpha1 CRD,
  logging reconciler, golangci-lint CI gate, and initial planning docs
  (`PLAN.md`, `PROPOSALS.md`, worklog convention).
- **Metrics layer:** Prometheus HTTP client and PromQL helpers feeding a
  snapshot-based detector interface.
- **Latency/error cascade:** Detector, `DestinationRule` `outlierDetection`
  primary patch, gradual restoration ramp, and `VirtualService` timeout
  secondary (two-object-kind trip + restore).
- **Retry storm:** Detector, `VirtualService` `retries.attempts` primary,
  `DestinationRule` `connectionPool.http.maxRetries` secondary, two-object-kind
  restoration, and signature-dispatched restore machinery.
- **Fan-out amplification:** Detector, `DestinationRule` `connectionPool.http`
  bulkhead primary, restoration, and cross-host PromQL.
- **Signature handoff:** Force-complete outgoing signature restore before
  adopting an incoming one on a shared Istio object.
- **Operator metrics:** Counters for signatures detected, patches applied,
  and restorations completed/regressed.
- **Dev environment:** Kind + Istio 1.30.4 install scripts, Prometheus
  scrape path, and `docs/dev-istio.md`.
- **Demo topology:** Checkout → {payments, inventory} microservices,
  `CascadePolicy` fixture, inventory retry `VirtualService`, and deploy
  scripts (`make demo-deploy`, port-forward helper).
- **k6 cascade simulations:** In-cluster Job scripts for all three signatures
  with live-evidence worklogs.
- **Integration tests:** `test/integration/` (`make test-integration`) —
  real reconciler against dev Kind cluster, unstructured JSON assertions for
  retry storm `attempts:0` and `maxRetries:1` trip/restore.
- **Root README** with architecture summary and demo instructions.

### Fixed

- **Retry-storm webhook rejection:** Clear `retryOn` / `perTryTimeout` /
  `backoff` alongside `attempts: 0` so Istio's validating webhook accepts
  the trip patch.
- **Zero-value serialization:** Patch-based writes for `attempts: 0` and
  (formerly) `maxRetries: 0` so `omitempty` does not strip explicit zeros from
  stored JSON.
- **Istio `maxRetries: 0` translation:** Trip value changed to `1` — Pilot
  treats explicit zero as unset; Envoy never received a real circuit-breaker
  cap at zero.
- **Retry-storm secondary overlap:** Dropped `http1MaxPendingRequests` from
  retry storm's `connectionPool.http` secondary; fan-out retains ownership of
  pending-request fields.
- **Signature-handoff restore:** Outgoing signature fields and annotations no
  longer orphaned when a different signature trips on the same object mid-ramp.

### Changed

- Retry-storm `connectionPool.http` secondary now writes only `maxRetries`
  (trip value `1`, not `0`).
- `demo/k6` documentation updated to reflect working retry-storm mitigation
  (supersedes earlier "patch broken" gap notes).

### Known gaps (documented, not yet fixed)

- Retry-storm restore completion may still hit the zero-value `omitempty` hole
  when the captured original was literally zero (PLAN.md §5 Phase 5).
- Integration coverage exists for retry storm only; latency/error and fan-out
  Kind tests are planned (Phase 2).
- Kind+Istio integration tests are not yet run in GitHub Actions CI (Phase 1).

[Unreleased]: https://github.com/gasthecreator/Cascade-Operator/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/gasthecreator/Cascade-Operator/releases/tag/v0.1.0
