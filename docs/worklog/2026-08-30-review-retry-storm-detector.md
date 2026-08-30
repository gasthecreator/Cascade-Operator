# Review: retry-storm detector slice

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/retry-storm-detector` against PLAN.md §2.4/§2.5 by
independently rebuilding, testing, and tracing the two-detector reconciler
logic. Fixed the one thing left for me: PLAN.md §2.3's YAML example still
described `retryStormMultiplier` as "retries/sec vs baseline" from before
real evidence existed.

## Why
Same reviewer role as every slice. This one also required judging whether a
non-PLAN.md file's comment edit (the CRD Go type's doc comment) crossed the
"don't edit architecture decisions directly" line.

## How
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/...
  ./internal/controller/... -cover`, `make test`, `make lint` — all
  independently run. Numbers matched exactly: signatures 91.9%, controller
  82.9%, metrics/mitigation unchanged, 0 lint issues.
- **First judged whether the CRD field comment edit was in-bounds.** Cursor
  changed `retryStormMultiplier`'s doc comment in
  `api/v1alpha1/cascadepolicy_types.go` (and the generated CRD YAML
  description that follows from it) from "retries/sec vs baseline" to the
  dest:source ratio description, but explicitly left PLAN.md §2.3's own YAML
  example comment untouched, flagging that one as mine to fix. This is the
  right distinction: the code comment isn't the protected decision record,
  and the new comment describes exactly what I'd recommended in the
  prompt — not a unilateral reinterpretation. Confirmed via
  `git show -- config/crd/bases/...yaml` that controller-gen regenerated the
  description consistently, no manual drift. Fixed PLAN.md §2.3's own
  comment myself, as flagged.
- Read `internal/signatures/retry_storm.go` and hand-verified the confidence
  formula: at `ratio == multiplier` (rel=1), confidence is exactly 0.5; at
  `rel=2` (2× the multiplier), confidence reaches 1.0 and is capped there.
  Confirmed no floor-clamp is needed (unlike the AND-based latency
  detector) because the function only reaches that branch when
  `ratio >= multiplier`, so `rel >= 1` is already guaranteed.
- Traced `detectSignatures` in the reconciler carefully: it evaluates both
  detectors **per host** (latency/error first, then retry storm, before
  moving to the next `dependsOn` entry) rather than evaluating one detector
  across all hosts before the other. My own prompt was ambiguous about which
  of these two orderings was intended — this is a reasonable, minimal
  extension of the existing single-detector per-host loop rather than a
  bigger restructuring, and the CRD's single-`lastSignature` status shape
  means the choice is a tie-break between simultaneous trips either way, not
  a correctness question. Not treating this as a deviation.
- Confirmed retry-storm trips never call `applyLatencyErrorMitigation`
  (gated by `sig == SignatureLatencyErrorCascade`) and that
  `RestoreStep = 0` resetting on any trip is harmless/correct even without a
  retry-storm-specific restoration path.
- Confirmed **no changes to `internal/mitigation/restore.go` or
  `internal/controller/restore.go`** were needed or made — traced why:
  `beginRestore`'s existing "zero managed edges → snap straight to Normal"
  fallback (built for a different reason during the restoration slice)
  turns out to be exactly the right behavior for a retry-storm trip too,
  since retry storm never annotates any `DestinationRule`. Cursor's test
  (`TestRetryStormHealthySnapsToNormalWithoutRestoring`) deliberately
  includes an *unmanaged* `DestinationRule` in the fixture, which correctly
  proves `listManagedEdges` doesn't mistake an unrelated object for one it
  should restore.
- Checked the `fakeQuerier` extension and confirmed no string-matching
  collision between `errorRateQuery` (no `reporter` label at all) and the
  new `reporter="destination"` substring check used to route the retry-storm
  ratio in tests.

## Verdict
**Approved, with one PLAN.md wording fix.** The design correctly reuses the
existing `SignatureRetryStorm` constant, keeps the detector pure and
dependency-free, gates mitigation to the one signature that has a patch
built, and needed zero changes to the restoration machinery because that
machinery was already shaped correctly for this case. Good judgment on the
CRD-comment-vs-PLAN.md-comment distinction — exactly the kind of nuance the
protocol is supposed to produce rather than blanket rule-following.

## Files touched
- `PLAN.md` — §2.3 YAML example comment corrected to dest:source ratio
- `docs/worklog/2026-08-30-review-retry-storm-detector.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- `VirtualService` retry-budget patch (§2.6 retry-storm primary) is next for
  this signature, same sequencing as latency/error cascade's own Istio patch
  slice.
- Fan-out detector still unbuilt — third and last signature.
- Ratio threshold (4.0 observed vs. 3.0 default multiplier) is carried from
  the 2026-08-29 Kind evidence, not re-verified live in this slice — flagged
  by Cursor as a PROPOSALS.md candidate if real mixed traffic makes it
  noisy, not assumed fine.
