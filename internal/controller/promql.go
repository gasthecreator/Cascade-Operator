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

func latencyP99Query(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`histogram_quantile(0.99, rate(istio_request_duration_milliseconds_bucket{destination_service=%q}[%ds]))`,
		host, windowSeconds,
	)
}

func errorRateQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`rate(istio_requests_total{destination_service=%q,response_code=~"5.."}[%ds]) / rate(istio_requests_total{destination_service=%q}[%ds])`,
		host, windowSeconds, host, windowSeconds,
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
