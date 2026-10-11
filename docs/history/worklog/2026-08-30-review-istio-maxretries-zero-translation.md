# Review: Istio maxRetries-zero translation investigation — approved, direction 2

**Date:** 2026-08-30
**Author:** Claude
**Type:** docs

## What
Reviewed and resolved Cursor's investigation-only slice on the pending
proposal filed after the zero-value patch fix: whether an explicit
`DestinationRule` `maxRetries: 0` can ever reach Envoy as `max_retries: 0`
through Istio 1.30.4. Approved direction 2 (`TripRetryStormMaxRetries = 1`)
and updated PLAN.md §2.6 and the status header myself, since this is a
resolution, not a routine implementation choice.

## Why
Same reviewer role as every slice, with the same standing bias toward not
trusting a citation at face value — the whole reason this slice exists is
that a previous live check wasn't deep enough, so a review that reads the
worklog's Go snippet and moves on would repeat that pattern one layer up.

## How
- Confirmed no operator code was touched: `git status` showed only
  `PROPOSALS.md`, `docs/worklog/README.md`, and the new worklog file — as
  promised, investigation only.
- **Independently re-derived the source claim rather than trusting the
  citation:**
  - Confirmed the cluster's actual running istiod image
    (`kubectl -n istio-system get deploy istiod -o jsonpath=...`) is
    `registry.istio.io/release/pilot:1.30.4`, and `pilot-discovery version`
    reports `GitTag:"1.30.4"`, revision `4220640be99e4cad69652d9eb2010bc5257f6a8e`
    — the citation's version claim matches what's actually deployed, not
    an assumed version.
  - Fetched `istio/istio`'s real `pilot/pkg/networking/core/cluster_traffic_policy.go`
    at tag `1.30.4` from GitHub directly (`raw.githubusercontent.com`) and
    grepped it myself: the FIXME at line 93
    (`// FIXME: there isn't a way to distinguish between unset values and
    zero values`), the guard at lines 119–120
    (`if settings.Http.MaxRetries > 0 { threshold.MaxRetries = ... }`), and
    the `math.MaxUint32` default at line 445 are all present, verbatim,
    exactly as cited (off by one line on the exact FIXME/guard numbering,
    which doesn't change the substance).
  - Fetched the same file from `master` and confirmed the identical
    `MaxRetries > 0` guard still exists (line 165) and that
    `applyRetryBudget` is a genuinely separate code path setting a
    `RetryBudget` field, not `MaxRetries` — both of Cursor's "ruled out"
    claims hold up.
- Read the rest of the worklog and the updated proposal text in full — the
  reasoning distinguishing this rejection-of-workaround situation from the
  earlier one (the "bump to 1 to dodge omitempty" idea rejected for the
  *previous* bug) is sound: that rejection was about not adopting a
  workaround when a correct fix existed for a bug in this project's own
  code; here there is no correct fix available through the DestinationRule
  API at all, only a different mitigation surface (EnvoyFilter) that the
  proposal itself correctly identifies as out of scope without a separate
  architectural decision.

## Verdict
**Approved — direction 2, `TripRetryStormMaxRetries = 1`.** The
investigation is accurate (independently confirmed against both the exact
deployed Istio version and current upstream `master`), correctly scoped
(no code change bundled with the investigation), and the recommended
direction is the right call for the reason already given in the proposal:
a proto field that cannot structurally distinguish "explicitly zero" from
"unset" makes `0` a permanent dead end for this specific field, not a
temporary gap. `1` is a real, enforced cap; the primary is unaffected.

## Files touched
- `PROPOSALS.md` — moved the entry to Resolved Proposals, marked APPROVED
  direction 2, added the independent-verification note
- `PLAN.md` — §2.6 matrix wording and the "separate gap" note in the
  two-object-kind section, plus the status header, updated to record the
  resolution and the `1` trip value
- `docs/worklog/2026-08-30-review-istio-maxretries-zero-translation.md` —
  this file
- `docs/worklog/README.md` — index this entry

## Testing
No code to test — this is a documentation/decision slice. Verification was
against Istio's actual deployed version and its real upstream source, both
fetched and read directly rather than assumed from the citation.

## Follow-ups / known gaps
- Implementing `TripRetryStormMaxRetries = 1` — the constant, its doc
  comment, the restore ramp's `from` anchor, `DetectOnly` logging, and the
  tests asserting the trip constant, per the proposal's "Impact if
  approved" — is the next Cursor slice.
- Once implemented, needs the same live re-verification standard as the
  marshal fix: raw stored object plus Envoy's actual `config_dump`,
  confirming `circuit_breakers.max_retries: 1` this time, not just that
  the Kubernetes object changed.
