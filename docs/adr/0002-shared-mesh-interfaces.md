---
status: accepted
date: 2026-08-31
decision-makers: "@gasthecreator"
---

# 0002. One shared `QueryBuilder`/`Mitigator` interface; Istio and Linkerd are both first-class implementations

## Context and Problem Statement

The first version was Istio-only. Everything Istio-specific lived in two
places: `internal/mitigation/` (every function typed on
`*DestinationRule`/`*VirtualService`) and hard-coded PromQL strings in
`internal/controller`. Supporting Linkerd meant deciding whether it would be
a special case or a peer.

## Decision Drivers

- The detection layer and the reconcile loop should not learn which mesh is
  in use.
- Adding a mesh must not risk the behavior already verified for Istio.
- The CRD change must be additive (no breaking `v1alpha1` change).

## Considered Options

- Linkerd branches added inside the existing Istio code paths
- A shared `mesh.QueryBuilder` and `mesh.Mitigator` interface, Istio as the
  reference implementation and Linkerd as a second implementation

## Decision Outcome

Chosen option: "shared interfaces". Istio's code was moved (not rewritten)
behind `internal/mesh/istio`, Linkerd was added as `internal/mesh/linkerd`,
and an additive `spec.mesh` field (`Istio | Linkerd`, default `Istio`)
selects the implementation per policy.

### Consequences

- Good: the reconciler is mesh-agnostic; an unset `spec.mesh` behaves exactly
  as before; `forceCompleteOutgoingRestore` turned out to need no changes
  because it already drove everything through the interface.
- Bad: the interface is the lowest common denominator, so mesh-specific
  limits (see ADR-0006, ADR-0007) must be surfaced explicitly rather than
  hidden.

### Confirmation

Integration tests for both meshes run in CI against one Kind cluster
(`.github/workflows/integration.yml`); `internal/controller/mesh_dispatch_test.go`
locks in per-policy dispatch.

## Pros and Cons of the Options

### Branches inside existing Istio code

- Good, because it is the smaller diff up front.
- Bad, because every Istio-typed function would grow a second path and the
  verified Istio behavior would be exposed to every Linkerd change.

### Shared interfaces

- Good, because the two meshes are peers and the move for Istio was
  mechanical.
- Bad, because the interface had to be designed before the second
  implementation existed.

## More Information

PLAN.md §5 Phase 6 (6.1-6.6);
`docs/worklog/2026-08-31-phase6-mesh-query-builder.md`,
`docs/worklog/2026-08-31-phase6.3-mitigator-fanout.md`,
`docs/worklog/2026-08-31-phase6.6-reconciler-mesh-dispatch.md`.
