# Two genuinely deferred deliverables closed: Phase 7's Claude Artifact copy, and a Tetragon reset k6 scenario

**Date:** 2026-09-04
**Author:** Claude (solo)
**Type:** feature + docs

## What
Asked to audit the entire project's history (PLAN.md, PROPOSALS.md, every
worklog's own Follow-ups section, CHANGELOG's Known gaps, source code
TODO/FIXME markers) for anything explicitly scoped but never actually
built — not just this session's own work. Found two genuine, real gaps
(distinct from the two remaining CHANGELOG "Known gaps" entries, which are
explicitly deliberate design choices, not deferred work):

1. **Phase 7's Claude Artifact copy of the visual cascade replay.** The
   original execution plan explicitly called for publishing
   `demo/replay/index.html`'s page "as a Claude Artifact too, for direct
   portfolio linking (separate from the repo-hosted copy)." The
   repo-hosted page was built and verified; the Artifact copy was flagged
   "not-yet-done" in that slice's own worklog
   (`2026-08-31-phase7-visual-cascade-replay.md`) and never revisited.
   Confirmed via `Artifact list` (zero published artifacts on this
   account) before building it, so this wasn't already done under a link
   I'd lost track of.
2. **A k6 scenario exercising Tetragon's `/control/reset`.** The TCP-reset
   fault-injection worklog
   (`2026-09-01-phase11-tcp-reset-fault-injection.md`) explicitly flagged
   "wiring a reset scenario into `demo/k6/`... is a reasonable next step"
   — confirmed via grep that none of the three existing k6 scripts ever
   called that endpoint.

## Why
User asked directly: "check the plan and necessary docs for any deferred
implementations... make this repo/project 100% complete against what we
scoped or planned, not just for this session, but the entire project."
PLAN.md's own checklist has zero unchecked items and PROPOSALS.md has zero
pending proposals, so the real audit had to be worklog-level ("Follow-ups"
sections narrate the state *at the time*, not current status — the
project's own worklog README says as much) — most "carried forward" items
from early worklogs (2026-08-28 through 2026-08-30) turned out to already
be resolved by later, subsequent slices (confirmed against PLAN.md's own
checklist, which tracks current status). These two were the ones that
never got a later slice.

## How
**Artifact copy**: `demo/replay/index.html`'s own script comment already
anticipated this exact need — "A separately-published Claude Artifact copy
of this same page embeds one trace's JSON inline instead, since an
artifact is a single sandboxed file with no ability to fetch sibling
files." Combined all three captured traces
(`demo/replay/traces/*.json`, ~133KB total) into one inline `TRACES` JS
object (~88KB) via a Python script (avoiding manually retyping/risking
corruption of that much JSON through a text-editing tool), swapped
`loadScenario()`'s `fetch('./traces/' + name + '.json')` for a direct
`TRACES[name]` lookup, and published — same design, same colors, same
topology/sparkline/diff-highlighting logic as the repo-hosted page, no
redesign (per the artifact-design skill's own "honor what's already
there" guidance: this is a distribution adaptation of an already-designed
page, not a fresh design brief). Live at
https://claude.ai/code/artifact/8285a2af-81d4-4493-bbed-9fe14e604775 —
private by default; making it publicly shareable is the user's own call
via the page's share menu, not assumed.

**k6 scenario**: `demo/k6/tetragon-reset.js`, modeled closely on
`latency-error-cascade.js` (same target dependency, `payments-service`,
same 20s-healthy/60s-induced/heal-at-80s timeline shape) but induces via
`/control/reset` instead of `/control/slow`. Doesn't assert a specific
response shape in `loadCheckout` the way the other scripts do (a reset
upstream call can surface to checkout's own caller as a non-2xx status, a
connection error, or a slow retry — genuinely disruptive, not fully
predictable, which is the point). `hack/run-k6-demo.sh`'s usage
string/comment updated to list it (the script itself needed no code
change — it already resolves any `demo/k6/<name>.js` by name).
`demo/k6/README.md` gets a new table row plus a dedicated "Tetragon
reset: kernel corroboration via k6" section, mirroring the existing
"Retry storm: mitigation confirmed" section's own style.

## Files touched
- `demo/replay/index.html` — comment updated with the real published
  Artifact URL.
- `PLAN.md` — Phase 7's checklist entry updated: the Artifact-copy
  deliverable closed, with the real URL and the "private by default"
  caveat.
- `demo/k6/tetragon-reset.js` — new k6 script.
- `hack/run-k6-demo.sh` — usage string/comment updated (both occurrences).
- `demo/k6/README.md` — new table row + dedicated section.
- This worklog entry + its index line.

## Testing
- Artifact: published successfully; validated locally before publishing
  that the embedded `TRACES` object parses as valid JSON with all three
  scenarios' full point arrays present, and that the file has no
  `<!DOCTYPE>`/`<html>`/`<head>`/`<body>` wrapper tags (Artifact tool
  requirement) before calling publish.
- k6 script: written and reviewed against the existing three scripts'
  established conventions; not yet run against the live cluster in this
  slice (see Follow-ups) — `bash -n`-equivalent isn't meaningful for a k6
  JS file, but the script's shape (executor config, function names,
  control-endpoint URLs) was checked by direct comparison against
  `latency-error-cascade.js`'s own already-verified structure.

## Follow-ups / known gaps
- `tetragon-reset.js` has not yet been run against the live cluster to
  confirm it actually produces `kernel_corroboration=true` in the
  operator's own logs (needs Tetragon installed, a healthy cluster, and
  the k6 Job to actually complete) — the immediate next step once the
  cluster is stable again.
