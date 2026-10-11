# Retry-storm restoration: wire the VirtualService patch into Reconcile, dispatch restore by signature

**Date:** 2026-08-30
**Author:** Cursor
**Type:** feature

## What
Closed the gap the previous slice deliberately left open: `Reconcile` now
calls `applyRetryStormMitigation` on a retry-storm trip, and restoration
(`beginRestore`/`advanceRestore`) dispatches by `status.LastSignature` to
either the existing `DestinationRule` outlier-detection path or a new
`VirtualService` retries.attempts path — landed together in this one
change, so there is no commit where a live patch could be left stuck. A
retry-storm trip now goes all the way through detect → mitigate → restore,
the same full loop latency/error cascade already had.

## Why
PLAN.md §2.6's retry-storm row (`VirtualService retries.attempts → 0` /
"Stepwise raise attempts") was the last unbuilt cell for the two signatures
that have mitigations at all. The previous slice's worklog and its review
both called this out explicitly as the next required step, and as
something that had to land in one change, not split — teaching
`beginRestore` to recognize a VirtualService it can't yet restore (option
(b), rejected last slice) would have been a worse halfway state than not
wiring the mitigation at all.

## How

### 1. Wiring the mitigation
`Reconcile`'s signature dispatch was an `if`/`else if`; added the
`SignatureRetryStorm` branch and converted it to a `switch` on `sig`
(staticcheck's own suggestion once there were two cases worth a tagged
switch, not a stylistic choice made unprompted). `applyRetryStormMitigation`
itself is unchanged except for its doc comment — the `//nolint:unparam` and
the "why this isn't called yet" explanation are gone, since `host` now
varies exactly like `applyLatencyErrorMitigation`'s does once it's called
per `dependsOn` edge from `Reconcile`.

### 2. Making restoration signature-aware
`restore.go` previously had one flat set of functions
(`listManagedEdges`, `beginRestore`, `advanceRestore`, `applyRestoreStep`,
`completeRestore`) hardcoded to `DestinationRule`. Renamed all of them with
a `LatencyError`/`DestinationRule` suffix
(`listManagedDestinationRuleEdges`, `beginRestoreLatencyError`,
`advanceRestoreLatencyError`, `applyLatencyErrorRestoreStep`,
`completeLatencyErrorRestore`) — pure rename, no behavior change, and every
existing restore test in `restore_test.go` still passes unchanged through
the new dispatch, which is itself evidence the rename didn't alter
behavior.

`beginRestore`/`advanceRestore` are now the dispatchers, switching on
`policy.Status.LastSignature`:
- `LatencyErrorCascade` → the renamed `DestinationRule` path.
- `RetryStorm` → a new parallel path in `internal/controller/retry_restore.go`
  (`listManagedVirtualServiceEdges`, `beginRestoreRetryStorm`,
  `advanceRestoreRetryStorm`, `applyRetryStormRestoreStep`,
  `completeRetryStormRestore`) — structurally identical to the
  DestinationRule path, one object kind over, same convention-based
  resolution (`mitigation.ParseServiceFQDN` + `Get` + managed-by filter).
- Anything else → `snapToNormalNoRestore`, a new shared fallback that logs
  and returns to `Normal`/step 0 without touching any object. This is the
  same "zero managed edges" branch each known path already has, pulled out
  one level so an *unrecognized* signature fails the same way an
  *recognized-but-nothing-managed* signature already did. Added a defensive
  test (`TestRestoreFallsBackToNormalForUnwiredSignature`) seeding a
  `FanOutAmplification` trip — not a currently-reachable path (nothing
  mitigates fan-out yet), but exactly the situation retry storm was in
  between its own mitigation and restoration slices, so the fallback this
  slice needed is now generalized for the next signature instead of being
  retrofitted again.

### 3. Per-route retry restoration (`internal/mitigation/retry_restore.go`)
Mirrors `outlier.go`/`restore.go`'s split (trip-time helpers vs.
restore-time helpers in separate files) rather than growing `retries.go`.
Reuses `restoreProgress(step)` unchanged — same 0→(step+1)/5 curve as
outlier detection, no new ramp math.

Three per-route states from `AnnotationOriginalRetries`, each handled the
way the prompt specified and outlier detection already precedented:
- **Skipped** (redirect/direct-response/delegate): never touched by
  `applyInterpolatedRetries` or `applyOriginalRetries` — matches trip time,
  which never touched it either.
- **Unset** (no explicit `retries` block pre-trip): ramps `attempts` from 0
  toward `istioDefaultRetryAttempts` (2) on every non-final step — same
  target the mitigation slice's worklog already established as Istio's
  implicit cluster-wide default (vendored `istio.io/api` v1.30.4 proto doc
  comment) — but the *final* restore (`applyOriginalRetries`, called from
  both the last ramp step and `CompleteRetryStormRestore`) clears the
  `retries` block entirely instead of writing `attempts: 2` explicitly.
  Same "don't invent config the user never had" rule
  `applyOriginalOutlier`'s `Unset` branch already follows for
  `outlierDetection`.
- **Explicit original**: ramps `attempts` from 0 back toward the captured
  value; `retryOn`/`perTryTimeout`/`backoff` are written back verbatim only
  at final restoration (mid-ramp, only `Attempts` is touched — matching
  trip time, which also only ever touched `Attempts` on a route that
  already had a `retries` block).

`lerpI32` is a new signed-integer sibling of `restore.go`'s `lerpU32` (same
round-half-away-from-zero interpolation, `int32` instead of `uint32` since
`HTTPRetry.Attempts` is signed) — didn't generalize the two into one
generic function; both are three lines and a shared generic would need a
type parameter for a single call site each, not worth it.

Annotation cleanup on full completion (`stripRetryStormAnnotations`) removes
both `managed-by` and `original-retries`, same rule as
`stripOperatorAnnotations` for the DestinationRule path.

### Numeric example from the tests
Multi-route fixture: route[0] Unset, route[1] explicit `attempts: 5,
retryOn: "5xx"`, route[2] skipped (redirect). `restoreProgress` gives
`t ∈ {0.2, 0.4, 0.6, 0.8, 1.0}` for steps 0–4. Route[1] (`lerp(0, 5, t)`,
rounded) comes out to `1, 2, 3, 4, 5` — a clean monotonic sequence, which is
why the multi-route restore test uses `attempts: 5` for the explicit route
rather than a smaller number that would round-trip through duplicate values
and be a weaker regression check.

## Files touched
- `internal/controller/cascadepolicy_controller.go` — wired the
  `SignatureRetryStorm` mitigation call; `if`/`else if` → `switch`; updated
  `Reconcile`'s doc comment
- `internal/controller/retry_mitigate.go` — trimmed the "not wired yet" doc
  comment and the now-stale `//nolint:unparam`
- `internal/controller/restore.go` — renamed the DestinationRule-specific
  functions/types; `beginRestore`/`advanceRestore` are now dispatchers;
  added `snapToNormalNoRestore`
- `internal/controller/retry_restore.go` — new: `managedVSEdge`,
  `listManagedVirtualServiceEdges`, `beginRestoreRetryStorm`,
  `advanceRestoreRetryStorm`, `applyRetryStormRestoreStep`,
  `completeRetryStormRestore`
- `internal/controller/retry_restore_test.go` — new: enters `Restoring` at
  step 0, advances/completes across all 5 steps with per-route assertions,
  regression re-trip, query-error-does-not-restore, two cross-signature
  dispatch tests, one fail-safe-fallback test
- `internal/controller/retry_mitigate_test.go` — replaced
  `TestReconcileDoesNotWireRetryStormMitigationYet` with
  `TestReconcileWiresRetryStormMitigationLive` (opposite assertion: a live
  trip must now patch the VirtualService)
- `internal/controller/retry_storm_test.go` — renamed
  `TestRetryStormTripsStatusOnlyWithoutPatchingDestinationRule` →
  `TestRetryStormTripDoesNotPatchDestinationRule` (still true — retry storm
  still never touches a DestinationRule — but "status only" stopped being
  accurate); updated a stale comment referencing the old `listManagedEdges`
  name
- `internal/mitigation/retry_restore.go` — new:
  `IsVirtualServiceManaged`, `parseOriginalRetries`, `lerpI32`,
  `applyInterpolatedRetries`, `applyOriginalRetries`,
  `stripRetryStormAnnotations`, `ApplyRetryStormRestoreStep`,
  `CompleteRetryStormRestore`, `istioDefaultRetryAttempts`
- `internal/mitigation/retry_restore_test.go` — new: ramp monotonicity
  across all three per-route states, final-step vs. complete distinction,
  unrelated-field preservation, missing-annotation error, `IsVirtualServiceManaged`
- `internal/mitigation/retries_test.go` — `destRoute` dropped its unused
  parameter (unparam, once a second call site with the same constant
  argument existed); one literal `"5xx"` switched to the existing
  `testRetryOn5xx` constant (goconst)
- `PLAN.md` — status line + checklist (not §2)
- `docs/worklog/README.md` — index this entry

## Testing
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./... -cover` — `internal/mitigation` 87.2% (up from 84.9%),
  `internal/controller` 79.9% (down slightly from 82.9% — expected: the new
  dispatch/fallback branches add surface area faster than this slice's
  tests cover every line of logging, not a regression in what's tested),
  `internal/signatures`/`internal/metrics` unchanged.
- Every pre-existing test in `restore_test.go` (`TestRestoreEntersAtStepZeroWhenTrippedGoesHealthy`,
  `TestRestoreAdvancesEachHealthyStepThenCompletes`,
  `TestRestoreRegressionReTripsAndBumpsLastTrippedAt`,
  `TestRestoreCompleteWithStoredOriginalValues`,
  `TestQueryErrorWhileTrippedDoesNotRestore`) passes unchanged after the
  rename + dispatch — confirms the `LatencyErrorCascade` path's *behavior*
  didn't move, only how it's reached.
- New parallel suite in `retry_restore_test.go` covers the same five
  scenarios for `VirtualService`, plus two dispatch-specific regression
  tests (`TestRestoreDispatchTouchesOnlyDestinationRuleForLatencyError`,
  `TestRestoreDispatchTouchesOnlyVirtualServiceForRetryStorm`) that seed
  *both* a managed `DestinationRule` and a managed `VirtualService` at once
  and assert only the one matching the tripped signature moves — the
  specific failure mode "dispatch picked the wrong object kind" wouldn't
  show up in either path's isolated test, since each only ever has its own
  object present.
- `TestRestoreFallsBackToNormalForUnwiredSignature` exercises the default
  case directly with a `FanOutAmplification`-tagged trip.
- `make lint` — 0 issues (fixed along the way: `goconst` on a new `"5xx"`
  literal, `modernize`'s `rangeint` suggestion on a hand-written counting
  loop, `staticcheck`'s tagged-switch suggestion, `unparam` on a test helper
  that gained a second always-same-value call site).
- `make manifests generate` — no diff. No new kubebuilder markers this
  slice (RBAC for `virtualservices` was already added last slice); no CRD
  or DeepCopy changes.

## Follow-ups / known gaps
- `DestinationRule` `connectionPool.http.maxRetries` / `http1MaxPendingRequests`
  (retry storm's §2.6 *secondary*) — still not built.
- Fan-out detector and its mitigation — still unbuilt, the third and last
  signature. `TestRestoreFallsBackToNormalForUnwiredSignature` means its
  mitigation can be wired into `Reconcile` ahead of its own restore logic
  without repeating this slice's risk, if that ordering is ever convenient
  — though building detector → mitigation → restore together in one slice,
  like fan-out will need anyway, avoids needing that fallback in practice.
- The live-cluster retry-default verification flagged as a gap in the
  mitigation slice's worklog (whether a plain 5xx response is actually
  covered by Istio's implicit `retryOn` default) is still open — this
  slice's restoration logic depends on the *value* of that default
  (`attempts: 2`) but not on which conditions trigger it, so it doesn't add
  new risk here, but it's still unverified against live traffic.
