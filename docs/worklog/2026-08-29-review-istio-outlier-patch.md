# Review: Istio outlier-detection patch slice

**Date:** 2026-08-29
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/istio-outlier-patch` against PLAN.md §2.3/§2.6 by
independently rebuilding, testing, and reading the code. Corrected one
inaccurate PLAN.md checklist checkbox; no code changes needed.

## Why
Same reviewer role as the previous slices — verify before approving, keep
PLAN.md honest as the source of truth.

## How
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/mitigation/...
  ./internal/controller/... -cover`, `make lint` — all independently run.
  Numbers matched exactly: mitigation 94.9%, controller 85.8% (down slightly
  from 88.7%, expected — new error/NoMatch branches aren't all exercised),
  0 lint issues.
- `make verify-generate` reported a diff — investigated rather than taken at
  face value. It compares regenerated files against **git HEAD**, and this
  entire branch is uncommitted, so any legitimately new RBAC content will
  show as a "diff" regardless of whether it's actually drifted from what
  codegen produces. Confirmed it's a false alarm: `git diff --stat` before
  and after running the regeneration step were identical — no additional
  changes appeared, meaning the working tree's `config/rbac/role.yaml`
  already matches fresh codegen output exactly. Not a Cursor defect.
- Read `internal/mitigation/resolve.go`: `ParseServiceFQDN` requires exactly
  two DNS-1123 labels before `.svc.cluster.local`, matching §2.3's
  resolution convention precisely.
- Read `internal/mitigation/outlier.go`: `ApplyLatencyErrorOutlierTrip`
  mutates only `outlierDetection` fields via the real Istio protobuf types
  (`durationpb`/`wrapperspb`), captures the pre-patch state into an
  annotation **only** when `managed-by` isn't already set, and stores an
  explicit `{"unset":true}` sentinel rather than inventing a "loose default"
  when there was nothing to restore to — exactly the forward-compatibility
  contract the prompt asked for, so the restoration slice has real ground
  truth.
- Read `internal/controller/mitigate.go`: no `Create` path exists at all —
  confirmed by `TestMitigateMissingDestinationRuleSetsCondition` explicitly
  asserting `apierrors.IsNotFound` after reconcile. `isAbsent` correctly
  covers both `NotFound` (object missing) and `NoMatchError` (Istio CRD not
  installed at all — our current Kind cluster's actual state).
- Checked `SetupWithManager`: no `.Watches()` was added for `DestinationRule`
  — confirmed the worklog's stated reasoning (a watch would fail manager
  startup on a cluster without the Istio CRD installed, which is today's
  Kind cluster) is actually reflected in the code, not just asserted.
  Get-on-demand inside the reconcile loop is the only interaction, so the
  manager still starts cleanly without Istio present.
- Read `istio_patch_test.go`: all four promised scenarios (missing → no
  create + condition; `DetectOnly` → no mutation; first `Mitigate` trip →
  both annotations + trip values; re-trip → trip values re-applied, original
  preserved) are real tests against the actual `Reconcile` path through a
  fake client, not just the isolated `mitigation` package functions in
  isolation — stronger coverage than the minimum I asked for.
- **Found one inaccuracy, not in the code:** PLAN.md's checklist checked off
  "Istio patch layer (DestinationRule / VirtualService client +
  annotations)" as a single item, but only the `DestinationRule` primary
  landed — the worklog's own "Follow-ups" section says as much
  ("Secondary cell (VirtualService timeout) still unbuilt"). A single
  checked box overclaims relative to what's actually built, which matters
  for a document meant to be skimmed by an interviewer. Split it into two
  checklist lines: one checked (DestinationRule primary), one still open
  (VirtualService cells) — no code implicated, just record-keeping.

## Verdict
**Approved, with one PLAN.md checklist correction.** Design and
implementation are exactly right: correct resolution convention, no object
creation, scoped read-modify-write, correct forward-compatibility annotation
contract for the restoration slice, and mode gating that doesn't touch
detection. The `make verify-generate` "failure" was investigated and
confirmed to be an artifact of comparing an uncommitted branch to HEAD, not
a real defect.

## Files touched
- `PLAN.md` — split the Istio-patch-layer checklist line into
  done (DestinationRule) / not-yet (VirtualService)
- `docs/worklog/2026-08-29-review-istio-outlier-patch.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- Next slice (Cursor's, per its own worklog): the restoration ramp, reading
  `original-outlier-detection` and stepwise loosening the same three fields.
- Carried forward, not resolved here: `VirtualService` secondary cell
  (timeout) for this signature; retry-storm and fan-out detectors/patches;
  `histogram_quantile` aggregation correctness; `response_flags=UR`
  verification; Kind still has no Istio installed.
