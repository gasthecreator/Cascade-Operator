# Phase 5 (1/4): retry storm's restore-completion zero-value bug — VirtualService side fixed, DestinationRule side found not applicable

**Date:** 2026-08-31
**Author:** Claude
**Type:** fix

## What
Investigated and fixed PLAN.md §5 Phase 5's first item: "the known zero-value
bug in retry storm's restore-completion path, same class as the trip-path
bug, never applied to restore." The investigation found the two objects
this signature restores are **not** in the same situation, despite both
having been flagged together:

- **VirtualService (`retries.attempts`)**: genuinely the same bug class as
  the trip path, and now fixed.
- **DestinationRule (`connectionPool.http.maxRetries`)**: investigated and
  found to be a different, already-correctly-handled situation — no code
  change, a clarifying comment added at the call site instead.

## Why
`api/v1alpha1` markers didn't apply here (this is Go code, not CRD schema),
but the same "verify before assuming" discipline this entire retry-storm
bug thread has run on did: reading `applyOriginalRetryConnectionPool`'s own
doc comment before touching it revealed it already says, on purpose, "0
correctly means restore to absent/default" — because
`originalRetryConnectionPoolJSON.MaxRetries` itself carries `omitempty`,
so a true original of exactly 0 is already indistinguishable from "never
set" at the moment the trip-time snapshot is captured, well before any
restore write is reached. Fixing only the final write (as originally
planned) would not have restored any information that isn't already lost
upstream, and would have fought the documented intent for no benefit —
compounded by the fact that Istio's Pilot doesn't push an explicit
`maxRetries: 0` to Envoy at all regardless of how it's written (the
already-resolved translation limit that's the whole reason this
secondary's *trip* value is 1, not 0 — PROPOSALS.md, approved 2026-08-30).

The VirtualService side is different: `originalRouteRetriesJSON` has a
separate `Unset` boolean specifically to distinguish "no retries block at
all" from "a retries block present with Attempts happening to be 0," so a
true captured original of `attempts: 0` **does** correctly make it into
memory as `route.Retries.Attempts = 0` during restore — the data loss was
only ever at the final typed `Update()` write, exactly the trip-path bug's
shape.

## How
- Added `RetryStormRestoreStepMergePatch` and `RetryStormRestoreCompleteJSONPatch`
  (`internal/mitigation/retries.go`) and wired both into
  `internal/controller/retry_restore.go`, replacing the two typed
  `r.Update()` calls that write the VirtualService during a restore ramp
  (both the intermediate per-tick write and the final completion write —
  an interpolated Attempts value can round to exactly 0 at an early ramp
  tick for a route with a small restore target, not only at the final
  step, so both call sites needed the fix, not just the one PLAN.md named).
- **First attempt used an RFC 6902 JSON Patch** (mirroring the trip path's
  own `RetryStormAttemptsJSONPatch` exactly) — this failed a real
  fake-client test
  (`TestRetryStormRestoreAdvancesBothObjectKindsTogetherThenCompletes`)
  with `error in remove for path: '/spec/http/0/retries': Unable to remove
  nonexistent key`. Root cause: `ApplyRetryStormRestoreStep`'s own
  final-step branch and `CompleteRetryStormRestore` both call the same
  `applyOriginalRetries` and both write the result — the *first* write
  already clears an Unset route's retries key, so the *second* write's
  JSON Patch "remove" targets a path that's already gone. This is a real
  finding, not a test artifact: production hits the identical two-call
  sequence (one tick reaches `RestoreStep == RestoreFinalStep` via the ramp,
  a *later* tick transitions `Phase` to `Normal` via
  `completeRetryStormRestore`), so the bug would have shipped to the real
  cluster, just never observed until an Unset route's restore ran twice.
- **Fixed by switching to an RFC 7396 JSON *merge* patch instead.** `spec.http`
  is an array, and merge patch replaces arrays wholesale rather than
  merging by index — the same reason the trip path chose JSON Patch over
  merge patch in the first place — but a whole-array replacement built
  fresh from the current in-memory state (`retryStormRouteValuesFixedUp`,
  extracted as a shared helper) is naturally idempotent: re-sending the
  same target state twice is a no-op, never an error, unlike JSON Patch's
  strict "remove" precondition. The two annotations are deleted the same
  way — explicit `null` values in the merge patch (RFC 7396's own
  null-means-delete rule), recursively merged so any *other* annotation on
  the object is untouched, rather than JSON Patch's per-key `remove`.
- `retryStormRouteValuesFixedUp` reuses the same trick the trip path's own
  patch already established: marshal the typed struct for the fields that
  are *not* the zero-value problem (RetryOn, PerTryTimeout, Backoff — all
  correct as-is, including Duration's protojson string format), then
  explicitly inject `"attempts"` afterward, the one field `omitempty` can
  drop regardless of whether it's actually 0.
- Left `CompleteRetryStormConnectionPoolRestore`'s write as typed `Update()`,
  with a new comment at the controller call site explaining exactly why
  (see Why above) — a fix that isn't actually a fix, applied without that
  investigation, would have been worse than no change at all.

## Files touched
- `internal/mitigation/retries.go` — `retryStormRouteValuesFixedUp`,
  `RetryStormRestoreStepMergePatch`, `RetryStormRestoreCompleteJSONPatch`,
  shared `jsonKeySpec`/`jsonKeyHTTP` constants (also applied to
  `retry_connpool.go`'s existing merge patch to stay under `goconst`'s
  package-wide threshold once introduced)
- `internal/mitigation/retries_test.go` — new regression-lock tests
- `internal/controller/retry_restore.go` — both VirtualService write call
  sites switched to `client.RawPatch(types.MergePatchType, ...)`; a new
  comment at the DestinationRule call site explaining why it's unchanged
- `PLAN.md` — §5 Phase 5, first sub-item only

## Testing
- `go build ./...`, `gofmt -l .`, `go vet ./...` — clean.
- `make lint` — caught and fixed a real `goconst` violation my own new code
  introduced (package-wide occurrences of `"spec"`/`"http"` across
  `retries.go` and the pre-existing `retry_connpool.go` crossed the
  threshold together) — extracted shared constants, 0 issues on the final
  pass.
- `make test` — `internal/mitigation` 90.3% (up from the dip while the new
  code was still untested), full suite otherwise unaffected: controller
  79.7%, notify 91.3%, signatures 94.1%, webhook 100%.
- **New unit tests exercise the actual zero-value edge case directly**, not
  just the mechanism: a route whose true pre-trip `attempts` was explicitly
  0 (not Unset — a real retries block with `attempts: 0` and `retryOn` set)
  restores to a merge patch containing literal `"attempts":0`, the
  annotation-deletion `null` values are present, and — mirroring the exact
  two-call production sequence that broke the first JSON-Patch attempt —
  `ApplyRetryStormRestoreStep(vs, RestoreFinalStep)` followed by
  `CompleteRetryStormRestore(vs)` both produce well-formed patches with the
  explicit zero intact.
- `make verify-generate` — no drift.
- **`make test-integration` against the live dev cluster — all three
  existing signature tests still pass**, confirming the merge-patch
  rewrite works against a real apiserver, not only the fake client. Did
  not add a new live scenario specifically for the "true original is 0"
  edge case: the existing integration test already proves the *mechanism*
  (merge patch correctly applied by a real Kubernetes API server) on the
  common restore-to-nonzero-original path, and the new unit tests prove
  the *edge-case value* survives a real `encoding/json.Marshal` — between
  the two, the actual risk (a real wire-format loss) is covered without
  needing bespoke live infrastructure for a narrow, low-probability case.

## Follow-ups / known gaps
- Phase 5's remaining three items (HA, per-edge threshold overrides,
  security threat-model doc) are unstarted — tracked separately in
  PLAN.md §5.
- The DestinationRule side's underlying limitation (an annotation-capture
  struct that can't distinguish true-0 from never-set) is not itself a bug
  to fix — it's a structural constraint of using `omitempty` on the
  snapshot type, and per this investigation, fixing it wouldn't even
  produce an observable difference at Envoy given the separate Istio Pilot
  translation limit. Not tracked as further work.
