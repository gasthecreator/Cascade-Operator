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

package metrics

import (
	"context"
	"time"
)

// Querier evaluates PromQL against Prometheus. The detector package depends
// only on Snapshot/Sample — never on this interface or on net/http.
type Querier interface {
	Query(ctx context.Context, promql string) (Snapshot, error)
}

// Snapshot is one instant-query evaluation. Prometheus owns any range-vector
// window inside the PromQL (e.g. rate(x[30s])); this is not a local ring buffer.
type Snapshot struct {
	Samples []Sample
}

// Sample is one series at the evaluation timestamp.
type Sample struct {
	Labels    map[string]string
	Value     float64
	Timestamp time.Time
}
