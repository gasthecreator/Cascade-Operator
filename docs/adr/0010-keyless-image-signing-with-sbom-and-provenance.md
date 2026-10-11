---
status: accepted
date: 2026-09-04
decision-makers: "@gasthecreator"
---

# 0010. Sign images keylessly with cosign and attach an SBOM and SLSA provenance

## Context and Problem Statement

The threat model listed "no image signing or provenance verification" as a
known gap. Consumers had no way to verify who built the manager image.

## Decision Drivers

- No private signing key to store, rotate or leak.
- Verifiable by anyone against a public transparency log.
- Reuse tooling already in the build step.

## Considered Options

- Leave images unsigned
- Key-based cosign signing with a stored private key
- Keyless cosign signing using the workflow's GitHub OIDC identity, with SBOM
  and provenance from `docker/build-push-action`

## Decision Outcome

Chosen option: "keyless cosign plus SBOM and provenance".
`.github/workflows/publish-image.yml` builds, pushes to GHCR and signs by
digest; `provenance: true` and `sbom: true` attach the attestations. It runs
on `v*` tags or manually via `workflow_dispatch`.

### Consequences

- Good: signatures verify with `cosign verify` against the workflow identity
  and Sigstore's public Rekor log.
- Bad: verification depends on the workflow path and OIDC issuer, so renaming
  the workflow changes the identity consumers must trust.

### Confirmation

The workflow was run for real; its first run failed on a mixed-case
repository name (`ghcr.io/gasthecreator/Cascade-Operator`), fixed by
lowercasing, which no local check could have caught.

## Pros and Cons of the Options

### Unsigned

- Good, because there is nothing to maintain.
- Bad, because provenance cannot be verified.

### Key-based cosign

- Good, because it works without OIDC.
- Bad, because a private key must be managed and can leak.

### Keyless cosign with SBOM and provenance

- Good, because there is no key material and the log is public.
- Bad, because it is tied to GitHub Actions.

## More Information

`docs/security-threat-model.md` (Known gaps);
`docs/history/worklog/2026-09-04-live-verification-completed.md`.
