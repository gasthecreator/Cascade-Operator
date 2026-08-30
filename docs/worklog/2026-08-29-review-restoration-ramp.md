# Review: restoration ramp slice

**Date:** 2026-08-29
**Author:** Claude
**Type:** docs

## What
Reviewed `feat/restoration-ramp` against PLAN.md §2.6 by independently
rebuilding, testing, and hand-verifying the interpolation math rather than
trusting the worklog's numbers.

## Why
Same reviewer role as every prior slice.

## How
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/mitigation/...
  ./internal/controller/... -cover`, `make lint`, `make test`,
  `make verify-generate` — all independently run. Coverage matched exactly:
  mitigation 82.6%, controller 79.8%, metrics/signatures unchanged. 0 lint
  issues. `verify-generate`'s diff was the same 4-file uncommitted-branch
  pattern as every prior slice (confirmed no new content appeared after
  regeneration) — not real drift, consistent with what's now an established,
  understood false-alarm shape for this Makefile target on an uncommitted
  branch.
- **Hand-computed the lerp arithmetic**, not just read the test's expected
  values: for the "original: consecutive=7, interval=10s" test case, worked
  out `round(3 + 4*t)` and `5s + 5s*t` at `t = (step+1)/5` for each step
  myself before comparing to `TestRestoreProgressMonotonicTowardOriginal`'s
  `wantConsec`/`wantInterval` tables. Every value matched
  (4, 5, 5, 6, 7 and 6s, 7s, 8s, 9s, 10s) — including the deliberate plateau
  at consecutive5xx steps 1→2, which the worklog correctly attributes to
  integer rounding on a small gap rather than a bug.
- **Traced the two-tick completion behavior through the actual code**, not
  just the worklog's prose: `advanceRestore` in
  `internal/controller/restore.go` checks
  `RestoreStep >= RestoreFinalStep` — if true, it calls `completeRestore`
  (strip annotations, `Normal`, `restoreStep=0`) instead of incrementing
  further. This means reaching step 4 applies the true original values but
  *stays* `Restoring`, and only a **subsequent** healthy tick at step 4
  triggers the actual transition to `Normal`. That's a real extra
  verification gate before declaring victory, not an artifact — confirmed
  by `TestRestoreAdvancesEachHealthyStepThenCompletes` running exactly six
  ticks (`Restoring 0,1,2,3,4` then `Normal`) and matching.
- Verified the "evaluated > 0" gate is real, not just asserted: read
  `detectLatencyErrorCascade`'s changed return signature and confirmed
  `evaluated` only increments on a complete (both queries succeeded)
  reading, and `TestQueryErrorWhileTrippedDoesNotRestore` proves a
  Prometheus outage leaves the policy `Tripped` with the trip patch still
  in place rather than treating missing data as health.
- Confirmed `DetectOnly` never calls `r.Update` on the `DestinationRule` in
  either `applyRestoreStep` or `completeRestore` — both check mode before
  the update loop, matching the same pattern as the trip-path slice.
- One minor documentation nuance, not a defect: the worklog's "DetectOnly
  still advances status so a demo can show the ramp" is accurate for a
  policy that was `Mitigate` during its trip and gets switched to
  `DetectOnly` mid-ramp (the managed `DestinationRule` still exists,
  so `listManagedEdges` finds it and the status machine keeps stepping
  without touching the mesh). It's *not* accurate for a policy that has
  been `DetectOnly` its entire life — that case never accumulates a managed
  edge, so `beginRestore` finds zero edges and snaps straight `Tripped→Normal`
  with no visible ramp at all. Both behaviors are individually correct; the
  one-line summary just doesn't distinguish the two paths. Not worth a code
  change, and not worth editing Cursor's own worklog entry over — noting it
  here for anyone who goes looking for a "DetectOnly ramp demo" and finds it
  only works when Mitigate produced the annotation first.

## Verdict
**Approved, no changes requested.** Design and tests are precise:
correct FSM transitions in all four directions (enter, advance, complete,
regress), a defensible interpolation scheme with the "unset stays unset at
completion" distinction correctly separated from "unset interpolates toward
Istio's default mid-ramp," and annotation cleanup that leaves the object
truly indistinguishable from pre-operator state. One signature is now
provably detect → trip → restore end to end.

## Files touched
- `docs/worklog/2026-08-29-review-restoration-ramp.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes independent hand-verification of the
interpolation math, not just re-running the existing tests.

## Follow-ups / known gaps
- Next per PLAN.md: `VirtualService` secondary cell (timeout) for this
  signature, or the retry-storm/fan-out detectors — Gideon's call on
  sequencing.
- Carried forward, not resolved here: `histogram_quantile` aggregation
  correctness, `response_flags=UR` verification, Kind has no Istio installed
  yet.
