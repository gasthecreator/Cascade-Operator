// Package integration holds Kind-based tests that exercise CascadePolicy
// reconciliation against a live cluster (the dev Kind+Istio install), not
// envtest or the scaffold e2e cluster. Build with -tags=integration and
// run via make test-integration.
package integration
