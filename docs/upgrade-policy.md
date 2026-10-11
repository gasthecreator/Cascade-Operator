# Upgrade and compatibility policy

## API stability

The only API is `cascade.gideonsanni.dev/v1alpha1` (`CascadePolicy`, namespaced).
**v1alpha1 makes no stability promise**: fields can be renamed, removed or
change meaning in any release before a `v1beta1`. The project does ship
additive changes whenever it can (for example `spec.mesh`, defaulting to
`Istio`, and `spec.thresholdOverrides`, were added without breaking existing
objects), and records breaking changes in [CHANGELOG.md](../CHANGELOG.md)
under the release that introduces them.

There is one served and stored version and no conversion webhook. When a
`v1beta1` is introduced, it will be added alongside `v1alpha1` with a
conversion path, and `v1alpha1` will be deprecated for at least one release
before removal; this is intent, not yet implemented.

Status fields (`phase`, `lastSignature`, `restoreStep`, conditions) are an
output of the operator and may gain values; do not parse them strictly.

## Upgrading the operator

Releases are tagged `vX.Y.Z`; each GitHub Release includes `install.yaml`
(CRDs, RBAC, webhook and Deployment pinned to a signed image digest) and
`checksums.txt`. Until the first tag exists, deploy from `main` with
`make deploy IMG=<image>`.

1. Read the CHANGELOG entries between your version and the target.
2. **Apply CRDs first.** `kubectl apply -f` the installer (or `make install`).
   Kubernetes does not upgrade CRDs through a Deployment rollout, and `kubectl
   apply` of a CRD is non-destructive: it does not delete existing
   `CascadePolicy` objects. Never `kubectl delete crd cascadepolicies...`
   to "refresh" it: deleting a CRD deletes every policy, and the operator's
   `original-*` annotations on mesh objects are then the only record left.
3. Roll out the manager (the installer does this). With two replicas and leader
   election, one replica always serves; state lives in `CascadePolicy.status`
   and in annotations on the patched mesh objects, so a restart resumes where
   it left off, including mid-restoration.
4. Verify: pods Ready, `kubectl get cascadepolicy -A` phases sensible, and the
   `Starting manager` / `reconciling CascadePolicy` log lines present.
5. Rollback is the reverse: re-apply the previous installer. Re-applying an
   older CRD schema makes the API server prune fields it does not know on the
   next write of each object, so rolling back past an additive field loses
   that field's values. Releases are meant to add fields rather than remove
   them, so an older operator reading a newer object ignores unknown fields;
   if a release states otherwise in the CHANGELOG, follow its notes.

Patched mesh objects are compatible across operator versions as long as the
`cascade.gideonsanni.dev/*` annotation keys stay the same; a release that
changes them will say so and describe the migration.

## Supported versions of the platform

These are the versions the project is built and tested against, taken from the
repository's pins. Anything else may work but is not exercised.

| Component | Version | Where it is pinned |
|---|---|---|
| Kubernetes API | 1.37 (client libraries `v0.37.0`, envtest 1.37.0) | `go.mod`, `Makefile` |
| controller-runtime | v0.25.1 | `go.mod` |
| Istio | 1.31.0 (`istio.io/api` and `istio.io/client-go` kept in lockstep with the control plane) | `hack/install-istio.sh`, `.github/workflows/integration.yml`, `go.mod` |
| Linkerd | edge-26.6.3 (with linkerd-viz for its Prometheus) | `hack/install-linkerd.sh`, `.github/workflows/integration.yml` |
| Go | per `go.mod` | `go.mod` |
| cert-manager (webhook TLS) | whatever `hack/deploy-operator.sh` installs | `hack/deploy-operator.sh` |
| Tetragon (optional kernel corroboration) | installed by `hack/install-tetragon.sh`; not exercised in CI | `hack/install-tetragon.sh` |

Policy for moving these pins: the Istio Go libraries and the control-plane
version move together in one change, with the integration suite run against the
new control plane (see the 1.31 upgrade entry in
`docs/history/worklog/2026-09-04-istio-1.31-upgrade.md`). Dependabot is
configured to avoid pre-release Istio Go modules for the same reason.

## Deprecation

Anything removed or renamed in the API, a flag, a metric or an annotation key
is listed under "Changed" or "Removed" in the CHANGELOG for the release that
does it. Metric names (`cascade_*`) and annotation keys are treated as
interfaces: they change only with a CHANGELOG entry.
