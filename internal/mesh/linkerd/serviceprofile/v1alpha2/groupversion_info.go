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

// Package v1alpha2 is a minimal, hand-written typed client for Linkerd's
// ServiceProfile CRD (group linkerd.io, kind ServiceProfile) — only the
// fields internal/mesh/linkerd's Mitigator actually reads or writes
// (spec.routes[].{name,condition,isRetryable}, spec.retryBudget), not a
// full mirror of Linkerd's own schema (spec.dstOverrides,
// spec.routes[].responseClasses, and others are intentionally omitted).
//
// Deliberately not the upstream github.com/linkerd/linkerd2 generated
// clientset: that module is Linkerd's entire control-plane monorepo, with
// its own large, independent dependency graph — pulling it in for one CRD
// type would be a heavy, version-fragile addition next to this project's
// already-pinned istio.io/client-go (a purpose-built, narrowly-scoped
// client library, not a monorepo). This package follows exactly the same
// boilerplate shape controller-gen already produces for api/v1alpha1
// (SchemeBuilder/AddToScheme, object:generate markers for
// zz_generated.deepcopy.go via `make generate`), so it's a small, ordinary
// extension of tooling this project already depends on, not a new one.
//
// Every field name, requiredness, and default here was confirmed directly
// against the live dev cluster's installed CRD
// (`kubectl get crd serviceprofiles.linkerd.io -o json`, both the
// v1alpha1 and v1alpha2 served versions — v1alpha2 is the storage
// version and what this package targets) — not assumed from Linkerd's
// documentation alone.
//
// +kubebuilder:skip
//
// The skip marker above is load-bearing, not decorative: controller-gen's
// `crd` generator (this project's `make manifests` target) discovers
// CRD-root candidates by scanning every package under paths="./..." for
// types shaped like a Kubernetes object (embedded TypeMeta/ObjectMeta) —
// confirmed directly against controller-tools v0.21.0's own source
// (pkg/crd/parser.go's indexTypes: "if skipPkg :=
// pkgMarkers.Get(kubebuilder:skip); skipPkg != nil { return }" runs before
// any type in the package is even indexed), not gated by the
// object-generate/object-root markers as the initial version of this
// package assumed — removing those two markers in turn and re-running
// `make manifests` each time still produced a (differently-named, but
// real) generated CRD file until this package-level skip marker was
// added. Without it, `make manifests`/`make install` would generate and
// try to install this package's own deliberately-partial ServiceProfile
// schema (missing dstOverrides, responseClasses, and others — see
// ServiceProfile's own doc comment in types.go) as if it were the real
// thing, right alongside or over Linkerd's own already-installed CRD,
// which this project does not own. `+kubebuilder:skip` only affects `crd`
// generation; scheme registration (this file's GroupVersion var, below)
// and `make generate`'s DeepCopy/DeepCopyInto output for this package are
// both unaffected — object:generate=true is still what drives the latter.
//
// +kubebuilder:object:generate=true
// +groupName=linkerd.io
package v1alpha2

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
)

var (
	// GroupVersion is the ServiceProfile CRD's storage version — confirmed
	// live (`served=true storage=true` for v1alpha2, `served=true
	// storage=false` for v1alpha1 on this same CRD).
	GroupVersion = schema.GroupVersion{Group: "linkerd.io", Version: "v1alpha2"}

	// SchemeBuilder is used to add go types to the GroupVersionKind scheme.
	SchemeBuilder = runtime.NewSchemeBuilder(addKnownTypes)

	// AddToScheme adds the types in this group-version to the given scheme.
	AddToScheme = SchemeBuilder.AddToScheme
)

// addKnownTypes registers ServiceProfile/ServiceProfileList — matching the
// standard client-gen register.go shape (AddKnownTypes, then
// AddToGroupVersion for the List type's metav1.ListOptions etc. support),
// not runtime.SchemeBuilder's own object-registration helper (that type is
// a plain []func(*Scheme) error, with no object-registering method of its
// own — object registration is always done via a builder func like this
// one calling scheme.AddKnownTypes directly).
func addKnownTypes(scheme *runtime.Scheme) error {
	scheme.AddKnownTypes(GroupVersion, &ServiceProfile{}, &ServiceProfileList{})
	metav1.AddToGroupVersion(scheme, GroupVersion)
	return nil
}
