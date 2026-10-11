# Review: fan-out signature (detector, mitigation, restoration) + signature-handoff proposal

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/fanout-signature` — the combined detector, mitigation, and
restoration slice for the third and final signature — by independently
rebuilding/testing and reading the code closely. Resolved the one real
architecture proposal this slice surfaced. Also flagging a protocol
boundary issue in this same slice, separately from the code review itself.

## Why
Same reviewer role as every slice, but this one carries more weight than
most: it's the biggest single slice yet, and it surfaced a genuine
correctness gap in the core reconciliation state machine rather than a
narrow implementation detail.

## Protocol flag (not silently fixed — reporting as instructed)
`PLAN.md`'s diff for this slice was not confined to the status line and
checklist. §2.6's prose was edited directly: the sentence "First mitigation
slice implements only the latency/error cascade primary... The other cells
are the contract for later slices, not built yet" was rewritten to "All
three primaries are now built...". This is inside "## 2. Architecture
Decisions," which the working agreement (set by Gideon at the start of this
project) says Cursor never edits directly — proposed changes go through
`PROPOSALS.md` instead, regardless of how factual or narrow the edit is.

The content itself is accurate and is exactly the kind of update I would
have made myself in review — it isn't a case of Cursor silently
reinterpreting a decision (unlike, say, unilaterally changing a threshold
or an object-kind choice). But per the explicit instruction to flag
drift rather than silently reconcile it: this happened, I'm reporting it
rather than quietly treating it as fine. I've folded my own edit into that
same paragraph as part of resolving the signature-handoff proposal below,
so going forward the section reflects my authorship, but the boundary was
crossed once here. Worth a reminder to Cursor that "this is just a status
fact, not really a decision" is not a carve-out — the rule is about the
section, not about judging each sentence's category before deciding whether
it's safe to touch.

## How (code review)
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/...
  ./internal/mitigation/... ./internal/controller/... -cover`, `make lint`,
  `make verify-generate` — all independently run. Coverage matched exactly:
  signatures 94.1%, mitigation 89.2%, controller 78.6%. 0 lint issues, no
  drift.
- Read `internal/signatures/fanout.go`: pure function, same shape and
  conventions as `DetectRetryStorm` (inclusive threshold, NaN/Inf guarded,
  confidence 0.5-at-threshold rising to 1 at 2×). Correctly reuses the
  existing `SignatureFanOutAmplification` constant.
- Read `internal/controller/promql.go`'s new `fanOutRatioQuery`: correctly
  cross-host (dependency's `reporter="destination"` count over
  `spec.Service`'s `reporter="destination"` count), matching exactly what
  the fan-out-demo-evidence slice measured — both sides use the same
  reporter, which is what that live scrape actually validated.
- Read `internal/mitigation/connpool.go` closely: trip value of 1 (not 0)
  for both `http1MaxPendingRequests`/`http2MaxRequests` is reasoned
  carefully — cites Envoy's actual default (1024) with a real doc link, and
  explicitly distinguishes "bulkhead" (cap concurrency) from "0 = full
  outage," a materially different mitigation than what §2.6 asks for. Also
  correctly reasons through a real proto3 subtlety: these two fields are
  plain `int32` scalars, not nullable wrappers like
  `Consecutive_5XxErrors`, so there's no wire-level way to distinguish
  "authored as 0" from "not specified" — correctly simpler `Unset`-at-the
  whole-block-level handling follows from that, not from an oversight.
- **The main event: read the signature-handoff finding and traced it
  through `cascadepolicy_controller.go` myself** rather than taking the
  `PROPOSALS.md` write-up at face value. Confirmed: `Reconcile`'s trip
  branch (`if tripped`) unconditionally sets `LastSignature = sig` and
  dispatches mitigation for whatever signature tripped this tick, with no
  check against what `LastSignature` was previously or what `Phase` was.
  The restore branch (`else if evaluated > 0`) is structurally only
  reachable when *nothing* trips. So a same-host handoff (latency/error
  clears, fan-out crosses threshold, same tick) genuinely does abandon the
  outgoing signature's fields and annotation exactly as described — this is
  real, not a hypothetical.
- Read `fanout_restore.go`'s doc comment in full: it independently reasons
  through why sharing `listManagedDestinationRuleEdges` between two
  signatures is fine for the *normal* case (each restore path only reads/
  writes its own fields and its own annotation key) and correctly
  identifies the handoff case as the one thing that isn't covered by that
  reasoning — matches my own analysis before I'd read this comment.
- Confirmed the related bug Cursor found and fixed *within* this slice is
  real and correctly fixed: `ApplyLatencyErrorOutlierTrip` and
  `ApplyFanOutConnectionPoolTrip` both now key their own baseline capture
  off their *own* annotation's presence (`AnnotationOriginalOutlier` /
  `AnnotationOriginalConnectionPool`), not the shared `managed-by` flag —
  otherwise, whichever signature's mitigation touches a `DestinationRule`
  *second* would see `managed-by` already set (by the first) and skip
  capturing its own true baseline, corrupting its own eventual restore.
  This is a distinct, smaller issue from the handoff gap, correctly
  resolved in this slice rather than left for later.

## Resolving the signature-handoff proposal
Neither of Cursor's two proposed directions was quite right: option 1 (force
completion) as written implied a multi-tick handoff state machine and a
status-shape change; option 2 pushes cross-signature awareness into the
trip path, undoing the clean signature-scoping every other part of this
codebase has maintained. The actual fix is smaller than either: **when
`Reconcile` is about to adopt a signature different from
`status.LastSignature` while `Phase != Normal`, synchronously call the
outgoing signature's existing `complete*Restore` function before applying
the incoming trip** — reusing the exact function each restore ramp already
calls at its final step, not new logic. Safe to do eagerly (skip the
gradual ramp) specifically because the outgoing signature's own detector
just confirmed, this same tick, that its condition is gone — the ramp's
per-step re-verification exists to catch a regression before committing
further to *that* signature's restore, which doesn't apply when we're not
continuing it, we're closing it. No CRD change. Approved and written up in
`PROPOSALS.md` and `PLAN.md` §2.6; implementation is the next slice.

## Verdict
**Approved, with the signature-handoff proposal resolved (implementation
next) and a protocol reminder flagged (not a code defect).** This is
careful, well-tested, well-reasoned work — the fact that it surfaced and
correctly diagnosed a real gap in the core state machine, then chose to
flag it rather than pick a fix unilaterally, is exactly the outcome the
PROPOSALS.md protocol exists to produce. All three signatures now have a
detector, a mitigation, and a restoration path; the one thing not yet
correct is what happens at the seam between them.

## Files touched
- `PROPOSALS.md` — resolved the signature-handoff proposal (APPROVED, my
  direction), fixed two duplicate `## Resolved Proposals` headers along the
  way (pre-existing formatting slip, not introduced by this slice)
- `PLAN.md` — §2.6 addition documenting the handoff decision; new checklist
  line tracking the not-yet-implemented fix
- `docs/worklog/2026-08-30-review-fanout-signature.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- **Next slice (priority over secondaries or anything else)**: implement
  the force-complete-on-handoff fix in `Reconcile`, per the resolved
  proposal and PLAN.md §2.6. This is a correctness gap in the now-complete
  three-signature system, not polish.
- Remaining Istio patch secondaries (latency/error's `VirtualService`
  timeout; retry storm's `DestinationRule` connectionPool cap) — still
  unbuilt, lower priority than the handoff fix.
- k6 scripts, integration test suite, README — all still open checklist
  items.
