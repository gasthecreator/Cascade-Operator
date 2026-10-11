# Review: retry-storm restoration, signature-dispatched restore machinery

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed `feat/retry-storm-restoration` against the prompt's requirements —
wire the mitigation live and make restoration signature-aware, together —
by independently rebuilding, testing, and hand-verifying the ramp math for
the new `VirtualService` restore path.

## Why
Same reviewer role as every slice. This is the slice that closes the loop
the previous two deliberately left open, so the main thing to verify is that
it actually closes it — not just that it says so.

## How
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/mitigation/...
  ./internal/controller/... -cover`, `make lint`, `make verify-generate` —
  all independently run. Coverage matched exactly: mitigation 87.2%,
  controller 79.9%. 0 lint issues. No drift (no new RBAC markers this slice,
  confirmed).
- Diffed `PLAN.md`: status line and checklist only — including a nice
  precision improvement to the "Istio patch layer" checklist line, correctly
  distinguishing retry storm's now-complete *primary* from its still-open
  *secondary*, matching the same accuracy bar I held the checklist to a few
  slices back.
- Confirmed the mitigation is genuinely wired: read `Reconcile`'s dispatch
  and saw the `switch` calling `applyRetryStormMitigation` for
  `SignatureRetryStorm`, not just a comment claiming it. Confirmed
  `TestReconcileWiresRetryStormMitigationLive` replaced (not just renamed)
  the old "must NOT patch" test with the inverse assertion.
- Read `restore.go`'s dispatch closely: `beginRestore`/`advanceRestore` are
  clean switches on `status.LastSignature`, routing to the renamed
  `*LatencyError`/`*DestinationRule` functions (confirmed pure rename — the
  pre-existing `restore_test.go` suite wasn't touched at all and still
  passes, which is real evidence the rename didn't change behavior, not
  just a claim) or the new parallel `*RetryStorm`/`*VirtualService` path in
  `retry_restore.go`. The `snapToNormalNoRestore` fallback is a sensible
  generalization of a pattern that already existed three times over.
- **Hand-verified the new `lerpI32` ramp arithmetic**, not just read the
  test's expected values: for the explicit-original route (attempts=5,
  trip=0), `round(0 + 5*t)` at `t=(step+1)/5` gives 1,2,3,4,5 — matches
  `wantRoute1Attempts` exactly. For the `Unset` route ramping toward Istio's
  implicit default (2), `round(0 + 2*t)` gives 0,1,1,2 for steps 0–3 (a
  deliberate plateau from rounding, same as the outlier-detection slice's
  known plateau behavior), then the block clears entirely at step 4 rather
  than landing on `attempts: 2` — matches `wantRoute0Attempts` and the
  documented "don't invent config the user never had" rule.
- **Independently confirmed the two dispatch-specific regression tests do
  what they claim**: both `TestRestoreDispatchTouchesOnlyDestinationRuleForLatencyError`
  and its `VirtualService` counterpart seed *both* a managed
  `DestinationRule` and a managed `VirtualService` simultaneously before
  tripping one specific signature — this is exactly the test shape that
  would catch a dispatch routing to the wrong object kind, which neither
  path's isolated tests could catch on their own (each only ever has its
  own object present). This was the single most important thing to verify
  in this slice, and it's genuinely covered, not just asserted to be.
- Confirmed `TestRestoreFallsBackToNormalForUnwiredSignature` exercises the
  `default` branch directly with a `FanOutAmplification`-tagged trip — a
  real test of currently-unreachable-in-production code, which is
  appropriate here since the whole point is to fail safe *before* fan-out's
  detector/mitigation exist, not after.

## Verdict
**Approved, no changes requested.** This is the cleanest "closes a
deliberately-left-open gap" slice so far — the risk window really is gone
(verified via the dispatch tests, not just the absence of a stuck-mitigation
report), the rename was genuinely behavior-preserving (verified via
unchanged passing tests, not just claimed), and the ramp math checks out by
hand. Two full signatures are now provably detect → trip → mitigate →
restore.

## Files touched
- `docs/worklog/2026-08-30-review-retry-storm-restoration.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- Fan-out amplification: no detector, no mitigation — the last signature.
  `TestRestoreFallsBackToNormalForUnwiredSignature` means its mitigation
  could be wired ahead of its restore logic without repeating this project's
  risk, though building it in one slice (detector → mitigation → restore
  together, this time knowing the shape in advance) is probably cleaner than
  reusing the split-then-close pattern a third time.
- `DestinationRule` connection-pool secondary for retry storm, and
  `VirtualService` timeout secondary for latency/error cascade — both still
  unbuilt (§2.6 secondaries).
- Live-cluster verification of Istio's implicit retry default (whether a
  plain 5xx is actually covered by the default `retryOn` list) is still
  open, carried forward honestly rather than assumed resolved.
