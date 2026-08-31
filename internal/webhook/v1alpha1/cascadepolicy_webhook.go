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
	"context"
	"fmt"
	"regexp"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	ctrl "sigs.k8s.io/controller-runtime"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/webhook/admission"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
)

// nolint:unused
// log is for logging in this package.
var cascadepolicylog = logf.Log.WithName("cascadepolicy-resource")

// SetupCascadePolicyWebhookWithManager registers the webhook for CascadePolicy in the manager.
func SetupCascadePolicyWebhookWithManager(mgr ctrl.Manager) error {
	return ctrl.NewWebhookManagedBy(mgr, &cascadev1alpha1.CascadePolicy{}).
		WithValidator(&CascadePolicyCustomValidator{}).
		Complete()
}

// TODO(user): EDIT THIS FILE!  THIS IS SCAFFOLDING FOR YOU TO OWN!

// TODO(user): change verbs to "verbs=create;update;delete" if you want to enable deletion validation.
// NOTE: If you want to customise the 'path', use the flags '--defaulting-path' or '--validation-path'.
// +kubebuilder:webhook:path=/validate-cascade-gideonsanni-dev-v1alpha1-cascadepolicy,mutating=false,failurePolicy=fail,sideEffects=None,groups=cascade.gideonsanni.dev,resources=cascadepolicies,verbs=create;update,versions=v1alpha1,name=vcascadepolicy-v1alpha1.kb.io,admissionReviewVersions=v1

// CascadePolicyCustomValidator validates CascadePolicy on create/update.
//
// Deliberately narrow scope (PLAN.md §5 Phase 3): every threshold field
// already carries a +kubebuilder:validation:Minimum/Maximum marker
// (api/v1alpha1/cascadepolicy_types.go), and non-positive thresholds/empty
// dependsOn are rejected by the OpenAPI schema at the apiserver *before*
// this webhook ever runs — re-checking them here would be pure duplication
// for zero additional protection, plus a new failure surface (cert
// rotation, webhook downtime blocking every CR write) for no real gain.
// What the CRD schema structurally cannot express is cross-field/semantic
// constraints, which is exactly what this validates instead:
//
//  1. spec.service must not appear in its own spec.dependsOn (a policy
//     can't depend on the service it's protecting).
//  2. spec.dependsOn must not contain duplicate entries (+listType=atomic
//     doesn't enforce set semantics or reject duplicates on its own).
//  3. spec.service and every spec.dependsOn entry must look like a
//     plausible Kubernetes Service FQDN, since the controller resolves
//     Istio objects from these hostnames by naming convention (§2.3) — a
//     typo here would otherwise only surface later as a silent
//     DependencyObjectMissing status condition instead of a rejected write.
//
// Deliberately NOT validated here (explicit, not an oversight): whether a
// dependsOn host actually resolves to a real Kubernetes Service or Istio
// object. That would need a live client lookup from inside the webhook,
// adding real latency/complexity to every CascadePolicy write for a check
// the reconciler already performs asynchronously via the
// DependencyObjectMissing status condition, which degrades per-edge rather
// than rejecting the whole write.
type CascadePolicyCustomValidator struct{}

// serviceFQDNPattern matches a plausible in-cluster Service FQDN, matching
// this project's own convention (PLAN.md §2.3): <service-name>.<namespace>.svc.cluster.local.
// DNS label rules (RFC 1123): lowercase alphanumeric and '-', not starting
// or ending with '-'.
var serviceFQDNPattern = regexp.MustCompile(
	`^[a-z0-9]([-a-z0-9]*[a-z0-9])?\.[a-z0-9]([-a-z0-9]*[a-z0-9])?\.svc\.cluster\.local$`,
)

func validateCascadePolicy(obj *cascadev1alpha1.CascadePolicy) error {
	var errs field.ErrorList
	specPath := field.NewPath("spec")

	if !serviceFQDNPattern.MatchString(obj.Spec.Service) {
		errs = append(errs, field.Invalid(specPath.Child("service"), obj.Spec.Service,
			"must be a Service FQDN of the form <name>.<namespace>.svc.cluster.local"))
	}

	seen := make(map[string]int, len(obj.Spec.DependsOn))
	for i, dep := range obj.Spec.DependsOn {
		depPath := specPath.Child("dependsOn").Index(i)
		if !serviceFQDNPattern.MatchString(dep) {
			errs = append(errs, field.Invalid(depPath, dep,
				"must be a Service FQDN of the form <name>.<namespace>.svc.cluster.local"))
		}
		if dep == obj.Spec.Service {
			errs = append(errs, field.Invalid(depPath, dep,
				"must not equal spec.service — a policy cannot depend on the service it protects"))
		}
		if first, dup := seen[dep]; dup {
			dupErr := field.Duplicate(depPath, dep)
			dupErr.Detail = fmt.Sprintf("already listed at dependsOn[%d]", first)
			errs = append(errs, dupErr)
		}
		seen[dep] = i
	}

	// thresholdOverrides keys must reference a real dependsOn entry — the
	// OpenAPI schema can't express this cross-field constraint (PLAN.md
	// §5, PROPOSALS.md 2026-08-31: additive per-edge overrides).
	for key := range obj.Spec.ThresholdOverrides {
		if _, ok := seen[key]; !ok {
			errs = append(errs, field.Invalid(
				specPath.Child("thresholdOverrides").Key(key), key,
				"must match an entry in spec.dependsOn",
			))
		}
	}

	if len(errs) == 0 {
		return nil
	}
	return apierrors.NewInvalid(
		schema.GroupKind{Group: cascadev1alpha1.GroupVersion.Group, Kind: "CascadePolicy"},
		obj.Name, errs,
	)
}

// ValidateCreate implements webhook.CustomValidator so a webhook will be registered for the type CascadePolicy.
func (v *CascadePolicyCustomValidator) ValidateCreate(_ context.Context, obj *cascadev1alpha1.CascadePolicy) (admission.Warnings, error) {
	cascadepolicylog.Info("Validation for CascadePolicy upon creation", "name", obj.GetName())
	return nil, validateCascadePolicy(obj)
}

// ValidateUpdate implements webhook.CustomValidator so a webhook will be registered for the type CascadePolicy.
func (v *CascadePolicyCustomValidator) ValidateUpdate(_ context.Context, _, newObj *cascadev1alpha1.CascadePolicy) (admission.Warnings, error) {
	cascadepolicylog.Info("Validation for CascadePolicy upon update", "name", newObj.GetName())
	return nil, validateCascadePolicy(newObj)
}

// ValidateDelete implements webhook.CustomValidator so a webhook will be registered for the type CascadePolicy.
// No-op: nothing about this CRD's semantics makes deletion unsafe to allow
// unconditionally (no finalizer-dependent invariant a delete could violate).
func (v *CascadePolicyCustomValidator) ValidateDelete(_ context.Context, obj *cascadev1alpha1.CascadePolicy) (admission.Warnings, error) {
	cascadepolicylog.Info("Validation for CascadePolicy upon deletion", "name", obj.GetName())
	return nil, nil
}
