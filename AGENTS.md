# Working in this repository

Short guide for anyone (or any tool) changing code here. For architecture see
[PLAN.md](PLAN.md) and [docs/adr/](docs/adr/README.md); for process see
[CONTRIBUTING.md](CONTRIBUTING.md).

## Structure

```
api/v1alpha1/            CascadePolicy CRD types (+kubebuilder markers)
internal/signatures/     Pure detectors: snapshot in, verdict out (no k8s/mesh imports)
internal/mesh/           QueryBuilder/Mitigator interfaces; istio/ and linkerd/ implementations
internal/controller/     Reconciler: metrics -> detect -> mitigate/restore
internal/webhook/        Validating admission webhook
cmd/                     Manager entrypoint; cmd/postmortem is a separate CLI
config/                  Kustomize bases; config/samples/ is the only hand-edited part
test/integration/        Kind-based tests (build tag `integration`)
hack/                    Dev-environment scripts
```

## Generated files: never edit by hand

- `config/crd/bases/*.yaml`, `config/rbac/role.yaml`, `config/webhook/manifests.yaml`
  (from `make manifests`)
- `api/**/zz_generated.*.go` (from `make generate`)
- `PROJECT` (from the kubebuilder CLI)

CI runs `make verify-generate` and fails on drift. Do not remove
`// +kubebuilder:scaffold:*` markers; the CLI injects code at them.

## Commands

```bash
make fmt vet lint     # format, vet, golangci-lint
make test             # unit + envtest (real API server and etcd), no cluster needed
make test-integration # needs a Kind cluster with Istio and Linkerd (see docs/dev-istio.md, docs/dev-linkerd.md)
make manifests generate   # after changing *_types.go or kubebuilder markers
```

Tests use Ginkgo/Gomega for controller and webhook suites and plain `testing`
elsewhere. Run e2e and integration tests only against a dedicated Kind
cluster, never a shared one.

## Conventions

- Keep reconciliation idempotent; re-fetch before updating objects.
- Log messages start with a capital letter, no trailing period, key/value
  pairs balanced (Kubernetes logging style).
- Operator patches carry the `cascade.gideonsanni.dev/managed-by` annotation;
  never modify mesh objects that do not.
- A change to detection, mitigation or restore behavior needs a history entry
  in `docs/history/worklog/`; a decision that is hard to reverse needs an ADR.

## Review expectations

Small, single-purpose changes with tests. Behavior changes in mitigation
paths are verified against the Kind cluster, not only with fake clients.
