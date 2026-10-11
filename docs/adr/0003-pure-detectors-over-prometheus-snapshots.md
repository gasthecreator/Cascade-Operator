---
status: accepted
date: 2026-08-28
decision-makers: "@gasthecreator"
---

# 0003. Detectors are pure functions over a metric snapshot; metrics come from polling Prometheus

## Context and Problem Statement

Each failure signature (latency/error cascade, retry storm, fan-out
amplification) needs a windowed view of mesh metrics. We had to decide where
that window lives and how much of Kubernetes or Prometheus the detection code
may know about.

## Decision Drivers

- Detection logic must be unit-testable without a cluster.
- Avoid extra moving parts and security surface in the cluster.
- The 30-second window should have one owner.

## Considered Options

- Poll the Prometheus HTTP API each reconcile; detectors take a plain
  snapshot struct and return a verdict
- Keep a local ring buffer of samples in the operator
- Stand up a custom-metrics API adapter

## Decision Outcome

Chosen option: "poll Prometheus; pure detectors". Prometheus owns the window
through range-vector queries. `internal/signatures` takes plain structs in and
returns a `Verdict` (signature, evidence, confidence) with no Kubernetes,
Prometheus-client or mesh imports. The client sits behind a narrow
`Query(ctx, promql) -> Snapshot` interface, and reconcile is driven by watch
events plus `RequeueAfter` (10s default).

### Consequences

- Good: detectors are table-testable, fuzzable and benchmarkable in isolation.
- Bad: detection depends on Prometheus availability; a missing Prometheus URL
  silently disables detection (found live 2026-09-03), so the client being nil
  must be loud in logs.

### Confirmation

`internal/signatures` has no imports of `k8s.io/*`, `controller-runtime` or
`internal/mesh/*`; its unit tests run with no cluster.

## Pros and Cons of the Options

### Poll Prometheus; pure detectors

- Good, because it adds no new component and reuses Prometheus's windowing.
- Bad, because each tick costs a few queries.

### Local ring buffer

- Good, because it avoids repeated queries.
- Bad, because it duplicates state Prometheus already holds and loses it on
  restart.

### Custom-metrics API adapter

- Good, because it is the standard path for HPA-style consumers.
- Bad, because it adds another Deployment, an aggregation-layer APIService and
  security surface for no benefit here.

## More Information

PLAN.md §2.4 (closed 2026-08-28) and §2.5;
PROPOSALS.md "Close custom-metrics-API vs. polling".
