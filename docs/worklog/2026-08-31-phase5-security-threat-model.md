# Phase 5 (3/4): security threat-model doc

**Date:** 2026-08-31
**Author:** Claude
**Type:** docs

## What
`docs/security-threat-model.md` (PLAN.md §5 Phase 5), linked from
`SECURITY.md`'s Scope section. Documents what the operator's RBAC actually
grants, its real trust boundaries, and known gaps — stated plainly, not
assumed or glossed over.

## Why
PLAN.md §5 Phase 5's last documentation item. `SECURITY.md` (Phase 0)
already covers vulnerability *reporting process*; nothing yet covered
*design* — what this operator can actually read/write and where an
attacker's leverage would actually come from.

## How
Every factual claim in the doc was checked against the actual generated
manifests and source, not written from memory or assumption:

- **RBAC table** — read `config/rbac/role.yaml` directly (already
  characterized this exact way earlier in the session's Phase 3
  planning), and separately confirmed `config/rbac/role_binding.yaml` is a
  `ClusterRoleBinding` (not a namespaced `RoleBinding`) before writing the
  "cluster-scoped" claim in Known gaps — didn't assume kubebuilder's
  default scaffold behavior, checked the actual committed file.
- **Metrics endpoint security** — read `cmd/main.go` directly to confirm
  `--metrics-secure` defaults to `true` and wires
  `filters.WithAuthenticationAndAuthorization` when secure, rather than
  restating a general kubebuilder-scaffold assumption.
- **Notify webhook trust boundary** — traced through Phase 4's own design
  (the URL is operator-level flag/env config, never CRD-influenceable) to
  state precisely why a malicious `CascadePolicy` can't redirect the
  operator's outbound notifications.
- **Known gaps section** deliberately repeats, rather than re-litigates,
  two gaps this session already found and recorded elsewhere (webhook not
  deployed to the persistent dev cluster, Phase 3; the cluster-scoped
  `ClusterRole`) — a threat model that quietly omitted an already-known gap
  to look more complete would be actively misleading, so it's named here
  too even though it isn't new information.

## Files touched
- `docs/security-threat-model.md` — new
- `SECURITY.md` — one-line link addition to the new doc
- `PLAN.md` — §5 Phase 5, third sub-item only

## Testing
Documentation only — no code changed. Every factual claim was verified
against the actual repository state (manifests, source) as part of writing
it, per the "How" section above, rather than tested via `go test`.

## Follow-ups / known gaps
- Phase 5's last remaining item is per-edge threshold overrides — the
  breaking v1alpha1 CRD change, which needs its own `PROPOSALS.md` entry
  per this project's own protocol before implementation, not routine work.
- The threat model's own "Known gaps" section lists real, not-yet-closed
  items (namespace-scoped RBAC, webhook deployment, egress `NetworkPolicy`,
  image signing) — none tracked as new PLAN.md checklist items, since none
  were asked for as part of Phase 5's scope; recorded here for whoever
  picks up further hardening work later.
