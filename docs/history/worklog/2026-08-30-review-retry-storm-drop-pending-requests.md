# Review: drop http1MaxPendingRequests — approved; investigation surfaced a much bigger finding

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed and committed `feat/retry-storm-drop-pending-requests` (implementing
the already-resolved direction 2). Independently verified build/vet/gofmt,
full test suite with `-race`, lint, and drift — all clean. This slice's own
content is approved without reservation.

While reviewing this slice's own honest note ("MaxRetries=0 still serializes
as empty http: {} at trip time... flagged in the worklog, not a proposal"),
I investigated further rather than accepting that framing, and found
something much more consequential — see the separate, dedicated worklog
entry and `PROPOSALS.md` entry for the full write-up. Summarizing here only
what's specific to this slice.

## Why
Same reviewer role as every slice for the slice itself. The deeper
investigation is documented separately because it's not really about this
slice — it affects retry storm's primary too, which was reviewed and
approved multiple slices ago.

## How (this slice)
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/...
  ./internal/mitigation/... ./internal/controller/... -race -count=1
  -cover`, `make lint`, `make verify-generate` — all independently run.
  Coverage: signatures 94.1%, mitigation 90.8%, controller 79.3%. 0 lint
  issues, no drift.
- Diffed `PLAN.md`: status line only ("implementation of that drop is the
  next slice" → "that drop is now implemented"), no architecture-section
  edit — correct.
- Confirmed `ApplyRetryStormConnectionPoolTrip` now only ever touches
  `MaxRetries`; `TripRetryStormMaxPendingRequests` and its field on
  `originalRetryConnectionPoolJSON` are gone.
- Confirmed the empty-shell follow-up from the last review was addressed:
  restore now calls `clearConnectionPoolHTTP` when the `http` sub-message
  ends up empty, mirroring fan-out's own restore path, now that this
  signature only manages one field and there's less reason to leave a
  residual empty message behind.
- Live evidence in the slice's own worklog (recreated `inventory-service`'s
  `DestinationRule`, tripped retry storm via k6, confirmed no
  `http1MaxPendingRequests` at trip time, confirmed a full restore leaves
  `trafficPolicy` fully absent) is consistent with what I found when I
  separately re-verified the underlying zero-value issue myself afterward.

## Verdict
**Approved and committed.** The field-drop and empty-shell fix are both
correct. See the separate entry for the much more important finding this
review surfaced.

## Files touched
- `docs/worklog/2026-08-30-review-retry-storm-drop-pending-requests.md` —
  this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above.

## Follow-ups / known gaps
- See `docs/worklog/2026-08-30-retry-storm-zero-value-serialization-bug.md`
  and `PROPOSALS.md`'s new entry — a serious, separate finding: retry
  storm's primary (`VirtualService.Retries.Attempts=0`) and this secondary
  (`DestinationRule...MaxRetries=0`) are both plain `int32` proto fields
  with `omitempty`, meaning the operator's typed `Update()` call never
  actually persists an explicit `0` to the stored object — Envoy likely
  never enforces either mitigation. This is the priority for the next
  slice, ahead of anything else.
