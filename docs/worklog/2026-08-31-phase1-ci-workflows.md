# Phase 1 CI: Kind+Istio integration workflow, govulncheck, CodeQL

**Date:** 2026-08-31
**Author:** Cursor
**Type:** infra

## What
Added three GitHub Actions workflows (PLAN.md §5 Phase 1):

- **`.github/workflows/integration.yml`** — PR-to-`main` + `workflow_dispatch`
  only. Creates Kind cluster `cascade-operator` (context
  `kind-cascade-operator`), runs `make istio-install` (wraps
  `hack/install-istio.sh`, Istio 1.30.4), applies CRDs, then
  `make test-integration` with `INTEGRATION_CONTEXT=kind-cascade-operator`.
  Separate from `test-e2e.yml` (plain Kind, no Istio).
- **`.github/workflows/govulncheck.yml`** — `govulncheck ./...` on every
  push and PR (same trigger pattern as `lint.yml` / `test.yml`).
- **`.github/workflows/codeql.yml`** — GitHub's standard Go CodeQL template
  (init/autobuild/analyze), pinned action SHAs, weekly schedule on Mondays.

No Makefile or Go source changes — `test-integration` target unchanged;
CI sets `INTEGRATION_CONTEXT` via workflow `env` to match Kind cluster name.

## Why
PLAN.md §5 Phase 1: run integration tests in CI with Istio in the loop;
add lightweight security scanning without slowing the every-push gate.

## How
- Reused Kind install pattern from `test-e2e.yml` (curl binary, linux arch).
- Istio install delegated to existing `make istio-install` rather than
  inlining install steps in YAML.
- `permissions: {}` at workflow top; per-job `contents: read` (+ CodeQL's
  `security-events: write` on the analyze job only).
- Pinned `actions/checkout` and `actions/setup-go` SHAs match existing
  workflows.

## Files touched
- `.github/workflows/integration.yml`
- `.github/workflows/govulncheck.yml`
- `.github/workflows/codeql.yml`
- `docs/worklog/README.md` — index this entry
- `PLAN.md` — §5 Phase 1 checklist (after verified green run)

## Testing
<!-- VERIFICATION SECTION UPDATED AFTER CI RUN -->

## Follow-ups / known gaps
- Integration workflow not on every push (by design — Istio install budget).
- Demo deployment images are not built in CI; integration test only needs
  Istio CRs + CascadePolicy CRD, not running demo pods.
