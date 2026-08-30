# Review: Kind + Istio dev environment, PromQL/response_flags proposals

**Date:** 2026-08-29
**Author:** Claude
**Type:** docs, fix

## What
Reviewed `feat/kind-istio-dev-env` — the Istio install scripts, the sleep/
httpbin validation workload, and the two resulting PROPOSALS.md entries.
Independently reproduced the core evidence on the same live cluster before
approving, then applied the approved PromQL fix.

## Why
Same reviewer role as every slice, but this one had a different shape:
mostly infra scripts and an evidence-based proposal rather than pure Go
code, so "review" meant re-running the actual experiment, not just reading
a diff.

## How
- Confirmed on this machine's actual Kind cluster (not just reading the
  worklog) that Istio 1.30.4 (`istio-ingressgateway`, `istio-egressgateway`,
  `istiod`, `prometheus`) and the sleep/httpbin sample were genuinely
  running — `kubectl get pods -n istio-system` / `-n default`.
- Read `hack/install-istio.sh`, `hack/deploy-istio-samples.sh`, and
  `hack/query-prom.sh`: pinned version (1.30.4, not `latest`), idempotent
  (checks before re-downloading `istioctl` or re-labeling the namespace),
  explicit comments distinguishing this validation workload from the §2.7
  demo topology. `config/dev/httpbin-retry-vs.yaml` and `docs/dev-istio.md`
  both exist and match what the worklog claims.
- **Independently reproduced the core finding**, not just re-read it:
  generated a fresh burst of sleep→httpbin traffic myself
  (`kubectl exec ... curl`), then ran both the current (unaggregated) and
  proposed (`reporter="source"` + `sum by (le)`) queries against the live
  Prometheus myself via `hack/query-prom.sh`. Got the same shape Cursor
  reported: the unaggregated query returned multiple series split by
  `reporter` and `response_code` with materially different values
  (source/200 ≈ 247ms vs. destination/200 ≈ 92.5ms in my run — different
  absolute numbers than Cursor's sustained-traffic run, same qualitative
  finding), and the proposed query collapsed cleanly to one value. This
  wasn't a rubber-stamp approval — I had the same cluster available and used
  it.
- Checked `config/dev/`, `docs/dev-istio.md` for accuracy against the
  worklog's claims — all matched. Noted one very minor staleness: the
  example query in `docs/dev-istio.md` still shows the pre-fix PromQL shape
  as an illustration of methodology; not worth blocking on, flagged below.
- Evaluated the `response_flags=URX` finding on its own evidence rather than
  re-running the retry experiment (no `VirtualService`/`DestinationRule`
  existed in the cluster anymore — Cursor had cleaned up after testing).
  The reported arithmetic (35 `URX` × 4 = 140, matching `retries.attempts: 3`
  exactly) is internally consistent and matches Envoy's documented `URX`
  ("upstream retry limit exceeded") semantics from training knowledge.
  Approved without re-deriving it myself — lower risk than the PromQL
  change since no code depends on it yet, and the evidence was already
  tight.
- **Applied the approved PromQL fix directly** (small, well-scoped, and
  already independently verified live): updated `latencyP99Query` in
  `internal/controller/promql.go` to `reporter="source"` +
  `sum by (le)`, and its exact-string test in `promql_test.go`. Re-ran
  `go build`, `gofmt -l`, `make test` (all four packages' coverage
  unchanged except the trivially-touched query string), and `make lint`
  (0 issues) — all clean.
- Moved both proposals to Resolved in `PROPOSALS.md` with a `Resolved by
  Claude` note each, and corrected PLAN.md §2.4's wording (`URX` not `UR`,
  and documented the `sum by (le)` / `reporter="source"` requirement with
  the live-scrape evidence cited).

## Verdict
**Approved, with the PromQL fix applied.** The infra work is careful and
correctly scoped (pinned version, idempotent scripts, explicit "this is not
the demo topology" boundary respected). Both evidence-based proposals held
up under independent re-verification — one fully reproduced live by me, the
other accepted on the strength of its internally consistent arithmetic. This
is exactly what the PROPOSALS.md protocol is for: a real live-data question
resolved with real live-data evidence, not a guess either way.

## Files touched
- `internal/controller/promql.go` — `latencyP99Query` uses `reporter="source"` + `sum by (le)`
- `internal/controller/promql_test.go` — updated exact-string expectation
- `PROPOSALS.md` — both entries moved to Resolved, marked APPROVED
- `PLAN.md` — §2.4 wording corrected (`URX`, `sum by (le)` requirement)
- `docs/worklog/2026-08-29-review-kind-istio-dev-env.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — includes live independent reproduction on the actual
cluster, not just re-running Cursor's own script output.

## Follow-ups / known gaps
- `docs/dev-istio.md`'s example query still shows the pre-fix PromQL shape
  as a methodology illustration — cosmetic, worth a quick touch-up next time
  that file is edited, not urgent enough to block on.
- Error-rate query (`errorRateQuery`) still lacks a `reporter` filter —
  deliberately left open rather than assumed fine; a rate ratio's
  double-counting across reporters is less fragile than a quantile's
  (numerator and denominator would inflate by the same factor), but that's
  reasoning, not evidence. Worth its own quick live-scrape check before the
  retry-storm/fan-out detectors lean on it.
- Custom 3-service demo topology (§2.7) and k6 scripts are still next,
  now genuinely unblocked by real Istio evidence instead of guesses.
- Retry-storm detector has real groundwork (`URX`, the 4x multiplier
  relationship) but is still unbuilt.
