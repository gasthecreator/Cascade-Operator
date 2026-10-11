# Review: retry storm's maxRetries trip value bumped to 1 — approved; organic trip gap filled myself

**Date:** 2026-08-31
**Author:** Claude
**Type:** docs

## What
Reviewed and committed Cursor's implementation of the approved
`TripRetryStormMaxRetries = 1` proposal (`fix/retry-storm-maxretries-one`).
Cursor's own session ran out of usage before this could be reviewed, so I
took over verification directly rather than waiting — including filling
the one real gap in Cursor's own report: no organic (detector-driven)
trip had actually exercised this code against the live cluster, only the
production patch bytes applied by hand.

## Why
Same reviewer role and same standing bias every slice gets — read the
diff myself, run the suite myself, re-derive the live claims myself. This
slice's own worklog was explicit that the operator never got a live
metrics feed this session (`connection refused` on the local Prometheus
port-forward), so the Kubernetes-object and Envoy checks it reported were
against hand-applied patch bytes, not a real detect→mitigate cycle. That's
a real, not cosmetic, gap given the entire point of the last three slices
has been "don't trust a check that stops one layer short of the thing
that actually matters."

## How
- `git status` confirmed the change was uncommitted on the correct branch,
  touching only the files Cursor's report claimed: the constant/doc
  comment in `internal/mitigation/retry_connpool.go`, three test files
  updating restore-ramp expectations, one test file updating the
  merge-patch assertion, and the worklog/README index.
- Read every diff. `TripRetryStormMaxRetries` is `1` with a doc comment
  citing the exact Pilot source location; the merge-patch write path is
  untouched (correct — it's still needed as the transport mechanism even
  though `1` itself would survive a typed `Update()`, since keeping one
  mechanism for both of this signature's writes is simpler than
  conditionally switching based on the current constant value).
- Independently redid the restore-ramp arithmetic rather than trusting the
  comments: `lerpI32(1, 3, 0.2) = round(1 + 2*0.2) = round(1.4) = 1`
  (unit-level step-0 case) and `lerpI32(1, 10, t)` at `t = (step+1)/5` for
  steps 0–4 gives `{3, 5, 6, 8, 10}` then `10` on completion — matches the
  controller-level test's updated `wantMaxRetries` exactly.
- Checked the seeded-`MaxRetries: 10` additions in `dualManagedDR` /
  `tripleManagedDR` for a subtler reason than "make the number nicer":
  with the trip value now `1`, a fixture whose true original was
  legitimately unset (`0`, defaulting to Envoy's `istioDefaultMaxRetries =
  3`) would lerp to `round(1 + 2*0.2) = 1` at step 0 — identical to the
  trip value, no longer distinguishing "restore has started" from "still
  tripped" the way the test needs to. Seeding a real original before the
  trip call is necessary, not cosmetic, and doesn't corrupt anything else
  captured on the same object — confirmed `originalConnectionPoolJSON`
  (fan-out's own snapshot) only ever records
  `Http1MaxPendingRequests`/`Http2MaxRequests`, never `MaxRetries`, so
  seeding that field after fan-out's trip call is safe.
- `go build ./...`, `gofmt -l .`, `go vet ./...` — clean.
- `go clean -testcache && go test ./... -race -count=1 -cover` — full
  suite, not just the touched packages: controller 79.3%, metrics 80.4%,
  mitigation 90.9%, signatures 94.1%, all pass.
- `make lint` — 0 issues.
- **Live re-verification, done myself:**
  - Found and killed a stray `go run ./cmd/main.go` process (and its
    compiled binary) squatting on port 8081 from an earlier, unrelated
    session — this is what had been silently starving Cursor's own local
    operator of a working Prometheus connection (`NaN` evidence, no organic
    trip, all session long). Not a code defect — a leftover local dev
    process.
  - Started a stable `kubectl -n istio-system port-forward svc/prometheus
    19090:9090` in the background (the same pattern `hack/query-prom.sh`
    already uses) and ran the operator locally
    (`PROMETHEUS_URL=http://127.0.0.1:19090 go run ./cmd/main.go`) against
    the real cluster on this branch's code.
  - Ran `hack/run-k6-demo.sh retry-storm` as an in-cluster Job. The
    operator **organically detected and mitigated a real RetryStorm trip**
    on `payments-service` (`dest_source_ratio=3.334` vs threshold `3`,
    confidence `0.556`), logging `patched VirtualService retries.attempts`
    and `patched DestinationRule connectionPool.http (retry storm
    secondary)` — the exact reviewed code path, not a hand-constructed
    patch. It restored cleanly ~20s later once the ratio fell back below
    threshold. The only error in the whole run was an unrelated,
    transient resource-version conflict on `fanout_mitigate.go`'s typed
    `Update()` for `inventory-service` (optimistic-concurrency retry,
    self-resolved on the next reconcile, pre-existing behavior untouched
    by this slice).
  - Separately, to catch the value *at* Envoy (the restore ramp completes
    fast enough that I didn't catch the trip mid-flight through `kubectl`),
    applied the exact production merge-patch bytes
    (`maxRetries: 1`) to `payments-service`'s clean `DestinationRule`
    directly and re-checked `checkout-service`'s Envoy `config_dump`
    myself: `outbound|80||payments-service...`'s
    `circuit_breakers.thresholds[0].max_retries` is **`1`** — independently
    reproducing Cursor's central claim, on a different host
    (`payments-service`, not `inventory-service`) than Cursor checked, for
    additional confidence this isn't a fixture-specific fluke.
  - Cleanup: restored `payments-service`'s `DestinationRule` to its clean
    fixture (`kubectl apply` plus an explicit `trafficPolicy` removal,
    since `kubectl apply`'s three-way merge doesn't clear fields absent
    from both the fixture and the last-applied annotation — the same
    gotcha noted in the previous slice's review). Confirmed both
    `payments-service` and `inventory-service` DestinationRules are back
    to `{host: ...}` only. Stopped the local operator and the port-forward.
    Removed the scratch config-dump files. The k6 Job/ConfigMap
    self-cleaned via its own `trap ... EXIT` once the run finished.

## Verdict
**Approved and committed.** The code change is exactly what the resolved
proposal called for, the restore-ramp math is correct (independently
re-derived, not just read), and the test seeding changes are load-bearing,
not decorative. The live-verification gap Cursor's own worklog was honest
about — no organic trip, only hand-applied patch bytes — is now closed: a
real detect→mitigate→restore cycle ran against this exact code on a live
cluster, and Envoy's rendered `max_retries: 1` was independently confirmed
on a second host as well.

## Files touched
- `docs/worklog/2026-08-30-review-retry-storm-maxretries-one.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — full independent re-verification, including the organic
trip Cursor's session couldn't complete.

## Follow-ups / known gaps
- The stray-process-on-8081 gotcha is now the second time this exact
  failure mode has shown up in this project (also seen earlier in this
  session's history) — worth remembering: `go run` leaves a compiled
  binary running under its own PID tree even after the shell that started
  it moves on, and it will silently blackhole a fresh manager's health
  probe port. Not worth a code fix; a note for whoever runs the operator
  locally next.
- Restoration of a captured *zero* original at the final step still goes
  through typed `Update()` and would strip a legitimate `maxRetries: 0`
  the same way as the original serialization bug — Cursor flagged this as
  out of scope and I agree it's low-priority (needs a user-authored policy
  that already had retries fully disabled), but it's the same shape of bug
  as everything else in this multi-slice thread and shouldn't be forgotten
  indefinitely.
- Kind-based integration test asserting Envoy's actual rendered
  `max_retries` post-trip remains the right long-term fix for this whole
  class of gap — both this slice and the previous one relied on manual
  live checks precisely because nothing in CI exercises the real Envoy
  admin API today.
