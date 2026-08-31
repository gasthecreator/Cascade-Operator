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
	"testing"
)

func TestParseServiceFQDN(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in      string
		name    string
		ns      string
		wantErr bool
	}{
		{
			in:   "payments-service.default.svc.cluster.local",
			name: "payments-service",
			ns:   "default",
		},
		{
			in:   "inventory-service.shop.svc.cluster.local.",
			name: "inventory-service",
			ns:   "shop",
		},
		{in: "payments-service.default.svc.cluster.local.", name: "payments-service", ns: "default"},
		{in: "not-a-fqdn", wantErr: true},
		{in: "default.svc.cluster.local", wantErr: true},
		{in: "foo.bar.ns.svc.cluster.local", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tc := range cases {
		name, ns, err := ParseServiceFQDN(tc.in)
		if tc.wantErr {
			if !errors.Is(err, ErrInvalidServiceFQDN) {
				t.Errorf("ParseServiceFQDN(%q) err = %v, want ErrInvalidServiceFQDN", tc.in, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseServiceFQDN(%q) unexpected err %v", tc.in, err)
			continue
		}
		if name != tc.name || ns != tc.ns {
			t.Errorf("ParseServiceFQDN(%q) = %s/%s, want %s/%s", tc.in, ns, name, tc.ns, tc.name)
		}
	}
}
