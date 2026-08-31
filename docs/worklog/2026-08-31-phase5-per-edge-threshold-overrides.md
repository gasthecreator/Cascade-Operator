# Phase 5 (4/4): per-edge threshold overrides — additive, not breaking

**Date:** 2026-08-31
**Author:** Claude
**Type:** feature

## What
PLAN.md §5's last remaining item, and the one flagged as needing its own
`PROPOSALS.md` entry given it was assumed to be "a real, breaking v1alpha1
CRD change." Investigation found a purely additive design that delivers
the identical feature with zero compatibility cost — proposed, resolved,
and implemented in this slice:

- New `ThresholdOverrides` type (`api/v1alpha1/cascadepolicy_types.go`) —
  every field a pointer (`*int32`/`*float64`), not a plain scalar.
- New, wholly optional `CascadePolicySpec.ThresholdOverrides map[string]ThresholdOverrides`,
  keyed by a `dependsOn` FQDN.
- `effectiveThresholds(policy, host) Thresholds` (`internal/controller/thresholds.go`)
  merges an override onto `spec.thresholds`, called at every per-host
  threshold read site instead of reading `policy.Spec.Thresholds` directly.
- Admission webhook validates an override's key matches a real `dependsOn`
  entry (the cross-field check the OpenAPI schema can't express).

## Why
PLAN.md's own phrasing ("breaking v1alpha1 API change") assumed the only
way to add per-edge overrides was changing `DependsOn`'s type from
`[]string` to some richer per-edge struct — which would indeed break
every existing object using the current flat shape. That assumption was
never actually checked against the code. Reading every current consumer
of `DependsOn` first (`cascadepolicy_controller.go`, `restore.go`,
`retry_restore.go`) showed all three just range over it as a plain
`[]string` — none of them need `DependsOn`'s type to change at all if
overrides live in a *separate*, new, `omitempty`-optional field instead.
An existing `CascadePolicy` with no `thresholdOverrides` key validates and
behaves exactly as it does today; `effectiveThresholds` with an absent
override just returns `spec.thresholds` unchanged. Filed as a
`PROPOSALS.md` entry and resolved before implementation, per this
project's own protocol for anything that revisits an architecture
assumption — even one that turns out to be avoidable rather than
necessary.

`ThresholdOverrides`' fields are pointers specifically because this
project spent this entire session fighting the opposite choice in the
vendored Istio proto types (the whole retry-storm zero-value bug thread).
This CRD is one this project owns outright — there's no reason to import
that same "0 is ambiguous with unset" mistake into a schema under direct
control, when a pointer field makes the two cases genuinely distinguishable
in both the OpenAPI schema and the Go struct, with no wire-transport
omitempty stripping anything (unlike the Istio/Envoy proto boundary this
whole thread was actually about).

## How
- Read every `policy.Spec.DependsOn`/`policy.Spec.Thresholds` consumer
  before writing the proposal, not just the two obvious ones — confirmed
  `detectSignatures`, `evalLatencyError`/`evalRetryStorm`/`evalFanOut`,
  the timeout-secondary mitigation call, and the timeout restore-step call
  are the complete set of per-host threshold read sites.
- Threaded `th cascadev1alpha1.Thresholds` through each of those instead
  of leaving them to read `policy.Spec.Thresholds` directly — computed
  once per host per reconcile tick (`effectiveThresholds(policy, host)`),
  not recomputed per-detector. `windowOrDefault` also moved inside the
  per-host loop (previously computed once for the whole policy) since
  `windowSeconds` is itself overridable.
- Two of the three detector-eval functions (`evalLatencyError`,
  `evalRetryStorm`) no longer need a `*CascadePolicy` parameter at all
  once they take `th` directly — removed it rather than leaving an unused
  parameter. `evalFanOut` keeps `policy` (still needs `policy.Spec.Service`
  as the fan-out ratio's caller-side host).
- Webhook validation reuses the existing per-entry `seen map[string]int`
  (built for `dependsOn` duplicate detection) to check `thresholdOverrides`
  keys — no new map, no second pass over `dependsOn`.

## Files touched
- `api/v1alpha1/cascadepolicy_types.go` — `ThresholdOverrides` type, new
  spec field; `zz_generated.deepcopy.go`, `config/crd/bases/...yaml`
  regenerated via `make generate manifests`
- `internal/controller/thresholds.go` — `effectiveThresholds`
- `internal/controller/thresholds_test.go` — unit tests, including the
  explicit-zero-vs-unset case
- `internal/controller/threshold_override_reconcile_test.go` — end-to-end
  `Reconcile` test proving an override changes real detection behavior on
  one host while leaving an unrelated host (same raw metrics, no override)
  correctly unaffected
- `internal/controller/cascadepolicy_controller.go`, `mitigate.go`,
  `restore.go` — per-host threshold read sites updated
  to use `effectiveThresholds`
- `internal/controller/cascadepolicy_controller_test.go`,
  `istio_patch_test.go` — shared FQDN constants extracted (goconst)
- `internal/webhook/v1alpha1/cascadepolicy_webhook.go`, `_test.go` —
  `thresholdOverrides` key validation
- `config/samples/cascade_v1alpha1_cascadepolicy.yaml` — live example
- `PROPOSALS.md` — new resolved entry; also fixed a pre-existing duplicate
  `## Resolved Proposals` header found while editing this file (a leftover
  from an earlier slice this session, not something this slice caused)
- `PLAN.md` — §5, fourth sub-item, and the Phase 5 top-level line (all
  four sub-items now complete)

## Testing
- `go build ./...`, `gofmt -l .`, `go vet ./...` — clean.
- `make lint` — caught and fixed five real `goconst` violations across
  four test files (a repeated `checkout-service...` FQDN literal and a
  repeated `inventory-service...` literal, both crossing the package-wide
  threshold once my new test files added a couple more occurrences) —
  extracted shared constants, 0 issues on the final pass.
- `make test` — controller coverage 80.0% (up from baseline 79.7% — the
  new code is genuinely exercised, not just added), full suite otherwise
  unaffected: mitigation 90.3%, notify 91.3%, signatures 94.1%, webhook
  100%.
- **New tests prove real behavior, not just that `effectiveThresholds`
  returns the right struct in isolation**: a controller-level `Reconcile`
  test gives two dependency hosts the *identical* raw p99/error-rate
  readings from the stub querier and only overrides one host's
  `latencyP99Ms` — the overridden host trips, the other (evaluated
  *first*, so a bug that let the override leak onto it would be caught)
  correctly stays healthy against the same numbers.
- `make verify-generate` — clean once these changes are committed (an
  intermediate run showed the expected diff against the *not-yet-committed*
  regenerated files, not real drift).
- **Live verification, done directly, not assumed:** applied the
  regenerated CRD (`kubectl apply -f config/crd/bases/...yaml`) to the dev
  cluster, then `kubectl apply --dry-run=server` on the updated sample
  (which includes a real `thresholdOverrides` entry) — accepted by the
  real API server's OpenAPI validation, not just a local schema check.
  Did **not** apply the sample for real: it shares the same
  name/namespace as the demo topology's actual `CascadePolicy` object, so
  a real apply would have overwritten it; the dry-run already gave the
  validation confidence needed. Ran the full `make test-integration` suite
  against the live cluster afterward with the updated CRD in place — all
  three existing signature tests still pass.

## Follow-ups / known gaps
- This closes every item in PLAN.md §5 — the production-readiness
  initiative's Phase 5 is fully complete.
- No live, organic (k6-driven) trip specifically exercising a
  `thresholdOverrides`-driven detection was run — the controller-level
  fake-client test already proves the actual behavior change end-to-end
  through the real `Reconcile` path, and a bespoke live k6 scenario for
  this one CRD-level feature would be testing the same detection logic
  this project's existing k6 scripts already exercise, not anything new
  about override plumbing specifically.
