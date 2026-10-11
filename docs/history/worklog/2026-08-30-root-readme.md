# Root README: the front door this repo has needed since the first checklist

**Date:** 2026-08-30
**Author:** Cursor
**Type:** docs

## What

Replaced the stock kubebuilder-scaffolded `README.md` with one that actually
orients someone opening this repo cold: elevator pitch, an architecture
table + mermaid diagram of the detect→mitigate→restore loop, setup
instructions for the Kind+Istio+demo environment, how to run the k6 demo
scripts and watch the operator react, current project status (linking
`PLAN.md`'s checklist rather than duplicating it), and a repo layout map.
Also fixed a stale claim in `demo/k6/README.md` found while cross-checking
this against what's actually true right now.

## Why

`PLAN.md`'s §3 checklist has carried `[ ] README (setup, architecture
summary, demo instructions)` unchecked since the very first slice. Nothing
wrong with that order — the project genuinely needed the three signatures,
the demo topology, and the k6 scripts to exist before a README describing
them could be anything but aspirational — but two sessions in a row hit
real Kind cluster instability under this machine's resource pressure
(documented in the retry-storm-mitigation-webhook-fix worklog), so this
was the right moment for a slice that's deliberately cluster-independent
and doesn't touch code.

## How

Read `PLAN.md` end to end plus `docs/dev-istio.md`, `docs/demo-topology.md`,
and `demo/k6/README.md` before writing anything, specifically to avoid
duplicating content that would drift — the README links to and summarizes
each of those in one or two sentences rather than re-explaining their
content. The one exception is the architecture table and mermaid diagram:
that's genuinely new (none of the existing docs have a single-glance
picture of the loop), not a duplicate of anything.

Kept the kubebuilder-boilerplate "deploy to a real cluster" instructions
(`make docker-build`/`make install`/`make deploy`) but compressed them to
one paragraph under Setup rather than the stock README's several sections
— for a portfolio piece whose own PLAN.md says "market viability is not a
goal," the full Kustomize/Helm distribution boilerplate the kubebuilder
scaffold generates by default (dist bundle, Helm chart instructions,
contributing template) wasn't worth keeping prominent. Dropped it rather
than migrate it verbatim; `make help` still surfaces every target for
anyone who needs it.

**Found and fixed one real inconsistency while cross-checking:**
`demo/k6/README.md`'s "Known gap: retry-storm's mitigation patch fails
against this exact fixture" section was written before this session's
earlier slice (`fix/retry-storm-mitigation-webhook`) actually fixed that
bug, and nothing had gone back to update it — so it was actively
contradicting `PLAN.md`'s already-updated caveat note. Rewrote that section
to reflect the fix, while being explicit that the fix itself hasn't been
re-confirmed live against this exact fixture yet (that session's Kind
cluster was unreachable under unrelated resource pressure) — same honesty
bar as everywhere else, not just marking it "resolved" and moving on.

Flipped `PLAN.md`'s `[ ] README` checklist line to `[x]` — a routine
current-state update per the file's own convention ("tracks current state
... don't let it drift"), not an architecture decision, so no
`PROPOSALS.md` entry.

## Files touched

- `README.md` — full rewrite: pitch, architecture table + mermaid diagram,
  setup, demo instructions, project status, repo layout, license (kept
  verbatim from the old file).
- `demo/k6/README.md` — "Known gap" section rewritten to "Resolved" now
  that the retry-storm webhook-rejection bug is fixed, with an explicit
  note that live re-confirmation against this fixture is still outstanding.
- `PLAN.md` — `[ ] README` → `[x] README` in the §3 checklist.

## Testing

Docs-only; nothing to run. Verified every file/path link in the new README
(`PLAN.md`, `config/samples/cascade_v1alpha1_cascadepolicy.yaml`,
`docs/dev-istio.md`, `docs/demo-topology.md`, `demo/k6/README.md`,
`docs/worklog/README.md`, `hack/run-k6-demo.sh`, `demo/k8s/cascadepolicy.yaml`,
`cmd/main.go`, `api/v1alpha1`) resolves to a real path in the repo with a
plain existence check, not just by eye.

## Follow-ups / known gaps

- The mermaid diagram renders on GitHub and in most Markdown previews, but
  wasn't rendered/screenshotted against this repo's actual GitHub remote as
  part of this slice (cluster-independent was the point, but this specific
  check didn't need a cluster — worth a quick look next time the repo is
  pushed/viewed there, flagging in case the syntax needs a tweak).
- `demo/k6/README.md`'s retry-storm section now says the fix is live-unconfirmed
  against that fixture — same open item as the fix's own worklog, not a new
  one; just surfaced in a second doc now that both exist and needed to agree.
