# Retry storm's DestinationRule connectionPool.http secondary — last unbuilt patch cell, with a same-field overlap filed as a proposal rather than locked

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
Built retry storm's last unbuilt §2.6 patch cell: on a retry-storm trip,
alongside the existing `VirtualService` `retries.attempts` primary, also
patch the dependency's `DestinationRule` `connectionPool.http` —
`maxRetries` and `http1MaxPendingRequests`, resolved by the usual
`ParseServiceFQDN` + `Get` convention (no `Create` on miss). Restoration
ramps both object kinds back to their true originals together.

This is the second signature to manage two Istio object kinds on one trip
(the timeout secondary proved the shape). It is also the first time two
signatures claim the **same proto field** (`http1MaxPendingRequests`, fan-
out's primary and this secondary), not just the same object kind on
disjoint fields. That overlap is filed as a pending `PROPOSALS.md` entry —
not written into PLAN.md §2 as a locked decision.

## Why
§2.6's matrix has always named this secondary; this slice builds it, now
that the two-object-kind shape is proven and reviewed. The process
correction from the last slice's review is the other half of why this is
shaped the way it is: PLAN.md's existing text names both field sets and
does not say what a same-field collision means, so that question goes
through `PROPOSALS.md` rather than being declared "obviously what the
matrix already intended."

## How

### Trip values
Picked with the same "cite Envoy's actual default, distinguish bulkhead
from outage" reasoning `connpool.go`'s fan-out constants used:

- `TripRetryStormMaxRetries = 0`. Envoy's circuit-breaker `max_retries`
  default is 3 (Envoy proto doc; Istio's own DestinationRule comment says
  `2^32-1` — same class of doc-comment mismatch `istioDefaultTimeout`
  already resolved for `HTTPRoute.Timeout`). Retry storm's primary already
  fully disables `retries.attempts`; a nonzero "tightened" cap here would
  contradict that. This secondary backs the primary up with the same
  all-the-way value.
- `TripRetryStormMaxPendingRequests = 1`, not 0. This field is a general
  request bulkhead, not retry-specific, so it gets fan-out's
  bulkhead-not-outage reasoning applied fresh (Envoy default 1024; 1 caps
  bursts, 0 would be a full outage of the dependency). A separately named
  constant from fan-out's `TripHTTP1MaxPendingRequests` even though the
  values coincide today — each signature's trip value has to be
  justifiable, and independently changeable, on its own terms.

### Capture / restore
Own annotation key `cascade.gideonsanni.dev/original-retry-connection-pool`,
own JSON shape (just the two fields this secondary writes), own-annotation-
presence capture check (not `managed-by`) — DestinationRule is now
potentially touched by three signatures. Unlike fan-out's whole-block
`Unset` snapshot, this one never nils `connectionPool.http` on restore:
fan-out's `Http2MaxRequests` (and any user-authored sibling) can live on
the same sub-message, and this signature does not own the block.

Interpolation uses the fixed trip values as the `from` anchor and
`restoreProgress(step)` as `t`, same as every other numeric field. A
captured 0 ramps toward Envoy's implicit default mid-restore
(`max_retries` 3, `max_pending_requests` 1024) and writes 0 back at
true completion.

### Two-object-kind controller wiring
Same independence already locked in §2.6 for latency/error-cascade (and
reviewed): primary applies if the secondary's object is missing and vice
versa; `DependencyObjectMissing` scoped to the primary (`VirtualService`)
only; restore gathers both edge lists independently; `forceCompleteOutgoingRestore`'s
RetryStorm case now force-completes both object kinds. This is carrying
out that already-reviewed shape, one signature over — not a new
independence rule.

### The overlap — proposed, not decided
The matrix as written has retry storm's secondary and fan-out's primary
both assigning `http.Http1MaxPendingRequests`. Implementing that is what
this slice's code does, so there is something to review against. It is
**not** a claim that the overlap is the right long-term shape.

Direction 1 (keep both claims) is runtime-correct today only because
force-complete-on-handoff restores the outgoing signature to true original
*before* the incoming one captures. Own-annotation-keyed capture is not
enough for a shared field: capturing "current" while the other signature
holds the object would snapshot *its* trip value as *your* original. That
failure mode does not exist for disjoint fields. Fake-client tests for
both handoff directions confirm the current ordering lands the true
original (64) in the incoming signature's snapshot, not a leftover
trip/interpolated value.

Direction 2 (drop `http1MaxPendingRequests` from retry storm, keep only
`maxRetries`) would make the fields disjoint, matching every other sharing
case in this project, and `maxRetries` is the retry-specific half of this
secondary anyway. Left for Claude — see `PROPOSALS.md`.

Did not write either direction into PLAN.md §2.

## Files touched
- `internal/mitigation/retry_connpool.go` — new: trip + annotation + constants
- `internal/mitigation/retry_connpool_restore.go` — new: restore-step + complete
- `internal/mitigation/retry_connpool_test.go`, `retry_connpool_restore_test.go`
- `internal/controller/retry_mitigate.go` — primary/secondary split
- `internal/controller/retry_restore.go` — both object kinds on restore
- `internal/controller/restore.go` — force-complete RetryStorm case extended
- `internal/controller/retry_connpool_mitigate_test.go` — independent
  primary/secondary (each missing-the-other, both present, both missing,
  DetectOnly)
- `internal/controller/retry_connpool_restore_test.go` — both object kinds
  ramp together to completion
- `internal/controller/retry_connpool_handoff_test.go` — both-direction
  handoff through the shared field
- `internal/controller/retry_restore_test.go`, `fanout_restore_test.go`,
  `retry_storm_test.go`, `handoff_restore_test.go` — dispatch isolation
  and fixture updates for a third DestinationRule-touching signature
- `demo/k8s/inventory-destinationrule.yaml` — baseline DR so the secondary
  has an object to patch on a live retry-storm run
- `PROPOSALS.md` — pending entry for the field overlap
- `PLAN.md` — checklist; status-header fact that the secondary is built;
  §2.6's "remains contract, not built" sentence updated to "is now built".
  No new architecture-decision subsection, no overlap resolution in §2.
- `docs/worklog/README.md` — index this entry

## Testing
- `gofmt -l .` — clean. `make lint` — 0 issues. `go test ./...` — all packages pass.
- Mitigation-package tests: first-patch capture, no-overwrite, unset
  original, leave fan-out's `Http2MaxRequests` and annotation alone on
  trip and every restore step, monotonic ramp, Envoy-default mid-ramp
  for a zero original, complete-restore writes zeros in place rather
  than nilling the shared sub-message.
- Controller tests: both-present / each-missing-the-other / both-missing /
  DetectOnly; both-object-kind restore ramp; dispatch isolation against
  latency/error's outlierDetection and fan-out's `Http2MaxRequests`; both
  directions of fan-out ↔ retry-storm handoff through the shared field.
- **Live-confirmed on the Kind cluster.** Applied
  `demo/k8s/inventory-destinationrule.yaml`, ran `hack/run-k6-demo.sh
  retry-storm` against the operator running locally. Logs showed
  `patched VirtualService retries.attempts` and `patched DestinationRule
  connectionPool.http (retry storm secondary)` in the same reconcile —
  no Istio admission rejection (the webhook-fix from earlier today
  holding against this exact fixture). Checkout's real traffic then
  organically crossed fan-out's threshold on the same inventory host,
  producing an unscripted `RetryStorm → FanOutAmplification → RetryStorm`
  handoff: `"outgoing": "RetryStorm", "vsEdges": 1, "drEdges": 1"` — the
  two-object-kind force-complete path, live, including through the
  shared `http1MaxPendingRequests` field the pending proposal is about.
  After heal, restoration advanced `vsEdges: 1, drEdges: 1` through all
  5 steps and completed. Final state: VirtualService back to the true
  original (`attempts: 3`, `retryOn`/`perTryTimeout` restored, no operator
  annotations); DestinationRule annotations gone, `connectionPool.http: {}`
  left in place — the zeros-in-place restore of an originally-absent
  block, matching this signature's "don't nil a shared sub-message"
  choice (fan-out would have cleared the whole block; this one cannot,
  because it does not own it).

## Follow-ups / known gaps
- The `http1MaxPendingRequests` overlap — pending Claude's review of the
  `PROPOSALS.md` entry. If direction 2 wins, the pending-requests half of
  this secondary comes out; `maxRetries` stays.
- Kind-based integration test suite is still the only other open
  checklist item.
