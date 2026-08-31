# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html)
once tagged releases begin. Until then, `[Unreleased]` tracks `main`.

## [Unreleased]

### Added

- Standard repository hygiene files (LICENSE, CONTRIBUTING, CODE_OF_CONDUCT,
  SECURITY, CODEOWNERS, CHANGELOG, `.editorconfig`, GitHub templates,
  Dependabot) — PLAN.md §5 Phase 0.

## [0.1.0] - 2026-08-31

First portfolio-complete milestone: three cascade signatures detectable from
Prometheus, mitigatable via Istio patches, and restorable through a shared
ramp — plus demo topology, k6 scripts, and Kind-based integration coverage
for retry storm wire-format.

### Added

- **Project foundation:** Kubebuilder scaffold, `CascadePolicy` v1alpha1 CRD,
  logging reconciler, golangci-lint CI gate, and initial planning docs
  (`PLAN.md`, `PROPOSALS.md`, worklog convention).
- **Metrics layer:** Prometheus HTTP client and PromQL helpers feeding a
  snapshot-based detector interface.
- **Latency/error cascade:** Detector, `DestinationRule` `outlierDetection`
  primary patch, gradual restoration ramp, and `VirtualService` timeout
  secondary (two-object-kind trip + restore).
- **Retry storm:** Detector, `VirtualService` `retries.attempts` primary,
  `DestinationRule` `connectionPool.http.maxRetries` secondary, two-object-kind
  restoration, and signature-dispatched restore machinery.
- **Fan-out amplification:** Detector, `DestinationRule` `connectionPool.http`
  bulkhead primary, restoration, and cross-host PromQL.
- **Signature handoff:** Force-complete outgoing signature restore before
  adopting an incoming one on a shared Istio object.
- **Operator metrics:** Counters for signatures detected, patches applied,
  and restorations completed/regressed.
- **Dev environment:** Kind + Istio 1.30.4 install scripts, Prometheus
  scrape path, and `docs/dev-istio.md`.
- **Demo topology:** Checkout → {payments, inventory} microservices,
  `CascadePolicy` fixture, inventory retry `VirtualService`, and deploy
  scripts (`make demo-deploy`, port-forward helper).
- **k6 cascade simulations:** In-cluster Job scripts for all three signatures
  with live-evidence worklogs.
- **Integration tests:** `test/integration/` (`make test-integration`) —
  real reconciler against dev Kind cluster, unstructured JSON assertions for
  retry storm `attempts:0` and `maxRetries:1` trip/restore.
- **Root README** with architecture summary and demo instructions.

### Fixed

- **Retry-storm webhook rejection:** Clear `retryOn` / `perTryTimeout` /
  `backoff` alongside `attempts: 0` so Istio's validating webhook accepts
  the trip patch.
- **Zero-value serialization:** Patch-based writes for `attempts: 0` and
  (formerly) `maxRetries: 0` so `omitempty` does not strip explicit zeros from
  stored JSON.
- **Istio `maxRetries: 0` translation:** Trip value changed to `1` — Pilot
  treats explicit zero as unset; Envoy never received a real circuit-breaker
  cap at zero.
- **Retry-storm secondary overlap:** Dropped `http1MaxPendingRequests` from
  retry storm's `connectionPool.http` secondary; fan-out retains ownership of
  pending-request fields.
- **Signature-handoff restore:** Outgoing signature fields and annotations no
  longer orphaned when a different signature trips on the same object mid-ramp.

### Changed

- Retry-storm `connectionPool.http` secondary now writes only `maxRetries`
  (trip value `1`, not `0`).
- `demo/k6` documentation updated to reflect working retry-storm mitigation
  (supersedes earlier "patch broken" gap notes).

### Known gaps (documented, not yet fixed)

- Retry-storm restore completion may still hit the zero-value `omitempty` hole
  when the captured original was literally zero (PLAN.md §5 Phase 5).
- Integration coverage exists for retry storm only; latency/error and fan-out
  Kind tests are planned (Phase 2).
- Kind+Istio integration tests are not yet run in GitHub Actions CI (Phase 1).

[Unreleased]: https://github.com/gasthecreator/Cascade-Operator/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/gasthecreator/Cascade-Operator/releases/tag/v0.1.0
