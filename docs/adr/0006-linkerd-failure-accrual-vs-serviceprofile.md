---
status: accepted
date: 2026-08-31
decision-makers: "@gasthecreator"
---

# 0006. Linkerd: failure-accrual annotations for latency/error cascade, ServiceProfile retry budgets for retry storm

## Context and Problem Statement

Linkerd's circuit breaking is configured with `Service` annotations
(`balancer.linkerd.io/failure-accrual*`). Its retry budgets live on
`ServiceProfile`. Linkerd's documentation states that circuit breaking is
incompatible with ServiceProfiles: while a ServiceProfile exists for a
Service, proxies do not perform circuit breaking. Gateway API retry budgets
(GEP-3388) are still experimental.

## Decision Drivers

- Use only mechanisms Linkerd supports for production today.
- Do not take ownership of object lifecycle (operators here only patch).
- Surface limits explicitly rather than failing silently.

## Considered Options

- Failure-accrual annotations for latency/error cascade, `ServiceProfile`
  `retryBudget` for retry storm
- Gateway API `HTTPRoute` retry budgets for retry storm
- Delete or create ServiceProfiles to resolve the exclusivity

## Decision Outcome

Chosen option: "annotations plus ServiceProfile". `ServiceProfile` is
feature-frozen but remains the only production-viable retry-budget mechanism.
The two mechanisms share one Service, so the existing handoff logic from
ADR-0005 is reused rather than adding new arbitration state.

### Consequences

- Good: no new bookkeeping; the Linkerd Mitigator implements
  `HasManagedEdges`/`CompleteRestore` and the mesh-agnostic handoff does the
  rest.
- Bad: a dependency that already has a ServiceProfile can never receive
  latency/error mitigation, because Linkerd suppresses circuit breaking while
  any ServiceProfile exists. This is logged, not worked around.

### Confirmation

Live trip/restore cycles for both signatures against Linkerd 2.16 with byte-exact
restoration, and the Linkerd integration tests in CI.

## Pros and Cons of the Options

### Annotations plus ServiceProfile

- Good, because both are supported and verified live.
- Bad, because of the exclusivity limit above.

### Gateway API retry budgets

- Good, because it is the direction Linkerd is moving.
- Bad, because GEP-3388 is experimental with unresolved semantic gaps.

### Manage ServiceProfile lifecycle

- Good, because it would remove the exclusivity limit.
- Bad, because the Mitigators never own object lifecycle, and deleting a
  user's ServiceProfile is a destructive side effect.

## More Information

PLAN.md §5 Phase 6.6; `docs/worklog/2026-08-31-phase6.6-linkerd-mitigator.md`.
