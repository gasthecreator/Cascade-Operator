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

package controller

import (
	"fmt"

	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

// latencyP99Query is the client-perceived (reporter=source) p99, aggregated
// across all remaining labels via sum by (le) before histogram_quantile.
// Without that, Prometheus returns one series per reporter/response_code/etc.
// (confirmed on a live Istio 1.30.4 scrape — see PROPOSALS.md's resolved
// "sum by (le)" entry), and taking source+destination together would double
// count the same request instead of picking one consistent view of it.
func latencyP99Query(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`histogram_quantile(0.99, sum by (le) (rate(istio_request_duration_milliseconds_bucket{destination_service=%q,reporter="source"}[%ds])))`,
		host, windowSeconds,
	)
}

func errorRateQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`rate(istio_requests_total{destination_service=%q,response_code=~"5.."}[%ds]) / rate(istio_requests_total{destination_service=%q}[%ds])`,
		host, windowSeconds, host, windowSeconds,
	)
}

// retryStormRatioQuery is dest-reporter request rate over source-reporter
// request rate. Implicit baseline is 1 (no retries). A live Istio 1.30.4
// scrape with retries.attempts:3 produced dest:source = 4 (140 dest 503s /
// 35 source URX) — URX only fires when every retry fails, so the ratio is
// the storm signal, not a URX rate (see PLAN.md §2.4).
func retryStormRatioQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(istio_requests_total{destination_service=%q,reporter="destination"}[%ds])) / sum(rate(istio_requests_total{destination_service=%q,reporter="source"}[%ds]))`,
		host, windowSeconds, host, windowSeconds,
	)
}

// fanOutRatioQuery is the dependency's request rate over the caller's own
// (spec.Service) request rate — cross-host, unlike retryStormRatioQuery's
// same-host reporter split. Both sides use reporter="destination" (what
// actually arrived at each service), since that is exactly what the fan-out
// demo topology's live scrape measured: a healthy checkout -> {payments,
// inventory} run held exactly 1:1:1 using this reporter on both sides (see
// the fan-out-demo-evidence worklog). Implicit baseline is 1, same pattern
// as retryStormRatioQuery.
func fanOutRatioQuery(dependencyHost, callerHost string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(istio_requests_total{destination_service=%q,reporter="destination"}[%ds])) / sum(rate(istio_requests_total{destination_service=%q,reporter="destination"}[%ds]))`,
		dependencyHost, windowSeconds, callerHost, windowSeconds,
	)
}

// snapshotMax returns the largest sample value. Multiple series (per reporter /
// source) are collapsed conservatively: a cascade on any remaining label set
// is enough to evaluate the detector. Empty snapshots are "no reading".
func snapshotMax(s metrics.Snapshot) (float64, bool) {
	if len(s.Samples) == 0 {
		return 0, false
	}
	max := s.Samples[0].Value
	for _, sample := range s.Samples[1:] {
		if sample.Value > max {
			max = sample.Value
		}
	}
	return max, true
}

func windowOrDefault(seconds int32) int32 {
	if seconds <= 0 {
		return 30
	}
	return seconds
}
