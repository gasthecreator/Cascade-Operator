# Planning pass on PLAN.md open questions

**Date:** 2026-08-28
**Author:** Cursor
**Type:** docs

## What
Read PLAN.md, PROPOSALS.md, and the worklog convention end-to-end before any operator code. Wrote five pending proposals covering every Open Question in PLAN.md section 4, plus a CRD-shape correction that fell out of the Istio patch-target question. Did **not** run `kubebuilder init` — Open Question #1 still blocks codegen.

## Why
The working agreement is that Architecture Decisions and Open Questions are not edited from Cursor; recommendations go through PROPOSALS.md so Claude can review them before they land in PLAN.md. The first implementation slice (`kubebuilder init`, CRD types, empty reconciler) cannot start honestly until the API group/kind is locked, because renaming after deepcopy/CRD generation is the expensive part PLAN.md already called out.

## How
Treated each open question as something to resolve with a concrete contract, not a preference:

- CRD lock includes dropping `spec.targetVirtualService`, because the objects we patch hang off the *dependency* host, not the protected service. That is a 2.3 correction discovered while answering Open Question #3, so it lives in the CRD proposal rather than being silently assumed at init time.
- Demo topology: recommended a custom 3-service graph over Bookinfo because Bookinfo cannot induce fan-out amplification or a controllable retry storm without fighting the sample.
- Mitigation: proposed a per-signature matrix (outlier detection + timeout / retry budget + pool / connection-pool bulkhead) and restoration that loosens those same fields, instead of a traffic-weight ramp that is a different pattern.
- Metrics and CI: close "keep polling" and "lint+unit CI now, Kind later" so the scaffold slice has a defined metrics approach and a CI gate without pulling Istio into Actions.

Sequencing recommendation (not a PLAN.md change): one detector wired through the reconciler before building the other two; Kind+Istio+demo after one detect→mitigate loop, not before.

## Files touched
- `PROPOSALS.md` — five pending entries, one per open question
- `docs/worklog/README.md` — index this entry
- `docs/worklog/2026-08-28-planning-pass-open-questions.md` — this file

## Testing
N/A — documentation only. No Go module, CRD, or cluster exists yet.

## Follow-ups / known gaps
- Blocked on Claude/Gideon review of the five proposals, especially CRD group/kind (and whether `gideonsanni.dev` is the API-group domain string they want).
- Need explicit answers that are not in PLAN.md: Go module path (repo is `gasthecreator/Cascade-Operator`), whether a Kind cluster should be created on this machine later, Go version if they care beyond "follow kubebuilder."
- First scaffold slice starts on `feat/repo-scaffold` once Open Question #1 is approved.
