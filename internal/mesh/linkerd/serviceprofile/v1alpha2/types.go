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

package v1alpha2

import metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

// RequestMatch is a (small) subset of ServiceProfile's route-matching
// condition — only the two fields this project's demo topology's own
// fixtures ever set (method, pathRegex). Any/All/Not (full request-match
// composition) are omitted: this package only ever *reads* pre-existing
// routes to preserve them across a retryBudget patch, never authors match
// conditions itself.
type RequestMatch struct {
	Method    string `json:"method,omitempty"`
	PathRegex string `json:"pathRegex,omitempty"`
}

// RouteSpec is one spec.routes[] entry. ResponseClasses is intentionally
// omitted — this package never reads or writes response classification,
// only isRetryable (read-only, to log whether a route is actually eligible
// for the budget this Mitigator manages) and the identifying fields needed
// to round-trip a route unchanged through a typed Update().
type RouteSpec struct {
	Name        string       `json:"name"`
	Condition   RequestMatch `json:"condition,omitempty"`
	IsRetryable bool         `json:"isRetryable,omitempty"`
}

// RetryBudget is spec.retryBudget. All three fields are required whenever
// the containing pointer is non-nil (confirmed live via the installed
// CRD's OpenAPI schema: `"required": ["minRetriesPerSecond", "retryRatio",
// "ttl"]`) — deliberately no `omitempty` on RetryRatio/MinRetriesPerSecond
// so a trip-time value of exactly 0 (full suppression — see
// retrybudget.go) survives a plain typed client.Update() without the
// zero-value-vs-omitempty problem this project's Istio retry-storm
// migration had to work around with hand-built JSON Patch bytes: that
// problem was specific to the *vendored* istio.io/api protobuf types,
// which this project does not control; this struct is hand-written for
// exactly this Mitigator, so the tags are written to not need that
// workaround in the first place.
type RetryBudget struct {
	RetryRatio          float32 `json:"retryRatio"`
	MinRetriesPerSecond int32   `json:"minRetriesPerSecond"`
	TTL                 string  `json:"ttl"`
}

// ServiceProfileSpec is spec on a ServiceProfile object. DstOverrides and
// OpaquePorts (real fields on the live CRD) are omitted — out of scope for
// every signature this package's Mitigator implements.
type ServiceProfileSpec struct {
	// +optional
	Routes []RouteSpec `json:"routes,omitempty"`
	// +optional
	RetryBudget *RetryBudget `json:"retryBudget,omitempty"`
}

// ServiceProfile is Linkerd's linkerd.io/v1alpha2 ServiceProfile — see this
// package's own doc comment for why this is a hand-written subset, not a
// generated mirror of Linkerd's full schema.
//
// Deliberately not marked as a kubebuilder object root: that particular
// marker is also what controller-gen's own `crd` generator (this
// project's `make manifests` target) uses to discover CRD-root
// candidates, and this project does not own the ServiceProfile CRD — it
// is Linkerd's, already installed on the cluster this operator runs
// against. Marking this type as a CRD root would make `make manifests`/
// `make install` generate and try to install this package's own
// deliberately-partial schema (missing dstOverrides, responseClasses, and
// others — see this package's own doc comment) as if it were the real
// thing, right alongside or over Linkerd's own. DeepCopyObject is
// hand-written just below instead of controller-gen-generated;
// DeepCopyInto/DeepCopy for every type in this file still come from
// `make generate`, driven by this package's own object-generate marker in
// groupversion_info.go — that package-level marker alone does not trigger
// `crd` generation, only the object-root one (deliberately absent here)
// does.
type ServiceProfile struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ServiceProfileSpec `json:"spec,omitempty"`
}

// ServiceProfileList is a list of ServiceProfile — see ServiceProfile's own
// doc comment for why this also omits +kubebuilder:object:root=true.
type ServiceProfileList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`

	Items []ServiceProfile `json:"items"`
}
