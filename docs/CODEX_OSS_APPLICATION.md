# Codex for Open Source application draft

Draft only, checked against the [official application form](https://openai.com/zh-Hans-CN/form/codex-for-oss/) and [program overview](https://developers.openai.com/community/codex-for-oss) on 2026-10-21. The form requires a public repository and public GitHub profile. Application readiness and acceptance are not established. Do not submit before publication and resolution of the release blockers.

## Identity and repository

| Field | Value |
| --- | --- |
| First / last name | 待我填写 |
| Email associated with ChatGPT | 待我填写 |
| GitHub username | light8771 (verified through the maintainer-authorized GitHub CLI login) |
| Public GitHub repository URL | https://github.com/light8771/clipvault |
| Maintainer role | Repository owner: light8771; personal identity must be supplied privately by the owner |
| OpenAI Organization ID | 待我填写 |
| Requested support | Proposed: API credits and Codex Security; owner must select |
| Stars / monthly downloads / external users | No verified public data available |

The public GitHub username was verified through the authorized login. No private email or Organization ID was read or inferred from local private files or Git author metadata. The owner must supply those fields in a private application copy; do not put private account identifiers in the public repository.

## Three short form fields (ASCII; each at most 500 characters)

### Why is this repository eligible?

ClipVault is preparing its first public release. It is a Windows clipboard manager with encrypted DuckDB storage, safe rich-text previews, and token-scoped read-only HTTP/MCP access. There are no verified public stars, downloads, or external adoption metrics yet. Its potential value is a locally controlled, auditable bridge between clipboard history and developer tools; broad ecosystem impact is not yet demonstrated.

### How would you use API credits for your project?

I plan to use API credits for maintainer-reviewed pull request checks, issue triage, synthetic security regression cases, and release verification. Automation would inspect source code and sanitized fixtures, never real clipboard databases, passwords, API tokens, or signing keys. Humans would approve changes and publication. These API workflows are planned, not currently deployed.

### Anything else we should know?

ClipVault uses Apache-2.0 for project-owned source and documentation, attributed to ClipVault contributors. Offline activation code remains included; recipients may modify and redistribute under the license. The release branch retains development history after excluding internal materials. Source is hosted on GitHub; Windows binary acceptance and distribution notices remain open. Support contacts are foobar placeholders. No OpenAI application has been submitted.

Canonical field values: `CODEX_OSS_FIELDS.json`. Validate with `python scripts/check-oss-application.py`; do not manually count words instead of characters.

## Evidence and maintenance plan

Local repository: baseline `f61c7c8`, 125 commits on the selected codex branch (128 across all local references); recent work on composite clipboard capture (2026-10-07), storage/preview separation (2026-10-13), and release preparation (2026-10-21). These are local development dates, not public release or contribution metrics. Current execution evidence, skipped tests, dependency findings and build hashes are in `OSS_RELEASE_REPORT.md`; do not quote older handoff test counts as this run's results.

The project combines Windows native clipboard/tray APIs, encrypted DuckDB, DPAPI, password-protected transfer formats, local Web UI and channel-scoped read-only integrations. This creates useful security and data-integrity review work; it does not demonstrate widespread adoption. Apache-2.0 and the scope for project-owned source have been selected. Internal materials have been excluded from the release branch history. Binary distribution notices and Windows stable-release validation remain separate work.

Planned maintenance: prioritize reproducible bug reports, protect capture/storage behavior with synthetic regression cases, review dependencies and transport/privacy boundaries, and publish only after repeatable Windows validation. OCR and semantic tags remain future work. API automation is a future proposal, subject to maintainer review and program approval; neither credits nor Codex Security access is guaranteed.
