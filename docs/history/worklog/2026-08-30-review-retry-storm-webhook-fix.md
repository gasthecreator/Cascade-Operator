# Review: retry-storm mitigation webhook-rejection fix

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed `fix/retry-storm-mitigation-webhook` — the implementation of the
already-resolved `PROPOSALS.md` fix (clear `retryOn`/`perTryTimeout`/
`backoff` alongside `attempts: 0` on trip) — by independently rebuilding
and testing, and attempting a live re-confirmation against the Kind
cluster.

## Why
Same reviewer role as every slice. This one's underlying webhook behavior
was already directly verified by me in the previous review (I applied the
exact before/after patches to the live `inventory-service` object myself),
so this review's job was mainly confirming the Go implementation matches
that already-proven mechanism correctly, plus attempting fresh live
confirmation if the cluster cooperated.

## How
- Found the Kind cluster's control-plane container `Exited (137)` — real,
  not a claim to take on faith. Restarted it myself
  (`docker start cascade-operator-control-plane`), waited for the API
  server and pods to come back, and did briefly get `kubectl get nodes`
  returning `Ready`. It then degraded again under the same resource
  pressure (`TLS handshake timeout` on subsequent calls, load average still
  8–13). Made a genuine attempt rather than assuming the claim — didn't
  succeed, same outcome as the original session, for the same reason.
  Didn't keep fighting an overloaded machine past a reasonable attempt.
- `go build`, `gofmt -l`, `go vet`, `go test ./internal/mitigation/...
  ./internal/controller/... -cover`, `make lint`, `make verify-generate` —
  all independently run despite the cluster being unavailable (these don't
  need it). Coverage: mitigation 89.6% (up slightly from 89.2%, matching
  the new field-clearing assertions), controller 77.3% unchanged. 0 lint
  issues, no drift.
- Read the `retries.go` diff: `RetryOn = ""`, `PerTryTimeout = nil`,
  `Backoff = nil` set alongside `Attempts = TripRetryAttempts` in the same
  trip loop — exactly the fix I approved, and exactly what I'd independently
  confirmed passes Istio's webhook in the prior review's live test. The
  doc comment rewrite correctly stops claiming these fields are "preserved"
  and explains why clearing them is required, not just convenient.
  Diffed `PLAN.md`: only the caveat note (marked implemented, honest about
  the missed live re-confirmation) — no architecture-section edit.
- Read the two test diffs: `retries_test.go`'s multi-route test now asserts
  clearing instead of preservation and added a `Backoff` value to the
  fixture so all three fields are actually exercised, not just two.
  `retry_restore_test.go`'s fix to the one test this change broke
  (`TestRetryStormRestoreAdvancesEachStepThenCompletes`) correctly derives
  `wantRetryOn` per tick — empty through every mid-ramp step (trip cleared
  it, and steps 0–3 only touch `attempts`), back to the real value only at
  `RestoreFinalStep` and beyond, matching the established "step 4 applies
  true original, stays `Restoring`; the *next* tick transitions to `Normal`"
  pattern this codebase has used consistently since the first restoration
  slice.

## Verdict
**Approved, no changes requested.** Small, mechanical, correctly-scoped
change that matches exactly what was resolved and what I'd already directly
verified works against the real webhook. The honest "couldn't re-confirm
live, here's exactly why" note in both the worklog and PLAN.md is the right
call given the circumstances — better than a live-run claim that didn't
actually happen, and lower-risk than usual given the underlying mechanism
was already proven, not fresh.

## Files touched
- `docs/worklog/2026-08-30-review-retry-storm-webhook-fix.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes a genuine attempt at live re-confirmation
(cluster restarted, briefly working, degraded again under real resource
pressure) alongside full independent code-level verification.

## Follow-ups / known gaps
- Live re-confirmation of this exact fix against a real
  retry-storm-tripped policy is still open — worth doing once the machine
  isn't under this much contention. Not blocking: the underlying webhook
  mechanics were already directly verified in the prior review.
- Remaining Istio patch secondaries, integration test suite, operator's own
  metrics, README — all still open checklist items.
