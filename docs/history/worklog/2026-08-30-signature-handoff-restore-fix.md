# Signature handoff on a shared object: force-complete the outgoing signature's restore before adopting the incoming one

**Date:** 2026-08-30
**Author:** Cursor
**Type:** fix

## What
Implemented the resolved direction from `PROPOSALS.md`'s "Signature handoff
on a shared DestinationRule can orphan the outgoing signature's fields"
entry (approved 2026-08-30, in the fan-out signature slice's review): when
`Reconcile`'s trip branch is about to adopt a signature different from
`status.LastSignature` while `Phase != Normal`, it now synchronously
force-completes the outgoing signature's restore — calling its existing
`complete*Restore` function, the same one its own gradual ramp already
calls at the final step — *before* applying the incoming signature's trip.
This is a correctness fix in the core reconcile loop, not a new feature: it
closes the one real gap the fan-out slice's own cross-signature tests
surfaced, where a same-host handoff between two `DestinationRule`-patching
signatures (latency/error-cascade, fan-out amplification) could leave the
outgoing signature's trip-time fields and its own `original-*` annotation
orphaned forever.

## Why
This was already decided, not re-litigated: `PLAN.md` §2.6's "Signature
handoff on a shared object" note and `PROPOSALS.md`'s approved entry both
lay out the exact fix, and per the prompt this slice's job was
implementation, not design. The two directions the proposal originally
offered — a multi-tick handoff state machine, or pushing cross-signature
awareness into each trip function — were both rejected in review in favor
of a third option that reuses existing tested logic instead of adding
either a status-shape change or new cross-signature coupling. Building
this ahead of the remaining Istio patch secondaries (latency/error's
`VirtualService` timeout, retry storm's `connectionPool` cap) was explicit
in the prompt: this is a correctness gap in the now-complete three-signature
system, not polish.

## How

### Where the fix lives
One new function, `forceCompleteOutgoingRestore` (`internal/controller/restore.go`),
mirroring `beginRestore`/`advanceRestore`'s dispatch shape but keyed on the
*outgoing* signature and calling straight through to each path's `complete*`
function instead of its step-by-step one:

```go
func (r *CascadePolicyReconciler) forceCompleteOutgoingRestore(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	outgoing cascadev1alpha1.SignatureType,
) error {
	switch outgoing {
	case cascadev1alpha1.SignatureLatencyErrorCascade:
		edges, err := r.listManagedDestinationRuleEdges(ctx, policy)
		...
		return r.completeLatencyErrorRestore(ctx, policy, edges)
	case cascadev1alpha1.SignatureRetryStorm:
		edges, err := r.listManagedVirtualServiceEdges(ctx, policy)
		...
		return r.completeRetryStormRestore(ctx, policy, edges)
	case cascadev1alpha1.SignatureFanOutAmplification:
		edges, err := r.listManagedDestinationRuleEdges(ctx, policy)
		...
		return r.completeFanOutRestore(ctx, policy, edges)
	default:
		return nil
	}
}
```

Each branch is a `listManaged*Edges` + `complete*Restore` pair already used
elsewhere (`advanceRestoreLatencyError`, `advanceRestoreRetryStorm`,
`advanceRestoreFanOut` each call the same `complete*Restore` function at
their own final step) — this is genuinely a new call site for existing
logic, zero new mitigation-package code, exactly as scoped. An empty-edges
result is a silent no-op (nothing to complete), matching every other
restore path's own "zero managed edges" branch.

### Wiring into `Reconcile`
```go
if tripped {
	outgoingSig := policy.Status.LastSignature
	handoff := outgoingSig != "" && outgoingSig != sig && policy.Status.Phase != cascadev1alpha1.PolicyPhaseNormal
	if handoff {
		mitErr = r.forceCompleteOutgoingRestore(ctx, policy, outgoingSig)
	}
	if mitErr == nil {
		// ... existing Phase/RestoreStep/LastSignature = sig, then the
		// mitigation-dispatch switch, unchanged ...
	}
}
```

Three things about this specific shape, each a deliberate choice:

- **Read `outgoingSig` before anything overwrites `LastSignature`.** The
  prompt called this out explicitly ("order matters"), and it's the whole
  point — `policy.Status.LastSignature` is still the *old* value at the
  point `forceCompleteOutgoingRestore` runs, which is exactly what its own
  dispatch switch needs to key off.
- **`Phase != Normal` alone handles "first trip ever" with no special
  case.** A fresh CR's `Phase` gets defaulted `"" → Normal` earlier in
  `Reconcile`, before `detectSignatures` even runs, so `handoff` is false
  on a first trip without needing an explicit `outgoingSig == ""` carve-out
  — though I kept `outgoingSig != ""` in the condition anyway (cheap,
  correct on its own, and reads clearly next to the `Phase` check rather
  than relying on a single condition to imply two different things).
  `Phase == Normal` also covers the *other* case that needed covering and
  wasn't explicitly named in the prompt: a policy that already fully
  restored under some signature (which does **not** clear
  `LastSignature` — `completeLatencyErrorRestore`/etc. only ever set
  `Phase = Normal`, `RestoreStep = 0`, they never reset `LastSignature`
  back to `""`) and is now tripping again, possibly under a *different*
  signature than the one it last fully restored. That's not a handoff at
  all — there's nothing left to complete, the object's already at true
  original — and `Phase != Normal` correctly says no here too, for the
  same reason it says no for a first-ever trip: nothing is in
  `Tripped`/`Restoring` to force-complete.
- **Gate the rest of the trip branch on `mitErr == nil`, not just fire the
  force-complete and continue regardless.** If force-completing the
  outgoing signature fails (e.g. a conflicting `Update`), the code does
  *not* go on to overwrite `LastSignature`/`Phase` to the incoming
  signature or attempt its mitigation this tick. This was a real decision,
  not just "handle the error the obvious way": the alternative (set
  `mitErr` but still proceed) would silently move `LastSignature` past the
  point where a retry could ever revisit the outgoing signature's own
  force-complete, since `beginRestore`/`advanceRestore` only ever dispatch
  on `LastSignature`'s *current* value. Failing closed here means the next
  tick sees the same stale `LastSignature`/`Phase` and — because
  `detectSignatures` will presumably still return the same incoming
  signature's trip if the underlying condition hasn't changed — retries
  the exact same handoff attempt, which is the correct idempotent behavior
  for a transient error.

### Why skipping the gradual ramp's regression check is actually safe here
This is the crux of why "force complete, don't force-begin-a-fast-ramp" is
the right shape, not just a shortcut. The gradual ramp
(`beginRestore`/`advanceRestore`, stepping through `restoreProgress(step)`)
exists because between any two reconcile ticks, the *same* signature's
condition could regress — that's what
`TestRestoreRegressionReTripsAndBumpsLastTrippedAt` (and its retry-storm
and fan-out twins) test: a mid-ramp DR/VS whose condition flares back up
gets re-tripped, not blindly carried to completion. `forceCompleteOutgoingRestore`
skips that check entirely and jumps straight to true original — which
would be wrong for *continuing* a ramp, but this isn't that. The caller
already ran `detectSignatures` this exact tick and got back a *different*
signature's trip, which is only possible because the outgoing signature's
own detector no longer trips *on this same tick's data*. There is no
future tick where the outgoing signature's condition could "come back" in
a way this code needs to protect against, because we are not leaving it
mid-ramp for a future tick to re-evaluate — we're closing it out
completely, now, as part of adopting a signature whose trip this same
Prometheus read already confirmed.

## Files touched
- `internal/controller/restore.go` — new: `forceCompleteOutgoingRestore`
- `internal/controller/cascadepolicy_controller.go` — wired the handoff
  check into `Reconcile`'s trip branch; updated `Reconcile`'s doc comment
- `internal/controller/handoff_restore_test.go` — new: the four tests
  below
- `PLAN.md` — status header updated (gap → resolved and implemented);
  checklist item flipped from unchecked to checked. Did **not** touch
  §2.6's decision prose itself — that's Claude's own authored content from
  the previous review, resolving the proposal; this slice implements it,
  doesn't re-decide it (a boundary the previous slice's review explicitly
  flagged crossing once, worth being extra careful about here)
- `docs/worklog/README.md` — index this entry

## Testing
- `go build ./...`, `go vet ./...`, `gofmt -l .` — clean.
- `go test ./... -cover`: `internal/controller` 77.3% (down slightly from
  78.6% — new branch/log-line surface from the handoff dispatcher, not a
  coverage regression in what's tested), `internal/signatures` 94.1%,
  `internal/mitigation` 89.2% unchanged (no mitigation-package code
  touched, as scoped).
- Every pre-existing test across `restore_test.go`, `retry_restore_test.go`,
  `fanout_restore_test.go`, `istio_patch_test.go`,
  `cascadepolicy_controller_test.go` passes unchanged, including the
  three-way `TestRestoreDispatchTouchesOnlyItsOwnFieldsAcrossAllThreeSignatures`
  — confirms the handoff guard's `outgoingSig != sig` condition correctly
  stays false for every one of those tests' same-signature scenarios.
- New tests in `handoff_restore_test.go`:
  - `TestSignatureHandoffLatencyErrorToFanOutForceCompletesOutgoing` and
    `TestSignatureHandoffFanOutToLatencyErrorForceCompletesOutgoing` (both
    directions asked for): seed a `DestinationRule` mid-ramp (restore step
    2, an explicit stored original annotation) under one signature, then
    reconcile with a querier where that signature no longer trips but the
    other one does. Assert the outgoing signature's fields land on the
    *true* stored original — not step 2's interpolated value — with its
    own annotation gone, while the incoming signature's trip is freshly
    applied with its own annotation and `managed-by` (re-)set, on the
    exact same object, in the same reconcile call.
  - `TestSignatureReTripSameSignatureDoesNotForceComplete`: the
    same-signature regression case the prompt asked for. A single
    `dependsOn` edge means the final object state would look identical
    whether or not force-complete incorrectly fired (a same-signature
    re-trip re-patches to the same trip values regardless), so this test
    uses a deliberately malformed `original-outlier-detection` annotation
    value. The trip path never parses that annotation's contents (only
    checks its presence); the restore path's `complete*Restore` does parse
    it and would return an error. If `Reconcile` ever returns that parse
    error here, the `outgoingSig != sig` guard broke — this is a real,
    not cosmetic, regression signal.
  - Sanity-checked the fix is actually load-bearing by stashing
    `cascadepolicy_controller.go`/`restore.go` back to their pre-fix state
    and confirming `TestSignatureHandoffLatencyErrorToFanOutForceCompletesOutgoing`
    and its reverse fail (outgoing signature's fields stay at the mid-ramp
    interpolated value instead of true original — exactly the orphaned
    state the proposal described), then restored the fix.
- `make lint` — 0 issues.
- `make manifests generate` — no diff (no CRD/marker changes; this slice
  touches only `internal/controller`).

## Follow-ups / known gaps
- Remaining Istio patch secondaries (latency/error's `VirtualService`
  timeout; retry storm's `DestinationRule` connectionPool cap) — still
  unbuilt, explicitly lower priority than this fix per the prompt, now
  clear to pick up next.
- k6 scripts, integration test suite, README — still open checklist items,
  untouched by this slice.
