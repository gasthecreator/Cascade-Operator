# Fan-out amplification: detector, cross-host PromQL, connectionPool bulkhead, restoration — built together

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
The third and last signature, built as one unit end to end: `DetectFanOut`
(pure function), a cross-host PromQL ratio query, reconciler wiring as the
third per-host check and third mitigation-dispatch case, a new
`DestinationRule` `connectionPool.http` bulkhead primary (trip + restore),
and a fourth restoration dispatch case. `FanOutAmplification` now goes all
the way through detect → mitigate → restore, the same full loop the other
two signatures already have. Along the way, found and fixed a real bug in
how "have I captured my own baseline yet" is decided on a `DestinationRule`
that more than one signature can manage, and filed the one part of that
gap that's still open as a `PROPOSALS.md` entry rather than silently
patching around it.

## Why
PLAN.md §2.6's fan-out row (`DestinationRule connectionPool.http` bulkhead)
was the last unbuilt cell, and the previous slice's live-scrape evidence
(fan-out-demo-evidence worklog) had already answered the two questions that
would otherwise have blocked design: the ratio is cross-host (dependency's
request count over the *caller's*, not a same-host reporter split like
retry storm), and its implicit baseline is 1 — a healthy `checkout →
{payments, inventory}` run held exactly 1:1:1, and `payments` failing with
`checkout`'s own app-level retry loop drove the ratio to exactly 3×. This
slice was scoped to build detector, mitigation, and restoration together
(unlike the first two signatures, which each had detector, mitigation, and
restoration as separate slices) — by this point the shape is proven twice
over, so there's less unknown risk in doing it as one unit, and a shared
subtlety (fan-out and latency/error-cascade both touching a
`DestinationRule`) is easier to reason about with the whole loop in view at
once than split across three separate reviews.

## How

### 1. Detector (`internal/signatures/fanout.go`)
`DetectFanOut` is structurally identical to `DetectRetryStorm` — same
`>=` inclusive threshold, same NaN/Inf guard, same `confidence = 0.5 +
0.5*(ratio/multiplier - 1)` capped at 1 — deliberately not generalized into
one shared "ratio detector" function despite the duplication: each is three
statements and reads clearly as its own signature's contract; a shared
helper would need a type or a name that doesn't accidentally suggest the
two ratios are computed the same way (they aren't — cross-host vs.
same-host-cross-reporter), which is worse for the interview-defensibility
goal than the small duplication.

The "exactly at threshold" test case uses `dependency_caller_ratio: 3.0`
against `multiplier: 3.0` — not an arbitrary boundary value, that's the
literal ratio the fan-out-demo-evidence worklog measured live
(`payments:checkout = 3×`). Table tests mirror `retry_storm_test.go`'s
structure exactly (well below / just below / at threshold / well above /
NaN / +Inf / −Inf / invalid multiplier).

### 2. PromQL (`internal/controller/promql.go`)
```
sum(rate(istio_requests_total{destination_service="<dependency>",reporter="destination"}[Ws]))
  / sum(rate(istio_requests_total{destination_service="<caller>",reporter="destination"}[Ws]))
```
Both sides use `reporter="destination"` (what actually arrived at each
service) — that's the exact reporter choice the fan-out-demo-evidence
worklog's live scrape used to get the clean 1:1:1 / 3× numbers, so this
query is not a new guess, it's transcribing an already-verified shape.
`evalFanOut` (mirroring `evalRetryStorm`) calls it with
`(host, policy.Spec.Service, window)` — `spec.Service` (the protected/caller
service) is an existing CRD field, no new one needed, exactly as scoped.

Testing the fake `Querier` needed one real decision: `retryStormRatioQuery`
and `fanOutRatioQuery` both contain the substring `reporter="destination"`
somewhere (retry storm has it on one side, fan-out has it on both), so
`fakeQuerier.Query`'s routing switch checks `reporter="source"` *first* —
only `retryStormRatioQuery` ever contains that string — and falls through
to a `reporter="destination"` case for fan-out. Order-dependent by
necessity, documented inline so a future signature added to this switch
doesn't get bitten by the same ambiguity.

### 3. Reconciler wiring
Third check in `detectSignatures`'s per-host loop, after latency/error and
retry storm, same "first trip wins" short-circuit — `evaluated` only
increments once per host even if multiple detectors return a reading.
Third case in `Reconcile`'s mitigation dispatch switch
(`SignatureFanOutAmplification` → `applyFanOutMitigation`).

Added `TestRetryStormWinsWhenRetryStormAndFanOutCouldTrip` (retry storm's
own twin of the existing `TestLatencyErrorCascadeWinsWhenBothSignaturesCouldTrip`)
to lock in where fan-out sits in the priority order, not just that it
exists.

### 4. Mitigation — `internal/mitigation/connpool.go` (trip)
New file, mirroring `outlier.go`'s shape but a fully independent
implementation — same reasoning the prompt gave for treating this as
parallel rather than reuse-with-tweaks: the field set
(`http1MaxPendingRequests`, `http2MaxRequests`) has nothing in common with
`outlierDetection`'s.

**Trip values — `TripHTTP1MaxPendingRequests = TripHTTP2MaxRequests = 1`.**
Envoy's own circuit-breaker proto doc
(https://www.envoyproxy.io/docs/envoy/latest/api-v3/config/cluster/v3/circuit_breaker.proto)
states both fields default to **1024** when unset — confirmed via a live
web search against the current Envoy docs rather than assumed, since the
Kind cluster wasn't queried live for this one (no new demo traffic pattern
needed to observe a *config default*, unlike the retry-storm/fan-out ratio
values which genuinely needed live evidence). Capping to 1 caps in-flight
calls to exactly one at a time — any burst beyond that (the fan-out signal
itself) gets rejected immediately rather than piling onto an already
degraded downstream. **Deliberately 1, not 0**: 0 would mean zero requests
get through at all — a full outage of the dependency, which is a
categorically different mitigation than "cap in-flight calls." This mirrors
`outlier.go`'s "tighten, don't disable" choice (`TripConsecutive5xx = 3`)
rather than `retries.go`'s "fully disable" choice (`TripRetryAttempts = 0`)
— a bulkhead's job is capping concurrency, not cutting the dependency off.

**Annotation:** new key, `cascade.gideonsanni.dev/original-connection-pool`,
same `{"unset":true}`-style sentinel convention as
`AnnotationOriginalOutlier`. Its JSON shape
(`originalConnectionPoolJSON{Unset, Http1MaxPendingRequests, Http2MaxRequests}`)
turned out simpler than `originalRouteRetriesJSON`'s per-route array: a
`DestinationRule` has exactly one `connectionPool`, not one per route, so
no array is needed, and — a genuine, worth-documenting difference from
`outlierDetection.Consecutive_5XxErrors` — `http1MaxPendingRequests` /
`http2MaxRequests` are **plain proto3 `int32` scalars**, not nullable
wrappers. Envoy's own doc comment ("if not specified, the default is 1024")
confirms there is no wire-level distinction between "explicitly authored as
0" and "not specified" for these two fields — so unlike
`originalRouteRetriesJSON` (which needs an explicit per-route `Unset` flag
because `Attempts: 0` *inside an existing retries block* is a real, distinct
state from no block at all), `origMaxPendingTarget`/`origMaxRequestsTarget`
correctly fall back to the Envoy default (1024) purely by checking `== 0`,
in every case, not just the whole-block-absent one. `Unset` at the JSON
level only decides one thing: whether to clear `connectionPool.http`
entirely on final restore (mirrors `clearOutlierDetection`) versus leave it
present with these two fields written back — reused `trafficPolicyEmpty`
from `restore.go` unchanged for the cascading empty-parent cleanup.

### 5. The bug this slice's own cross-signature test caught
Wrote `TestApplyFanOutConnectionPoolTripLeavesOutlierDetectionAlone`
(`internal/mitigation/connpool_test.go`) to check the shared-object-kind
subtlety at the trip level, not just restore — and it failed on the first
run. `ApplyFanOutConnectionPoolTrip`'s original guard
(`if dr.Annotations[AnnotationManagedBy] != ManagedByValue { capture }`,
copied verbatim from `ApplyLatencyErrorOutlierTrip`) assumes "managed-by is
already set" means "*this function* already captured its own baseline" —
true when only one signature ever touches a `DestinationRule`, false the
moment a *different* signature set managed-by first. In that case fan-out
would skip capturing its own `connectionPool.http` baseline entirely,
silently losing the true pre-trip state. Fixed both `ApplyFanOutConnectionPoolTrip`
and `ApplyLatencyErrorOutlierTrip` (the only other function that can share a
`DestinationRule`) to key the capture-once check off *each function's own*
annotation's presence (`AnnotationOriginalConnectionPool` /
`AnnotationOriginalOutlier`) instead of the shared `managed-by` flag —
`managed-by` itself is still always set/kept, just no longer doubles as the
"have I personally captured a baseline" signal. Zero behavior change for
every existing single-signature test (managed-by and the signature-specific
annotation are always set together in that case), confirmed by the full
suite passing unchanged.

### 6. Restoration — `internal/mitigation/connpool_restore.go` +
`internal/controller/fanout_restore.go`
Same split as retry storm's restoration (`retries.go` trip /
`retry_restore.go` restore): `connpool.go` for trip,
`connpool_restore.go` for the ramp, reusing `restoreProgress(step)` and
`lerpI32` unchanged from the existing files in the same package — no new
ramp math needed since these are also `int32` fields.

`fanout_restore.go` (controller) is the interesting part: it reuses
`listManagedDestinationRuleEdges`/`managedDREdge` **directly** from
`restore.go` — no separate "list managed-by-fan-out" function — because
that helper only ever checks the `managed-by` annotation
(`mitigation.IsOperatorManaged`), which cannot and does not distinguish
*which* signature patched a given object's fields. This is exactly the
shared-object-kind subtlety flagged for this slice, reasoned through in
that file's doc comment rather than assumed:
- Restoration dispatch is keyed by `status.LastSignature`, and the CRD
  tracks exactly one active signature per policy.
- Each restore path's functions are field-scoped: fan-out's
  `ApplyFanOutConnectionPoolRestoreStep`/`CompleteFanOutConnectionPoolRestore`
  never read or write `outlierDetection`; latency/error's equivalents never
  touch `connectionPool.http`. Listing the same object from two call sites
  is harmless as long as neither call site's read-modify-write crosses into
  the other's fields — verified directly by
  `TestRestoreDispatchTouchesOnlyItsOwnFieldsAcrossAllThreeSignatures`
  (below), not just asserted in the comment.
- What is **not** fully closed by that reasoning: if a policy's tripped
  signature *changes* on the same host without an intervening healthy
  tick — latency/error trips, then before it ever heals, fan-out's ratio
  also crosses threshold on that same host — `Reconcile` adopts the new
  signature immediately. §5's fix means the newly-tripping signature still
  captures its own baseline correctly, but the *outgoing* signature's
  trip-time fields and its own `original-*` annotation are left exactly as
  they were, with no future tick pointed back at restoring them (since
  `LastSignature` has already moved on). Filed as a `PROPOSALS.md` entry
  rather than silently deciding a fix, since the two directions that came
  up while writing this (force-restore-before-handoff, vs.
  defensive-cleanup-in-the-trip-path-of-the-other-signature's-leftovers)
  both have real tradeoffs the prompt didn't ask me to pick between
  unprompted.

`beginRestore`/`advanceRestore` (`restore.go`) got a third case,
`SignatureFanOutAmplification → beginRestoreFanOut`/`advanceRestoreFanOut`,
alongside the existing two and the `snapToNormalNoRestore` fallback
(unchanged — still there for whatever signature comes after this one, not
needed by any of the three now).

## Files touched
- `internal/signatures/fanout.go` — new: `FanOutInput`, `DetectFanOut`
- `internal/signatures/fanout_test.go` — new: table-driven tests
- `internal/signatures/latency_error_test.go` — added shared test
  constants `evidenceBelowThreshold`/`depInventory` (goconst, once a third
  file duplicated the same literals)
- `internal/signatures/retry_storm_test.go` — switched to the shared
  constants above; added `TestRetryStormWinsWhenRetryStormAndFanOutCouldTrip`
- `internal/controller/promql.go` — new: `fanOutRatioQuery`
- `internal/controller/promql_test.go` — new: `TestFanOutRatioQuery`
- `internal/controller/cascadepolicy_controller.go` — new: `evalFanOut`;
  third check in `detectSignatures`; third mitigation-dispatch case;
  updated `Reconcile`'s doc comment
- `internal/controller/cascadepolicy_controller_test.go` — `fakeQuerier`
  gained `fanOutRatio` + the reporter="source"-first routing fix
- `internal/controller/fanout_mitigate.go` — new: `applyFanOutMitigation`
- `internal/controller/fanout_mitigate_test.go` — new: missing/DetectOnly/
  first-trip/retrigger/live-Reconcile/no-create tests
- `internal/controller/fanout_restore.go` — new:
  `beginRestoreFanOut`/`advanceRestoreFanOut`/`applyFanOutRestoreStep`/
  `completeFanOutRestore`, reusing `listManagedDestinationRuleEdges`
- `internal/controller/fanout_restore_test.go` — new: enters/advances/
  completes/regression/query-error tests, plus
  `TestRestoreDispatchTouchesOnlyItsOwnFieldsAcrossAllThreeSignatures`
  (three-way, one `DestinationRule` carrying both DR-based signatures'
  trip state at once, plus a separate managed `VirtualService`)
- `internal/controller/restore.go` — third dispatch case in both
  `beginRestore`/`advanceRestore`; doc comment update
- `internal/controller/retry_storm_test.go` — n/a (see fanout signature
  test above; no other change to this file this slice)
- `internal/controller/retry_restore_test.go` — replaced
  `TestRestoreFallsBackToNormalForUnwiredSignature` (used
  `FanOutAmplification` as its stand-in "unwired" value, now wired) with
  `TestRestoreFallsBackToNormalForUnrecognizedSignature` (uses a
  `SignatureType` value outside the CRD's own enum)
- `internal/mitigation/connpool.go` — new: `AnnotationOriginalConnectionPool`,
  `OriginalConnectionPoolUnsetJSON`, `TripHTTP1MaxPendingRequests`,
  `TripHTTP2MaxRequests`, `originalConnectionPoolJSON`,
  `ApplyFanOutConnectionPoolTrip`, `ensureConnectionPoolHTTP`
- `internal/mitigation/connpool_test.go` — new: first-patch/unset/
  retrigger tests, plus the cross-signature bug-catching test from §5
- `internal/mitigation/connpool_restore.go` — new:
  `istioDefaultMaxPendingRequests`/`istioDefaultMaxRequests`,
  `ApplyFanOutConnectionPoolRestoreStep`, `CompleteFanOutConnectionPoolRestore`,
  supporting helpers
- `internal/mitigation/connpool_restore_test.go` — new: ramp monotonicity,
  unset-ramps-toward-Envoy-default, complete/strip, tcp-preserved-while-
  clearing-http, empty-TrafficPolicy-cleanup, missing-annotation error
- `internal/mitigation/outlier.go` — `ApplyLatencyErrorOutlierTrip`'s
  capture-once check now keys off `AnnotationOriginalOutlier`'s presence,
  not `managed-by` (see §5)
- `PLAN.md` — status line, §2.6 paragraph, checklist
- `PROPOSALS.md` — new pending entry: signature handoff on a shared
  `DestinationRule` can orphan the outgoing signature's fields
- `docs/worklog/README.md` — index this entry

## Testing
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./... -cover`: `internal/signatures` 94.1%, `internal/mitigation`
  89.2% (up from 87.2%), `internal/controller` 78.6% (down slightly from
  79.9% — same reason as the retry-storm restoration slice: new
  dispatch/logging surface area growing faster than line coverage, not a
  regression in what's actually exercised).
- Every pre-existing test in `restore_test.go`, `retry_restore_test.go`
  (mitigation and controller), `retries_test.go`, `outlier_test.go`,
  `cascadepolicy_controller_test.go` passes unchanged — confirms the
  `managed-by`→own-annotation capture-check fix and the new dispatch cases
  didn't move any existing signature's behavior.
- New test suites, mirroring the full shape asked for: detector table
  tests (`fanout_test.go`), PromQL exact-string test
  (`TestFanOutRatioQuery`), mitigation trip/restore unit tests
  (`connpool_test.go`, `connpool_restore_test.go`), reconciler fake-client
  tests for trip/DetectOnly/retrigger/live-wiring/no-create
  (`fanout_mitigate_test.go`) and enter/advance/complete/regression/
  query-error (`fanout_restore_test.go`).
- The cross-signature dispatch test asked for
  (`TestRestoreDispatchTouchesOnlyItsOwnFieldsAcrossAllThreeSignatures`)
  seeds one `DestinationRule` already carrying *both* latency/error's and
  fan-out's trip-time fields and annotations simultaneously (via
  `ApplyLatencyErrorOutlierTrip` then `ApplyFanOutConnectionPoolTrip` on the
  same object — which only produced the right result because of the §5
  fix), plus a separately managed `VirtualService`, then trips each of the
  three signatures in three subtests and asserts each restore path only
  ever advances its own fields/annotation, leaving the other two
  untouched — this is the test that would have caught the shared-object-
  kind bug on its own even without the narrower unit test in §5.
- `make lint` — 0 issues (fixed along the way: `goconst` on `"below
  threshold"` and `"inventory"` literals that crossed the 3-occurrence
  threshold once `fanout_test.go` existed alongside the other two
  detectors' table tests).
- `make manifests generate` — no diff, as scoped: no CRD change
  (`fanOutMultiplier`/`SignatureFanOutAmplification` already existed), and
  fan-out's `DestinationRule` RBAC verbs were already granted by the
  latency/error-cascade slice.

## Follow-ups / known gaps
- `PROPOSALS.md`'s new pending entry: a same-host signature handoff between
  `LatencyErrorCascade` and `FanOutAmplification` while `Tripped`/`Restoring`
  (no intervening healthy tick) can leave the outgoing signature's fields
  and annotation orphaned — not silently fixed here, needs a direction
  decision (force-restore-before-handoff vs. defensive cross-signature
  cleanup in the trip path).
- No `VirtualService` secondary for any signature, no retry storm
  `connectionPool` secondary, no latency/error `VirtualService` timeout
  secondary — all still contract-only in §2.6, as scoped for this slice.
- No k6 scripts — still a later checklist item.
- The operator's own Prometheus metrics (signatures detected, patches
  applied) — still unbuilt, unrelated to this slice.
