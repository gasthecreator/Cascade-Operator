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

package linkerd

import "github.com/gasthecreator/Cascade-Operator/internal/mitigation"

// deploymentLabel and namespaceLabel resolve host's Service name/namespace
// for matching Linkerd's inbound-metric deployment/namespace labels (see
// FanOutRatioQuery's own doc comment for why this convention-based
// resolution, rather than an FQDN-shaped label, is necessary on the
// inbound side). On an unparseable host — not expected in practice, since
// the admission webhook already validates Service FQDN shape before a
// CascadePolicy can specify one — both return host itself: a Kubernetes
// object name can never contain a dot, so a raw FQDN string can never
// match a real deployment or namespace label, degrading to "no data"
// rather than a malformed query.
func deploymentLabel(host string) string {
	name, _, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return host
	}
	return name
}

func namespaceLabel(host string) string {
	_, ns, err := mitigation.ParseServiceFQDN(host)
	if err != nil {
		return host
	}
	return ns
}
