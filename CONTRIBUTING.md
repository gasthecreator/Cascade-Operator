# Contributing to Cascade Operator

Thank you for your interest in contributing. This document covers how to work
in this repository day to day. For building, running, and live-demo setup, see
the [README](README.md) — that file is the source of truth for dev
environment instructions; this file does not duplicate them.

## Code of conduct

Participation is governed by [CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md).

## License

By contributing, you agree that your contributions will be licensed under the
[Apache License 2.0](LICENSE), matching the headers in source files.

## Branch and commit conventions

- Branch from `main` with a short, descriptive prefix when it helps review:
  `feat/…`, `fix/…`, `docs/…`, `chore/…`.
- Keep commits focused. One logical change per commit is preferred.
- Write commit messages in the imperative mood (`Add …`, `Fix …`, `Update …`)
  with a subject line that explains *why* the change matters, not just what
  files moved.
- Do not force-push to `main`. Branch protection on `main` (required reviews,
  status checks, etc.) is configured in **GitHub repository settings** — it
  cannot be enforced from a file in this tree. Ask a maintainer if you need
  those settings adjusted.

## Before you open a PR

```bash
make fmt
make lint
make test
```

If your change touches Istio patch paths or reconciliation wire-format,
also run against the dev Kind cluster:

```bash
make test-integration   # requires kind-cascade-operator context; see README
```

## How decisions and history are recorded

This repository keeps architecture decisions stable and records the reasoning
behind changes. Four documents have distinct jobs:

### `PLAN.md` — current state and roadmap

- Reflects what is decided and built now: architecture, patch matrix,
  checklist status.
- Do not edit **Architecture Decisions** (§2) or **Open Questions** (§4)
  directly in a feature PR; those sections change only after a proposal is
  reviewed.
- Checklist items in §3 and §5 may be flipped to `[x]` when the work is done,
  backed by a history entry.

### `PROPOSALS.md` — proposed changes to decided architecture

- File a proposal to change something already decided in `PLAN.md` (CRD shape,
  patch matrix, CI policy, multi-mesh direction, and so on).
- Proposals are reviewed before implementation. Approved proposals are merged
  into `PLAN.md`; rejected ones stay documented for context.
- Small bug fixes and tests do not need a proposal.

### `docs/adr/` — architecture decision records

- Write an ADR for any significant or hard-to-reverse decision (CRD shape,
  mesh adapter interfaces, security posture, anything costly to undo). Copy
  [`docs/adr/0000-template.md`](docs/adr/0000-template.md), take the next
  number, and add it to the index in [`docs/adr/README.md`](docs/adr/README.md).
- ADRs are immutable once accepted; to change one, add a new ADR that
  supersedes it.

### `docs/history/worklog/` — history and reasoning

- Each meaningful piece of work gets an append-only entry
  (`YYYY-MM-DD-short-slug.md`) explaining what, why, how, and what was tested.
- Index new entries in `docs/history/worklog/README.md`.

### Typical flow

1. **Architecture change?** Proposal, review, update `PLAN.md`, ADR if the
   decision is significant, implement, history entry.
2. **Implementation within existing architecture?** Branch, code, history
   entry, PR (use the PR template checklist).
3. **Review** rebuilds, runs the tests, and re-verifies any live-cluster
   claims before merge.

### `AGENTS.md`

[AGENTS.md](AGENTS.md) is a short guide to the repository layout, make
targets and generated files, for anyone (or any tool) working in the code.

## Pull requests

Use the [pull request template](.github/PULL_REQUEST_TEMPLATE.md). Link
related issues. For user-visible behavior changes, add a line to
[CHANGELOG.md](CHANGELOG.md) under `[Unreleased]`.

## Security

See [SECURITY.md](SECURITY.md) for vulnerability reporting — do not file
security issues publicly.

## Questions

Open a [feature request](.github/ISSUE_TEMPLATE/feature_request.yml) or
[bug report](.github/ISSUE_TEMPLATE/bug_report.yml) issue, or reach out via
the contact in [SECURITY.md](SECURITY.md) for sensitive topics.
