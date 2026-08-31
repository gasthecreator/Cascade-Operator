# Phase 7: visual cascade replay

**Date:** 2026-08-31
**Author:** Claude (solo — Cursor unavailable)
**Type:** feature (new static page + capture script, no runtime/operator behavior change)

## What
- `demo/replay/index.html` — a single self-contained page (no build step,
  no external CDN, inline CSS/JS) that replays a captured
  trip→mitigate→restore episode: an SVG topology graph
  (`checkout → {payments, inventory}`) that colors the affected node/edge
  by phase (green/red/amber), a metric sparkline with a scrub cursor, the
  `Phase`/`Signature`/`RestoreStep` readout, and the affected object's raw
  JSON spec with fields changed since the pre-trip baseline highlighted.
  Play/pause/scrub controls, a scenario dropdown.
- `hack/capture-episode.sh <scenario>` — runs a k6 scenario against the
  live dev cluster while polling the `CascadePolicy` status and the
  affected `DestinationRule`/`VirtualService`'s raw spec every 2s, writing
  `demo/replay/traces/<scenario>.json`.

## Why
PLAN.md §5 Phase 7 exists to solve the portfolio's actual distribution
problem — most reviewers will never `kubectl apply` this repo, but will
click a link. Capturing real cluster data rather than fabricating a trace
matters for the same reason every other "live-verified" claim in this
project's history has mattered: the point is proof, not a plausible-
looking mockup.

## How
- The page always `fetch()`es `./traces/<scenario>.json` (must be served
  over HTTP — `python3 -m http.server`, documented in
  `demo/replay/README.md` — browsers block `fetch` of `file://` URLs). A
  separate, later Claude Artifact publish of this same page would embed
  one trace's data inline instead, since an Artifact can't fetch sibling
  files — noted as a distinct future deliverable, not built in this slice.
- Verified the rendering logic first against a small hand-written
  synthetic trace (topology coloring across all three phases, JSON-diff
  highlighting, play/scrub) before trusting it with real data — caught and
  fixed two real bugs this way: the diff-highlight's contrast was too
  faint (bumped `.json-changed`'s opacity/box-shadow), and confirmed the
  topology color classes were applying correctly despite a screenshot
  rendering artifact that initially looked like a bug (verified via
  `getComputedStyle`, not just a screenshot).
- **Real bug found capturing actual data, not the synthetic trace**: the
  final JSON-assembly step's Python one-liner used an f-string containing
  an escaped backslash (`f"...{os.environ[\"CAP_OUT\"]}..."`), which
  Python's f-string grammar rejects — crashed after a full ~170s capture
  run, leaving only the raw `.tmp` array file. Fixed by extracting the
  environment lookup to a local variable before the f-string. Recovered
  the already-captured data by re-running just the assembly step against
  the intact `.tmp` file rather than re-capturing from scratch.
- **Second real bug, also found against real data**: Prometheus can
  return `NaN` for a query with no matching series yet (e.g. before load
  ramps up); the capture script's `float(mv)` happily produces Python
  `nan`, and `json.dump` serializes that as the bare token `NaN` — valid
  for Python's own (lenient) json module, but **not valid JSON** by
  strict spec, meaning any browser's `fetch().json()` would throw on that
  trace file. Fixed by explicitly checking `f == f` (false only for NaN)
  before accepting a parsed float, converting to `null` otherwise — caught
  by deliberately checking with `JSON.parse` (Node) rather than only
  Python's own tolerant reader.

## Files touched
- `demo/replay/index.html`, `demo/replay/README.md` (new)
- `hack/capture-episode.sh` (new)
- `demo/replay/traces/*.json` (generated, real captured data)

## Testing
- `bash -n` on the capture script; the rest of this phase is browser-side,
  not Go, so no `go test`/`go vet` involvement (confirmed the rest of the
  repo's Go tests/lint are unaffected by this phase's changes).
- **Live-verified in-browser**, not just eyeballed: served the page via
  `python3 -m http.server`, drove it through the Browser tool — confirmed
  scenario loading, play, scrub-to-arbitrary-point, topology color classes
  (checked via `getComputedStyle`, not just a screenshot, after a
  screenshot artifact initially looked wrong), the metric sparkline
  cursor, and the JSON-diff highlight — first against a synthetic trace to
  validate the rendering logic, then against a real captured
  `latency-error-cascade` episode from the live Kind cluster to confirm
  the whole pipeline end to end.
- Confirmed the captured trace file is strict-JSON-valid (`node -e
  "JSON.parse(...)"`, not just Python's lenient reader) after the NaN fix.

## Follow-ups / known gaps
- Traces captured for all three signatures (`latency-error-cascade`,
  `retry-storm`, `fanout-amplification`) — see each capture's own live
  run for scenario-specific notes if anything about it differs from the
  latency-error-cascade case described above.
- The Claude Artifact copy (embedding trace data inline) is a separate,
  not-yet-done deliverable per PLAN.md §5 Phase 7's own text — this
  worklog covers the repo-hosted page only.
