# OSPS Baseline Level 1

Self-assessment of Panurus against **Level 1** of the
[OpenSSF Security Baseline](https://baseline.openssf.org), version **v2026.02.19**.

- **Assessment date:** 2026-08-19
- **Assessed release:** `v0.17.0`
- **Status legend and methodology:** see the [section overview](README.md)

Level 1 applies to any project regardless of size. Requirement wording below is abbreviated; the
authoritative text is the [Baseline v2026.02.19 control list](https://baseline.openssf.org/versions/2026-02-19).

## Access Control

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-AC-01.01` | MFA required to read or modify sensitive resources in the authoritative repository | **Unverified** | Organization-level setting for `LFDT-Panurus`, not readable from the repository. Needs confirmation by an org administrator. |
| `OSPS-AC-02.01` | New collaborators get manual permission assignment or the lowest privileges by default | **Unverified** | Contributions arrive through forks and pull requests, so no repository write access is needed to contribute. The default member privilege of the organization still needs confirmation by an org administrator. |
| `OSPS-AC-03.01` | Direct commits to the primary branch are blocked by an enforcement mechanism | **Met** | Active organization ruleset `DCO` targets `~DEFAULT_BRANCH` and includes a `pull_request` rule, so changes must arrive through a pull request (`gh api repos/LFDT-Panurus/panurus/rulesets`). |
| `OSPS-AC-03.02` | Deleting the primary branch is treated as sensitive and requires confirmation | **Met** | The same active ruleset includes `deletion` and `non_fast_forward` rules, which block branch deletion and force-pushes on `main` outright. |

## Build and Release

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-BR-01.01` | Untrusted pipeline metadata is sanitized and validated before use | **Met** | No attacker-controlled free text (branch name, PR title, PR body) reaches a `run:` block. The only untrusted-event fields interpolated into shell steps are the base/head commit SHAs and PR number in [token-validation-benchmark.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/token-validation-benchmark.yml) (lines 116-125 and 262), which are safe to interpolate. Two `workflow_dispatch` inputs do reach `run:` blocks — `fsc-version` in [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml) lines 38-40 and `fuzztime` in [nightly-fuzz.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fuzz.yml) line 169 — but `workflow_dispatch` is gated to repository collaborators with write access, so these are operator-supplied values rather than attacker-controlled external input. |
| `OSPS-BR-01.03` | Pipelines operating on untrusted code snapshots cannot reach privileged credentials | **Met** | [token-validation-benchmark.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/token-validation-benchmark.yml) triggers on `pull_request` (not `pull_request_target`): fork PRs run in the fork's read-only context with no access to repository secrets (lines 31-39). The default token is `permissions: contents: read` (line 49); `pull-requests: write` is scoped only to the `compare` job (line 207), which never checks out or executes PR code. The `benchmark` job that runs untrusted fork code holds no write scope and no secrets. |
| `OSPS-BR-03.01` | Official project channel URIs are delivered over encrypted channels | **Met** | All channels listed in [README.md](../../README.md) and [CONTRIBUTING.md](../../CONTRIBUTING.md) are HTTPS (GitHub, `discord.gg`), and the documentation site is served over HTTPS (`site_url` in [mkdocs.yml](https://github.com/LFDT-Panurus/panurus/blob/main/mkdocs.yml)). |
| `OSPS-BR-03.02` | Official distribution channels are protected against adversary-in-the-middle attacks | **Met** | Releases are consumed as Go modules through the HTTPS module proxy with `go.sum` checksum verification, and as HTTPS GitHub release archives. Tool downloads in the [Makefile](https://github.com/LFDT-Panurus/panurus/blob/main/Makefile) also use HTTPS. |
| `OSPS-BR-07.01` | Unintentional storage of unencrypted secrets in version control is prevented | **Partially Met** | CI credentials are referenced only through the GitHub `secrets` context, and generated/local artifacts are excluded by [.gitignore](https://github.com/LFDT-Panurus/panurus/blob/main/.gitignore). However no repository-side secret scanner (for example `gitleaks`) runs in CI or as a pre-commit hook, and GitHub secret-scanning/push-protection settings are not publicly readable. |

## Documentation

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-DO-01.01` | User guides for all basic functionality | **Met** | [Documentation index](../README.md), covering the [Token API](../tokenapi.md), [usage guide](../token_sdk_usage.md), [configuration](../configuration.md), [services](../services.md) and per-tool READMEs under `cmd/`. |
| `OSPS-DO-02.01` | A guide for reporting defects | **Met** | "Reporting Issues" in [CONTRIBUTING.md](../../CONTRIBUTING.md), plus structured issue forms in [.github/ISSUE_TEMPLATE](https://github.com/LFDT-Panurus/panurus/tree/main/.github/ISSUE_TEMPLATE) (`bug_report.yml`, `feature_request.yml`, `good_first_issue.yml`). |

## Governance

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-GV-02.01` | Mechanisms for public discussion of proposed changes and obstacles | **Met** | GitHub issues and pull requests, GitHub Discussions (enabled on the repository), and the `#panurus` Discord channel linked from [README.md](../../README.md) and [CONTRIBUTING.md](../../CONTRIBUTING.md). |
| `OSPS-GV-03.01` | Documented explanation of the contribution process | **Met** | [CONTRIBUTING.md](../../CONTRIBUTING.md), [DEVELOPMENT.md](../../DEVELOPMENT.md) and [docs/development](../development/development.md). |

## Legal

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-LE-02.01` | Source code license meets the OSI or FSF definition | **Met** | Apache License 2.0 ([LICENSE](https://github.com/LFDT-Panurus/panurus/blob/main/LICENSE)); GitHub reports the SPDX identifier `Apache-2.0`. |
| `OSPS-LE-02.02` | Released software assets are under an OSI/FSF-conforming license | **Met** | Releases are source and Go module releases of this repository, covered by the same Apache-2.0 license; per-file license headers are enforced by the `licensecheck` target in [checks.mk](https://github.com/LFDT-Panurus/panurus/blob/main/checks.mk). |
| `OSPS-LE-03.01` | License is kept in the repository's `LICENSE` file | **Met** | [LICENSE](https://github.com/LFDT-Panurus/panurus/blob/main/LICENSE) at the repository root. |
| `OSPS-LE-03.02` | License is included alongside release assets | **Met** | Release archives are snapshots of the tagged tree and therefore contain `LICENSE`. |

## Quality

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-QA-01.01` | Source repository is publicly readable at a static URL | **Met** | <https://github.com/LFDT-Panurus/panurus> is public. |
| `OSPS-QA-01.02` | Public record of all changes, authors and timestamps | **Met** | Full Git history is public; the project requires a linear, rebase-based history ([DEVELOPMENT.md](../../DEVELOPMENT.md)). |
| `OSPS-QA-02.01` | Repository contains a dependency list for direct language dependencies | **Met** | `go.mod` / `go.sum` for the root module and each of the eight additional modules (`cmd/*`, `integration`, `tools`, `token/services/storage/db/kvs/hashicorp`). |
| `OSPS-QA-04.01` | Projects with multiple repositories document the list of codebases | **Met** | Panurus is a single repository. Its Go modules and standalone CLI tools are listed in the [documentation index](../README.md). |
| `OSPS-QA-05.01` | Version control contains no generated executable artifacts | **Met** | No tracked executables, archives or shared objects; build output (`/site/`, `coverage.out`, generated service output directories) is excluded by [.gitignore](https://github.com/LFDT-Panurus/panurus/blob/main/.gitignore). |
| `OSPS-QA-05.02` | Version control contains no unreviewable binary artifacts | **Partially Met** | No binaries are shipped, but binary fixtures are tracked: Idemix key material under `cmd/tokengen/testdata/idemix/` and `token/core/zkatdlog/nogh/v1/**/testdata/`, and PNG diagrams under `docs/imgs/`. These are regenerable test/documentation assets rather than code, but they are not human-reviewable in a diff. |

## Vulnerability Management

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-VM-02.01` | Documentation contains security contacts | **Met** | [SECURITY.md](../../SECURITY.md) lists the Panurus security team and directs reports to the LF Decentralized Trust security email list (`security@lists.lfdecentralizedtrust.org`), with GitHub private vulnerability reporting as the alternative channel. |

## Summary

| Status | Count |
|--------|------:|
| Met | 20 |
| Partially Met | 2 |
| Not Met | 0 |
| Unverified | 2 |
| **Total** | **24** |

The two **Unverified** rows are organization settings rather than repository content, and the two
**Partially Met** rows are narrow: no CI-side secret scanner, and binary test fixtures in the tree.
