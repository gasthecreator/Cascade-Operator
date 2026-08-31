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

package mitigation

import (
	"errors"
	"fmt"
	"strings"
)

const clusterLocalSuffix = ".svc.cluster.local"

// ErrInvalidServiceFQDN is returned when a dependsOn host is not
// <service>.<namespace>.svc.cluster.local.
var ErrInvalidServiceFQDN = errors.New("dependsOn host is not a cluster.local Service FQDN")

// ParseServiceFQDN maps a Kubernetes Service DNS name to (name, namespace)
// for DestinationRule lookup by convention (PLAN.md §2.3).
func ParseServiceFQDN(fqdn string) (string, string, error) {
	host := strings.TrimSuffix(strings.TrimSpace(fqdn), ".")
	if !strings.HasSuffix(host, clusterLocalSuffix) {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidServiceFQDN, fqdn)
	}
	rest := strings.TrimSuffix(host, clusterLocalSuffix)
	// Service and namespace names are DNS-1123 labels (no dots), so the
	// remainder must be exactly <service>.<namespace>.
	parts := strings.Split(rest, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("%w: %q", ErrInvalidServiceFQDN, fqdn)
	}
	return parts[0], parts[1], nil
}
