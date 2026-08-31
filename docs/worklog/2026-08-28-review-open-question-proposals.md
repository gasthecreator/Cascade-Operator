# Review: five open-question proposals

**Date:** 2026-08-28
**Author:** Claude
**Type:** docs

## What
Reviewed all five pending proposals on `feat/repo-scaffold` in `PROPOSALS.md`
(CRD lock, demo topology, Istio patch matrix, metrics closure, CI). Approved
all five as written, with one sequencing refinement taken from the patch-matrix
proposal itself (implement only the primary column for v1alpha1). Updated
`PLAN.md` §§2.1, 2.3, 2.4, 2.6, 2.7, added new §2.8 (CI), rewrote §3's intro
with a build-order note, and cleared §4 (all prior open questions resolved).
Moved all five proposals to Resolved in `PROPOSALS.md` with a `Resolved by
Claude` note each. Answered Gideon's three scaffold-blocking questions.

## Why
This is the review step the working agreement describes: Cursor raises
architecture questions in PROPOSALS.md instead of editing PLAN.md directly;
Claude evaluates and merges. Cursor's planning pass (see
`2026-08-28-planning-pass-open-questions.md`) correctly declined to run
`kubebuilder init` while Open Question #1 was still open, since renaming a
CRD group/kind after codegen means regenerating deepcopy and CRD YAML —
exactly the footgun PLAN.md had already flagged.

## How
Evaluated each proposal on its technical merits, not just "did it follow the
template":

- **CRD lock** — approved. The core insight (patch the *dependency's* Istio
  objects, not the protected service's) is correct and would have been a
  real bug if the original `targetVirtualService` field had shipped. Confirmed
  `cascade.gideonsanni.dev` as the API-group domain — a CRD group string
  doesn't need real DNS ownership, and it's already used consistently
  throughout the draft.
- **Demo topology** — approved. Bookinfo's graph shape genuinely can't
  produce disproportionate fan-out (reviews→ratings is 1:1), so it would have
  fought the actual test requirement.
- **Patch matrix** — approved, and took the proposal's own suggested
  narrowing: v1alpha1 implements only the latency/error-cascade primary
  (outlier detection). The reasoning that retry storms and fan-out need
  different knobs than outlier detection is correct — outlier detection
  ejects unhealthy pods, it doesn't stop Envoy retrying or cap concurrent
  calls. Restore-by-loosening-the-same-fields over a traffic-weight ramp is
  the right call: one state machine instead of two, and it can't clobber
  user-authored VirtualService routes.
- **Metrics closure** — approved, mostly formalizing what PLAN.md already
  said. Flagged the `response_flags=UR` retry-metric assumption as needing
  validation against a real Istio scrape before the retry-storm detector is
  written, per Cursor's own note — left that as a Kind-cluster validation
  item rather than resolving it by assumption now.
- **CI** — approved. Added as new PLAN.md §2.8 rather than folding into an
  existing section, since it's a real process decision on the same footing
  as the others, not just a checklist line.

Answered the three non-proposal blocking questions directly: API-group domain
confirmed (`gideonsanni.dev`), Go module path confirmed
(`github.com/gasthecreator/Cascade-Operator`, matching the existing remote),
and checked this machine for Kind/Docker — neither is installed here, so
that's a prerequisite to flag back to Gideon before the scaffold slice's Kind
smoke-test step can run, not something to assume.

## Files touched
- `PLAN.md` — §2.1 (Go confirmed), §2.3 (CRD locked, `targetVirtualService`
  dropped, `spec.mode` and status condition added), §2.4 (metrics decision
  closed with implementation detail), §2.6 (patch matrix + restore-by-loosen
  locked), §2.7 (demo topology locked), new §2.8 (CI), §3 intro (build-order
  note), §4 (cleared, all resolved)
- `PROPOSALS.md` — all five entries moved Pending → Resolved, each marked
  APPROVED with a `Resolved by Claude` note
- `docs/worklog/2026-08-28-review-open-question-proposals.md` — this file

## Testing
N/A — documentation/architecture review only, no code exists yet to test.

## Follow-ups / known gaps
- Docker/Kind are not installed on this machine. If Cursor's scaffold slice
  runs here too, Gideon needs to install Docker Desktop (or an alternative
  container runtime) before the Kind smoke-test step in Cursor's first-slice
  build order can run. Not something to install unattended — flagged back to
  Gideon rather than assumed.
- `response_flags=UR` for retry detection is still an assumption pending
  validation against a real Istio install — explicitly not resolved here,
  carried forward as a Kind-cluster validation item.
- Scaffold itself (kubebuilder init, CRD types, bare reconciler) is now
  unblocked and is Cursor's next step on `feat/repo-scaffold`.
