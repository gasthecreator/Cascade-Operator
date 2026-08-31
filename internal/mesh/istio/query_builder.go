/*
Copyright 2026 Gideon Sanni.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

// Package istio implements mesh.QueryBuilder against Istio's standard
// istio_requests_total/istio_request_duration_milliseconds_bucket metrics.
// Moved verbatim from internal/controller/promql.go (PLAN.md §5 Phase 6.1)
// — this is a mechanical extraction, not a rewrite: the query shapes, the
// reasoning in each doc comment, and any known issue with them (see
// ErrorRateQuery's own comment) are carried over unchanged, not fixed as a
// side effect of this refactor.
package istio

import "fmt"

// QueryBuilder implements mesh.QueryBuilder for Istio. Stateless — every
// method is a pure fmt.Sprintf, so the zero value is always ready to use.
type QueryBuilder struct{}

// LatencyP99Query is the client-perceived (reporter=source) p99, aggregated
// across all remaining labels via sum by (le) before histogram_quantile.
// Without that, Prometheus returns one series per reporter/response_code/etc.
// (confirmed on a live Istio 1.30.4 scrape — see PROPOSALS.md's resolved
// "sum by (le)" entry), and taking source+destination together would double
// count the same request instead of picking one consistent view of it.
func (QueryBuilder) LatencyP99Query(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`histogram_quantile(0.99, sum by (le) (rate(istio_request_duration_milliseconds_bucket{destination_service=%q,reporter="source"}[%ds])))`,
		host, windowSeconds,
	)
}

// ErrorRateQuery is 5xx rate over total rate for a dependency, aggregated
// across all remaining labels via sum() on both sides before dividing —
// same reasoning as LatencyP99Query's sum by (le), applied here because
// this query has no le label to group by.
//
// Fixed 2026-08-31 (PLAN.md §2.4): the un-summed form — one rate() per
// side, divided directly — relied on Prometheus's default one-to-one
// vector matching, which pairs numerator and denominator series only when
// their full label sets (including response_code) are identical. Verified
// live against the dev cluster's real traffic (docs/worklog): depending on
// which/how many response_code series exist at query time, that produced
// either NaN (mismatched label sets on every pair — no match found,
// division on the unmatched leftovers yields NaN) or, when synthetic
// traffic narrowed to a single response_code, an exact 1.0 (that code's
// rate divided by its own identical-label rate on the "total" side) —
// never the true error fraction either way. A live sum()-wrapped query
// against the same traffic returned the correct aggregate (~0.47). The
// NaN case is silently swallowed as "incomplete readings" by
// signatures.DetectLatencyError's finite() check (false negative, cascade
// never detected); the 1.0 case satisfies errorRateFraction >= threshold
// on any single stray 5xx (false positive, ignores errorRateThreshold
// entirely). sum() removes the per-series label sets on both sides before
// the division, so there is exactly one series per side and no matching
// ambiguity.
func (QueryBuilder) ErrorRateQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(istio_requests_total{destination_service=%q,response_code=~"5.."}[%ds])) / sum(rate(istio_requests_total{destination_service=%q}[%ds]))`,
		host, windowSeconds, host, windowSeconds,
	)
}

// RetryStormRatioQuery is dest-reporter request rate over source-reporter
// request rate. Implicit baseline is 1 (no retries). A live Istio 1.30.4
// scrape with retries.attempts:3 produced dest:source = 4 (140 dest 503s /
// 35 source URX) — URX only fires when every retry fails, so the ratio is
// the storm signal, not a URX rate (see PLAN.md §2.4).
func (QueryBuilder) RetryStormRatioQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(istio_requests_total{destination_service=%q,reporter="destination"}[%ds])) / sum(rate(istio_requests_total{destination_service=%q,reporter="source"}[%ds]))`,
		host, windowSeconds, host, windowSeconds,
	)
}

// FanOutRatioQuery is the dependency's request rate over the caller's own
// (spec.Service) request rate — cross-host, unlike RetryStormRatioQuery's
// same-host reporter split. Both sides use reporter="destination" (what
// actually arrived at each service), since that is exactly what the fan-out
// demo topology's live scrape measured: a healthy checkout -> {payments,
// inventory} run held exactly 1:1:1 using this reporter on both sides (see
// the fan-out-demo-evidence worklog). Implicit baseline is 1, same pattern
// as RetryStormRatioQuery.
func (QueryBuilder) FanOutRatioQuery(dependencyHost, callerHost string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(istio_requests_total{destination_service=%q,reporter="destination"}[%ds])) / sum(rate(istio_requests_total{destination_service=%q,reporter="destination"}[%ds]))`,
		dependencyHost, windowSeconds, callerHost, windowSeconds,
	)
}
