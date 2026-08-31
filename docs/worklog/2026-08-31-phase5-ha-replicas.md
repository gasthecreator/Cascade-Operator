# Phase 5 (2/4): HA — replicas: 2 + preferred pod anti-affinity

**Date:** 2026-08-31
**Author:** Claude
**Type:** infra

## What
`config/manager/manager.yaml`: `replicas: 1` → `2`, plus a preferred (not
required) `podAntiAffinity` so the two replicas prefer different nodes.

## Why
PLAN.md §5 Phase 5: leader election was already wired
(`--leader-elect`, confirmed present as a container arg) but the
Deployment only ever ran one replica — the plumbing for HA existed
without the HA it's meant to provide. Leader election is exactly what
makes running more than one replica safe: precisely one replica actively
reconciles at a time (holds the lease), the other stands by as a hot
spare, so a pod eviction or crash doesn't leave `CascadePolicy` unwatched
until a brand-new pod schedules and image-pulls from cold.

## How
- `replicas: 2`, with an inline comment explaining why this is safe only
  because leader election is already on (a reader shouldn't have to
  cross-reference the container args to understand why 2 replicas isn't a
  split-brain risk).
- `podAntiAffinity.preferredDuringSchedulingIgnoredDuringExecution`
  (weight 100, `topologyKey: kubernetes.io/hostname`), not `required` —
  a required anti-affinity rule on a single-node cluster (this project's
  own Kind dev cluster) would leave the second replica permanently
  `Pending` with no way to schedule, which is a worse outcome than no
  anti-affinity at all for a dev/demo context. Preferred still gets the
  real benefit (different nodes when more than one exists) without that
  failure mode.

## Files touched
- `config/manager/manager.yaml`
- `PLAN.md` — §5 Phase 5, second sub-item only

## Testing
- `./bin/kustomize build config/default` — builds cleanly, confirmed
  `replicas: 2` present in the rendered output.
- `make verify-generate` — no drift (this is a hand-maintained manifest,
  not a `controller-gen` output, so nothing to regenerate against, but the
  drift check itself still passes).
- `make test` — full suite unaffected (pure YAML change, no Go code
  touched): controller 79.7%, mitigation 90.3%, notify 91.3%, signatures
  94.1%, webhook 100%.
- Not deployed to the live dev cluster to confirm two pods actually come
  up and one wins the leader-election lease — that cluster is single-node
  (Kind), so this can't be meaningfully observed there (both replicas would
  land on the same node either way, and leader election's actual mechanics
  were already reviewed/trusted as controller-runtime's own well-tested
  feature, not something this project implements itself).

## Follow-ups / known gaps
- Phase 5's remaining two items (per-edge threshold overrides, security
  threat-model doc) are unstarted.
