# Codex for Open Source application draft

Draft only, checked against the [official application form](https://openai.com/zh-Hans-CN/form/codex-for-oss/) and [program overview](https://developers.openai.com/community/codex-for-oss) on 2026-10-21. The form requires a public repository and public GitHub profile. Application readiness and acceptance are not established. Do not submit before publication and resolution of the release blockers.

## Identity and repository

| Field | Value |
| --- | --- |
| First / last name | 待我填写 |
| Email associated with ChatGPT | 待我填写 |
| GitHub username | 待我填写 |
| Public GitHub repository URL | 待我填写（拟为 `https://github.com/<OWNER>/clipvault`，尚未创建） |
| Maintainer role | Proposed primary maintainer; owner must confirm identity and write access |
| OpenAI Organization ID | 待我填写 |
| Requested support | Proposed: API credits and Codex Security; owner must select |
| Stars / monthly downloads / external users | No verified public data available |

No account identifiers were inferred from local private files or Git author metadata. Organization ID and email must be provided by the account owner. Do not place actual account identifiers in the public source snapshot; complete a private copy of this application later.

## Three short form fields (ASCII; each at most 500 characters)

### Why is this repository eligible?

ClipVault is preparing its first public release. It is a Windows clipboard manager with encrypted DuckDB storage, safe rich-text previews, and token-scoped read-only HTTP/MCP access. There are no verified public stars, downloads, or external adoption metrics yet. Its potential value is a locally controlled, auditable bridge between clipboard history and developer tools; broad ecosystem impact is not yet demonstrated.

### How would you use API credits for your project?

I plan to use API credits for maintainer-reviewed pull request checks, issue triage, synthetic security regression cases, and release verification. Automation would inspect source code and sanitized fixtures, never real clipboard databases, passwords, API tokens, or signing keys. Humans would approve changes and publication. These API workflows are planned, not currently deployed.

### Anything else we should know?

ClipVault uses Apache-2.0 for project-owned source and documentation, attributed to ClipVault contributors. Offline activation code remains included; recipients may modify and redistribute under the license. Full codex branch history is planned for GitHub main. Historical privacy review, third-party notices, and Windows release acceptance remain open. Security contacts are the placeholder foobar. No repository has been published and no application submitted.

Canonical field values: `CODEX_OSS_FIELDS.json`. Validate with `python scripts/check-oss-application.py`; do not manually count words instead of characters.

## Evidence and maintenance plan

Local repository: baseline `f61c7c8`, 125 commits on the selected codex branch (128 across all local references); recent work on composite clipboard capture (2026-10-07), storage/preview separation (2026-10-13), and release preparation (2026-10-21). These are local development dates, not public release or contribution metrics. Current execution evidence, skipped tests, dependency findings and build hashes are in `OSS_RELEASE_REPORT.md`; do not quote older handoff test counts as this run's results.

The project combines Windows native clipboard/tray APIs, encrypted DuckDB, DPAPI, password-protected transfer formats, local Web UI and channel-scoped read-only integrations. This creates useful security and data-integrity review work; it does not demonstrate widespread adoption. Apache-2.0 and the scope for project-owned source have been selected. Historical internal data, third-party rights and binary redistribution still need review before public release.

Planned maintenance: prioritize reproducible bug reports, protect capture/storage behavior with synthetic regression cases, review dependencies and transport/privacy boundaries, and publish only after repeatable Windows validation. OCR and semantic tags remain future work. API automation is a future proposal, subject to maintainer review and program approval; neither credits nor Codex Security access is guaranteed.
