# Review: Phase 1 CI workflows — approved, verification independently confirmed

**Date:** 2026-08-31
**Author:** Claude
**Type:** docs

## What
Reviewed and approved Phase 1 (PLAN.md §5): `.github/workflows/integration.yml`,
`govulncheck.yml`, `codeql.yml`, plus the `go.mod`/`go.sum` dependency bumps
required to make govulncheck pass. Unlike prior slices this one made a
concrete, checkable claim — "verified green in GitHub Actions" — so the
review focused on confirming that claim independently rather than reading
the YAML and trusting the report.

## Why
Same standing bias this whole session: a claim that something passed CI is
only as good as someone re-checking it against the actual CI system, the
same way every live-cluster claim in the retry-storm thread got
independently re-run rather than accepted from a pasted transcript.

## How
- `gh pr view 1 --json ...` — confirmed PR #1 (`ci/phase1-workflows` → `main`)
  is real, open, and mergeable.
- Checked the exact run IDs cited in the report
  (`33366623351`/`33366623362`/`33366623352`) via `gh run view` —
  independently confirmed `conclusion: success` for Integration Tests,
  govulncheck, and CodeQL, all at the reported timestamp. Not fabricated.
- Checked the PR's *current* status-check rollup (as of my own review,
  after my own unrelated `PLAN.md` commit landed on the same branch and
  triggered a fresh CI round): Integration Tests, both Lint runs, both Tests
  runs, and both govulncheck runs all `SUCCESS`; CodeQL still `IN_PROGRESS`
  at review time. The duplicate Lint/Tests/govulncheck entries are expected,
  not a bug — those three workflows trigger on both `push` and
  `pull_request`, so a PR branch commit fires both independently.
- Read `govulncheck.yml`'s actual fix: `actions/setup-go`'s `go-version:
  "1.26.6"` pinned explicitly, with a comment explaining `go.mod` itself
  stays at `1.26.0` — this is the workflow's own toolchain, not the
  project's minimum Go version, a real and correctly-scoped distinction.
- Confirmed `PLAN.md` §5 Phase 1 is flipped to `[x]`.
- Read the worklog's Testing section in full — it was an empty placeholder
  (`<!-- VERIFICATION SECTION UPDATED AFTER CI RUN -->`) the last time I
  looked at this file; it's now filled in with a real table of run links,
  durations, and an honestly-reported transient CodeQL `ECONNRESET`/rerun
  rather than a silently-omitted retry. This is exactly the standard this
  project has held throughout.

## Verdict
**Approved.** The CI wiring is correctly scoped (fast lint/test/govulncheck
gate stays on every push; the heavier Kind+Istio integration job is
PR/dispatch-only), the govulncheck fix is a real, minimal, well-explained
toolchain pin rather than a workaround that papers over something, and the
"verified in real CI, not locally" claim held up under independent
re-checking.

## A separate, important note — not a defect, a decision point
PR #1 is not a small CI-only PR. `git log --oneline main..ci/phase1-workflows`
shows **46 commits** — this branch carries the entire project's history
since the repo scaffold, because `main` has never absorbed any of this
project's work before now (confirmed: `main`'s tip is still the very first
"Add change-proposal queue and engineering worklog" commit). Merging PR #1
as it stands would be the first time everything built this session — all
three signatures, every mitigation/restore fix, the integration suite, the
repo hygiene files, and now this CI wiring — lands on `main` in one shot.
That may well be exactly the right call now that CI can actually gate it,
but it's a big enough action that it belongs in front of the user
explicitly rather than assumed as a natural next step of "Phase 1 is green."

## Files touched
- `docs/worklog/2026-08-31-review-phase1-ci-workflows.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — every claim independently re-checked against the real
GitHub Actions API, not re-derived from the pasted report alone.

## Follow-ups / known gaps
- Whether/when to merge PR #1 to `main` is an open decision, raised
  separately with the user — not resolved in this review.
