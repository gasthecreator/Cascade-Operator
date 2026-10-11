# Repo init and PLAN.md

**Date:** 2026-08-28
**Author:** Claude
**Type:** infra, docs

## What
Investigated the actual state of the Cascade Operator project (repo was
claimed to already exist), cloned the GitHub repo locally, and wrote the
initial `PLAN.md` establishing goal, architecture decisions, a build
checklist, and open questions.

## Why
Gideon asked me to act as planner/architect/reviewer for this project and to
create a living PLAN.md as the first deliverable, but first to verify the
repo's actual state rather than assume it matched the brief.

## How
- Searched the local filesystem (`/Users/gsanni`) for any existing Cascade
  Operator code — found none. The only unrelated local git repo
  (`Documents/New project`) turned out to be an unrelated folder of Python
  homework assignments.
- Checked Notion (`Project Portfolio` page) — confirmed Cascade Operator is
  tracked there as `⏳ Planned`, consistent with nothing being built yet.
- Checked GitHub via `gh repo list` / `gh repo view` — found
  `gasthecreator/Cascade-Operator`, created 2026-04-24, but `isEmpty: true`,
  0 disk usage, no default branch, never pushed to since creation.
- Cloned it to `/Users/gsanni/cascade-operator` (`gh repo clone`).
- Decided **Go** over Rust for the implementation language: `controller-runtime`
  + `kubebuilder` is the standard tooling for Kubernetes operators (CRD/deepcopy
  codegen, informer caching, leader election, admission webhooks all
  first-class), versus Rust's much smaller `kube-rs` pattern library for this
  exact problem shape. Given the project's goal is interview defensibility on
  distributed-systems/reconciliation-loop understanding — not a
  systems-programming showcase — Go keeps the conversation on the actual
  cascade-detection design instead of tooling justification.
- Wrote `PLAN.md` covering: goal, the three failure signatures, six
  architecture decisions with rationale (language, scaffold tool, CRD shape,
  metrics approach, decoupled detection engine, gradual-restoration mitigation,
  local dev/test setup), a checklist (everything unchecked), and five open
  questions (CRD naming, demo topology choice, which Istio object to patch per
  signature, custom-metrics-API vs. polling, CI timing).
- Committed `PLAN.md` as the repo's first commit on `main`.

## Files touched
- `PLAN.md` — created, full initial content

## Testing
N/A — documentation only, no code to test yet.

## Follow-ups / known gaps
- `PLAN.md`'s Go decision and CRD draft shape are marked as pending Gideon's
  explicit sign-off in the plan itself.
- Nothing has been pushed to `origin/main` yet — held pending explicit
  go-ahead per the push-permission rule.
- No repo scaffold (`kubebuilder init`, `go.mod`, CI) exists yet — that's the
  first item on PLAN.md's checklist and is Cursor's work per the working
  agreement, not something built in this session.
