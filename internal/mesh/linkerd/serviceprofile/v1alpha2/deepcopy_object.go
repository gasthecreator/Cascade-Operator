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

import "k8s.io/apimachinery/pkg/runtime"

// DeepCopyObject implements runtime.Object. Hand-written, not
// controller-gen-generated — see ServiceProfile's own doc comment in
// types.go for why this package deliberately omits the
// kubebuilder object-root marker that would normally generate this
// method (that same marker also makes this type a CRD-generation
// candidate, which this package must avoid). DeepCopy itself is still
// controller-gen-generated (`make generate`), driven by the package-level
// object-generate marker in groupversion_info.go — only this one small
// method needs to be written by hand.
func (in *ServiceProfile) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}

// DeepCopyObject implements runtime.Object — see ServiceProfile's own
// DeepCopyObject just above for why this is hand-written.
func (in *ServiceProfileList) DeepCopyObject() runtime.Object {
	if in == nil {
		return nil
	}
	return in.DeepCopy()
}
