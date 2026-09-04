# Three security-hardening items built; a real upstream Calico bug found and fixed along the way; full live verification incomplete

**Date:** 2026-09-04
**Author:** Claude (solo)
**Type:** feature + infra + honest incident report

## What
Closed all three items in `docs/security-threat-model.md`'s "Known gaps"
section that were genuine, actionable future work (not the two deliberate
CHANGELOG gaps, which aren't deferred work):

1. **Image signing/provenance** — `.github/workflows/publish-image.yml`:
   builds and publishes the manager image to GHCR, signs it keylessly with
   `cosign` via the workflow's own GitHub Actions OIDC identity, attaches
   SLSA provenance + an SBOM via `docker/build-push-action`'s own support.
2. **Egress `NetworkPolicy`** —
   `config/network-policy-egress/restrict-egress.yaml`: restricts the
   operator to DNS, the Kubernetes API server, each mesh's Prometheus, and
   (added mid-verification, see below) Linkerd's own control plane.
3. **Namespace-scoped RBAC** — `cmd/main.go`'s `--watch-namespaces` flag,
   `config/rbac-namespaced/role.yaml.tmpl` +
   `hack/generate-namespaced-rbac.sh`, `hack/switch-to-namespaced-rbac.sh`.

All three: written, `go build`/`go vet`/`go test ./... -race`/`make lint`
clean. Live verification is where this slice gets honest rather than
rounding up — see Testing below for exactly what did and didn't get
confirmed, and Follow-ups for what's still open.

## Why
Asked directly to audit the entire project for deferred implementation,
not just this session's work (see the separate
`2026-09-04-phase7-artifact-copy-and-tetragon-k6-scenario.md` worklog for
that audit's other two findings). `docs/security-threat-model.md`'s own
Known gaps section — written back in Phase 5, 2026-08-31 — named these
three explicitly and had never been revisited.

## How
**Image signing**: straightforward — `docker/build-push-action`'s
`provenance`/`sbom` inputs plus a `cosign sign --yes` step using the
workflow's own OIDC token, no secrets to manage. Every action pinned by
commit SHA (this repo's own convention), resolved via `gh api
repos/<repo>/commits/<tag>` rather than guessed.

**Namespace-scoped RBAC**: `cache.Options.DefaultNamespaces` (confirmed
the exact field name and semantics by reading controller-runtime
v0.24.1's own vendored source, not assumed from memory of an older API
shape) restricts the manager's watch; a Go template + `sed`-based
generator (not a kustomize plugin — simpler, and kustomize has no native
"repeat per namespace in a list" primitive) emits a `Role`+`RoleBinding`
pair per target namespace with the exact same rules
`config/rbac/role.yaml`'s `ClusterRole` grants (every one of that
`ClusterRole`'s rules is over a namespaced resource type, confirmed by
reading it directly — nothing inherently required cluster scope).

**Egress `NetworkPolicy`**: selectors for DNS (CoreDNS), the API server
(Kind's real static `kube-apiserver` pod), and each mesh's Prometheus were
all confirmed against real cluster objects
(`kubectl get svc ... -o jsonpath='{.spec.selector}'`) before writing the
manifest, not assumed. Checked CNI feasibility *before* writing this:
Kind's default CNI (`kindnet`) doesn't enforce `NetworkPolicy` at all —
confirmed, not assumed — so proving real enforcement needed
`hack/install-calico-for-policy.sh` (Calico in policy-only mode, layered
on top of kindnet's own pod networking, not replacing it).

## A real, previously-unknown Calico bug, found live
Installing Calico for policy triggered the cluster's third severe
instability incident this session — but unlike the first two (generic
resource contention, resolved by a `docker stop`/wait/`start` cycle),
`kubectl get pods -A` stayed stuck with ~20 pods in `Unknown` for 160+
seconds straight even after CPU settled into a normal range, which didn't
match the earlier pattern. Investigated rather than assumed the same
cause: `crictl ps -a` showed the actual container runtime *was* healthy
and active (control-plane components genuinely `Running`), and
`kubectl describe pod` on one stuck pod showed
`SandboxChanged (x44 over 12m)` — the pod's network sandbox being torn
down and recreated on a tight loop. `journalctl -u kubelet` gave the exact
mechanism: `plugin type="calico" failed (delete): error getting
ClusterInformation: connection is unauthorized:
clusterinformations.crd.projectcalico.org "default" is forbidden: User
"system:serviceaccount:kube-system:calico-cni-plugin" cannot get resource
"clusterinformations"...` — Calico's own CNI binary needs this permission
at pod-sandbox-teardown time, and its official v3.32.2 policy-only
manifest's `calico-cni-plugin` `ClusterRole` never grants it (confirmed
by diffing the live `ClusterRole` against the exact block in the
downloaded manifest — byte-identical; this is what upstream actually
ships, not a local corruption). Every pod-sandbox teardown across the
*entire* cluster was failing and retrying forever — the reason so many
unrelated pods looked "stuck," not resource exhaustion at all. Put to the
user directly (a genuine security-relevant RBAC change) rather than
patched unilaterally; approved. Fix: add `get`/`list`/`watch` on
`clusterinformations.crd.projectcalico.org` to that `ClusterRole` — now
in `hack/install-calico-for-policy.sh` so anyone re-running this hits the
fix, not the bug.

## A real gap in the NetworkPolicy's own first draft, found by real enforcement
With Calico's bug fixed and enforcement genuinely active, the operator's
Linkerd-mesh-injected replica (from the *prior* slice's
`hack/deploy-operator.sh` mesh-injection step — needed so the operator can
reach `linkerd-viz`'s locked-down Prometheus) got stuck: its own
`linkerd-proxy` sidecar's startup probe failed with a real 503/timeout.
This is exactly what real `NetworkPolicy` enforcement should do to traffic
the policy doesn't allow — and it's a genuine dependency the first draft
missed: the sidecar's own mTLS identity bootstrap talks directly to
`linkerd-identity`/`linkerd-destination`/`linkerd-policy`, independent of
anything a `CascadePolicy` configures. Confirmed the exact selectors/ports
(`linkerd.io/control-plane-component: identity`/`destination`, 8080/8086/
8090) against the real `linkerd` namespace's own Services, added the rule,
reapplied, confirmed the pod came up clean. This is a real, useful finding
that only surfaced *because* enforcement was genuinely active — it would
never have been caught by the schema-only "applies without error" bar the
same policy was shipped at before Calico was involved.

## The fourth incident, and where this slice stopped
Even after fixing the Calico bug, `kube-controller-manager` continued
crash-looping (leaderelection lost/stopped, repeatedly) — the same
generic aggregate-resource-exhaustion pattern from the session's first two
incidents, now recurring with Calico's own three extra pods
(`calico-node`, `calico-typha`, `calico-kube-controllers`) added on top of
an already-large stack (Istio, Linkerd, cert-manager, Tetragon, two demo
topologies, the operator) on an 8-core/8GB host. Put the choice to the
user plainly rather than guessing at another remediation: remove Calico,
keep pushing, or stop for the session. Chose to stop. The Kind node
container was stopped cleanly (not left crash-looping and burning
resources for no benefit) rather than left running in a degraded state.

## Files touched
- `.github/workflows/publish-image.yml` — new.
- `config/network-policy-egress/{restrict-egress.yaml,kustomization.yaml}` —
  new; `config/default/kustomization.yaml` activates it.
- `config/rbac-namespaced/role.yaml.tmpl` — new.
- `hack/generate-namespaced-rbac.sh`, `hack/switch-to-namespaced-rbac.sh`,
  `hack/install-calico-for-policy.sh` — new.
- `cmd/main.go` — `--watch-namespaces`/`WATCH_NAMESPACES` flag,
  `cache.Options.DefaultNamespaces` wiring.
- `Makefile` — three new targets (`switch-to-namespaced-rbac`,
  `calico-install`, alongside the existing `deploy-operator`).
- `docs/security-threat-model.md` — all three gaps updated with their
  real, current, honestly-stated status (not uniformly "resolved").
- `CHANGELOG.md` — the security-hardening entry rewritten to state each
  item's actual verification status individually, not claim uniform
  completion.

## Testing
- `go build ./...` / `go vet ./...` / `go test ./... -race` / `make lint`
  — all clean, for the code touched this slice.
- **Confirmed live**: Calico's own RBAC bug (the exact `journalctl`
  evidence above) and its fix (fresh errors stopped appearing within 15s
  of applying the patched `ClusterRole`; the whole cluster returned to
  all-`Running` within ~4 minutes after that). The `NetworkPolicy`'s own
  missing Linkerd-control-plane rule (the mesh-injected replica's
  `linkerd-proxy` sidecar failing its startup probe under real
  enforcement, then coming up clean once the rule was added).
- **Not confirmed live**: a deliberate, complete allow/deny pass/fail test
  of the egress `NetworkPolicy` (confirm each allow-listed destination
  succeeds *and* a non-allow-listed one genuinely times out); the
  namespace-scoped-RBAC switch (`hack/switch-to-namespaced-rbac.sh`)
  against the live cluster at all; the image-publish-and-sign workflow
  actually running once; `demo/k6/tetragon-reset.js` (written the same
  day, see the other worklog) against a live cluster. All four were
  planned for this slice and derailed by the fourth instability wave
  before being reached — stated here plainly rather than left implicit.

## Follow-ups / known gaps
- The four "not confirmed live" items above are the concrete next steps,
  once the dev cluster is stable again (possibly needing less running
  simultaneously, or a beefier host — this session's host is 8 cores/8GB,
  and the aggregate of everything now installed appears to be genuinely
  at or past that ceiling).
- The Calico upstream RBAC bug hasn't been reported to
  `projectcalico/calico` — a reasonable follow-up, not required to keep
  using the fix documented here.
- `docs/security-threat-model.md`'s three updated entries each say
  explicitly what's confirmed vs. still open — treat those as the source
  of truth for exactly where each item stands, not this worklog's own
  summary above.
