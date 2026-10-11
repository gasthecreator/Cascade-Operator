# Security Policy

## Supported versions

This project is under active development (v1alpha1 API). Security fixes are
applied on `main` only. There are no long-term release branches yet.

| Version | Supported          |
| ------- | ------------------ |
| main    | :white_check_mark: |

## Reporting a vulnerability

**Please do not open a public GitHub issue for security vulnerabilities.**

Use one of these channels:

1. **GitHub Security Advisories (preferred):** open a
   [private vulnerability report](https://github.com/gasthecreator/Cascade-Operator/security/advisories/new)
   on this repository if you have access to the GitHub UI.
2. **Email:** send details to **gideonsanni2023@gmail.com** with the subject
   `Cascade-Operator security`.

Include:

- A description of the issue and its impact
- Steps to reproduce (proof-of-concept if available)
- Affected versions or commits
- Any suggested fix, if you have one

## What to expect

| Step | Target |
|---|---|
| Acknowledgement of your report | within 3 business days |
| Triage (confirmed or not, severity, affected versions) | within 7 days of acknowledgement |
| Fix or mitigation | within 90 days of the report, sooner for high severity |
| Public disclosure | after a fix or mitigation is available, coordinated with you |

If a target will be missed we will tell you why and give a new date. We follow
coordinated disclosure: please do not disclose publicly before the fix is
released or the 90 days have passed, whichever comes first, unless we agree
otherwise. You can be credited in the advisory and the CHANGELOG under your
name or handle, or stay anonymous; say which in your report.

This is a single-maintainer project, so these are good-faith targets, not a
contractual SLA.

## Scope

In scope: this repository's operator code, CRD validation/webhook logic (when
present), RBAC manifests, and documented deployment paths.

See [docs/security-threat-model.md](docs/security-threat-model.md) for what
the operator's RBAC actually grants, its trust boundaries, and known gaps
stated plainly rather than assumed away.

Out of scope: vulnerabilities in upstream dependencies (report those to the
upstream project), misconfigurations in your own cluster, or issues in Istio /
Prometheus themselves unless this operator introduces a specific unsafe default.
