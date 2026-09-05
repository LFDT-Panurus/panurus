# OSPS Baseline Level 2

Self-assessment of Panurus against **Level 2** of the
[OpenSSF Security Baseline](https://baseline.openssf.org), version **v2026.02.19**.

- **Assessment date:** 2026-08-19
- **Assessed release:** `v0.17.0`
- **Status legend and methodology:** see the [section overview](README.md)

Level 2 applies to code projects with at least two maintainers and a small, consistent user base, and
builds on [Level 1](baseline_level_1.md): a project claiming Level 2 must satisfy the Level 1 controls
as well. **This is the level Panurus aims to satisfy in full.** Requirement wording below is
abbreviated; the authoritative text is the
[Baseline v2026.02.19 control list](https://baseline.openssf.org/versions/2026-02-19).

## Access Control

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-AC-04.01` | A CI/CD task with no permissions specified defaults to the lowest permissions in the pipeline | **Met** | Every workflow declares an explicit top-level `permissions:` block, so no job falls back to the repository default. Specifically: [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml) (`contents: read`), [protect-integration-test-types.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/protect-integration-test-types.yml) (`contents: read`), [nightly-fsc.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fsc.yml) (`contents: read`), [nightly-fuzz.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fuzz.yml) (`contents: read` top-level; the `fuzz` job adds `issues: write`), [token-validation-benchmark.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/token-validation-benchmark.yml) (`contents: read` top-level; the `compare` job adds `pull-requests: write`), [scorecard.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/scorecard.yml) (`read-all`), [md_links.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/md_links.yml) (`contents: read, pull-requests: write`), [docs.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/docs.yml) (`contents: read, pages: write, id-token: write`); [codeql-analysis.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/codeql-analysis.yml) uses `permissions: {}` at the top level with per-job grants. The elevated scopes on `md_links.yml` and `docs.yml` are the minimum required for their respective tasks (posting link-check review comments and deploying GitHub Pages). |

## Build and Release

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-BR-02.01` | Every official release is assigned a unique version identifier | **Met** | Semantic version tags, most recently `v0.17.0` (2026-08-12), with matching per-module tags such as `cmd/tokengen/v0.17.0`. Conventions are documented in [versioning](../development/versioning.md). |
| `OSPS-BR-04.01` | Each release contains a descriptive log of functional and security modifications | **Met** | GitHub release notes for `v0.17.0` list every merged pull request plus a full-changelog comparison link (`gh release view v0.17.0`). |
| `OSPS-BR-05.01` | The build and release pipeline ingests dependencies with standardized tooling | **Met** | Go modules throughout; `make tidy` and the `tidy-check` target in [checks.mk](https://github.com/LFDT-Panurus/panurus/blob/main/checks.mk) keep `go.mod`/`go.sum` authoritative across all modules. |
| `OSPS-BR-06.01` | Each release is signed, or covered by a signed manifest containing asset hashes | **Not Met** | `v0.17.0` publishes no release assets beyond GitHub's auto-generated source archives, there is no checksum manifest, and the release tags are lightweight tags rather than signed tag objects (`git cat-file -t v0.17.0` returns `commit`). Consumers verifying a module still get `go.sum` checksum protection, but the release itself is unsigned. |

## Documentation

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-DO-06.01` | Documentation describes how the project selects, obtains and tracks dependencies | **Partially Met** | How dependencies are *obtained and tracked* is documented: Go modules, `make tidy`, the `tidy-check` gate, and the [FSC update runbook](../development/update-fsc.md) for the main upstream dependency. There is no documented policy for how a new dependency is *selected* or vetted before adoption. |
| `OSPS-DO-07.01` | Documentation includes build instructions, with required libraries and SDKs | **Met** | [Testing guide](../development/testing.md) ("Getting Started", "Prerequisites"), [Makefile guide](../development/makefile.md), [tools](../development/tools.md), and the setup sequence in [AGENTS.md](../../AGENTS.md). |

## Governance

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-GV-01.01` | Documentation lists project members with access to sensitive resources | **Met** | [MAINTAINERS.md](../../MAINTAINERS.md) lists active maintainers with GitHub handles and contact addresses. |
| `OSPS-GV-01.02` | Documentation describes roles and responsibilities of project members | **Partially Met** | [MAINTAINERS.md](../../MAINTAINERS.md) distinguishes active from emeritus maintainers, and [DEVELOPMENT.md](../../DEVELOPMENT.md) states what maintainers must check before approving a pull request, but neither describes the responsibilities of a maintainer or how the role is granted; that is left to the [LFDT charter](https://www.lfdecentralizedtrust.org/about/charter) linked from [CONTRIBUTING.md](../../CONTRIBUTING.md). |
| `OSPS-GV-03.02` | A contributor guide states the requirements for acceptable contributions | **Met** | [DEVELOPMENT.md](../../DEVELOPMENT.md) section 3 (description, labels, project assignment, linked issue, one approval) plus the coding and issue conventions in [docs/development/general.md](../development/general.md) and [idiomatic Go](../development/idiomatic.md). |

## Legal

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-LE-01.01` | Version control requires every commit to assert the contributor's legal authority | **Met** | DCO sign-off is mandatory ([DEVELOPMENT.md](../../DEVELOPMENT.md) section 1) and enforced: the active organization ruleset requires the `DCO` status check on the default branch, and the repository has `web_commit_signoff_required` enabled. |

## Quality

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-QA-03.01` | Automated status checks for commits to the primary branch must pass or be manually bypassed | **Partially Met** | Checks do run on every pull request ([tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml), [codeql-analysis.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/codeql-analysis.yml), [md_links.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/md_links.yml), [docs.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/docs.yml)), but the only *required* status check in the active ruleset is `DCO`. The ruleset that additionally required `CodeQL` (`main`, id `5047032`) has `enforcement: disabled`, so a red test run does not mechanically block a merge. |
| `OSPS-QA-06.01` | CI/CD runs at least one automated test suite before a commit is accepted | **Met** | [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml) runs `make checks`, `make lint`, unit tests with coverage and matrixed integration tests on every pull request to `main`. |

## Security Assessment

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-SA-01.01` | Design documentation demonstrating all actions and actors in the system | **Met** | [Panurus overview](../tokensdk.md), [driver API](../driverapi.md), [services](../services.md) and the per-service pages such as [TTX](../services/ttx.md) and [network](../services/network.md), including sequence diagrams. |
| `OSPS-SA-02.01` | Documentation describes all external software interfaces of released assets | **Met** | [Token API](../tokenapi.md), [Token API usage](../token_sdk_usage.md), [driver API](../driverapi.md), [configuration reference](../configuration.md), and the CLI tool READMEs under `cmd/`. |
| `OSPS-SA-03.01` | A security assessment identifying the most likely and impactful security problems | **Not Met** | [README.md](../../README.md) states that Panurus "has not been audited and is provided as-is". CodeQL scanning, `golangci-lint`, and a nightly fuzzing matrix ([nightly-fuzz.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fuzz.yml)) are in place, but no assessment document identifies and ranks the project's likely security problems. |

## Vulnerability Management

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-VM-01.01` | A coordinated vulnerability disclosure policy with a clear response timeframe | **Met** | [SECURITY.md](../../SECURITY.md) documents a full coordinated-disclosure flow with concrete timeframes: acknowledgement of a report within **2 business days**, an embargo capped at **90 days**, and public disclosure via a GitHub Security Advisory **within 48 hours after the fix is released**. |
| `OSPS-VM-03.01` | A means of private vulnerability reporting to the project's security contacts | **Met** | Private email to the LF Decentralized Trust security list (`security@lists.lfdecentralizedtrust.org`, [SECURITY.md](../../SECURITY.md)), and GitHub private vulnerability reporting is enabled on the repository (`gh api repos/LFDT-Panurus/panurus/private-vulnerability-reporting` returns `{"enabled": true}`). |
| `OSPS-VM-04.01` | Data about discovered vulnerabilities is published publicly | **Unverified** | [SECURITY.md](../../SECURITY.md) designates GitHub Security Advisories as the authoritative public record of disclosed vulnerabilities, but none have been published for this repository to date (`gh api repos/LFDT-Panurus/panurus/security-advisories` returns an empty list), so the channel cannot be confirmed as exercised from repository state. |

## Summary

| Status | Count |
|--------|------:|
| Met | 13 |
| Partially Met | 3 |
| Not Met | 2 |
| Unverified | 1 |
| **Total** | **19** |

Closing Level 2 requires, in rough order of effort: signing releases or publishing a signed checksum
manifest (`OSPS-BR-06.01`), producing a security assessment (`OSPS-SA-03.01`), documenting the
dependency selection policy (`OSPS-DO-06.01`), enforcing CI status checks on `main`
(`OSPS-QA-03.01`), and describing maintainer responsibilities (`OSPS-GV-01.02`). Declaring
`permissions:` on every workflow (`OSPS-AC-04.01`) and stating a concrete disclosure timeframe
(`OSPS-VM-01.01`) are now done.
