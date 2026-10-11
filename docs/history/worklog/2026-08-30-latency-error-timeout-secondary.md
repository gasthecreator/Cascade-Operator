# Latency/error-cascade's `VirtualService` timeout secondary: the first signature to manage two object kinds on one trip

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
Built latency/error-cascade's `VirtualService` `timeout` secondary
(§2.6's patch matrix) alongside its existing `DestinationRule`
`outlierDetection` primary: on a latency/error-cascade trip, the operator
now also resolves the dependency's `VirtualService` by the usual
`ParseServiceFQDN` + `Get` convention (no `Create` on miss) and sets every
forwarding route's `timeout` to `thresholds.latencyP99Ms` — the policy's
own CRD threshold, not an invented constant. Restoration ramps that
timeout back to its true original alongside the outlierDetection ramp,
using the same `restoreProgress(step)` curve and annotation-based
original-capture pattern as every other field.

This is the first signature to manage **two different Istio object kinds
on a single trip**. Every signature before this managed exactly one object
kind per trip, even though object *kinds* were already shared across
*different* signatures (`DestinationRule` between latency/error-cascade
and fan-out; and now `VirtualService` between retry storm and
latency/error-cascade too). Proving that shape here, deliberately, is what
unblocks retry storm's own remaining secondary
(`DestinationRule.connectionPool` cap) next.

## Why
§2.6's patch matrix has always listed this secondary as contract; this
slice builds it. Explicit scope from the prompt: this slice is
latency/error-cascade's secondary *only*, not both remaining secondaries at
once, specifically so the two-object-kind shape gets proven once before
being repeated.

## How

### The patch (`internal/mitigation/timeout.go`, `timeout_restore.go`)
Mirrors `retries.go`/`retry_restore.go`'s existing shape almost exactly,
one field over: `ApplyLatencyErrorTimeoutTrip` sets `route.Timeout` on
every route with a real destination (skips Redirect/DirectResponse/
Delegate routes, same as retry storm's own skip rule), captures the
pre-trip state in a new `cascade.gideonsanni.dev/original-timeout`
annotation (JSON array, one entry per `spec.http[]` route, same shape as
`AnnotationOriginalRetries`), and is an **unconditional set**, not
`min(existing, latencyP99Ms)` — a pre-trip timeout already shorter than
the threshold doesn't need loosening; the point of the backstop is that no
request to a confirmed-unhealthy dependency should run longer than the
threshold that defines the cascade in the first place.

Restoration (`timeout_restore.go`) needed one thing none of the other
ramped fields needed: the trip-time value (`latencyP99Ms`) is a per-policy
CRD field, not a package constant, and it's not part of the original-state
annotation (which only ever holds the *pre-trip* state). So
`ApplyLatencyErrorTimeoutRestoreStep` takes `tripLatencyP99Ms` as an
explicit parameter and interpolates from that fixed value toward each
route's stored original (or Envoy's documented 15s implicit default,
`istioDefaultTimeout`, for a route that had no explicit timeout pre-trip) —
confirmed against Envoy's own docs since Istio's `HTTPRoute.Timeout` field
comment just says "default is disabled" with no numeric default of its
own to check against.

**Bug caught and fixed while building this:** the first version of
`applyInterpolatedTimeout` anchored interpolation on the route's *current*
live `route.Timeout` value each step, not the fixed trip-time value. That
compounds across steps instead of producing the same linear-in-`t` ramp
every other field in this package uses (`restoreProgress(step)` computes
total progress from trip toward original, recomputed fresh every step).
Fixed by threading `tripTimeout` through explicitly, same fix in shape as
the `tripLatencyP99Ms` parameter above.

### Shared-object capture bug found and fixed (`internal/mitigation/retries.go`)
`VirtualService` becoming a shared object kind (retry storm's `retries`,
latency/error-cascade's `timeout`, disjoint fields — the same shape
`DestinationRule` already has between fan-out and latency/error-cascade)
exposed a latent bug: `ApplyRetryStormTrip`'s original-state capture was
keyed off `AnnotationManagedBy`'s presence, not `AnnotationOriginalRetries`'s
own. If latency/error-cascade's secondary patched a `VirtualService` first
(setting `managed-by`), and retry storm later tripped on the *same*
object, retry storm would see `managed-by` already set and skip capturing
its own baseline — losing the true pre-trip retries block. Fixed by keying
the check on `AnnotationOriginalRetries`'s own presence instead, mirroring
the defensive pattern `outlier.go`'s `ApplyLatencyErrorOutlierTrip`
already used for the `DestinationRule`-sharing case. `ApplyLatencyErrorTimeoutTrip`
was written with the correct (own-annotation-keyed) check from the start,
having this precedent already in hand.

### The two-object-kind decision (documented judgment, not a proposal)
Worked through deliberately, written up in `applyLatencyErrorMitigation`'s
doc comment (`internal/controller/mitigate.go`) and now in `PLAN.md` §2.6:

- The `DestinationRule` primary applies even if the `VirtualService`
  secondary is missing, and symmetrically the secondary applies even if
  the primary's object is missing. The two patches are fully independent,
  not a joint precondition — "secondary" in the matrix means additive to
  the primary, not a co-requirement.
- `DependencyObjectMissing` stays a single boolean, scoped to the primary
  only. A missing secondary is logged at info level and separately
  observable via `mitigationPatchesAppliedTotal{kind="VirtualService"}`
  simply not incrementing that tick, but doesn't flip the condition — with
  two independent objects, a missing secondary while the primary is
  present still means real mitigation is happening for that edge.

Did not file this in `PROPOSALS.md`: per that file's own rule, "routine
implementation choices that don't contradict or extend a decision already
in PLAN.md... do not need a proposal." §2.6 already called the timeout a
*secondary*, additive to the primary — this decision carries that word
choice out, it doesn't change what PLAN.md says. Filed as a §2.6 update
instead (with the full reasoning kept inline, same as every other
locked-decision paragraph in that section).

### Controller wiring (`internal/controller/mitigate.go`, `restore.go`)
Split `applyLatencyErrorMitigation` into `applyLatencyErrorOutlierPrimary`
(unchanged behavior, `DestinationRule`) and a new
`applyLatencyErrorTimeoutSecondary` (`VirtualService`), called
independently — one failing to resolve its object never blocks the other.
`beginRestoreLatencyError`/`advanceRestoreLatencyError`/
`applyLatencyErrorRestoreStep`/`completeLatencyErrorRestore` all now gather
and act on both `listManagedDestinationRuleEdges` and
`listManagedVirtualServiceEdges` results independently; only "both empty"
snaps to `Normal`. `forceCompleteOutgoingRestore`'s
`LatencyErrorCascade` case (the existing signature-handoff fix) was
extended to force-complete both object kinds when latency/error-cascade is
the outgoing signature.

### RBAC / scheme
Confirmed, not assumed: `VirtualService` was already registered on the
manager's scheme and already has RBAC markers from the retry-storm
slices (`config/rbac/role.yaml` already lists `virtualservices` verbs).
No changes needed.

## Files touched
- `internal/mitigation/timeout.go` — new: `ApplyLatencyErrorTimeoutTrip`,
  `AnnotationOriginalTimeout`, `originalRouteTimeoutJSON`
- `internal/mitigation/timeout_restore.go` — new:
  `ApplyLatencyErrorTimeoutRestoreStep`, `CompleteLatencyErrorTimeoutRestore`,
  `istioDefaultTimeout`
- `internal/mitigation/timeout_test.go`, `timeout_restore_test.go` — new
  unit tests
- `internal/mitigation/retries.go` — fixed the managed-by-keyed capture bug
  (own-annotation-keyed instead), doc comment explains why
- `internal/mitigation/retries_test.go`, `retry_restore_test.go` — extracted
  `testRedirectURI`/`testKeepMeRouteID` constants (goconst lint), no
  behavior change
- `internal/controller/mitigate.go` — split `applyLatencyErrorMitigation`
  into primary/secondary helpers; two-object-kind reasoning in the doc
  comment
- `internal/controller/restore.go` — `beginRestoreLatencyError`/
  `advanceRestoreLatencyError`/`applyLatencyErrorRestoreStep`/
  `completeLatencyErrorRestore` extended to both object kinds;
  `forceCompleteOutgoingRestore`'s `LatencyErrorCascade` case extended
- `internal/controller/latency_timeout_mitigate_test.go` — new: two-object
  mitigate scenarios (both present, VS missing, DR missing, both missing,
  DetectOnly)
- `internal/controller/latency_timeout_restore_test.go` — new: end-to-end
  restore ramp advancing both object kinds together to completion
- `internal/controller/retry_restore_test.go`,
  `internal/controller/fanout_restore_test.go` — added `dualManagedVS()`
  fixture helper; updated cross-signature dispatch assertions now that
  `VirtualService` carries two signatures' annotations in these scenarios
- `internal/controller/handoff_restore_test.go` — new:
  `TestSignatureHandoffLatencyErrorToFanOutForceCompletesBothObjectKinds`,
  `TestSignatureHandoffFanOutToLatencyErrorPatchesFreshVirtualServiceSecondary`
- `demo/k8s/payments-virtualservice.yaml` — new: baseline `VirtualService`
  fixture for live verification
- `PLAN.md` — §2.6 updated (secondary now built, two-object-kind shape
  documented); status header; checklist
- `docs/worklog/README.md` — index this entry

## Testing
- `gofmt -l .` — clean. `make lint` — 0 issues. `go build ./...` — clean.
- `go test ./...` — all packages pass (`internal/controller`,
  `internal/mitigation`, `internal/metrics`, `internal/signatures`).
- New unit tests cover: trip capture correctness (multi-route, explicit/
  unset timeout, redirect-only routes, fresh-capture-under-shared-managed-by
  regression), restore-step monotonic ramp progress, final-step exact
  restoration, both-object-kind mitigate dispatch (present/missing/
  DetectOnly permutations), end-to-end restore of both objects together,
  and both directions of signature handoff with mixed object-kind
  footprints.
- **Live-confirmed on the Kind cluster**, and better than planned: ran the
  `latency-error-cascade` k6 job against the real demo topology
  (`checkout → payments`) with the operator running locally against a
  port-forwarded Prometheus. Logs confirmed `patched DestinationRule
  outlierDetection` and `patched VirtualService timeout` firing together
  in the same reconcile on trip. Checkout's own real retry-loop traffic
  pattern against a failing `payments-service` organically crossed *both*
  latency/error-cascade's and fan-out's thresholds within the same run —
  producing a real, unscripted signature handoff
  (`LatencyErrorCascade → FanOutAmplification → LatencyErrorCascade`) that
  exercised the two-object-kind force-complete path live, not just in
  fake-client tests: log line `"Signature handoff: force-completing
  outgoing restore", "outgoing": "LatencyErrorCascade", "drEdges": 1,
  "vsEdges": 1` confirms both object kinds were force-completed together
  on handoff. After the induced failure cleared, the restoration ramp
  advanced through all 5 steps (`restoreStep: 0` → `4` → `Completed
  restoration ramp`) in the same tick-driven sequence, and final `kubectl
  get` on both objects confirmed a fully clean state: no `trafficPolicy`
  on the `DestinationRule`, no `timeout` and no operator annotations on
  the `VirtualService`, `CascadePolicy.status.phase: Normal`.

## Follow-ups / known gaps
- Retry storm's own remaining secondary (`DestinationRule.connectionPool`
  cap) — still unbuilt, explicitly next per the prompt now that this
  slice proved the two-object-kind shape.
- No fan-out secondary — none planned for v1alpha1, per §2.6.
- Operator self-metrics' `/metrics` endpoint was not spot-checked during
  this slice's live run (metrics server binds to `0`/disabled by default
  unless `--metrics-bind-address` is passed) — not in scope for this
  slice, but worth doing alongside the next live-verification pass since
  it's cheap to add.
