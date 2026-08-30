# Review: fan-out demo topology + live-scrape evidence

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed `feat/fanout-demo-evidence` by deploying nothing new — the demo
topology was already live on this machine's Kind cluster — and independently
reproducing the core fan-out evidence myself, live, rather than trusting the
reported numbers.

## Why
Same reviewer role as every slice, but this one's central claim (a 3×
request-count amplification, driven purely by application-level retries) is
exactly the kind of thing worth checking hands-on when the infrastructure to
check it is sitting right there on the same machine.

## How
- Confirmed the demo topology is genuinely deployed: `kubectl get pods -n
  default` showed `checkout-service`/`payments-service`/`inventory-service`
  all `Running 2/2` (sidecar-injected), alongside the still-present
  `sleep`/`httpbin`.
- Read `demo/checkout/main.go` and `demo/internal/depsvc/depsvc.go` in full:
  confirmed the code deterministically guarantees exactly one call to
  `inventory` and up to three to `payments` (stopping on the first 2xx) per
  `/checkout` request, with each attempt a fully independent `http.Client.Get`
  — not an Envoy-level retry, matching the worklog's own careful distinction
  from the retry-storm signature.
- **Independently reproduced the healthy baseline**: generated traffic
  through the live `sleep` pod, hit the same `increase()`-over-a-tight-burst
  trap the worklog itself documents (got `0` on a fast, unspread burst before
  realizing why), then switched to raw cumulative-counter deltas across a
  single request — clean, unambiguous, no extrapolation noise. Result:
  checkout=1, payments=1, inventory=1, confirmed across three separate
  trials.
- **Independently reproduced the failure-amplification finding** the same
  way: toggled `payments` to failing via `POST /control/fail`, took a raw
  counter snapshot, sent exactly one `/checkout` request, snapshotted again
  15s later. Delta: checkout=1, payments=**3**, inventory=1 — exact match to
  the claimed 3× ratio, on the first clean attempt.
- One of my own intermediate readings looked off (a `payments=2` delta right
  after healing) — traced it to my own test timing (a request landing too
  close to the heal-toggle transition), not a real inconsistency; three
  repeated trials afterward all came back clean 1:1:1. Worth noting since
  it's the same class of harness-timing artifact the worklog itself flags
  (the "stray +1" on payments' 200 count) — this signature's evidence is
  sensitive to toggle/traffic timing, and both the worklog's own account and
  my independent check separately ran into and correctly attributed a
  version of it.
- Healed `payments` back after my own testing (it was already healed by the
  time I got there, then I re-tested against it healthy) — left the cluster
  in the same state I found it.
- Verified module isolation claims directly rather than taking them on
  faith: `git diff` against the previous branch showed zero changes to the
  operator's root `go.mod`/`go.sum`, and `go list ./...` from the repo root
  returns zero `demo/` packages — full nested-module isolation confirmed,
  not just asserted.
- `cd demo && go build/vet/test/gofmt` — all clean, matching the worklog's
  own report.
- Read `demo/checkout/main_test.go`: three correct, focused tests
  (succeeds-first-try, exhausts-on-persistent-failure, stops-once-healthy)
  directly covering the one piece of real logic in the demo topology.
- Diffed `PLAN.md`: status line and checklist only, correctly checking off
  "Demo microservice topology for fault injection" now that it's genuinely
  built and deployed, not just planned.

## Verdict
**Approved, no changes requested.** This is exemplary evidence-gathering
work: methodical, honest about a harness-timing artifact in its own numbers
(the stray +1), and it produced a real, useful forward-looking finding —
that the fan-out detector will need a **cross-host** ratio (dependency vs.
caller), architecturally different from retry storm's same-host
reporter-split ratio — without touching any detector or mitigation code, as
scoped. I don't often get to independently reproduce a slice's central
numeric claim on the same live infrastructure; this one held up exactly.

## Files touched
- `docs/worklog/2026-08-30-review-fanout-demo-evidence.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes live independent reproduction of both the
healthy and failure-amplification numbers, not just re-reading the report.

## Follow-ups / known gaps
- Next slice: the fan-out detector, mitigation, and restoration — per
  Cursor's own stated intent, likely built together this time (not
  split into three slices like the first two signatures), now that the
  pattern is well-established. The cross-host ratio shape and the
  `fanOutMultiplier`-as-implicit-baseline-1 semantics (same pattern as
  `retryStormMultiplier`) are both already scoped from this slice's
  findings.
- No fan-out mitigation yet (`DestinationRule` connection-pool bulkhead
  per §2.6).
- k6 scripts still not built — still a later checklist item.
