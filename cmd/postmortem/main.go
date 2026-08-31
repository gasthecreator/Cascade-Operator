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

// Command postmortem renders a real incident postmortem for one
// CascadePolicy's most recent trip, from its live status plus Prometheus —
// not from operator logs (PLAN.md §5 Phase 8; the plan's original text says
// "logged confidence/evidence string", but that string is only ever
// log.Info'd, never persisted to status — see this command's own worklog
// entry for why Prometheus's @<timestamp> historical-query modifier is a
// better, more portable source for reconstructing root cause than requiring
// access to the operator's pod logs).
//
// On-demand CLI, run against a live cluster:
//
//	go run ./cmd/postmortem --policy=checkout-service --prometheus-url=http://127.0.0.1:19090
package main

import (
	"context"
	"flag"
	"fmt"
	"math"
	"os"
	"strings"
	"text/template"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	networkingv1 "istio.io/client-go/pkg/apis/networking/v1"

	cascadev1alpha1 "github.com/gasthecreator/Cascade-Operator/api/v1alpha1"
	"github.com/gasthecreator/Cascade-Operator/internal/metrics"
)

// Same reconcile cadence and ramp length as the reconciler itself
// (internal/controller/cascadepolicy_controller.go's DefaultRequeueAfter,
// internal/mitigation's RestoreFinalStep) — duplicated as literal constants
// here rather than importing internal/controller, which would pull in the
// full manager/client-go-scheme/webhook dependency graph for a one-shot CLI
// that only needs two numbers. If these ever drift from the reconciler's
// own constants, that's a real bug worth catching — noted in this file's
// worklog entry as a known duplication, not silently assumed permanent.
const (
	reconcileInterval   = 10 * time.Second
	restoreFinalStep    = 4
	typicalRampDuration = reconcileInterval * (restoreFinalStep + 1)
)

func main() {
	var policyName, policyNamespace, prometheusURL, outPath string
	flag.StringVar(&policyName, "policy", "", "CascadePolicy name (required)")
	flag.StringVar(&policyNamespace, "namespace", "default", "CascadePolicy namespace")
	flag.StringVar(&prometheusURL, "prometheus-url", os.Getenv("PROMETHEUS_URL"),
		"Base URL of the Prometheus HTTP API. Also read from PROMETHEUS_URL.")
	flag.StringVar(&outPath, "out", "", "Output file path (default: stdout)")
	flag.Parse()

	if policyName == "" {
		fmt.Fprintln(os.Stderr, "usage: postmortem --policy=<name> [--namespace=default] "+
			"--prometheus-url=<url> [--out=file.md]")
		os.Exit(2)
	}
	if prometheusURL == "" {
		fmt.Fprintln(os.Stderr, "--prometheus-url (or PROMETHEUS_URL) is required")
		os.Exit(2)
	}

	if err := run(policyName, policyNamespace, prometheusURL, outPath); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(policyName, policyNamespace, prometheusURL, outPath string) error {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register client-go scheme: %w", err)
	}
	if err := cascadev1alpha1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register cascade scheme: %w", err)
	}
	if err := networkingv1.AddToScheme(scheme); err != nil {
		return fmt.Errorf("register istio scheme: %w", err)
	}

	c, err := client.New(ctrl.GetConfigOrDie(), client.Options{Scheme: scheme})
	if err != nil {
		return fmt.Errorf("build k8s client: %w", err)
	}

	ctx := context.Background()
	policy := &cascadev1alpha1.CascadePolicy{}
	if err := c.Get(ctx, types.NamespacedName{Name: policyName, Namespace: policyNamespace}, policy); err != nil {
		return fmt.Errorf("get CascadePolicy %s/%s: %w", policyNamespace, policyName, err)
	}

	if policy.Status.LastTrippedAt == nil {
		return fmt.Errorf("CascadePolicy %s/%s has never tripped (status.lastTrippedAt is unset) — nothing to report",
			policyNamespace, policyName)
	}

	promClient, err := metrics.NewClient(prometheusURL, nil)
	if err != nil {
		return fmt.Errorf("build Prometheus client: %w", err)
	}

	report, err := buildReport(ctx, policy, promClient)
	if err != nil {
		return err
	}

	out := os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return fmt.Errorf("create %s: %w", outPath, err)
		}
		defer func() { _ = f.Close() }()
		out = f
	}
	_, err = out.WriteString(report)
	return err
}

// affectedHost tries each dependsOn host in order and returns the first
// one Prometheus shows *any* signal for — status.LastSignature does not
// record which specific edge tripped, only which signature. If several
// edges show activity this picks the first, same evaluation order
// detectSignatures itself uses; a genuinely more precise answer would need
// status to record the host too, which it currently doesn't (see the
// worklog's "known gaps" section).
func affectedHost(policy *cascadev1alpha1.CascadePolicy) string {
	if len(policy.Spec.DependsOn) == 0 {
		return ""
	}
	return policy.Spec.DependsOn[0]
}

type reportData struct {
	PolicyName      string
	PolicyNamespace string
	Signature       string
	Phase           string
	RestoreStep     int32
	TrippedAt       string
	Host            string
	WindowSeconds   int32
	P99AtTrip       string
	ErrorRateAtTrip string
	TotalRequests   string
	ErrorRequests   string
	ErrorRatePct    string
	ReconcileEvery  string
	RampEstimate    string
	Conditions      []string
	GeneratedAt     string
}

const reportTemplate = `# Postmortem: {{.PolicyName}} — {{.Signature}}

**Generated:** {{.GeneratedAt}}
**Policy:** {{.PolicyNamespace}}/{{.PolicyName}}
**Signature:** {{.Signature}}
**Current phase:** {{.Phase}}{{if eq .Phase "Restoring"}} (restore step {{.RestoreStep}}/4){{end}}

## Timeline

- **Tripped at:** {{.TrippedAt}}
- **Affected dependency:** {{.Host}}
{{if eq .Phase "Normal"}}- **Restoration:** ramp completed. Exact completion timestamp is not tracked in
status (a known gap — see this tool's worklog entry); typical ramp duration at the default
{{.ReconcileEvery}} reconcile cadence is ~{{.RampEstimate}}.
{{else if eq .Phase "Restoring"}}- **Restoration:** in progress, step {{.RestoreStep}} of 4.
{{else}}- **Restoration:** not yet started (still Tripped).
{{end}}

## Root cause

Reconstructed from Prometheus history at the trip timestamp (the operator
itself only logs this at trip time — it is not persisted to status; see
the worklog for why this command reconstructs it retroactively instead of
requiring pod log access):

- p99 latency at trip: {{.P99AtTrip}} ms (window: {{.WindowSeconds}}s)
- error rate at trip: {{.ErrorRateAtTrip}}

## Impact (blast radius)

Over the incident window (trip timestamp to now):

- Total requests to {{.Host}}: {{.TotalRequests}}
- 5xx requests: {{.ErrorRequests}} ({{.ErrorRatePct}})

## Status conditions
{{range .Conditions}}- {{.}}
{{else}}- none
{{end}}
## Known limitations of this report

- status.LastSignature records *which signature* tripped, not *which
  dependsOn host* — this report assumes the first configured dependency
  ({{.Host}}), which is correct for this project's demo topology (one
  dependency per signature type) but would need status to record the host
  explicitly to be precise for a policy with multiple dependencies tripping
  independently.
- Restore-completion is not timestamped in status; the ramp-duration figure
  above is the *typical* value from known constants, not a measured one.
`

func buildReport(
	ctx context.Context,
	policy *cascadev1alpha1.CascadePolicy,
	promClient metrics.Querier,
) (string, error) {
	host := affectedHost(policy)
	windowSeconds := policy.Spec.Thresholds.WindowSeconds
	if windowSeconds <= 0 {
		windowSeconds = 30
	}
	trippedAt := policy.Status.LastTrippedAt.Time
	tripUnix := trippedAt.Unix()

	p99Query := fmt.Sprintf(
		`histogram_quantile(0.99, sum by (le) (rate(istio_request_duration_milliseconds_bucket`+
			`{destination_service=%q,reporter="source"}[%ds] @ %d)))`,
		host, windowSeconds, tripUnix,
	)
	errRateQuery := fmt.Sprintf(
		`rate(istio_requests_total{destination_service=%q,response_code=~"5.."}[%ds] @ %d) / `+
			`rate(istio_requests_total{destination_service=%q}[%ds] @ %d)`,
		host, windowSeconds, tripUnix, host, windowSeconds, tripUnix,
	)

	p99Val := queryFirst(ctx, promClient, p99Query)
	errRateVal := queryFirst(ctx, promClient, errRateQuery)

	now := time.Now()
	incidentWindow := max(int64(now.Sub(trippedAt).Seconds()), 1)
	totalQuery := fmt.Sprintf(
		`sum(increase(istio_requests_total{destination_service=%q,reporter="destination"}[%ds]))`,
		host, incidentWindow,
	)
	errQuery := fmt.Sprintf(
		`sum(increase(istio_requests_total{destination_service=%q,reporter="destination",response_code=~"5.."}[%ds]))`,
		host, incidentWindow,
	)
	totalVal := queryFirst(ctx, promClient, totalQuery)
	errVal := queryFirst(ctx, promClient, errQuery)

	errRatePct := "n/a"
	if totalVal > 0 {
		errRatePct = fmt.Sprintf("%.1f%%", errVal/totalVal*100)
	}

	conditions := make([]string, 0, len(policy.Status.Conditions))
	for _, c := range policy.Status.Conditions {
		conditions = append(conditions, fmt.Sprintf("%s: %s (%s) — %s", c.Type, c.Status, c.Reason, c.Message))
	}

	data := reportData{
		PolicyName:      policy.Name,
		PolicyNamespace: policy.Namespace,
		Signature:       string(policy.Status.LastSignature),
		Phase:           string(policy.Status.Phase),
		RestoreStep:     policy.Status.RestoreStep,
		TrippedAt:       trippedAt.UTC().Format(time.RFC3339),
		Host:            host,
		WindowSeconds:   windowSeconds,
		P99AtTrip:       formatFloat(p99Val),
		ErrorRateAtTrip: formatFloat(errRateVal),
		TotalRequests:   formatFloat(totalVal),
		ErrorRequests:   formatFloat(errVal),
		ErrorRatePct:    errRatePct,
		ReconcileEvery:  reconcileInterval.String(),
		RampEstimate:    typicalRampDuration.String(),
		Conditions:      conditions,
		GeneratedAt:     now.UTC().Format(time.RFC3339),
	}

	tmpl, err := template.New("postmortem").Parse(reportTemplate)
	if err != nil {
		return "", fmt.Errorf("parse template: %w", err)
	}
	var sb strings.Builder
	if err := tmpl.Execute(&sb, data); err != nil {
		return "", fmt.Errorf("render template: %w", err)
	}
	return sb.String(), nil
}

// queryFirst returns the first sample's value, or NaN if the query errored
// or returned no data — formatFloat renders NaN as "n/a".
func queryFirst(ctx context.Context, q metrics.Querier, promql string) float64 {
	snap, err := q.Query(ctx, promql)
	if err != nil || len(snap.Samples) == 0 {
		return math.NaN()
	}
	return snap.Samples[0].Value
}

func formatFloat(v float64) string {
	if v != v { // NaN
		return "n/a"
	}
	return fmt.Sprintf("%.2f", v)
}
