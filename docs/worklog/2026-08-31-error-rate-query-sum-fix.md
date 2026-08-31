# `ErrorRateQuery` missing `sum()` — false negatives/positives in the latency/error detector

**Date:** 2026-08-31
**Author:** Claude
**Type:** fix

## What
`istio.QueryBuilder.ErrorRateQuery` (`internal/mesh/istio/query_builder.go`,
moved verbatim from `internal/controller/promql.go` in the Phase 6.1
extraction) computed 5xx-rate over total-rate with no `sum()` wrapper on
either side — unlike `LatencyP99Query`'s `sum by (le)`, which PLAN.md §2.4
already documents as required. Fixed by wrapping both sides in `sum()`.

## Why
Without `sum()`, the division relies on Prometheus's default one-to-one
vector matching: a numerator series only divides against a denominator
series that shares its *entire* remaining label set, `response_code`
included. The denominator query has no `response_code` matcher in its
PromQL, but every series it returns still carries whatever `response_code`
that series' underlying samples actually had (200, 404, 500, 503, ...) — so
matching is not "5xx rate over all traffic," it's "each 5xx-coded series
divided by whichever all-traffic series happens to carry the identical
label set as it, including that same code."

Verified live against the dev Kind+Istio cluster's real traffic
(`hack/query-prom.sh`, ports 19092–19094 to avoid the concurrently-running
`hack/run-benchmark.sh` Phase 9 run's own port-forward on 19090):

- **Buggy query, live traffic with multiple response codes on both
  `reporter=source` and `reporter=destination`:** every returned series was
  `NaN` — no numerator/denominator pair shared an identical full label set
  (differing `reporter`, `pod`, `instance`, etc., not just `response_code`),
  so the match failed and division produced `NaN` rather than being dropped.
- **Correctly-summed query, same traffic:** a single clean series,
  `0.47355780532526764` — matching a manual `sum(...)/sum(...)` sanity
  query.
- **Buggy query at a later moment (30s window, traffic shifted to a
  different scenario):** still nonsensical, though not always `NaN` —
  confirming the failure mode is traffic-composition-dependent, not a fixed
  constant.

Tracing the effect through the rest of the pipeline:
- `snapshotMax` (`internal/controller/promql.go`) takes the first sample as
  its running max and only replaces it when a later sample compares
  greater — but `NaN` compares false against everything (Go's `>` on NaN is
  always false), so a `NaN`-led sample list returns `NaN` with `ok=true`
  (samples were present, just poisoned).
- `signatures.DetectLatencyError`'s `finite()` guard
  (`internal/signatures/latency_error.go`) then rejects a `NaN`
  `ErrorRateFraction` as "incomplete readings" — the detector silently
  never trips. **False negative**, the opposite of the failure mode
  originally hypothesized before live-testing.
- Separately, when live traffic happens to narrow to one dominant response
  code (as seen in an earlier benchmark run), the one-to-one match *does*
  succeed for that single code, producing a clean `rate/rate == 1.0` —
  satisfying `errorRateFraction >= errorRateThreshold` on the very first
  stray 5xx, ignoring `errorRateThreshold` entirely. **False positive.**

Both failure modes trace to the same root cause (no `sum()`), just
different live label cardinalities at query time — so the fix, not either
failure mode individually, is what needed regression coverage.

## How
- `internal/mesh/istio/query_builder.go`: wrapped both sides of
  `ErrorRateQuery` in `sum(...)`, matching `LatencyP99Query`'s existing
  `sum by (le)` pattern. Doc comment rewritten to record the live
  verification and both observed failure modes, not just "known issue."
- `internal/mesh/istio/query_builder_test.go`: `TestErrorRateQuery` switched
  from loose `strings.Contains` assertions (which the buggy, un-summed
  query already satisfied — they never checked for `sum()` and so could not
  have caught this) to an exact string match, same style already used for
  `TestLatencyP99Query`/`TestRetryStormRatioQuery`. Added
  `TestErrorRateQueryVectorMatchingRequiresSum`, which asserts both operands
  are `sum(rate(...))`, with a comment explaining why this needs its own
  test rather than relying on the exact-match test alone.

## Test-coverage masking, checked per this project's own retry-storm precedent
`test/integration/latency_error_test.go`'s `hostAwareQuerier`
(`test/integration/cluster.go`) is a fake `metrics.Querier` that pattern-matches
on substrings of the PromQL text and returns a canned scalar — it never
evaluates real Prometheus vector-matching semantics, so it cannot exercise
label-cardinality bugs like this one at all; any query text containing
neither `histogram_quantile` nor a `reporter=` label falls through to its
`default` case regardless of whether the query is correctly formed. This is
the same class of gap the retry-storm restore-completion bug thread
(`docs/worklog/2026-08-31-phase5-retry-storm-restore-zero-value.md`) found —
there, a typed-struct fake-client read was masking a wire-format loss that
only a raw-JSON apiserver read caught.

Unlike that case, though, this isn't fixable by making the integration
fake "more real" — a `metrics.Querier` stub is structurally incapable of
reproducing Prometheus's own vector-matching engine; the only thing that
can validate that is Prometheus itself. That's a structural limitation of
the fake, not a gap to close inside it (same conclusion the retry-storm
thread reached about the DestinationRule `omitempty` constraint — not
every masking finding is a bug in the mask). The coverage that actually
catches this class of bug is: (1) the query-builder unit test now pinning
the exact query text, since the bug lived in string construction, not
reconcile-loop logic, and (2) live verification against a real Prometheus,
done here via `hack/query-prom.sh` and captured in this worklog rather than
as an automated CI check — matching how PLAN.md §2.4's other two live-scrape
findings (`sum by (le)`, `response_flags=URX`) were verified and documented,
not added as live-cluster CI assertions either.

## Files touched
- `internal/mesh/istio/query_builder.go` — `ErrorRateQuery` fix + doc comment
- `internal/mesh/istio/query_builder_test.go` — exact-match test + new
  sum-wrapping regression test
- `PLAN.md` — §2.4, new paragraph after the `response_flags=URX` finding
- `docs/worklog/2026-08-31-error-rate-query-sum-fix.md` — this file

## Testing
- Live: `hack/query-prom.sh` (custom `PROM_LOCAL_PORT` to avoid the
  concurrently-running Phase 9 benchmark's own port-forward) — buggy query
  returns `NaN`/non-aggregate results, fixed query returns the same value
  as a manual `sum(...)/sum(...)` sanity query.
- `go build ./...`, `gofmt -l internal/mesh/istio/`, `go vet
  ./internal/mesh/istio/...` — clean.
- `go test ./internal/mesh/istio/... ./internal/controller/...
  ./internal/signatures/...` — pass.
- Did not run `make test-integration` or restart the operator: another
  session in this same working tree had `hack/run-benchmark.sh` actively
  running against the live dev cluster (its own operator process bound to
  `:18091`, a `kubectl port-forward` on `:19090`) for unrelated Phase 6–11
  work; avoided competing for those ports or the CascadePolicy object
  rather than risk corrupting its in-flight benchmark run.

## Follow-ups / known gaps
- No live-Prometheus CI check exists for this class of bug (see masking
  discussion above) — consistent with existing project precedent, not a
  new gap introduced here.
- Coordinate with the concurrent session (Phases 6–11) before this lands,
  since `internal/mesh/istio` is code that session just moved into place.
