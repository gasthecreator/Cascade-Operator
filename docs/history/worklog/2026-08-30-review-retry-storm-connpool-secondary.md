# Review: retry storm's connectionPool secondary — process correction confirmed, overlap resolved

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/retry-storm-connpool-secondary` — the last unbuilt Istio
patch cell — by independently rebuilding/testing, confirming the live
evidence, and resolving the `http1MaxPendingRequests` overlap proposal this
slice correctly routed through `PROPOSALS.md` instead of deciding itself.

## Why
Same reviewer role as every slice. Worth saying plainly: the process
correction from the last review landed. This slice caught its own
pre-existing uncommitted work repeating the exact flagged pattern
(implementing a decision and citing a proposal that didn't exist yet),
stopped, filed the real proposal, and implemented only enough to have
something concrete to review against — explicitly not claiming that
constituted a decision.

## How
- Diffed `PLAN.md`: confined to the status paragraph and checklist this
  time — correctly describing the overlap as "pending in PROPOSALS.md, not
  locked here" rather than writing a resolution into §2. This is exactly
  what should have happened the last two times too.
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/signatures/...
  ./internal/mitigation/... ./internal/controller/... -race -count=1
  -cover`, `make lint`, `make verify-generate` — all independently run.
  Coverage: signatures 94.1%, mitigation 91.1%, controller 79.3%. 0 lint
  issues, no drift.
- Read `retry_connpool.go`: trip values reasoned carefully
  (`MaxRetries=0` matches the primary's full-disable rather than
  contradicting it with a partial cap; `MaxPendingRequests=1` gets fresh
  bulkhead-not-outage reasoning rather than reusing fan-out's constant by
  reference, since "each signature's own trip value should be justifiable
  on its own terms" is the right call even when two numbers coincide). The
  code's own doc comments already correctly flag the overlap as unresolved
  ("whether the overlap should remain at all is the pending PROPOSALS.md
  entry, not a decision this function gets to make") rather than assuming
  an answer.
- **Independently confirmed the live evidence**: `kubectl get
  destinationrule,virtualservice -n default` showed the new
  `inventory-service` `DestinationRule` genuinely deployed, and the
  `CascadePolicy` settled at `Phase: Normal`, `LastSignature: RetryStorm` —
  matching the claimed final state after the live handoff run. Confirmed no
  leftover k6 `Job`. `inventory-service`'s `VirtualService` is back to its
  exact true original (`attempts:3, perTryTimeout:2s, retryOn:...}`,
  byte-for-byte matching the committed fixture).
- **One real, minor finding from that same live check**: the
  `DestinationRule`'s `trafficPolicy.connectionPool.http` restored to an
  *empty* `{}` rather than fully absent like the true original fixture
  (which has no `trafficPolicy` at all). Functionally harmless — Envoy
  treats an empty `connectionPool.http` identically to an absent one, all
  defaults apply — but it's the one restore path in this project that
  doesn't null out empty parent structures the way `outlier.go`'s and
  `connpool.go`'s (fan-out's) restore paths both do, and that's by
  documented design (never touch the sub-message as a whole, since fan-out
  might legitimately have fields living there too). Worth a note, not worth
  blocking on — flagging it below rather than letting it pass unmentioned
  just because it's cosmetic.

## Resolving the `http1MaxPendingRequests` overlap
Approved **direction 2** (drop the field from retry storm's secondary, keep
only `maxRetries`), not direction 1 (keep both, rely on force-complete). Both
are correct *today* — I independently confirmed force-complete-on-handoff
already works for exactly this shared-field scenario, live, during this
slice's own k6 run. But direction 1 would be the first case in this project
where a shared field's data integrity depends on one mechanism (handoff)
working correctly forever, with no fallback — every other sharing case is
correct by construction (disjoint fields mean "capture current" always gets
your own true original, regardless of what any other signature's code does).
Direction 2 costs retry storm a general-bulkhead capability it doesn't
uniquely provide anyway (fan-out already bulkheads general concurrency on
the same object kind) — disjointness at essentially no real project-wide
cost, not a genuine trade-off. Full reasoning in `PROPOSALS.md`'s resolved
entry. Implementation is the next slice.

## Verdict
**Approved, with the overlap proposal resolved (implementation next) and
one minor cosmetic restore gap flagged for awareness.** This slice is also
worth calling out as evidence the process correction actually worked, not
just that the code is good.

## Files touched
- `PROPOSALS.md` — resolved the overlap proposal (APPROVED, direction 2),
  fixed a duplicate `## Resolved Proposals` header
- `PLAN.md` — status paragraph updated to reflect the resolution
- `docs/worklog/2026-08-30-review-retry-storm-connpool-secondary.md` — this
  file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes independently confirmed live cluster state, not
just re-reading the report.

## Follow-ups / known gaps
- **Next slice**: implement direction 2 — remove
  `Http1MaxPendingRequests` from `ApplyRetryStormConnectionPoolTrip`, its
  field from `originalRetryConnectionPoolJSON`, and the corresponding
  restore-interpolation logic. Fan-out's own primary is untouched.
- Minor, non-blocking: retry storm's connectionPool restore leaves an empty
  `{}` shell rather than a fully absent `trafficPolicy` when it's the only
  signature that ever touched the object — functionally harmless, worth a
  glance if the next slice is already touching this code, not worth its own
  dedicated slice.
- All three signatures' primaries and secondaries are now built (after the
  next slice lands). The Kind-based integration test suite is the one
  remaining open checklist item.
