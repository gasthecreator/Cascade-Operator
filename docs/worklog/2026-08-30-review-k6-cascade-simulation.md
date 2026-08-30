# Review: k6 cascade-simulation scripts + retry-storm webhook-rejection proposal

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/k6-cascade-simulation` — k6 scripts for all three signatures,
a `depsvc` "slow" mode, committed retry-storm/DestinationRule/CascadePolicy
demo fixtures, and the in-cluster-Job runner — by independently rebuilding
the `demo` module and, for the slice's most consequential finding,
reproducing the actual Istio admission-webhook rejection live myself rather
than trusting the quoted error.

## Why
Same reviewer role as every slice. This one surfaced a real mitigation bug
via live traffic (not a unit test gap), so the review's job was mostly to
confirm the bug is real and pick the right fix, not just read code.

## How
- Diffed `PLAN.md`: confined to the checklist and the intro paragraph ahead
  of the checklist (status narrative, same category as similar updates in
  prior slices) — no edit to §2's Architecture Decisions body this time.
- `cd demo && go build/vet/test -cover && gofmt -l .` — clean;
  `internal/depsvc` coverage jumped to 91.2% now that `NewMux` lets tests
  exercise the real handler over `httptest` instead of a duplicated fake.
  Confirmed (again) that root's `go list ./...` still sees zero `demo/`
  packages and root `go build` is unaffected — module isolation holds.
- Read the `depsvc.go` diff: `slowLatency`/`slowErrorEvery` constants are
  reasoned the same way the mitigation package's own trip constants are
  (comfortably clear the threshold, not right at the line), and `/control/
  heal` correctly clears *both* `failing` and `slow` now — a real
  cross-script contamination risk if one script's induce and another
  script's heal got interleaved, caught and handled.
- Confirmed the demo fixtures are genuinely live on this machine's cluster,
  not just described: `kubectl get virtualservice,destinationrule,
  cascadepolicy -n default` showed `inventory-service`'s retry
  `VirtualService`, `payments-service`'s `DestinationRule`, and the
  `checkout-service` `CascadePolicy`, all already applied.
- **Independently reproduced the core finding myself**, live: applied a
  `VirtualService` patch setting `attempts: 0` while leaving
  `retryOn`/`perTryTimeout` set on `inventory-service` — got the identical
  rejection verbatim (`http retry policy configured when attempts are set
  to 0 (disabled)`). Then tested both proposed directions against the same
  live object before deciding: clearing `retryOn`/`perTryTimeout` alongside
  `attempts: 0` was accepted with no error; `attempts: 1` with `retryOn`/
  `perTryTimeout` still set was *also* accepted (so the proposal's
  uncertainty about direction 2's webhook-acceptance is resolved too — it
  would have worked). Restored the original fixture after each test.
- Chose direction 1 anyway, on the merits rather than "it's what was
  checked first": it's semantically cleaner (retries disabled shouldn't
  carry retry-behavior fields describing retries that will never run — the
  webhook isn't creating an artificial constraint, it's catching something
  that was already a contradiction), loses no information (the full
  original block is already captured for restoration), and doesn't reopen
  the already-reasoned "0, not 1" amplification-headroom decision that's on
  record elsewhere in the mitigation package.
- Read through the "How" section of the worklog's port-forward and
  stale-pod-tag findings — both are real, correctly diagnosed Istio/K8s
  behaviors (sidecar iptables interception not applying to port-forward
  traffic; a mutable image tag giving Kubernetes no signal that a
  `Deployment`'s already-running pod needs replacing), not guesses dressed
  up as findings.

## Verdict
**Approved, with the webhook-rejection proposal resolved (implementation
next).** This is careful, honest work that used real infrastructure to
find a real bug the fake-client unit tests structurally couldn't catch
(no admission webhook in a fake client), then correctly routed the fix
through `PROPOSALS.md` instead of picking one silently. Two of three
signatures are now demonstrated working end-to-end on real k6-driven
traffic against live Istio; the third's detection half is proven the same
way, with a clean fix identified and queued for its mitigation half.

## Files touched
- `PROPOSALS.md` — resolved the webhook-rejection proposal (APPROVED,
  direction 1, both directions independently verified live before
  deciding), fixed a duplicate `## Resolved Proposals` header
- `PLAN.md` — caveat note updated from "found" to "found, now resolved,"
  with the confirmed fix direction
- `docs/worklog/2026-08-30-review-k6-cascade-simulation.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes independently reproducing both the bug and the
fix live against the actual cluster, not re-reading the report.

## Follow-ups / known gaps
- **Next slice**: implement the resolved fix in
  `internal/mitigation/retries.go`'s `ApplyRetryStormTrip` — clear
  `retryOn`/`perTryTimeout`/`backoff` alongside `attempts: 0` on trip. This
  closes the one thing keeping retry storm from being a fully-proven
  signature like the other two.
- Remaining Istio patch secondaries — still open, lower priority.
- Integration test suite, operator's own metrics, README — still open
  checklist items.
