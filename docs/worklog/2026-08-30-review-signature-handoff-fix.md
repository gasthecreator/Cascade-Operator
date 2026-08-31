# Review: signature-handoff restore fix

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed `fix/signature-handoff-restore` — the implementation of the
signature-handoff direction I resolved in the previous review — by
independently rebuilding/testing and, notably, independently reproducing
the "revert the fix and watch the new tests fail" verification myself
rather than trusting the claim.

## Why
Same reviewer role as every slice. This one's central claim (that the new
tests are load-bearing, not passing coincidentally) is directly and cheaply
checkable, so I checked it rather than taking it on faith.

## How
- Diffed `PLAN.md`: this time confined to the status header and checklist
  only — §2.6's decision prose (my own authored content from the prior
  review) was left untouched, exactly as the worklog claims and exactly
  what should happen now that the decision itself isn't being revisited,
  only implemented.
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/...
  ./internal/mitigation/... ./internal/controller/... -cover`, `make lint`,
  `make verify-generate` — all independently run. Coverage matched exactly:
  controller 77.3% (down slightly from 78.6%, expected — new dispatch
  branches, not a coverage regression in what's tested), signatures 94.1%
  and mitigation 89.2% unchanged (no mitigation-package code touched, as
  scoped). 0 lint issues, no drift.
- Read `forceCompleteOutgoingRestore` and its wiring into `Reconcile`
  closely: reads `outgoingSig` before it gets overwritten, gates on
  `outgoingSig != "" && outgoingSig != sig && Phase != Normal`, and —
  a detail not explicitly asked for in the prompt but the right call —
  gates the rest of the trip branch on `mitErr == nil`, so a failed
  force-complete doesn't also proceed to apply the incoming trip on top of
  a still-orphaned object. Confirmed by reading, not assuming, that no
  `complete*Restore` function ever resets `LastSignature` back to `""`
  (`grep` across all three restore files) — which is what makes
  `Phase == Normal` alone sufficient to also correctly cover "already fully
  restored under a different signature, tripping again" without a separate
  carve-out, exactly as the worklog reasons through.
- **Independently reproduced the strongest claim in the worklog myself**,
  rather than trusting it: checked out the pre-fix versions of
  `cascadepolicy_controller.go` and `restore.go` from the previous commit,
  ran the two new handoff tests against them, and got the *exact* failure
  described — `consecutive5xx = 5, want true original 7` and the matching
  connection-pool-direction failure — then restored the fix and confirmed
  both pass again. This is real, not a coincidence or a restated claim.
- Read `TestSignatureReTripSameSignatureDoesNotForceComplete`'s design:
  using a deliberately malformed `original-outlier-detection` annotation as
  a canary (the trip path never parses it, only checks presence; the
  restore path would parse it and error) is a genuinely well-thought-out
  regression oracle — it doesn't just assert on end state, which a bug
  could satisfy by coincidence in a single-edge scenario, it makes the bug
  and the test outcome causally linked.

## Verdict
**Approved, no changes requested.** This closes the one real correctness
gap the three-signature system had, cleanly: a new call site for existing,
already-tested logic, no new mitigation-package code, no CRD change, and a
test suite that's demonstrably load-bearing rather than just green. Also
worth noting: this slice respected the PLAN.md boundary that the previous
slice crossed once — a good sign the flag landed as a reminder rather than
being ignored.

## Files touched
- `docs/worklog/2026-08-30-review-signature-handoff-fix.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes independently reproducing the pre-fix failure,
not just re-running the post-fix passing suite.

## Follow-ups / known gaps
- Remaining Istio patch secondaries (latency/error's `VirtualService`
  timeout; retry storm's `DestinationRule` connectionPool cap) — now clear
  to pick up, no longer blocked behind a correctness fix.
- k6 scripts, integration test suite, README — still open checklist items.
