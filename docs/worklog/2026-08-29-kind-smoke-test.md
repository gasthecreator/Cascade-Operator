# Kind smoke test: manager + CRD + sample CR

**Date:** 2026-08-29
**Author:** Claude
**Type:** test

## What
Ran the smoke test that was blocked on Docker: created a local Kind cluster,
installed the `CascadePolicy` CRD, ran the manager locally against the
cluster, applied the sample CR, and confirmed the reconciler logs and
defaults status correctly — including the 10s `RequeueAfter` timer actually
firing, not just watch-triggered reconciles.

## Why
This was the last item blocking full verification of the `feat/repo-scaffold`
slice (docs/worklog/2026-08-28-repo-scaffold-crd-reconciler.md's "Follow-ups"),
once Docker Desktop and Kind were installed.

## How
```
kind create cluster --name cascade-operator
make install            # CRD applied: cascadepolicies.cascade.gideonsanni.dev
make run                # backgrounded; go run ./cmd/main.go against the Kind context
kubectl apply -f config/samples/cascade_v1alpha1_cascadepolicy.yaml
```
- `kubectl get cascadepolicy` showed `PHASE=Normal` immediately — the
  reconciler's empty-phase defaulting worked.
- Log showed `reconciling CascadePolicy` with `generation`, `mode: Mitigate`,
  `service: checkout-service...` exactly as coded — two reconciles at
  creation (initial Get, then the follow-up triggered by the status Update),
  then a third at **exactly +10s** (00:50:28 → 00:50:38), confirming
  `RequeueAfter` is a real timer tick, not only watch-driven.
- Stopped the local manager process after verification. Left the Kind
  cluster (`kind-cascade-operator`) running for the next slice rather than
  tearing it down — `kind delete cluster --name cascade-operator` reclaims it
  if unwanted.

## Files touched
None — verification only, no code changed.

## Testing
This entry *is* the test record. All three original smoke-test criteria met:
operator starts, CRD installs, applying a sample CR produces the expected log
line (plus the bonus check that the requeue timer itself works).

## Follow-ups / known gaps
- Kind cluster is left running (`kind-cascade-operator`). No Istio installed
  on it yet — that's still a later checklist item, needed once the metrics/
  mitigation slices land.
- Next slice: Prometheus HTTP client behind `Query(ctx, promql) → Snapshot`
  (PLAN.md §2.4), unblocked and ready for Cursor.
