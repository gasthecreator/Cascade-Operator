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

package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// PolicyMode is whether the operator may patch Istio objects for this policy.
// +kubebuilder:validation:Enum=DetectOnly;Mitigate
type PolicyMode string

const (
	// PolicyModeDetectOnly records signatures without mutating mesh config.
	PolicyModeDetectOnly PolicyMode = "DetectOnly"
	// PolicyModeMitigate applies the Istio patch matrix on a tripped signature.
	PolicyModeMitigate PolicyMode = "Mitigate"
)

// PolicyPhase is the per-policy state machine.
// +kubebuilder:validation:Enum=Normal;Tripped;Restoring
type PolicyPhase string

const (
	PolicyPhaseNormal    PolicyPhase = "Normal"
	PolicyPhaseTripped   PolicyPhase = "Tripped"
	PolicyPhaseRestoring PolicyPhase = "Restoring"
)

// MeshType selects which service mesh's queries/mitigation this policy
// uses (PLAN.md §5 Phase 6.2). Additive: existing v1alpha1 objects have no
// spec.mesh value, default to Istio (this project's only implementation
// until Phase 6's Linkerd backend lands — internal/mesh, internal/mesh/istio),
// and are completely unaffected by this field's existence.
// +kubebuilder:validation:Enum=Istio;Linkerd
type MeshType string

const (
	MeshIstio   MeshType = "Istio"
	MeshLinkerd MeshType = "Linkerd"
)

// SignatureType is a detected cascade-failure signature.
// +kubebuilder:validation:Enum=LatencyErrorCascade;RetryStorm;FanOutAmplification
type SignatureType string

const (
	SignatureLatencyErrorCascade SignatureType = "LatencyErrorCascade"
	SignatureRetryStorm          SignatureType = "RetryStorm"
	SignatureFanOutAmplification SignatureType = "FanOutAmplification"
)

// ConditionTypeDependencyObjectMissing is set when a dependsOn host has no
// DestinationRule or VirtualService to patch (resolved by Service-name convention).
const ConditionTypeDependencyObjectMissing = "DependencyObjectMissing"

// Thresholds are detection cutoffs, shared across all dependsOn edges in v1alpha1.
type Thresholds struct {
	// latencyP99Ms is the p99 latency in milliseconds that counts as a spike.
	// +optional
	// +kubebuilder:default=500
	// +kubebuilder:validation:Minimum=1
	LatencyP99Ms int32 `json:"latencyP99Ms,omitempty"`

	// errorRateFraction is the downstream error-rate (0–1) that counts as a rise.
	// +optional
	// +kubebuilder:default=0.05
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	ErrorRateFraction float64 `json:"errorRateFraction,omitempty"`

	// windowSeconds is the PromQL range-vector window. Prometheus owns this
	// window; detectors consume a snapshot, not a local ring buffer.
	// +optional
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	WindowSeconds int32 `json:"windowSeconds,omitempty"`

	// retryStormMultiplier is the destination:source request-count ratio that
	// counts as a storm. Implicit baseline is 1 (one destination attempt per
	// source request).
	// +optional
	// +kubebuilder:default=3
	// +kubebuilder:validation:Minimum=1
	RetryStormMultiplier float64 `json:"retryStormMultiplier,omitempty"`

	// fanOutMultiplier is downstream calls versus baseline per inbound call.
	// +optional
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	FanOutMultiplier float64 `json:"fanOutMultiplier,omitempty"`
}

// CascadePolicySpec defines the desired state of CascadePolicy.
type CascadePolicySpec struct {
	// service is the protected service FQDN (the caller in the dependency graph).
	// +kubebuilder:validation:MinLength=1
	// +required
	Service string `json:"service"`

	// dependsOn is the list of dependency service FQDNs. Istio objects to
	// patch are resolved from each host by convention (object name = Kubernetes
	// Service name, same namespace as that Service) — not named on this CR.
	// +kubebuilder:validation:MinItems=1
	// +kubebuilder:validation:items:MinLength=1
	// +listType=atomic
	// +required
	DependsOn []string `json:"dependsOn"`

	// thresholds are detection cutoffs applied to every dependsOn edge,
	// unless a specific edge has a thresholdOverrides entry.
	// +required
	Thresholds Thresholds `json:"thresholds"`

	// thresholdOverrides overrides individual thresholds fields for
	// specific dependsOn edges, keyed by the dependency's FQDN — each key
	// must match an entry in dependsOn (enforced by the validating
	// webhook, not the OpenAPI schema, since cross-field references
	// aren't expressible there). A field left unset on an override falls
	// back to thresholds' own value for that field; an edge with no entry
	// here uses thresholds unmodified (PLAN.md §5, PROPOSALS.md 2026-08-31
	// — purely additive, existing v1alpha1 objects are unaffected).
	// +optional
	ThresholdOverrides map[string]ThresholdOverrides `json:"thresholdOverrides,omitempty"`

	// mode controls whether a tripped signature patches the mesh.
	// DetectOnly logs/records without patching; Mitigate applies the patch matrix.
	// +optional
	// +kubebuilder:default=Mitigate
	Mode PolicyMode `json:"mode,omitempty"`

	// mesh selects which service mesh's detection queries and mitigation
	// this policy uses. Defaults to Istio — the only implementation this
	// project has today (PLAN.md §5 Phase 6; Linkerd support is still in
	// progress, see internal/mesh's own doc comment for exactly what's
	// wired so far).
	// +optional
	// +kubebuilder:default=Istio
	Mesh MeshType `json:"mesh,omitempty"`
}

// ThresholdOverrides overrides individual Thresholds fields for one
// dependsOn edge. Every field is a pointer, not a plain scalar — this CRD
// is one this project owns outright, so unlike the vendored Istio proto
// types this project's retry-storm bug thread spent an entire session
// working around, there's no reason to repeat "0 is ambiguous with unset"
// here: a pointer field makes "explicitly zero" and "not overridden"
// genuinely distinguishable, in both the OpenAPI schema and the Go struct.
type ThresholdOverrides struct {
	// +optional
	// +kubebuilder:validation:Minimum=1
	LatencyP99Ms *int32 `json:"latencyP99Ms,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=1
	ErrorRateFraction *float64 `json:"errorRateFraction,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=1
	WindowSeconds *int32 `json:"windowSeconds,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=1
	RetryStormMultiplier *float64 `json:"retryStormMultiplier,omitempty"`

	// +optional
	// +kubebuilder:validation:Minimum=1
	FanOutMultiplier *float64 `json:"fanOutMultiplier,omitempty"`
}

// CascadePolicyStatus defines the observed state of CascadePolicy.
type CascadePolicyStatus struct {
	// phase is the state-machine position: Normal, Tripped, or Restoring.
	// +optional
	Phase PolicyPhase `json:"phase,omitempty"`

	// lastSignature is the signature that last tripped this policy, if any.
	// +optional
	LastSignature SignatureType `json:"lastSignature,omitempty"`

	// lastTrippedAt is when the policy last entered Tripped.
	// +optional
	LastTrippedAt *metav1.Time `json:"lastTrippedAt,omitempty"`

	// restoreStep is the restoration ramp index (0–4) while phase is Restoring.
	// +optional
	// +kubebuilder:validation:Minimum=0
	// +kubebuilder:validation:Maximum=4
	RestoreStep int32 `json:"restoreStep,omitempty"`

	// conditions represent the current state of the CascadePolicy resource.
	// DependencyObjectMissing is True when a dependsOn edge has no resolvable
	// DestinationRule or VirtualService; that edge is skipped, not the whole reconcile.
	//
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Namespaced,shortName=cpol
// +kubebuilder:printcolumn:name="Phase",type=string,JSONPath=".status.phase"
// +kubebuilder:printcolumn:name="Mode",type=string,JSONPath=".spec.mode"
// +kubebuilder:printcolumn:name="Last Signature",type=string,JSONPath=".status.lastSignature"
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=".metadata.creationTimestamp"

// CascadePolicy describes a service's dependency edges and detection thresholds.
// The controller resolves Istio objects to patch per dependsOn host; this CR
// does not name DestinationRules or VirtualServices.
type CascadePolicy struct {
	metav1.TypeMeta `json:",inline"`

	// metadata is a standard object metadata
	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// spec defines the desired state of CascadePolicy
	// +required
	Spec CascadePolicySpec `json:"spec"`

	// status defines the observed state of CascadePolicy
	// +optional
	Status CascadePolicyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// CascadePolicyList contains a list of CascadePolicy
type CascadePolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []CascadePolicy `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &CascadePolicy{}, &CascadePolicyList{})
		return nil
	})
}
