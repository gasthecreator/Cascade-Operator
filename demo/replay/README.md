# Visual cascade replay

A self-contained static page (`index.html`, no build step, no external CDN)
that replays one real trip → mitigate → restore episode per signature,
captured from the live dev Kind cluster — not simulated data. This exists
to solve the portfolio's actual distribution problem, not to add a feature
to the operator itself: most reviewers will never `kubectl apply` this
repo, but will click a link (PLAN.md §5 Phase 7).

## Capturing an episode

Same live-cluster prerequisites as `demo/k6/README.md` (Kind + Istio +
Prometheus up, demo topology deployed). Unlike that doc's four-terminal
manual setup, `hack/capture-episode.sh` starts and tears down its own
Prometheus port-forward and operator process:

```bash
hack/capture-episode.sh latency-error-cascade
hack/capture-episode.sh retry-storm
hack/capture-episode.sh fanout-amplification
```

Each run polls the live `CascadePolicy` status and the affected
`DestinationRule`/`VirtualService`'s raw spec (via `kubectl get -o json` —
the same raw-JSON-not-typed-struct discipline `test/integration/` uses,
since the whole point is showing the actual patch, not a typed Go struct's
view of it) every 2 seconds for the scenario's ~170s timeline, and writes
`traces/<scenario>.json`.

## Viewing

`index.html` always fetches its trace data from `./traces/<scenario>.json`
via a relative `fetch()` — it needs to be served over HTTP, not opened via
`file://` (browsers block `fetch` of local files under that scheme):

```bash
cd demo/replay && python3 -m http.server 8934
# open http://localhost:8934/
```

Pick a scenario from the dropdown, then Play or drag the scrubber. The
topology graph, metric sparkline, phase/signature/restore-step readout, and
the live object's raw JSON (with fields that changed since the pre-trip
baseline highlighted) all animate in sync with the captured trace.

## Claude Artifact copy

A separate, artifact-published copy of this same page embeds one trace's
JSON data inline instead of fetching it — an Artifact is a single
sandboxed file with no ability to fetch sibling files. That copy is a
distinct deliverable from this repo-hosted version, built from the same
captured trace, not a replacement for it.
