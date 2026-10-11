# Documentation follow-up: `docs/dev-linkerd.md`, and a closer look at the operator-metrics gap

**Date:** 2026-09-01
**Author:** Claude (solo — Cursor unavailable)
**Type:** docs + a small script generalization; no runtime source changed

## What
- `docs/dev-linkerd.md` (new): the Linkerd twin of `docs/dev-istio.md` —
  install, verify sidecars, deploy the Linkerd demo topology, generate
  traffic and query `linkerd-viz`'s Prometheus, the retry-storm
  `ServiceProfile` fixture, and the Tetragon TCP-reset disruption.
- `hack/query-prom.sh`: generalized to take `PROM_NAMESPACE`/
  `PROM_SERVICE` (default `istio-system`/`prometheus`, unchanged), so
  `docs/dev-linkerd.md` can point the same script at `linkerd-viz`'s
  Prometheus instead of needing a second copy of it.
- `README.md`/`CHANGELOG.md`: updated the two mentions of "no Linkerd dev
  doc yet" now that one exists, and rewrote the operator-metrics
  known-gap entry with the fuller scope described below.

## Why
A prior session turn audited PLAN.md's own checklist against the actual
repo (requested: "update the README/portfolio writeup... audit to
confirm we did not miss anything") and surfaced two real, if minor, loose
ends: no Linkerd-equivalent dev doc, and the operator's own metrics still
not scraped by either mesh's Prometheus. This closes the first outright
and re-scopes the second more precisely after digging into it further.

## The operator-metrics gap turned out to need more than a scrape-config line
The original audit summary characterized this as "needs a static
Prometheus job the same way Tetragon's was" — accurate as far as it went,
but incomplete. Checking `config/default/kustomization.yaml` and
`cmd/main.go` before writing anything found a second, prior blocker: the
operator has never actually been deployed in-cluster anywhere in this
project's history — every live-verification pass all session (and every
prior session) ran it via `go run ./cmd/main.go` from the host, which a
cluster-internal Prometheus can never reach regardless of scrape config.
Deploying it for real means resolving Phase 3's own already-documented
follow-up first: the webhook's TLS needs cert-manager (or a manually
provisioned cert) before `make deploy` will even come up cleanly, since
`config/default/kustomization.yaml` already has the webhook/certmanager
kustomize sections active.

Installing cert-manager is a genuine, new, persistent piece of
cluster-wide infrastructure — not a one-line config change — on a dev
cluster this project's own worklogs have repeatedly found to be
resource-constrained (Istio's and Linkerd's full stacks together have
already pushed even `kube-apiserver`/`kube-scheduler` into restart loops
at points this session). Deciding to add another permanent control-plane
component there is an infrastructure tradeoff worth a deliberate call, not
something to do as a side effect of "handle the loose ends" without
flagging it — so this slice re-describes the gap accurately (both
blockers, not just the scrape-config half) rather than attempting the
larger fix unprompted.

## Testing
- `bash -n hack/query-prom.sh` — clean.
- Ran the generalized script live against both `istio-system/prometheus`
  (default, unchanged behavior confirmed) and
  `PROM_NAMESPACE=linkerd-viz PROM_SERVICE=prometheus` (confirmed it
  reaches `linkerd-viz`'s Prometheus and returns real data).
- No Go source touched; `go build ./...` unaffected.

## Follow-ups / known gaps
- The operator-metrics scrape gap remains open, now accurately scoped:
  it needs a deliberate decision to install cert-manager (or provision a
  manual cert) and deploy the operator in-cluster before a scrape config
  addition would have anything real to point at.
- No standalone Tetragon dev-environment doc yet (its own install
  script's header comment and the Phase 11 worklogs cover it) — not
  addressed in this slice.
