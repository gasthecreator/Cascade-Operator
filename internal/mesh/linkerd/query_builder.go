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

// Package linkerd implements mesh.QueryBuilder (and, in a following slice,
// mesh.Mitigator) against Linkerd's proxy metrics (PLAN.md §5 Phase 6.6).
//
// Every metric name and label used here was confirmed live against a real
// Linkerd 2.16 control plane + linkerd-viz Prometheus, installed on the dev
// Kind cluster alongside the existing Istio install, with real generated
// traffic through a Linkerd-injected copy of the demo topology (checkout ->
// {payments, inventory} in a new `linkerd-demo` namespace) — not assumed
// from documentation, matching this project's own "verify, don't assume"
// discipline for the original Istio queries (see PROPOSALS.md's resolved
// "sum by (le)" entry, and this package's own worklog entry for the exact
// live queries run). Two corrections against the plan's own draft text
// fell out of that spike: the request counter is request_total, not
// response_total (response_total is a *different*, real metric — the
// completed-response counter used below for ErrorRateQuery); and the
// latency histogram is response_latency_ms_bucket, not
// route_response_latency_ms_bucket.
package linkerd

import "fmt"

// QueryBuilder implements mesh.QueryBuilder for Linkerd. Stateless — every
// method is a pure fmt.Sprintf, so the zero value is always ready to use,
// same as istio.QueryBuilder.
type QueryBuilder struct{}

// LatencyP99Query is the client-perceived (direction=outbound, observed at
// the calling proxy) p99, aggregated across all remaining labels via sum
// by (le) before histogram_quantile — same reasoning as Istio's
// LatencyP99Query: without the sum by (le), Prometheus returns one series
// per status_code/dst_pod/etc., and histogram_quantile needs exactly one
// series per le bucket. authority is the caller-observed Host/:authority
// value, confirmed live to equal the plain Service FQDN (e.g.
// "payments-service.linkerd-demo.svc.cluster.local", no port) — unlike
// Istio's destination_service, this is not mesh-assigned; it is whatever
// the client sent, but for in-cluster calls through the Service's own
// ClusterIP/DNS name (this project's only supported call shape) it is
// always the Service FQDN, confirmed against real traffic.
func (QueryBuilder) LatencyP99Query(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`histogram_quantile(0.99, sum by (le) (rate(response_latency_ms_bucket{authority=%q,direction="outbound"}[%ds])))`,
		host, windowSeconds,
	)
}

// ErrorRateQuery is failure-classified rate over total rate for a
// dependency, both sides summed before dividing — same one-series-per-side
// reasoning as Istio's ErrorRateQuery (PLAN.md §2.4's "sum() removes
// per-series label sets on both sides" fix applies identically here;
// built with sum() from the start rather than repeating that bug).
// classification="failure" is Linkerd's own success/failure judgment
// (confirmed live: a 500 from demo/internal/depsvc's /control/fail handler
// produces classification="failure", status_code="500") — the Linkerd-
// native equivalent of Istio's response_code=~"5.." filter, not a
// hand-rolled status-code regex.
func (QueryBuilder) ErrorRateQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(response_total{authority=%q,direction="outbound",classification="failure"}[%ds])) / sum(rate(response_total{authority=%q,direction="outbound"}[%ds]))`,
		host, windowSeconds, host, windowSeconds,
	)
}

// RetryStormRatioQuery is a dependency's actual (post-retry-expansion)
// request rate over its logical (pre-retry, one-per-application-call)
// request rate, both observed at the *caller's own* outbound proxy via the
// route_actual_request_total/route_request_total pair — Linkerd's direct
// equivalent of Istio's reporter=destination/reporter=source split,
// confirmed live to behave the same way despite coming from a different
// metric family: with a ServiceProfile's retryBudget applied to a failing
// dependency, route_actual_request_total (every physical attempt,
// including retries) came back at ~4-6x route_request_total (one row per
// original application-level call) for the same live traffic. This pair
// only exists once a ServiceProfile with a matching route exists for the
// dependency (confirmed live: absent before the ServiceProfile was
// applied) — an acceptable precondition, not a new one: retry storm's own
// Linkerd mitigation is that same ServiceProfile's retryBudget (PLAN.md
// §5's Mitigator slice), exactly mirroring Istio's retry-storm signature
// already assuming a pre-existing VirtualService retry policy on the
// dependency (see demo/k8s/inventory-retry-vs.yaml's own comment).
//
// Plain request_total/response_total cannot serve this signal on Linkerd
// the way istio_requests_total's reporter split does: confirmed live that
// Linkerd's direction=outbound request_total at the caller's own proxy
// already includes every retry attempt (the proxy that performs the retry
// is the same one being measured), so it tracks direction=inbound at the
// dependency almost exactly (160 vs. 161 in the same live run) — neither
// side gives a "logical calls before retries" baseline the way Istio's
// reporter=source does. route_request_total (as opposed to
// route_actual_request_total) is what supplies that missing baseline.
//
// dst matches the ServiceProfile-scoped metrics' own host:port label
// (confirmed live as "<service-fqdn>:<service-port>", e.g.
// "inventory-service.linkerd-demo.svc.cluster.local:80") — matched via
// regex since QueryBuilder is not given the dependency's Service port; a
// literal dot in host is extremely unlikely to false-match a differently-
// structured dst value here, the same non-escaping tradeoff Istio's own
// exact-match queries already accept for FQDNs.
func (QueryBuilder) RetryStormRatioQuery(host string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(route_actual_request_total{dst=~%q,direction="outbound"}[%ds])) / sum(rate(route_request_total{dst=~%q,direction="outbound"}[%ds]))`,
		host+":.*", windowSeconds, host+":.*", windowSeconds,
	)
}

// FanOutRatioQuery is one dependency's own inbound request rate over the
// calling service's own inbound request rate — cross-host, same shape as
// Istio's FanOutRatioQuery (both sides use "what actually arrived," Istio's
// reporter="destination" on both sides; here, direction="inbound" on both
// sides, observed at each service's own proxy). authz_name!="probe"
// excludes the proxy's own liveness/readiness admin-port traffic (confirmed
// live: kubelet probes land on request_total with
// authz_name="probe",srv_port="4191", alongside real application traffic
// on the app's own port with authz_name="all-unauthenticated" — excluding
// by authz_name avoids hardcoding a container port).
//
// Unlike LatencyP99Query/ErrorRateQuery, inbound request_total carries no
// FQDN-shaped label at all (no authority, no dst_service — confirmed live:
// direction=inbound samples only ever carry deployment/app/namespace,
// never a dst_* label, since the proxy receiving inbound traffic has no
// notion of "destination service", only its own identity) — necessary
// because the caller's own inbound rate (callerHost) has no other
// candidate metric: nothing meshed calls into checkout's own inbound port
// from outside the mesh in this topology's terms, so there is no
// "outbound-toward-caller" view to use instead, unlike the dependency side
// where an authority-based outbound view would have been available. Both
// sides are therefore resolved from the Service FQDN to a Deployment name
// via mitigation.ParseServiceFQDN and matched by the deployment label —
// this assumes the Deployment shares its Service's name, true for every
// manifest in demo/k8s and demo/k8s-linkerd and the only convention this
// project's Istio migration already assumes too (DestinationRule/
// VirtualService objects are resolved and named the same way). A host that
// fails to parse (should not happen — the admission webhook already
// requires plausible Service FQDN shape) degrades to a deployment label
// match on the raw, unparsed host string, which can never match a real
// deployment label (Kubernetes object names never contain dots), rather
// than building a malformed query or panicking.
func (QueryBuilder) FanOutRatioQuery(dependencyHost, callerHost string, windowSeconds int32) string {
	return fmt.Sprintf(
		`sum(rate(request_total{deployment=%q,namespace=%q,direction="inbound",authz_name!="probe"}[%ds])) / sum(rate(request_total{deployment=%q,namespace=%q,direction="inbound",authz_name!="probe"}[%ds]))`,
		deploymentLabel(dependencyHost), namespaceLabel(dependencyHost), windowSeconds,
		deploymentLabel(callerHost), namespaceLabel(callerHost), windowSeconds,
	)
}
