# Cascade Operator

A Kubernetes operator that watches Prometheus for **cascade failure
signatures** across a microservice mesh — a latency/error spike spreading
downstream, a retry storm amplifying load on an already-degraded dependency,
one failing call fanning out into a disproportionate number of others — and
automatically tightens the relevant mesh circuit breaker *before* the
failure completes, then gradually loosens it back once the signal clears.
It runs against **either Istio or Linkerd**, selected per-policy, behind one
shared detection/mitigation interface, and can optionally corroborate what
it sees in mesh metrics against real kernel-level TCP events captured by
eBPF (Cilium's Tetragon).

Every mesh already has circuit breaking (Istio's `DestinationRule` outlier
detection and retry budgets; Linkerd's failure-accrual annotations and
`ServiceProfile` retry budgets). What none of them have is anything that
*decides when to use it*: today that's a human, hand-tuning thresholds
after an incident. This project closes that gap for three specific failure
shapes, across two meshes. It's a portfolio piece, not a product — see
[`PLAN.md`](PLAN.md)'s §1 for the full goal statement and why that framing
drives every tradeoff in here.

**License:** [Apache 2.0](LICENSE) · **Contributing:** [CONTRIBUTING.md](CONTRIBUTING.md) · **Security:** [SECURITY.md](SECURITY.md)

## Architecture at a glance

Every reconcile tick (10s, or immediately on a `CascadePolicy` watch event),
the controller queries Prometheus for the protected service and its declared
dependencies, runs three pure detectors against the result, and — if one
trips — patches whichever mesh's primitive actually controls that failure
mode:

| Signature | Trips on | Istio patch | Linkerd patch |
|---|---|---|---|
| Latency/error cascade | upstream p99 latency + downstream error rate both over threshold in the same window | `DestinationRule` `outlierDetection` (eject unhealthy pods faster) | `Service` `balancer.linkerd.io/failure-accrual*` annotations |
| Retry storm | dependency:caller request-count ratio over threshold (retries amplifying load) | `VirtualService` `retries.attempts` → 0 (cut the retry budget) | `ServiceProfile` `spec.retryBudget` → fully suppressed |
| Fan-out amplification | downstream call count disproportionate to inbound call count | `DestinationRule` `connectionPool.http` (bulkhead in-flight calls) | detect-only — Linkerd has no connection-pool primitive, stated explicitly rather than silently skipped |

Detection and mitigation both sit behind one interface
(`internal/mesh.QueryBuilder` / `internal/mesh.Mitigator`) with Istio and
Linkerd as two equally first-class implementations
(`internal/mesh/istio`, `internal/mesh/linkerd`) — `spec.mesh` on the CR
picks which one a policy uses; everything else in the reconciler is
mesh-agnostic. `internal/signatures` (the three detectors) never imports
either mesh's types at all. Optionally, a fourth, independent signal —
real kernel-level TCP resets captured by [Tetragon](https://tetragon.io)
— corroborates an already-tripped verdict (a confidence boost + evidence
note, never a hard dependency: detection is unchanged with Tetragon
absent). Full reasoning for every choice above — including the two
mesh-specific incompatibilities this had to route around (Linkerd's
failure-accrual annotations and `ServiceProfile` are mutually exclusive
per `Service`) — is in [`PLAN.md`](PLAN.md) §2.6 and §5. The CRD itself
(`CascadePolicy`: a protected service, its dependency FQDNs, detection
thresholds, per-edge threshold overrides, and which mesh) is in §2.3,
reproduced in full in
[`config/samples/cascade_v1alpha1_cascadepolicy.yaml`](config/samples/cascade_v1alpha1_cascadepolicy.yaml).

The loop, once tripped, always runs the same way regardless of which
signature or mesh is in play:

```mermaid
flowchart LR
    A["Normal"] -- "signature detected<br/>(mesh patch applied)" --> B["Tripped"]
    B -- "condition clears" --> C["Restoring<br/>(step 0 → 4)"]
    C -- "regression" --> B
    C -- "step 4: true original restored" --> A
```

Restoration is a stepwise ramp back toward the exact pre-trip values (not a
traffic-weighted canary — see §2.6 for why), gated by a fresh metrics
check between steps, and a regression during the ramp re-trips immediately
rather than continuing to loosen. If two signatures could both patch the
same host's object (e.g. Istio's `DestinationRule`, shared between
latency/error-cascade and fan-out on disjoint fields) and a handoff
happens with no healthy tick in between, the outgoing signature's restore
is force-completed synchronously before the incoming one's trip is applied
— see §2.6's "Signature handoff on a shared object." That same
already-existing, fully mesh-agnostic mechanism is what resolves Linkerd's
own failure-accrual/`ServiceProfile` exclusivity too, with no additional
bookkeeping.

## Setup

You need a Kind cluster with a mesh installed to see any of this actually
patch something — the detectors themselves are cluster-independent and
unit-tested (`go test ./internal/signatures/...`), but the mitigate/restore
path patches real mesh objects and needs a real admission webhook to
validate against.

```bash
# 1. Kind cluster (bring your own, or use the one the scaffold smoke test used)

# 2a. Istio (pinned version) + Prometheus, on the current kube context
make istio-install
# 2b. ...and/or Linkerd + linkerd-viz (for its bundled Prometheus) — both
#     can coexist on one cluster, in separate namespaces, at the same time
make linkerd-install

# 3. The demo topology: checkout -> {payments, inventory}
make demo-deploy                                     # Istio, namespace `default`
kubectl apply -f demo/k8s-linkerd/                   # Linkerd, namespace `linkerd-demo`

# 4. The CascadePolicy CR that watches checkout-service
kubectl apply -f demo/k8s/cascadepolicy.yaml          # spec.mesh: Istio (the default)
kubectl apply -f demo/k8s-linkerd/cascadepolicy.yaml  # spec.mesh: Linkerd

# 5. The operator itself, pointed at whichever mesh's Prometheus you want
#    this run to detect against (port-forward it first)
go run ./cmd/main.go --prometheus-url=http://127.0.0.1:19090
```

Full detail on each of those — prerequisites, what gets installed, how to
confirm sidecars are injected, how to hand-query Prometheus — lives in
[`docs/dev-istio.md`](docs/dev-istio.md) (the Kind+Istio mesh),
[`docs/dev-linkerd.md`](docs/dev-linkerd.md) (the Kind+Linkerd mesh, plus
the retry-storm `ServiceProfile` fixture and the Tetragon TCP-reset
disruption), and [`docs/demo-topology.md`](docs/demo-topology.md) (the
three demo services and their control endpoints). `hack/install-tetragon.sh`
is self-documenting (read its own header comment) — there's no standalone
Tetragon dev-environment doc yet.

To deploy the operator itself to any cluster (build an image, install the
CRD, run the manager as a `Deployment` rather than `go run` from your host):
`make docker-build docker-push IMG=<registry>/cascade-operator:tag &&
make install && make deploy IMG=<registry>/cascade-operator:tag`. Run
`make help` for the full target list.

## Running the demo

Three k6 scripts, one per signature, each drive load through
`checkout-service` and toggle the right control endpoint at the right point
to induce that signature's exact pattern, then heal it and let restoration
run to completion:

```bash
hack/run-k6-demo.sh fanout-amplification
hack/run-k6-demo.sh latency-error-cascade
hack/run-k6-demo.sh retry-storm
```

Watch it happen in another terminal:

```bash
kubectl get cascadepolicy checkout-service -w
```

You should see `PHASE` move `Normal` → `Tripped` → `Restoring` (with
`RESTORE STEP` counting 0 through 4) → `Normal` over the ~170s each script
runs. **k6 has to run inside the cluster** (`hack/run-k6-demo.sh` does this
as a `Job`) — running it from your host against `kubectl port-forward`'d
URLs bypasses the mesh sidecar and produces empty/`+Inf` metrics for the
ratio-based signatures. Full detail, including which host each script
targets and why, prerequisites, and a couple of known transient-metric
artifacts that are expected and self-correct, is in
[`demo/k6/README.md`](demo/k6/README.md).

## Beyond detect → mitigate → restore

A handful of deliverables built on top of the core loop, each independently
demoable:

- **Visual cascade replay** — [`demo/replay/index.html`](demo/replay/index.html),
  a self-contained (no build step, no external CDN) page that animates a
  real captured trip→mitigate→restore trace: topology graph, metric
  sparkline, phase/signature/restore-step readout, and the live object's
  raw JSON spec with changed-since-baseline fields highlighted.
  `hack/capture-episode.sh <scenario>` captures a fresh trace from the live
  cluster into `demo/replay/traces/`.
- **Postmortem generator** — `cmd/postmortem` renders a real incident
  postmortem (timeline, root cause reconstructed from Prometheus history at
  the trip timestamp, blast radius, restoration status) from one
  `CascadePolicy`'s live status, with its own "Known limitations" section
  on every report rather than overclaiming precision.
- **Quantified resilience benchmark** — `make benchmark`
  (`hack/run-benchmark.sh`) runs each signature's k6 scenario twice against
  the live cluster — once in `DetectOnly`, once in `Mitigate` — and writes
  [`docs/benchmark-results.md`](docs/benchmark-results.md): time-to-detect,
  blast radius, time-to-restore, with an honest caveats section instead of
  numbers presented as more precise than a single live run actually
  supports.
- **Property-based state-machine verification** — `pgregory.net/rapid`
  generates random event sequences through the real `Reconcile` path and
  checks invariants (restore-step monotonicity, no orphaned annotations
  across a signature handoff) hold across the input space, not just the
  fixed regression fixtures.
- **eBPF kernel-signal corroboration** — a real TCP reset, forced from
  plain Go application code (`demo/internal/depsvc`'s `/control/reset`,
  `SO_LINGER 0` + `Close()`), captured by a Tetragon `TracingPolicy`
  watching `tcp_send_active_reset`, exported as a real Prometheus counter,
  and folded into an already-tripped verdict's confidence — corroboration
  only, never a replacement for the mesh-metric signal.
- **Security threat model** — [`docs/security-threat-model.md`](docs/security-threat-model.md),
  RBAC and trust boundaries checked against the actual generated manifests
  rather than assumed, with its own known-gaps section (cluster-scoped
  `ClusterRole`, no egress `NetworkPolicy`, no image signing) stated
  plainly.

## Project status

**All eleven planned phases are complete** — three signatures, two meshes,
detect → mitigate → restore end to end, including cross-signature and
cross-mesh-primitive handoffs, plus every deliverable in the section above.
Every phase's exact scope, the reasoning behind each nontrivial decision,
and the one remaining, deliberately-not-attempted loose end — an
operator-metrics scrape config, which needs the operator actually
deployed in-cluster first (itself needing cert-manager or a manual cert
for the admission webhook, Phase 3's own follow-up) plus a static
Prometheus job rather than the scaffolded `ServiceMonitor` (neither
dev-cluster Prometheus runs the Prometheus Operator) — are tracked live in
[`PLAN.md`](PLAN.md)'s §5 checklist, which is the source of truth for
what's built — this section won't try to keep a duplicate in sync. Every unit of work, including the reasoning behind decisions and the
real bugs found and fixed along the way (there were a lot, and hiding them
would defeat the point of a portfolio piece), has a dated entry in
[`docs/worklog/`](docs/worklog/README.md).

## Repo layout

```
api/v1alpha1/            CascadePolicy CRD types + generated deepcopy
internal/signatures/     Pure detectors: metric snapshot in, verdict out — no k8s/mesh/Prometheus deps
internal/metrics/        Prometheus HTTP client (Query → Snapshot)
internal/mesh/           QueryBuilder/Mitigator interface + istio and linkerd implementations
internal/mitigation/     Istio object mutation logic (moved behind internal/mesh/istio)
internal/controller/     Reconciler: metrics -> detectors -> mesh dispatch -> mitigation/restore -> corroboration
internal/notify/         Optional trip/restore webhook notifications
internal/webhook/        CascadePolicy validating admission webhook
cmd/                     Manager entrypoint
cmd/postmortem/          Standalone CLI: renders a postmortem from a CascadePolicy's live status
config/                  Kustomize: CRDs, RBAC, webhook, observability, sample CRs (generated except config/samples/)
demo/                    checkout -> {payments, inventory} demo topology (Istio + Linkerd copies), k6 scripts, replay UI
demo/k8s-linkerd/        Linkerd-injected copy of the demo topology
demo/replay/             Self-contained visual cascade-replay page + captured traces
demo/tetragon/           TracingPolicies watching TCP retransmits/resets
docs/                    Dev-env setup, demo-topology notes, benchmark results, security threat model
docs/worklog/            Dated history of what was built, why, and how (80+ entries)
hack/                    Local dev scripts (install Istio/Linkerd/Tetragon, deploy demo, run k6/benchmarks)
test/integration/        Kind-based integration tests: real apiserver, raw-JSON wire-format assertions
PLAN.md                  Goal, architecture decisions, and the live checklist
PROPOSALS.md             Queue for proposed changes to PLAN.md's locked decisions
```

## License

Copyright 2026 Gideon Sanni.

Licensed under the Apache License, Version 2.0. See [LICENSE](LICENSE) for the
full text. For how to contribute, see [CONTRIBUTING.md](CONTRIBUTING.md).
