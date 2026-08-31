# Review: root README

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed `docs/root-readme` — the new root `README.md` plus a consistency
fix to `demo/k6/README.md` — by reading the content closely and verifying
every referenced path actually exists.

## Why
Same reviewer role as every slice, calibrated to the actual risk here: no
code changed, so the review is about accuracy and whether it actually
orients a cold reader, not build/test/lint.

## How
- Diffed `PLAN.md`: exactly one line, the README checklist item flipped —
  correctly scoped, no architecture-section edit.
- Read the full `README.md`: pitch, architecture table + mermaid diagram,
  setup, demo instructions, project status, repo layout. Confirmed it does
  what a README should — orients a cold reader — without duplicating
  content that would drift: `PLAN.md` §§1/2.3/2.6 are linked and summarized
  in a sentence each, not re-explained; the project-status section
  explicitly declines to keep a duplicate checklist in sync and points at
  the live one instead.
- Verified every file path the README references actually exists in the
  repo (`PLAN.md`, `config/samples/cascade_v1alpha1_cascadepolicy.yaml`,
  `docs/dev-istio.md`, `docs/demo-topology.md`, `demo/k6/README.md`,
  `docs/worklog/README.md`, `demo/k8s/cascadepolicy.yaml`,
  `PROPOSALS.md`) — a broken link in a document whose whole job is
  orientation would be a real defect, and there wasn't one.
- Checked the mermaid diagram against the actual state machine
  (`Normal → Tripped → Restoring(0-4) → Normal`, regression during
  `Restoring` back to `Tripped`) — matches the real implementation, not a
  simplified/wrong version of it.
- Read the `demo/k6/README.md` diff: the "Known gap" section correctly
  updated to reflect the webhook-rejection fix landing earlier this
  session, while explicitly preserving the "not yet re-confirmed live"
  caveat rather than overclaiming it's fully verified — consistent with
  what I flagged in my own review of that fix.
- No `go build`/`test`/`lint` run — no Go files in this diff (confirmed via
  `git diff --stat`), so there's nothing for those to check.

## Verdict
**Approved, no changes requested.** Genuinely useful front door for the
repo, honest about what's still open, and internally consistent with the
rest of the documentation it links to and summarizes.

## Files touched
- `docs/worklog/2026-08-30-review-root-readme.md` — this file
- `docs/worklog/README.md` — index this entry

## Testing
See "How" above — link verification and content cross-checking, no code
to build/test.

## Follow-ups / known gaps
- Remaining Istio patch secondaries, operator's own Prometheus metrics, and
  the Kind-based integration test suite are the only checklist items left
  open, all correctly reflected as such in the new README.
- Retry-storm mitigation's live re-confirmation is still outstanding
  (tracked in two places now, consistently: the fix's own worklog and
  `demo/k6/README.md`).
