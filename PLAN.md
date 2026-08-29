# Cascade Operator — PLAN.md

**Status as of 2026-08-28: repo initialized, zero code written.** This file is the
single source of truth for goal, architecture, and progress. Read it before
touching code in any session (Cursor or Claude). Keep it updated as work lands —
this is a living document, not a one-time spec.

---

## 1. Goal

A custom Kubernetes operator that detects cascade failure signatures across
microservices and automatically applies circuit breaking — before the failure
completes — by patching Istio config directly. Istio's own circuit breaking
(`DestinationRule` outlier detection) is manual and config-driven: an engineer
hand-tunes thresholds after an incident. Nothing auto-detects these signatures
and dynamically reacts. That's the gap this project closes.

**Why this project exists:** portfolio piece for competitive SWE recruiting
(Core Technology & Engineering sector). It exists to demonstrate understanding
of distributed-systems failure modes and the Kubernetes reconciliation loop —
not a commercial product. Optimize for code that is **clean and defensible in
a technical interview** over feature completeness. Prioritize order: novelty →
recruiting relevance → feasibility → tech diversity. Market viability is not a
goal.

### The three failure signatures it must detect

1. **Latency/error cascade** — upstream latency spike + downstream error rate
   rise within a 30-second window.
2. **Retry storm** — repeated retries amplifying load on an already-degraded
   dependency.
3. **Fan-out amplification** — one failing call triggering a disproportionate
   number of downstream calls.

---

## 2. Architecture Decisions

### 2.1 Language: **Go** (decided, pending your sign-off)

`controller-runtime` + `kubebuilder` is the de facto standard for Kubernetes
operators — CRD/deepcopy codegen, informer caching, leader election, and
admission webhooks are first-class and battle-tested (cert-manager, ArgoCD,
Istio itself are all Go). Rust's `kube-rs` is capable but a much smaller
pattern library for this exact problem shape. For an interview-defensibility
goal, Go lets the conversation stay on cascade-detection logic and
reconciliation design rather than justifying tooling choices. Rust would be
the right call if this were a systems-programming showcase — it isn't.

### 2.2 Project scaffold: kubebuilder

Generate the base project (types, controller skeleton, RBAC manifests, CRD
YAML) with `kubebuilder init` / `kubebuilder create api`. Standard tooling,
predictable structure Cursor can pattern-match against.

### 2.3 CRD: `CascadePolicy` (name open to change — see Open Questions)

One CRD describing a service's dependency edges, detection thresholds, and
which Istio objects (`VirtualService`/`DestinationRule`) are eligible targets
for patching. Draft shape (not finalized):

```yaml
apiVersion: cascade.gideonsanni.dev/v1alpha1
kind: CascadePolicy
metadata:
  name: checkout-service
spec:
  service: checkout-service.default.svc.cluster.local
  dependsOn:
    - payments-service.default.svc.cluster.local
    - inventory-service.default.svc.cluster.local
  thresholds:
    latencyP99Ms: 500
    errorRateFraction: 0.05
    windowSeconds: 30
    retryStormMultiplier: 3.0     # retries/sec vs baseline
    fanOutMultiplier: 5.0         # downstream calls vs baseline per inbound call
  targetVirtualService:
    name: checkout-service
    namespace: default
status:
  phase: Normal | Tripped | Restoring
  lastSignature: LatencyErrorCascade | RetryStorm | FanOutAmplification
  lastTrippedAt: ...
  restoreStep: 0-4
```

### 2.4 Metrics: poll Prometheus HTTP API

Reconciler queries PromQL (`histogram_quantile`, `rate(...)`) on each
reconcile tick rather than standing up a custom metrics adapter — much less
infra for a portfolio-scope project, and the PromQL itself is a legible
interview talking point. Relies on Istio's standard `istio_requests_total` /
`istio_request_duration_milliseconds` metrics, broken out by
`destination_service`, `response_code`, and `response_flags` (retries show up
via `response_flags=UR` and related).

### 2.5 Detection engine: decoupled from the reconciler

`internal/signatures/` holds one detector per failure type, each a pure
function over a windowed metric snapshot → verdict (signature type +
evidence + confidence). No Kubernetes or Prometheus client dependency inside
this package — takes plain structs in, returns plain structs out. This is the
part that must be unit-testable without a cluster, and it's the part most
worth walking an interviewer through.

### 2.6 Mitigation: Istio patching + gradual restoration

- On a tripped signature, patch the target `DestinationRule` (outlier
  detection / connection pool limits) or `VirtualService` (retry budget,
  timeout) for the offending destination.
- Every patch the operator makes is annotated
  (`cascade.gideonsanni.dev/managed-by: cascade-operator`) so reconciliation
  can distinguish operator-applied patches from user-authored config and never
  clobbers the latter.
- Restoration is a step-function ramp, not an unpatch: e.g. 10% → 25% → 50% →
  100% traffic weight (or loosened outlier-detection thresholds), with a
  metrics re-check gate between each step. A regression during ramp re-trips
  immediately and resets to step 0.
- State machine per policy: `Normal → Tripped → Restoring(step N) → Normal`,
  tracked in `CascadePolicy.status`.

### 2.7 Local dev/test environment

- Kind cluster + Istio (demo profile) installed locally.
- Demo microservice topology to induce failures against — likely Istio's
  Bookinfo sample extended with a fault-injection sidecar endpoint, unless
  that proves too limited (see Open Questions).
- k6 (preferred over Locust — single static binary, easy to invoke from CI
  and from Go-based test harnesses without a Python dependency) to simulate
  latency spikes, retry storms, and fan-out load patterns.

---

## 3. Checklist — Built vs. Not Yet

Everything below is **not started**. Repo was an empty GitHub shell (0 commits)
until this session.

- [ ] Repo scaffold (kubebuilder init, go.mod, Makefile, CI skeleton)
- [ ] `CascadePolicy` CRD types + deepcopy + CRD YAML
- [ ] Prometheus client + PromQL query layer
- [ ] Signature detector: latency/error cascade
- [ ] Signature detector: retry storm
- [ ] Signature detector: fan-out amplification
- [ ] Reconciler wiring (metrics → detectors → decision)
- [ ] Istio patch layer (DestinationRule / VirtualService client + annotations)
- [ ] Gradual restoration state machine
- [ ] Operator's own Prometheus metrics (signatures detected, patches applied)
- [ ] Kind + Istio local dev environment docs/scripts
- [ ] Demo microservice topology for fault injection
- [ ] k6 cascade-simulation test scripts (latency spike, retry storm, fan-out)
- [ ] Unit test suite for detectors (no cluster required)
- [ ] Integration test suite (Kind-based, exercises real reconcile loop)
- [ ] golangci-lint + gofmt CI gate
- [ ] README (setup, architecture summary, demo instructions)

---

## 4. Open Questions

1. **CRD name/group** — `CascadePolicy` under `cascade.gideonsanni.dev/v1alpha1`
   is a placeholder. Confirm before generating kubebuilder scaffolding, since
   renaming after codegen means regenerating deepcopy/CRD YAML.
2. **Demo topology** — extend Istio's Bookinfo sample, or write a minimal
   custom 3-service app? Bookinfo is faster to stand up; a custom app gives
   cleaner control over inducing exactly the three signatures on demand.
3. **Istio patch target** — for the latency/error cascade signature, is the
   right mitigation an `outlier detection` change on `DestinationRule`, or a
   timeout/retry-budget change on `VirtualService`, or both depending on
   signature type? Needs a decision per signature before the mitigation layer
   is built.
4. **Custom-metrics API vs. direct Prometheus polling** — current plan is
   direct polling (2.4). Revisit only if reconcile-loop latency against
   Prometheus becomes a real bottleneck in testing — unlikely at demo scale.
5. **CI** — GitHub Actions for lint/test: set up now (empty repo, cheap) or
   defer until there's code to run against? Leaning toward now, since it's
   nearly free and enforces the gofmt/golangci-lint standard from commit one.

---

*Update this file after every meaningful milestone — new signature detector
landed, architecture decision changed, checklist item completed. Don't let it
drift from what's actually in the repo.*
