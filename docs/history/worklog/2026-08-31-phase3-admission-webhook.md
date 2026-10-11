# Phase 3: CascadePolicy admission webhook

**Date:** 2026-08-31
**Author:** Claude
**Type:** feature

## What
Added a validating admission webhook for `CascadePolicy` (PLAN.md §5 Phase
3), scaffolded with the real `kubebuilder create webhook --group cascade
--version v1alpha1 --kind CascadePolicy --programmatic-validation` command
(not hand-written boilerplate) and then implemented with a deliberately
narrow validation scope:

1. `spec.service` must not appear in its own `spec.dependsOn`.
2. `spec.dependsOn` must not contain duplicate entries.
3. `spec.service` and every `spec.dependsOn` entry must look like a
   plausible Kubernetes Service FQDN
   (`<name>.<namespace>.svc.cluster.local`).

## Why
Before writing any validation logic, re-read `api/v1alpha1/cascadepolicy_types.go`
in full. Every threshold field already carries a
`+kubebuilder:validation:Minimum`/`Maximum` marker, and `dependsOn`/`service`
already carry `MinItems=1`/`MinLength=1` — all enforced by the OpenAPI schema
at the apiserver *before* any admission webhook runs. Writing a webhook that
re-checks "thresholds are positive" or "dependsOn isn't empty" would be pure
duplication for zero additional protection, plus a new failure surface (cert
rotation, webhook downtime blocking every CascadePolicy write) for no real
gain — the original Phase 3 scope note ("field-level checks only") undersold
this; the honest scope is *cross-field/semantic* checks the CRD schema
structurally cannot express, which is what got built instead.

Self-dependency, duplicates, and malformed FQDNs are all real gaps: nothing
in the OpenAPI schema can express "field A must differ from every element of
field B," "list must be a set," or "string must match this specific shape,"
and a typo'd `dependsOn` host today only surfaces later as a silent
`DependencyObjectMissing` status condition instead of a rejected write.

## How
- Scaffold: `kubebuilder create webhook` generated
  `internal/webhook/v1alpha1/cascadepolicy_webhook.go` (+ test files),
  wired `cmd/main.go` (behind the existing `ENABLE_WEBHOOKS` env var
  escape hatch, standard kubebuilder pattern for local dev without a cert),
  uncommented the `[WEBHOOK]`/`[CERTMANAGER]` sections in
  `config/default/kustomization.yaml`, and added `config/webhook/`,
  `config/certmanager/`, `config/network-policy/allow-webhook-traffic.yaml`.
  `make manifests`/`make generate` regenerated RBAC/CRD/deepcopy — confirmed
  via `make verify-generate` that nothing drifted beyond the scaffold's own
  changes.
- `validateCascadePolicy` builds a `field.ErrorList` (not fail-fast) so a
  single bad write reports every violation at once, then wraps it with
  `apierrors.NewInvalid` — one caught mistake while writing this: initially
  used `field.Duplicate(...).WithOrigin("already listed at...")` for the
  duplicate-entry message, then checked `field.Error`'s actual doc comment —
  `Origin` is for tagging *which validation subsystem* produced an error
  (declarative vs. imperative), not a free-text detail string. Fixed by
  setting `.Detail` directly, the field actually meant for this.
- `ValidateDelete` is an intentional no-op — nothing about this CRD's
  semantics makes deletion unsafe to allow unconditionally.

## Files touched
- `internal/webhook/v1alpha1/cascadepolicy_webhook.go` — validation logic
- `internal/webhook/v1alpha1/cascadepolicy_webhook_test.go` — both
  direct-validator unit tests and real-admission-path tests (see Testing)
- `internal/webhook/v1alpha1/webhook_suite_test.go` — kubebuilder scaffold,
  unmodified
- `cmd/main.go`, `PROJECT`, `config/default/kustomization.yaml`,
  `config/network-policy/kustomization.yaml`, `config/webhook/`,
  `config/certmanager/`, `config/default/manager_webhook_patch.yaml`,
  `config/network-policy/allow-webhook-traffic.yaml` — scaffold-generated
  wiring, not hand-written
- `test/e2e/e2e_test.go` — scaffold's own regeneration touched this;
  diffed it, no semantic change beyond what codegen produced
- `PLAN.md` — §5 Phase 3 checklist only
- `docs/worklog/README.md` — index this entry

## Testing
- `go build ./...`, `gofmt -l .`, `go vet ./...` — clean.
- `make lint` — caught two real `goconst` violations while iterating
  (a repeated FQDN literal, then a repeated `"default"` namespace literal
  in the tests I added afterward) — both fixed by extracting constants,
  0 issues on the final pass.
- `make verify-generate` — no drift.
- `make test` (envtest, all packages) — **100% statement coverage** on
  `internal/webhook/v1alpha1`, full suite otherwise unaffected (controller
  79.3%, metrics 80.4%, mitigation 90.9%, signatures 94.1%).
- **The coverage number alone doesn't prove the webhook is correctly
  wired** — the first pass of tests called `CascadePolicyCustomValidator`'s
  methods directly, which only proves the validation *logic* is right, not
  that admission control actually reaches it (path/resource/group-version
  matching against the generated `ValidatingWebhookConfiguration`, TLS,
  registration). `webhook_suite_test.go`'s `BeforeSuite` already starts a
  real envtest API server with this exact webhook wired in via
  `envtest.WebhookInstallOptions` — specifically so it can be exercised this
  way — so three more tests were added that go through `k8sClient.Create`/
  `Update` instead of calling the validator directly: one confirms a real
  `Create` against the live (test) admission path is genuinely rejected
  (`apierrors.IsInvalid`), one confirms a valid object is genuinely admitted
  and persisted (fetched back via `k8sClient.Get`), one confirms rejection
  on `Update` too. All three pass, confirmed individually via
  `-args -ginkgo.v`, not just the aggregate `ok`.

## Follow-ups / known gaps
- **Not deployed to the persistent dev Kind cluster.** envtest's
  verification is real (genuine TLS, genuine admission wiring, genuine
  apiserver) but ephemeral, and not the same long-lived cluster the demo
  topology/k6 scripts/integration suite use. Deploying there needs either
  cert-manager (a new cluster dependency not yet installed — the scaffold's
  `config/certmanager/` manifests exist but nothing has applied them) or a
  manually-issued cert. Deliberately not attempted in this pass: doing it
  hastily risks live-cluster disruption (a misconfigured
  `ValidatingWebhookConfiguration` can block every `CascadePolicy` write,
  including the ones the other integration tests depend on) for a
  verification envtest already covers at the mechanism level. Flagged
  honestly rather than silently skipped or overclaimed as done.
- No live dependency-resolution check (confirming a `dependsOn` host
  actually resolves to a real Service/Istio object) — explicitly out of
  scope, would need a client call from inside the webhook; the reconciler's
  own `DependencyObjectMissing` condition already covers this asynchronously.
