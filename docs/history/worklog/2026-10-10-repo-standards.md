# Repo standards: ADRs, history cleanup, CI hardening, fuzz tests, runbooks

**Date:** 2026-10-10
**Author:** Claude (solo)
**Type:** docs + test + infra

## What
A set of repository-quality changes in five commits, with no change to
production behavior:

1. `docs/adr/`: MADR template, index and ten accepted ADRs distilled from
   PLAN.md, resolved PROPOSALS.md entries and the worklog.
2. The worklog moved to `docs/history/worklog/`; AGENTS.md cut to a short
   project guide; process wording in CONTRIBUTING made tool-neutral; a
   documentation map added to the README.
3. CI: Scorecard workflow, race detector and coverage summary in `test.yml`,
   Dependabot for the Dockerfile and a Go-module cooldown, a release workflow
   (`release.yml`, `hack/release-notes.sh`) that calls a now-reusable
   `publish-image.yml`, README badges.
4. Tests: fuzz targets and benchmarks for the signature detectors, failure-path
   tests for the Istio Mitigator and for `Reconcile`, and corrupt-annotation
   restore tests.
5. Runbooks, proposed SLOs, upgrade policy, SECURITY.md timelines and
   CODEOWNERS.

## Why
An audit found the repository strong on security and CI but weak on visible
decision records, front-door clutter, releases, failure-path test depth and
operational documentation.

## How
Notable choices, not obvious from the diff:

- `publish-image.yml` gained `workflow_call` and lost its tag trigger;
  `release.yml` calls it on `vX.Y.Z` tags. Keeping both triggers would have
  built and signed every tag twice. The keyless signing identity should
  continue to match the documented `publish-image.yml@.*` pattern, but this
  has not been verified: it needs a real tag run (see Follow-ups).
- `PROPOSALS.md` stayed at the repository root. It is referenced from about
  twenty Go source comments and from the generated CRD's field descriptions;
  moving it would mean editing code and regenerating API artifacts, which is
  outside a documentation-only change.
- Historical worklog entries are unchanged. They refer to the old
  `docs/worklog/` path in prose; the worklog README notes the move.
- Failure injection uses controller-runtime's `interceptor` client around the
  fake client's real store, so tests assert both the returned error and the
  state left behind. Each new test file was mutation-checked: a deliberate
  break in the production code made the relevant test fail, and the break was
  reverted.

## Files touched
See the five commits. New directories: `docs/adr/`, `docs/runbooks/`,
`docs/history/`. New workflows: `scorecard.yml`, `release.yml`.

## Testing
`make fmt vet lint` and `make test` pass after every commit; `actionlint`
passes on all workflows; every relative Markdown link resolves. Each of the
four fuzz targets ran for 20 seconds (about 27 million executions in total)
with no failure other than the finding below. `make build-installer` and
`hack/release-notes.sh` were run locally. The workflows themselves can only be
fully exercised by GitHub (Scorecard run, release on a tag).

## Follow-ups / known gaps
- **Finding from fuzzing (not fixed, production code was out of scope):**
  `DetectRetryStorm` and `DetectFanOut` return `Tripped: true` with
  `Confidence: NaN` when the multiplier is NaN. Their `<= 0` guard is false for
  NaN, and the later `ratio < multiplier` comparison is false too. Not
  reachable through the CRD (JSON cannot carry NaN, and the schema has a
  minimum), but it is a latent hole in a pure function. A fix is a one-line
  `!finite(in.Multiplier)` check; `DetectLatencyError` is unaffected because
  all its comparisons go false. The fuzz targets currently skip NaN
  multipliers and should drop that skip once fixed.
- Verify, on the first real tag, that the image signature identity still
  matches `publish-image.yml` when called through `release.yml`.
- The `[Unreleased]` section of CHANGELOG.md holds almost everything; promote
  it to a version section before tagging, since `release.yml` takes release
  notes from the section matching the tag.
- Observability gaps named in `docs/slo.md` (detection-to-mitigation latency,
  policy phase gauge) need new metrics.
