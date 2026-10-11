# Phase 4: Grafana dashboard + trip/restore webhook notifier

**Date:** 2026-08-31
**Author:** Claude
**Type:** feature

## What
Two pieces (PLAN.md §5 Phase 4), both deliberately small and built on
existing pieces rather than adding new moving parts:

1. **Grafana dashboard** (`config/observability/grafana-dashboard.json`) —
   visualizes the operator's four existing `cascade_*` Prometheus counters
   (`internal/controller/metrics.go`) plus controller-runtime's standard
   reconcile-health metrics. No new metrics. Installed via Istio's own
   sample Grafana addon (`hack/install-grafana.sh`, `make grafana-install`),
   which imports/re-imports the dashboard through Grafana's own API so
   edits to the JSON show up without a reinstall.
2. **`internal/notify`** — a small `Notifier` interface with one
   concrete `WebhookNotifier` that POSTs a Slack-compatible `{"text": "..."}`
   payload on a signature trip or a completed restoration. Wired as an
   optional `Notify` field on `CascadePolicyReconciler` (same nil-able
   pattern as `Metrics`), configured via `--notify-webhook-url` /
   `NOTIFY_WEBHOOK_URL` (empty disables it, matching `--prometheus-url`'s
   own convention).

## Why
PLAN.md §5 Phase 4: cheap, visual observability over what Phase-earlier
work already built, plus a lightweight way to know a trip happened without
watching logs or a dashboard continuously. Deliberately **not** a full
Alertmanager rule-authoring path — standing up and configuring Alertmanager
as a new moving part would cost more than a single webhook POST buys here.

## How
- **Dashboard**: six panels (signature-detection rate, mitigation-patch
  rate, restorations-completed-vs-regressions, a regression-rate gauge,
  reconcile rate by result, reconcile duration p50/p99), plus the same
  `${datasource}` templating-variable pattern Istio's own sample dashboards
  use, so it plugs into whatever Prometheus datasource Grafana already has
  configured rather than hardcoding a UID.
- **Notifier**: a failure to send must never fail or block a reconcile —
  `notifyTrip`/`notifyRestore` (new `internal/controller/notify.go`) nil-check
  `r.Notify` and log any send error rather than propagating it, mirroring
  how `Metrics` being nil already disables polling without touching
  reconcile's own error path. Wired at the single "cascade signature
  tripped" log line (one call site covers every signature) and at each of
  the three signature-specific `complete*Restore` functions (the same three
  call sites `restorationsCompletedTotal.Inc()` already uses) — not a new
  abstraction, just riding the existing multi-call-site pattern.

## Files touched
- `config/observability/grafana-dashboard.json` — the dashboard
- `hack/install-grafana.sh`, `Makefile` (`grafana-install` target) —
  install + idempotent re-import
- `docs/dev-istio.md` — usage section, including the honest scrape-gap note
- `internal/notify/notify.go`, `notify_test.go` — the notifier
- `internal/controller/notify.go`, `notify_test.go` — reconciler wiring +
  tests exercising the actual Reconcile call sites, not just the notifier
  package in isolation
- `internal/controller/cascadepolicy_controller.go` — `Notify` field, trip
  call site
- `internal/controller/retry_restore.go`, `fanout_restore.go`, `restore.go` —
  restore-completion call sites
- `cmd/main.go` — `--notify-webhook-url` flag/env wiring
- `PLAN.md` — §5 Phase 4 checklist only

## Testing
- `go build ./...`, `gofmt -l .`, `go vet ./...` — clean.
- `make lint` — caught three real issues while iterating: an unchecked
  `resp.Body.Close()` (fixed to match `internal/metrics/client.go`'s own
  existing convention), a `goconst` repeated string literal in a test, and
  a `modernize` range-over-int suggestion (`for i := 0; i < 6; i++` →
  `for i := range 6`) — all fixed, 0 issues on the final pass.
- `make test` — `internal/notify` 91.3%, `internal/controller` 79.7%
  (up slightly from 79.3% — the new notify call sites are actually
  exercised, not just added and left uncovered). Full suite otherwise
  unaffected.
- `make verify-generate` — no drift. `make test-integration` — all three
  existing signature tests still pass against the live cluster, confirming
  the new wiring didn't regress anything.
- **New controller-level tests exercise the real `Reconcile` call sites**,
  not just the `notify` package in isolation: a recording `Notifier` stub
  confirms `NotifyTrip` fires exactly once on a real trip (with the correct
  policy/signature/dependency fields), `NotifyRestore` fires exactly once
  only after the full restoration ramp completes (not before), a failing
  notifier never turns into a failed `Reconcile`, and a nil `Notifier`
  (the default) doesn't panic.
- **Live verification, done directly against the running operator and the
  real Prometheus, not assumed:**
  - Ran the operator locally (`--metrics-bind-address=:8080
    --metrics-secure=false` — the default leaves the metrics server
    disabled entirely) against the dev Kind cluster, drove a real
    k6-induced `RetryStorm` trip+restore, and confirmed real, correctly
    labeled values via `curl localhost:8080/metrics` directly — the same
    verification method the original `operator-metrics` worklog used, not
    a new one invented for this slice:
    `cascade_signatures_detected_total{dependency="inventory-service...",signature="RetryStorm"} 3`,
    `cascade_mitigation_patches_applied_total{kind="DestinationRule",signature="RetryStorm"} 3`
    (and `VirtualService` 3), `cascade_restorations_completed_total{signature="RetryStorm"} 1`.
  - Extracted all 8 PromQL expressions from the dashboard JSON
    programmatically and ran each against the real Prometheus
    (`/api/v1/query`) — all returned `"status":"success"`, confirming no
    syntax errors and that the metric/label names match exactly what the
    operator actually emits (cross-checked against the curl output above).
  - Installed Grafana (`make grafana-install`) on the dev cluster and
    confirmed the dashboard import API call itself returned
    `"status":"success"` — Grafana accepts and parses the JSON model
    correctly.
  - Cleaned up fully afterward: k6 job/configmap, local operator process
    (including a stray orphaned child process from an unrelated earlier
    `go run` — the same recurring gotcha this project has hit before, not
    new here), Prometheus port-forward, and confirmed both DestinationRules
    plus the VirtualService are back to their clean fixture baseline.
  - Left Grafana installed on the dev cluster long-term (same treatment as
    Prometheus, which is also left running) rather than uninstalling after
    verification.

## Follow-ups / known gaps
- **Not demonstrated end-to-end with live data flowing through Prometheus
  into Grafana.** This dev cluster's Prometheus has no scrape path to a
  locally-run (not in-cluster) operator process — this is a pre-existing
  gap in the dev environment, not something newly introduced or newly
  discovered by this slice (the `operator-metrics` worklog already
  established `curl`-the-endpoint-directly as this project's verification
  method for exactly this reason). Actually wiring Prometheus to scrape a
  real in-cluster operator deployment is a separate, larger task — it needs
  either the operator deployed for real (which, per Phase 3's own
  follow-up, needs cert-manager or a manual cert for the webhook) or a
  plain static scrape config change on Prometheus (this cluster's
  Istio-sample Prometheus is not prometheus-operator-driven, so the
  scaffold-generated `ServiceMonitor` CRD doesn't even exist here — checked
  directly, not assumed). Flagged honestly rather than silently claimed.
- The dashboard has not been visually inspected in a browser (only
  API-level verification: valid JSON, successful import, valid PromQL) —
  reasonable given the above gap means there's no live data to look at yet
  anyway.
