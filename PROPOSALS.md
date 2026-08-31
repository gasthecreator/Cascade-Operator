# PROPOSALS.md — Architecture & Process Change Requests

## Protocol (Cursor and Claude both follow this — read before editing anything)

- **Never edit `PLAN.md` directly** to change an architecture decision, the
  CRD shape, the language/tooling choice, the mitigation strategy, or anything
  else in PLAN.md section 2 ("Architecture Decisions") or section 4
  ("Open Questions"). If, while building, you (Cursor) hit a reason to change
  or resolve one of those, add a new entry below under **Pending Proposals**
  instead of editing PLAN.md.
- Gideon brings this file to Claude for review in a separate session. Claude
  evaluates the proposal against PLAN.md and the project's goals, then does
  exactly one of:
  - **Approves** — updates PLAN.md itself, moves the entry to
    **Resolved Proposals** marked `APPROVED`, with a one-line note on what
    changed in PLAN.md and where.
  - **Rejects** — moves the entry to **Resolved Proposals** marked
    `REJECTED`, with reasoning, and PLAN.md stays as-is.
  - **Needs discussion** — leaves it in Pending, adds a `Claude's question:`
    line under it; Gideon relays the answer back to Cursor, which updates the
    same entry rather than opening a new one.
- This file is a proposal *queue*, not a history of what was built — that's
  `docs/worklog/`.
- Routine implementation choices that don't contradict or extend a decision
  already in PLAN.md (variable/function naming, which helper to extract,
  normal refactors within an already-decided approach) do **not** need a
  proposal. This file is for things that change what PLAN.md says, not how a
  decision already made in PLAN.md gets carried out.

## Template — copy this for a new proposal

```
### [PENDING] <short title>
**Proposed by:** Cursor
**Date:** YYYY-MM-DD
**Affects:** <PLAN.md section, e.g. "2.3 CRD shape" or "Open Question #2">

**Current state:** what PLAN.md says now (quote or summarize the relevant part).

**Proposed change:** what you want it to say instead.

**Why:** the concrete thing you ran into while building that makes the
current plan wrong, incomplete, or worth revisiting. Cite the actual
constraint, error, API limitation, or test result — not a general preference.

**Impact if approved:** what else in the codebase or plan this touches
(files, other open questions, checklist items).
```

---

## Pending Proposals

### [PENDING] `connectionPool.http.http1MaxPendingRequests` is named by both fan-out's primary and retry storm's secondary
**Proposed by:** Cursor
**Date:** 2026-08-30
**Affects:** 2.6 Mitigation (retry-storm secondary; fan-out primary field set)

**Current state:** The locked §2.6 matrix names both of these, independently, on the same `DestinationRule` `connectionPool.http` sub-message:

- Fan-out amplification primary: `http1MaxPendingRequests`, `http2MaxRequests`
- Retry storm secondary: `maxRetries`, `http1MaxPendingRequests`

Every prior shared-object case in this project was **disjoint fields on a shared object kind** (`outlierDetection` vs `connectionPool.http`; `retries.attempts` vs `timeout`). This is the first time two signatures' own fields are the **same proto field**. PLAN.md does not say what happens then — the matrix lists both claims, the handoff note talks about disjoint field sets, and nothing addresses a same-field collision.

**Proposed change:** Not proposing a specific fix yet — flagging the overlap for a decision, same as the signature-handoff and retry-storm-webhook proposals. Two directions:

1. **Keep both signatures claiming `http1MaxPendingRequests`**, i.e. implement the matrix as currently written. Under the existing one-active-signature model plus synchronous force-complete-on-handoff, the two never apply at the same time: the outgoing signature is restored to its true original (including this field) *before* the incoming signature captures its own baseline and writes its trip value. Fake-client tests for both handoff directions (`internal/controller/retry_connpool_handoff_test.go`) confirm that ordering: the incoming signature's captured original is the true pre-any-trip value (64), not the outgoing signature's leftover trip/interpolated value. What this direction does *not* have, that every prior sharing case did, is field-level disjointness as a second line of defense — if force-complete ever failed to run, the incoming signature would snapshot the outgoing one's trip value as "original" and restore to it later, silently losing the user's number. For disjoint fields that failure mode doesn't exist: capturing "current" still gets *your* field's true original because the other signature never wrote it.

2. **Drop `http1MaxPendingRequests` from retry storm's secondary**, leaving only `maxRetries`. That makes the two signatures' `connectionPool.http` fields disjoint (`maxRetries` vs `http1MaxPendingRequests`/`http2MaxRequests`) — the same shape every other sharing case in this project already has — and `maxRetries` is the retry-specific circuit breaker (Envoy's `max_retries`, default 3), which is the part of this secondary that actually backs the primary's `retries.attempts → 0` rather than duplicating fan-out's general request bulkhead. Fan-out's primary is left untouched (this slice is scoped not to change any other signature's mitigation). The cost: retry storm no longer bulkheads non-retry pending volume on the dependency, which is a real (if secondary) part of what §2.6 currently names.

Not on the table: dropping the field from fan-out instead, or inventing a split (retry storm writes it only when fan-out isn't the last signature, etc.) — both would be changes to a different signature, out of this slice's scope, or a new cross-signature coupling the codebase has spent the last several slices avoiding.

**Why:** Found while building retry storm's last unbuilt patch cell, not hypothetically. Implementing the matrix as written means `ApplyRetryStormConnectionPoolTrip` and `ApplyFanOutConnectionPoolTrip` both assign `http.Http1MaxPendingRequests`. The own-annotation-keyed capture check (the defensive pattern every trip function now uses) is necessary and *not sufficient* for a shared field: it only stops you from overwriting *your own* already-captured baseline, it does not stop you from capturing the *other* signature's live trip value as that baseline if you run against an object it currently holds. Force-complete-on-handoff is what actually makes direction 1 correct at runtime; without it, this would be a real restore-to-wrong-original bug, not just an orphaned annotation. That is a stronger dependency on the handoff path than any previous sharing case had, and it is not something PLAN.md's existing text already answers.

Whether two signatures would only ever plausibly trip on the same edge in practice is the wrong framing to dismiss this with. Checkout's live k6 runs have already produced an unscripted latency/error ↔ fan-out handoff on `payments-service` in a single episode (see the timeout-secondary worklog). Retry storm and fan-out are also co-plausible on a host that's both retry-amplifying and call-count-amplifying — `TestRetryStormWinsWhenRetryStormAndFanOutCouldTrip` exists specifically because both detectors can return true on the same tick. The one-active-signature model plus detector priority means they don't *apply* together, but they *do* hand off, which is exactly when a shared field's capture is load-bearing on force-complete.

**Impact if approved:** Direction 1 is what this slice implements, so that the secondary can land against the matrix as currently written rather than silently rewriting it. If Claude takes direction 1, PLAN.md §2.6 should record that two signatures may claim the same field and that force-complete-on-handoff is load-bearing for that, not just for orphaned annotations. If Claude takes direction 2, this slice's `TripRetryStormMaxPendingRequests` writes, its original-snapshot JSON field, and the restore interpolation for it come out; `maxRetries` stays; fan-out is untouched. Either direction is a small, localized change — the reason this is a proposal is that picking one in code and writing the conclusion into PLAN.md is the exact pattern the last slice got flagged for.

This slice does **not** edit PLAN.md §2 to lock either direction. Implementation follows the matrix as currently written (direction 1 in code) so there is something to review against; that is not a claim that direction 1 is decided.

---

## Resolved Proposals

### [APPROVED] Retry-storm mitigation's `attempts: 0` trip is rejected by Istio's validating webhook whenever the route already has `retryOn`/`perTryTimeout` set
**Proposed by:** Cursor
**Date:** 2026-08-30
**Affects:** 2.6 Mitigation (retry-storm primary); `internal/mitigation/retries.go`'s `ApplyRetryStormTrip`

**Current state:** `ApplyRetryStormTrip` sets `route.Retries.Attempts = 0` on trip and, by design (per its own doc comment), leaves a route's pre-existing `retryOn`/`perTryTimeout`/`backoff` untouched — "if a route already had an explicit retries block, its retryOn/perTryTimeout/backoff are preserved and only attempts changes." This was written and unit-tested (fake client, no admission webhook) while the Kind cluster was unreachable (`2026-08-30-retry-storm-mitigation.md`'s own "Follow-ups" explicitly flagged this as unverified: "Worth a live check once the cluster is healthy again").

**Proposed change:** Not proposing a specific fix yet — flagging the gap for a decision, same as the signature-handoff proposal was. Two directions:
1. When tripping (`attempts → 0`), also clear `retryOn`/`perTryTimeout`/`backoff` on that route. They're already captured in `AnnotationOriginalRetries` for restoration, so clearing them on trip loses nothing — the restore path already restores the *whole* original block, not just `attempts`, per `originalRouteRetriesJSON`'s existing shape.
2. Use `attempts: 1` instead of `attempts: 0` as the trip value. The existing doc comment explicitly rejected this ("1 would still let a request through a second time — under the dest:source ratio detector, a 2x amplification can itself sit at or above a low `retryStormMultiplier`, so it would not reliably stop the storm") — that reasoning was sound at the time but never checked against whether Istio's webhook has the same objection to `attempts: 1` with `retryOn` set (it likely does not — the rejection message is specific to `attempts: 0`, i.e. "disabled," not to "attempts is low"). If `attempts: 1` is webhook-clean, it might be the smaller change, at the cost of the amplification-headroom concern already on record.

**Why:** Found live, during this slice's `retry-storm.js` k6 run against `demo/k8s/inventory-retry-vs.yaml` (`attempts: 3, retryOn: 5xx,reset,connect-failure,refused-stream, perTryTimeout: 2s` — a realistic policy, not a contrived edge case). `RetryStorm` tripped correctly (`dest_source_ratio≈4` against threshold `3`, confidence 1.0 — detector and status transition both confirmed working), but every reconcile's mitigation call failed:

```
update VirtualService default/inventory-service: admission webhook "validation.istio.io" denied
the request: configuration is invalid: http retry policy configured when attempts are set to 0
(disabled)
```

The `VirtualService` was never actually patched — `kubectl get virtualservice inventory-service` after the run still shows `attempts: 3`, no `cascade.gideonsanni.dev/managed-by` annotation. Once the induced failure cleared, restoration's "no managed object" fallback correctly snapped the policy back to `Normal` without erroring further, so this doesn't hang the demo or corrupt state — but the mitigation itself has never worked against a route that already has `retryOn` set, which is realistically the *only* shape a real retry storm's pre-existing policy would ever have (a retry storm requires some retry policy already lenient enough to amplify traffic — that's what `retryOn` being set *is*). Full evidence: `docs/worklog/2026-08-30-k6-cascade-simulation.md`.

**Impact if approved:** `internal/mitigation/retries.go`'s `ApplyRetryStormTrip` (and its existing fake-client unit tests, which did not catch this because they never exercise a real admission webhook). No CRD change either direction. Direction 1 is a small, mechanical change with no semantic trade-off. Direction 2 revisits an already-reasoned trip-value decision and needs a live webhook check against `attempts: 1` + `retryOn` before it can be trusted either way.

**Resolved by Claude, 2026-08-30: APPROVED — direction 1, not direction 2.** Independently reproduced both directions live against this exact fixture before deciding, not just read the report: applying `attempts: 0` with `retryOn`/`perTryTimeout` still set reproduced the identical rejection verbatim; clearing `retryOn`/`perTryTimeout` alongside `attempts: 0` was accepted by the webhook with no error; `attempts: 1` with `retryOn`/`perTryTimeout` still set was *also* accepted (so direction 2 is technically viable too, contrary to no evidence either way at proposal time). Went with direction 1 anyway: it's semantically cleaner (a route whose retries are disabled shouldn't carry retry-behavior fields describing retries that will never execute — that's not a workaround for the webhook, the webhook is enforcing something that was already true), it loses nothing (the full original block is already captured in `AnnotationOriginalRetries` and restored exactly at completion), and it doesn't reopen the already-reasoned "0, not 1" amplification-headroom decision on record in `outlier.go`'s sibling comment. Implementation is the next slice.

---

### [APPROVED] Signature handoff on a shared DestinationRule can orphan the outgoing signature's fields
**Proposed by:** Cursor
**Date:** 2026-08-30
**Affects:** 2.6 Mitigation (restoration state machine); `status.LastSignature`'s single-active-signature model

**Current state:** Restoration dispatches purely by `status.LastSignature` (`internal/controller/restore.go`). `Reconcile` re-runs `detectSignatures` on every tick regardless of current phase, and immediately adopts whatever signature trips that tick — including a *different* signature than the one currently tracked, with no requirement to first drive the outgoing signature's restore ramp to completion. Latency/error-cascade and fan-out amplification both patch `DestinationRule` (disjoint field sets: `outlierDetection` vs. `connectionPool.http`), so this slice is the first time two signatures can legitimately manage the exact same object.

**Proposed change:** Not proposing a specific fix yet — flagging the gap for a decision. Two directions that came up while building this slice, neither picked:
1. Before `Reconcile` adopts a newly-tripped signature that differs from `status.LastSignature` while `Phase` is `Tripped`/`Restoring`, force the outgoing signature's restore path to run to completion first (on the *same* tick or over a few extra ticks), so its fields and its own `original-*` annotation are never left dangling.
2. Accept the current one-active-signature model's limits here and instead make each signature's *trip* function defensively clean up the *other* signature's leftover state if it finds it on the same object — e.g. fan-out's trip function checks for a stale `original-outlier-detection` annotation with no corresponding active `LastSignature` and restores it to original before proceeding. This avoids a status-shape change but pushes cross-signature awareness into the trip path instead of the restore path, which cuts against how cleanly separated the two are today.

**Why:** Found while writing this slice's cross-signature dispatch tests. Concretely reachable: latency/error-cascade trips on host X (managed-by set, `outlierDetection` at trip values, `original-outlier-detection` captured), and on a later tick — while still `Tripped` or `Restoring`, i.e. before latency/error ever heals — that same host's fan-out ratio also crosses its threshold. `detectSignatures` returns `FanOutAmplification` for that tick (latency/error no longer trips, fan-out now does), `Reconcile` sets `LastSignature = FanOutAmplification` and mitigates via `applyFanOutMitigation`. Fixed as part of this slice: `ApplyFanOutConnectionPoolTrip`'s own-baseline capture is now keyed off `AnnotationOriginalConnectionPool`'s presence rather than the shared `managed-by` flag, so fan-out still correctly captures its own connectionPool.http baseline in this scenario (same fix applied to `ApplyLatencyErrorOutlierTrip`, in case the order reverses) — that part is tested (`TestApplyFanOutConnectionPoolTripLeavesOutlierDetectionAlone`, `internal/mitigation`). Not fixed: latency/error's own trip-time `outlierDetection` values and its `original-outlier-detection` annotation are left exactly as they were the moment the handoff happened, and no future tick is pointed back at restoring or cleaning them up — `beginRestore`/`advanceRestore` now dispatch exclusively to the fan-out path for that policy, which never reads or writes `outlierDetection` at all (by design — see `fanout_restore.go`'s doc comment). If fan-out later fully restores and its annotations are stripped, the object is left with a live, non-default `outlierDetection` configuration and no annotation pointing at what it should be, until/unless latency/error independently trips on that same host again.

This is a narrower case than it might look: it requires the *same* policy's *same* host to flip which detector trips without an intervening healthy tick, which real traffic patterns could produce (e.g. a payments outage that starts as a latency spike and later also crosses the fan-out ratio once retries kick in) but is not the common path. Retry storm is unaffected — its object kind (`VirtualService`) is never shared with either DestinationRule-based signature.

**Impact if approved:** Depends on direction. Option 1 likely touches `Reconcile`'s phase-transition logic and possibly needs a small status-shape addition (e.g. tracking that a restore-before-handoff is in progress) — a CRD change, which is why this is flagged here rather than built silently. Option 2 stays CRD-neutral but adds cross-signature read logic to each trip function, which the current design has deliberately kept signature-scoped.

**Resolved by Claude, 2026-08-30: APPROVED — a refinement of option 1, no CRD change needed.** Neither option as written is quite right: option 1 as described implies a multi-tick handoff state machine (status-shape addition), and option 2 pushes cross-signature awareness into the trip path, cutting against the clean signature-scoping the codebase has maintained throughout. The actual fix is simpler than either: when `Reconcile` is about to adopt a signature different from `status.LastSignature` while `Phase != Normal`, **synchronously force-complete the outgoing signature's restore first, in the same reconcile call, before applying the new signature's trip** — by calling that signature's *existing* `complete*Restore` controller function (`completeLatencyErrorRestore`, `completeRetryStormRestore`, or the fan-out equivalent), the same function each restore ramp already calls at its final step. This isn't new logic, just a new call site for logic that already exists and is already tested: "restore to true original, strip both annotations" is exactly what completing a restore ramp already means, and doing it eagerly on handoff instead of only reaching it via the gradual ramp is safe specifically *because* the outgoing signature's own detector just confirmed, this same tick, that its condition is no longer present — the gradual ramp's per-step re-verification exists to catch a regression before committing further, and there's nothing to catch here since we're not continuing the old signature's ramp, we're closing it out. No new status field, no multi-tick bookkeeping. Implementation is the next slice — see the follow-up prompt.

---

### [APPROVED] Add `reporter="source"` and `sum by (le)` to the latency p99 PromQL
**Proposed by:** Cursor
**Date:** 2026-08-29
**Affects:** 2.4 Metrics (query construction in `internal/controller/promql.go`; already reviewed)

**Current state:** `latencyP99Query` is
`histogram_quantile(0.99, rate(istio_request_duration_milliseconds_bucket{destination_service=%q}[%ds]))`
with no `reporter` filter and no `sum by (le)`. The reconciler takes `max` if Prometheus returns several samples.

**Proposed change:** Change the p99 query to a single service-level client quantile:

```
histogram_quantile(0.99, sum by (le) (
  rate(istio_request_duration_milliseconds_bucket{destination_service=%q,reporter="source"}[%ds])
))
```

Leave the error-rate query for a follow-up unless we want the same `reporter="source"` filter there too (it has the same source/destination split).

**Why:** Live scrape on Kind + Istio 1.30.4 (sleep → httpbin, exact current PromQL):

- Without `sum by (le)` the instant vector is **two series**, not one: `reporter=source` p99 ≈ 23.95ms and `reporter=destination` p99 ≈ 9.3ms. Each side has a complete 20-bucket histogram (`le` from 0.5 to +Inf). This is not a mixed-bucket garbage number — `histogram_quantile` groups by every label except `le` — but it is also not the one service-level reading the detector was written against.
- `sum by (le)` across **both** reporters returned one value (~22.9ms). That double-counts the same request (client and server each record it). Do not do that.
- Restricting to `reporter="source"` then `sum by (le)` is the client-perceived latency (what a cascade actually feels like upstream) and collapses leftover labels (`response_code`, pod, …). After we induced 503s, the unaggregated query split further: one series per `response_code` (200 vs 503). `snapshotMax` would then pick the slower code class rather than a combined p99.

**Impact if approved:** One-line change to `latencyP99Query` plus the exact-string test in `promql_test.go`. The `snapshotMax` fallback can stay for other detectors. No CRD change.

**Resolved by Claude, 2026-08-29: APPROVED and applied.** Independently reproduced on the same live cluster before approving — generated fresh sleep→httpbin traffic, ran both the unaggregated and proposed queries myself, and got the same shape (multiple series unaggregated; one clean value with `reporter="source"` + `sum by (le)`). Updated `latencyP99Query` in `internal/controller/promql.go` and its exact-string test in `promql_test.go`; `go build`/`gofmt`/`make test`/`make lint` all confirmed clean afterward. Left the error-rate query as a known, lower-priority follow-up (not decided here) — a rate *ratio*'s double-counting across reporters is less likely to be wrong than a quantile's, since both numerator and denominator would inflate by the same factor, but that's reasoning, not evidence, so it stays open rather than being assumed fixed or assumed fine.

---

### [APPROVED] Retry signal is `response_flags=URX`, not `UR`
**Proposed by:** Cursor
**Date:** 2026-08-29
**Affects:** 2.4 Metrics (`response_flags=UR` assumption); future retry-storm detector

**Current state:** PLAN.md §2.4: "retries show up via `response_flags=UR` and related — must be validated against a real Istio scrape before the retry-storm detector is written."

**Proposed change:** Record in §2.4 that on Istio 1.30.4, exhausted retries (`VirtualService` `retries.attempts: 3` against a 503) show as **`response_flags=URX` on the source reporter**, not `UR`. Destination reports each attempt as a normal 503 with `response_flags="-"`. A cluster-wide query for `response_flags=~".*UR.*"` on this scrape returned only `URX`. The retry-storm detector should not filter `UR` alone.

**Why:** Kind scrape, sleep → httpbin `/status/503`, retry VS with `attempts: 3`. Source `istio_requests_total` 503/URX count = 35; destination 503/`-` count = 140 = 35 × 4 (one try + three retries). `UR` never appeared, including a follow-up 50% abort + retry experiment (successes were 200 with `response_flags="-"`). Envoy's overflow flag is URX; `UR` may still exist for a retry that later succeeds, but this mesh did not produce it.

**Impact if approved:** PLAN.md §2.4 wording only until the retry-storm detector is written. That detector should start from `URX` and/or the destination:source request ratio, not copy `UR` from the original guess.

**Resolved by Claude, 2026-08-29: APPROVED as written, on the strength of the evidence rather than re-running the experiment myself.** The arithmetic is internally consistent (35 `URX` × 4 = 140 total 503s, exactly matching a `retries.attempts: 3` policy — one original try plus three retries), matches Envoy's documented `URX` ("upstream retry limit exceeded") semantics, and nothing in the codebase depends on this yet since the retry-storm detector doesn't exist. Updated PLAN.md §2.4's wording to reflect `URX`, not `UR`.

---

### [APPROVED] Lock CRD as CascadePolicy / cascade.gideonsanni.dev/v1alpha1
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #1; 2.3 CRD shape

**Current state:** `CascadePolicy` under `cascade.gideonsanni.dev/v1alpha1` is a placeholder. PLAN.md says confirm before kubebuilder scaffolding because renaming after codegen means regenerating deepcopy and CRD YAML. The draft spec also has a single `targetVirtualService` on the policy, named after the *protected* service (e.g. `checkout-service`).

**Proposed change:**
- Lock kind `CascadePolicy`, group `cascade.gideonsanni.dev`, version `v1alpha1`.
- Namespaced (not cluster-scoped).
- Drop `spec.targetVirtualService`. The operator resolves Istio objects from each `dependsOn` host by the usual one-DestinationRule-per-host / one-VirtualService-per-host convention (object name = Kubernetes Service name, same namespace as the Service). If a referenced DestinationRule or VirtualService is missing, set a status condition and do not patch anything else for that edge.
- Keep `spec.service` + `spec.dependsOn` as FQDNs. Thresholds stay policy-wide for v1alpha1 (no per-edge overrides).
- Optional, still in this CRD lock: add `spec.mode: DetectOnly | Mitigate` (default `Mitigate`) so a demo can show detection without mutating mesh config.

**Why:** Open Question #1 is explicitly blocking for `kubebuilder init` / `create api`. `CascadePolicy` is the right kind: the CR is a desired constraint the controller enforces, not the breaker implementation. `CircuitBreakerPolicy` would collide with Istio's own vocabulary and muddy interview conversations. The draft's single `targetVirtualService: checkout-service` points at the *caller*, but the objects we actually patch are the *callee's* DestinationRule / VirtualService (outlier detection, retry budget, connection pool, timeout all hang off the destination host). Encoding that as a convention rather than a required field keeps the CRD honest and avoids a field we would immediately misuse. `DetectOnly` is the answer to "what if the patch is wrong during a demo."

**Impact if approved:** Unblocks repo scaffold and CRD types. Mitigation layer (checklist item "Istio patch layer") looks up objects per `dependsOn` instead of reading `targetVirtualService`. Status needs a condition type for missing Istio objects. If Gideon does not want `gideonsanni.dev` as the API-group domain string, substitute before codegen — same kind/version, different group.

**Resolved by Claude, 2026-08-28: APPROVED as written**, including the optional `spec.mode` field. `gideonsanni.dev` confirmed as the API-group domain — no real DNS ownership needed for a CRD group string, and it's already used consistently. PLAN.md §2.3 rewritten with the locked group/kind, dropped `targetVirtualService`, added `spec.mode` and the `DependencyObjectMissing` status condition. This is the correct call: patching the dependency's Istio objects rather than the protected service's is not a style preference, it's the difference between the operator working and silently patching the wrong service.

---

### [APPROVED] Custom 3-service demo topology, not Bookinfo
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #2; 2.7 Local dev/test environment

**Current state:** "likely Istio's Bookinfo sample extended with a fault-injection sidecar endpoint, unless that proves too limited."

**Proposed change:** Do not use Bookinfo. Ship a minimal custom topology that matches the CRD example: `checkout` → `{payments, inventory}`, plus a tiny frontend/gateway if we need a single k6 entrypoint. Three small Go services, Dockerfiles, and Kubernetes+Istio manifests. Induce each signature with Istio fault injection and k6, not with extra sidecars. Defer building this until after one detect→mitigate loop exists; it is not part of the first scaffold slice.

**Why:** Bookinfo's graph (productpage → details/reviews → ratings) is built to demo Istio traffic splitting, not these three failure modes. Fan-out is not disproportionate (reviews→ratings is 1:1). Retry behavior is not a first-class, controllable client. Extending it with a custom fault-injection sidecar is the worst of both worlds: an implicit topology plus extra moving parts. A purpose-built graph is a cleaner interview story and makes "induce exactly this signature" a script, not a fight with sample-app defaults. Cost is bounded: ~50-line services, not a fourth product.

**Impact if approved:** Checklist item "Demo microservice topology" becomes a small in-repo app under something like `demo/`, not a Bookinfo overlay. k6 scripts target that graph. Kind+Istio docs install the custom manifests instead of `bookinfo.yaml`. No change to detection or mitigation code.

**Resolved by Claude, 2026-08-28: APPROVED as written.** PLAN.md §2.7 updated with the locked topology and the rejected-Bookinfo reasoning kept inline so a future reader doesn't re-litigate it. Sequencing note (build after one detect→mitigate loop) carried into §3's new build-order note rather than duplicated.

---

### [APPROVED] Per-signature Istio patch matrix; restore by loosening the same knobs
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #3; 2.6 Mitigation

**Current state:** On trip, patch DestinationRule (outlier detection / connection pool) *or* VirtualService (retry budget, timeout). Restoration example is a traffic-weight ramp (10% → 25% → 50% → 100%) *or* loosened outlier-detection thresholds. No per-signature mapping.

**Proposed change:** One primary (and one optional secondary) patch per signature. Restoration always ramps the *same fields we tightened*, never a separate weighted-route shed.

| Signature | Trip (primary) | Trip (secondary, same slice or immediately after) | Restore |
|---|---|---|---|
| Latency/error cascade | DestinationRule `outlierDetection`: lower `consecutive5xxErrors`, shorter `interval`, longer `baseEjectionTime` | VirtualService `timeout` on the dependency host, capped at `thresholds.latencyP99Ms` | Stepwise loosen those same fields |
| Retry storm | VirtualService `retries.attempts` → 0 or 1 | DestinationRule `connectionPool.http.maxRetries` and `http1MaxPendingRequests` | Stepwise raise attempts / pool limits |
| Fan-out amplification | DestinationRule `connectionPool.http` (`http1MaxPendingRequests`, `http2MaxRequests`) on the *downstream* host — bulkhead in-flight calls | none for v1alpha1 | Stepwise raise pool limits |

Every patch still carries `cascade.gideonsanni.dev/managed-by: cascade-operator`. Do not use VirtualService destination weights for restoration in v1alpha1.

**Why:** Open Question #3 is marked as required before the mitigation layer. The three signatures are different amplifiers, so they need different knobs:

- A latency/error *cascade* is service-level. Outlier detection is instance-scoped (eject bad pods); it is the project's stated gap vs. hand-tuned Istio circuit breaking, so it is the primary for this signature. A VirtualService timeout is the fail-fast that stops remaining latency from propagating up the chain if every pod is sick (outlier detection ejects 100% and you still need a timeout).
- A retry storm is a *policy* problem. DestinationRule outlier detection does not stop Envoy from retrying. Cutting `retries.attempts` is the direct counter; the connection-pool retry/pending caps are the bulkhead.
- Fan-out amplification is a *concurrency* problem. Connection-pool max pending / HTTP2 max requests on the callee is bulkheading; timeouts and outlier ejection do not reduce call count.

Traffic-weight restoration is a different pattern (load shedding / canary). It needs a dummy destination or an abort route, a second state machine, and it is easy to clobber user routing. Loosening the knobs we already annotated is one state machine (`Normal → Tripped → Restoring(step N) → Normal`) and matches the existing `restoreStep: 0-4` status field.

**Impact if approved:** Unblocks the Istio patch layer and restoration state machine. CRD no longer needs a single `targetVirtualService` (see CRD proposal). Restoration code has one ramp implementation parameterized by field set, not a weight table. k6 / demo assertions check the specific fields above. If Claude wants v1alpha1 even thinner: implement only the **primary** column for the first mitigation slice (latency → outlier detection), keep the matrix in PLAN.md as the contract for the other two.

**Resolved by Claude, 2026-08-28: APPROVED, and taking the thinner v1alpha1 option** — first mitigation slice implements only the latency/error cascade primary (outlier detection); the rest of the matrix is contract, not yet built. PLAN.md §2.6 rewritten with the full matrix and the reasoning per signature. The restore-by-loosening-the-same-knobs call over a traffic-weight ramp is the right simplification — one state machine instead of two, and it removes the risk of the operator's restoration logic fighting user-authored VirtualService routing.

---

### [APPROVED] Close custom-metrics-API vs. polling: keep direct Prometheus HTTP
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #4; 2.4 Metrics

**Current state:** Direct PromQL over the Prometheus HTTP API on each reconcile tick. Revisit only if reconcile-loop latency against Prometheus is a real bottleneck.

**Proposed change:** Close the question. Do not add a custom metrics adapter, Metrics APIService, or controller-runtime custom-metrics path. Operator-level config (flag/env) for Prometheus URL, not a per-policy field. Reconcile on CR watch events *and* `RequeueAfter` (implementation default: 10s) so polling happens without an extra timer controller.

**Why:** The custom-metrics API exists to feed HPA. Standing it up is another Deployment, an aggregation-layer APIService, and a security surface, for no interview-visible gain. PromQL (`histogram_quantile`, `rate(...[30s])`) *is* the talking point, and Prometheus already owns the 30-second window — detectors then take a snapshot struct, not a local ring buffer. Demo scale will not bottleneck on one HTTP query per policy per 10s. Closing it now prevents a mid-scaffold "should we adapter this" detour.

**Impact if approved:** Open Question #4 marked resolved in PLAN.md. Prometheus client in the first metrics slice is an HTTP client behind a narrow interface (`Query(ctx, PromQL) → snapshot`) so detectors stay dependency-free. No Kubernetes Metrics APIs in RBAC.

**Resolved by Claude, 2026-08-28: APPROVED as written.** This mostly confirmed the existing PLAN.md §2.4 decision; folded in the operator-level-config and watch+RequeueAfter(10s) detail. Noted directly in PLAN.md: the `response_flags=UR` retry-metric assumption needs validating against a real Istio scrape before the retry-storm detector is written — that's flagged as a Kind-cluster validation item, not something to guess at in the scaffold slice.

---

### [APPROVED] CI from commit one: lint + unit tests only (no Kind in Actions yet)
**Proposed by:** Cursor
**Date:** 2026-08-28
**Affects:** Open Question #5; checklist "golangci-lint + gofmt CI gate"

**Current state:** GitHub Actions for lint/test now (empty repo, cheap) vs. defer until there is code. PLAN.md leans toward now so gofmt/golangci-lint exist from commit one.

**Proposed change:** Add GitHub Actions on the first scaffold PR, running on PRs and on `main`:

1. `gofmt -l` must be empty
2. `golangci-lint` (version pinned)
3. `go test ./...`
4. `make manifests` / `make generate` drift check (generated CRD YAML and deepcopy match committed files)

Do **not** run Kind, Istio, or k6 in CI until the integration-test checklist item is actually being built. Pin Go (propose 1.24.x, matching current kubebuilder defaults unless `kubebuilder init` picks otherwise — follow the tool).

**Why:** The working agreement already requires gofmt and golangci-lint from the first commit; CI is how that is enforced after the laptop. Kind-in-CI is the expensive part (Istio install, image loads, flake). Putting that in an empty-repo workflow would slow every PR for no signal. A generate-drift check is worth having the moment CRD YAML is generated, because hand-edits to generated files are the usual kubebuilder footgun.

**Impact if approved:** First scaffold slice includes `.github/workflows/ci.yml`, `.golangci.yml`, and Makefile targets. Checklist items "Repo scaffold" and "golangci-lint + gofmt CI gate" complete together. Integration tests stay a later workflow file.

**Resolved by Claude, 2026-08-28: APPROVED as written.** PLAN.md gets a new §2.8 for this (it's a real architecture/process decision, same as the others, not just a checklist item). Go version: follow whatever `kubebuilder init` picks — don't pre-pin a version number that init might override.
