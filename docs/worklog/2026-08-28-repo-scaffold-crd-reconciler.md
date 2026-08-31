# Repo scaffold, CascadePolicy CRD, logging reconciler

**Date:** 2026-08-28
**Author:** Cursor
**Type:** infra, feature

## What
Initialized the kubebuilder project, locked-in `CascadePolicy` types, a
reconciler that watches/logs/`RequeueAfter(10s)` and sets `status.phase=Normal`,
and CI for gofmt, golangci-lint, unit tests, and generate-drift. Did **not**
run a Kind smoke test: Docker is not installed on this machine.

## Why
PLAN.md §3 first slice, unblocked once Open Question #1 was approved:
`CascadePolicy` / `cascade.gideonsanni.dev/v1alpha1`, namespaced, no
`targetVirtualService`. CI from commit one is PLAN.md §2.8. The reconciler
has to tick on a timer (not only watch events) so Prometheus polling in the
next slice has a place to hang off — that 10s requeue is §2.4, implemented
now so we do not have to retrofit the loop later.

## How
- Installed Go 1.27, kubebuilder 4.15.0, and golangci-lint via Homebrew; none
  of them were on the machine. `kubebuilder init` wrote `go 1.26.0` in
  go.mod — followed the tool, per §2.8, rather than fighting brew's 1.27.
- `kubebuilder init --domain gideonsanni.dev --repo github.com/gasthecreator/Cascade-Operator`
  then `create api --group cascade --version v1alpha1 --kind CascadePolicy`
  (namespaced resource, cluster-scoped manager — `--namespaced` on *init*
  would have turned the operator into Role/RoleBinding, which is a different
  decision than "the CR is namespaced").
- CRD types follow the locked YAML: `service`, `dependsOn` FQDNs,
  policy-wide `thresholds`, `mode` defaulting to `Mitigate`, status
  `phase` / `lastSignature` / `lastTrippedAt` / `restoreStep` / conditions.
  Istio object names are not on the CR. `DependencyObjectMissing` is a
  condition type constant for the later patch layer.
- `float64` fields (`errorRateFraction`, multipliers) made
  `controller-gen` refuse to emit the CRD until
  `crd:allowDangerousTypes=true`. That flag is an implementation choice to keep
  the locked YAML (`errorRateFraction: 0.05`) rather than renaming to
  millipercent integers. Documented on the Makefile `manifests` target.
- Reconciler: Get, log generation/mode/service, set `phase=Normal` if empty,
  requeue 10s. RBAC is still CR-only; no Istio permissions yet.
- Envtest (not Kind) covers the reconciler: create the checkout sample spec,
  assert `RequeueAfter` and `status.phase`.
- CI: kept kubebuilder's `lint.yml` / `test.yml`, added `make verify-fmt`
  (`gofmt -l`) and `make verify-generate` (deepcopy + CRD + manager-role
  drift). E2E workflow is `workflow_dispatch` only — Kind stays out of PR
  CI per §2.8. golangci-lint version is pinned in the Makefile (`v2.12.2`).

## Files touched
- `api/v1alpha1/cascadepolicy_types.go` — locked CRD Go types
- `api/v1alpha1/zz_generated.deepcopy.go` — generated
- `config/crd/bases/cascade.gideonsanni.dev_cascadepolicies.yaml` — generated CRD
- `config/samples/cascade_v1alpha1_cascadepolicy.yaml` — checkout example from PLAN.md
- `internal/controller/cascadepolicy_controller.go` — watch/log/requeue
- `internal/controller/cascadepolicy_controller_test.go` — envtest assertions
- `Makefile` — `allowDangerousTypes`, `verify-fmt`, `verify-generate`
- `.github/workflows/test.yml`, `lint.yml` — gofmt + generate-drift steps
- `.github/workflows/test-e2e.yml` — push/PR disabled
- `README.md` — project blurb pointing at PLAN.md (full demo README still later)
- plus the rest of the kubebuilder scaffold (`cmd/main.go`, `config/`, `go.mod`, …)
- `PLAN.md` — checklist: scaffold, CRD, CI; status line
- `docs/worklog/README.md` — index this entry

## Testing
- `make test` — pass. Controller package 81.2% coverage via envtest (no Docker).
- `make lint` — 0 issues (custom golangci-lint with logcheck).
- `make verify-fmt` / `make verify-generate` / `make lint-config` — pass.
- `make build` — `bin/manager` compiles.
- Kind / `make run` against a cluster — **not run**. Docker is not installed.

## Follow-ups / known gaps
- Install Docker Desktop (or another runtime) before the Kind smoke test:
  `make install` the CRD, `make run`, apply `config/samples/cascade_v1alpha1_cascadepolicy.yaml`,
  confirm a log line. That was in the original build order and is the only
  first-slice item still blocked on this machine.
- Next slice: Prometheus HTTP client behind `Query(ctx, promql) → Snapshot`,
  operator-level Prometheus URL flag. Not started.
- Istio CRD RBAC and patch code wait until the latency/error detector is wired.
- Full README (demo instructions) remains a later checklist item.
