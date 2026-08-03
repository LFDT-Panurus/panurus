# OSPS Baseline Level 3

Self-assessment of Panurus against **Level 3** of the
[OpenSSF Security Baseline](https://baseline.openssf.org), version **v2026.02.19**.

- **Assessment date:** 2026-08-19
- **Assessed release:** `v0.17.0`
- **Status legend and methodology:** see the [section overview](README.md)

Level 3 applies to code projects with a large, consistent user base and builds on
[Level 1](baseline_level_1.md) and [Level 2](baseline_level_2.md). It is documented here as a
longer-term target: most of its controls concern release provenance, support lifecycle and formal
security analysis, none of which exist yet. Requirement wording below is abbreviated; the
authoritative text is the
[Baseline v2026.02.19 control list](https://baseline.openssf.org/versions/2026-02-19).

## Access Control

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-AC-04.02` | Jobs are assigned only the minimum privileges necessary | **Met** | Every workflow declares a minimal top-level token and elevates only at the job level where needed. [token-validation-benchmark.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/token-validation-benchmark.yml) grants `pull-requests: write` only to the `compare` job (which never executes PR code); [nightly-fuzz.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fuzz.yml) grants `issues: write` only to the `fuzz` job; [docs.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/docs.yml) pins the `build` job to `contents: read` and scopes `pages: write` and `id-token: write` exclusively to the `deploy` job (lines 81-82); [scorecard.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/scorecard.yml) uses `read-all` at the top level and grants `security-events: write` and `id-token: write` only to its analysis job; [codeql-analysis.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/codeql-analysis.yml) uses `permissions: {}` at the top level with per-job grants; [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml), [protect-integration-test-types.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/protect-integration-test-types.yml), and [nightly-fsc.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fsc.yml) pin `contents: read` with no per-job elevations; [md_links.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/md_links.yml) declares `pull-requests: write` workflow-wide as it has a single job. |

## Build and Release

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-BR-01.04` | Input from trusted collaborators is sanitized and validated before use | **Not Met** | Two `workflow_dispatch` inputs are interpolated directly into shell steps without validation or an `env:` variable indirection: `fsc-version` in [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml) lines 38-40 (`go mod edit -replace=...=${{ github.event.inputs.fsc-version }}`) and `fuzztime` in [nightly-fuzz.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fuzz.yml) line 169 (`-fuzztime="${{ github.event.inputs.fuzztime \|\| '4h' }}"`). Both inputs come from trusted collaborators but are exactly the pattern this control targets. |
| `OSPS-BR-02.02` | Every asset in a release is clearly associated with the release identifier | **Met** | Releases publish only GitHub's auto-generated source archives, which are named after the tag, and each Go module carries a tag bearing the same version (`v0.17.0`, `cmd/tokengen/v0.17.0`, `integration/v0.17.0`, ...). |
| `OSPS-BR-07.02` | A defined policy for storing, accessing and rotating secrets and credentials | **Not Met** | Workflows consume credentials only through the GitHub `secrets` context, but no document states who may add a secret, how access is reviewed, or when secrets are rotated. |

## Documentation

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-DO-03.01` | Instructions to verify the integrity and authenticity of release assets | **Not Met** | No such instructions exist, and there is nothing to verify against today (see `OSPS-BR-06.01` in [Level 2](baseline_level_2.md)). Module consumers do get `go.sum` checksum verification, which is not documented as a verification procedure. |
| `OSPS-DO-03.02` | Instructions to verify the expected identity of the release author | **Not Met** | Release tags are unsigned, so author identity cannot be verified cryptographically and no procedure is documented. |
| `OSPS-DO-04.01` | A descriptive statement about the scope and duration of support for each release | **Not Met** | [versioning](../development/versioning.md) explains SemVer mechanics, but no page states how long any given minor release is supported. |
| `OSPS-DO-05.01` | A descriptive statement of when releases stop receiving security updates | **Not Met** | No end-of-life or security-support window is documented. |

## Governance

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-GV-04.01` | A documented policy that code collaborators are reviewed before permissions are escalated | **Not Met** | [MAINTAINERS.md](../../MAINTAINERS.md) lists who holds elevated access, and the [LFDT charter](https://www.lfdecentralizedtrust.org/about/charter) governs the project, but no repository document describes the review that precedes granting escalated permissions. |

## Quality

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-QA-02.02` | Compiled released assets are delivered with a software bill of materials | **Not Met** | No SBOM is generated or published. Applicability is partly limited because releases currently ship source and Go modules rather than compiled binaries, but the container images and CLI tools built from this repository are not accompanied by an SBOM either. |
| `OSPS-QA-04.02` | Subprojects enforce security requirements at least as strict as the primary codebase | **Met** | Panurus is a single repository with no external subprojects. Its Go modules share one set of gates: `checks.mk` and the `lint` target iterate over `$(GO_MODULES)`, and [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml) runs them for the whole tree. The auxiliary `tools` module is the one exception — it appears in `TIDY_GO_MODULES` only, so it is tidy-checked but not linted or vetted. |
| `OSPS-QA-06.02` | Documentation clearly states when and how tests are run | **Met** | [Testing guide](../development/testing.md) covers unit, fuzz, integration and Fabric-X tests with the exact commands; [Makefile guide](../development/makefile.md) documents the targets; the nightly fuzz matrix is described alongside [nightly-fuzz.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/nightly-fuzz.yml). |
| `OSPS-QA-06.03` | A documented policy that major changes add or update automated tests | **Partially Met** | [AGENTS.md](../../AGENTS.md) requires a fuzz target for every new parser of untrusted input and describes the expected unit/integration test practice, and [docs/development/testing.md](../development/testing.md) documents how to add tests. Neither [CONTRIBUTING.md](../../CONTRIBUTING.md) nor [DEVELOPMENT.md](../../DEVELOPMENT.md) states the general rule that a major change must come with tests. |
| `OSPS-QA-07.01` | The version control system requires at least one non-author human approval before merge | **Partially Met** | [DEVELOPMENT.md](../../DEVELOPMENT.md) section 3 states a "One Approve Policy", but the active branch ruleset sets `required_approving_review_count: 0`, so the platform does not enforce it. The "Code Quality Copilot review for default branch" ruleset (id 19363822) has `enforcement: disabled`, so it has no effect on merge gating. |

## Security Assessment

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-SA-03.02` | Threat modeling and attack surface analysis of critical code paths | **Not Met** | No threat model is published. Individual hardening notes exist (for example [selector resource limits](../security/selector_resource_limits.md)), and the driver and validator documentation describes trust boundaries, but there is no systematic attack-surface analysis. |

## Vulnerability Management

| Control | Requirement | Status | Evidence / notes |
|---------|-------------|--------|------------------|
| `OSPS-VM-04.02` | Non-exploitable vulnerabilities in components are accounted for in a VEX document | **Not Met** | No VEX documents are produced. |
| `OSPS-VM-05.01` | A documented threshold for remediating SCA findings (vulnerabilities and licenses) | **Not Met** | No documented severity threshold for dependency findings. |
| `OSPS-VM-05.02` | A documented policy to address SCA violations before any release | **Not Met** | The release process does not document a dependency-scanning gate. |
| `OSPS-VM-05.03` | All changes are automatically evaluated against a documented policy for malicious and vulnerable dependencies, and blocked on violation | **Partially Met** | Automated dependency updates are demonstrably active — the `v0.17.0` release notes contain several `build(deps)` pull requests opened by Dependabot — and the repository now checks in [.github/dependabot.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/dependabot.yml), which watches the SHA-pinned GitHub Actions weekly and groups major vs. minor/patch bumps into separate reviewable PRs (Go modules are intentionally out of scope, handled by the Nightly FSC workflow). Still missing for full compliance: the alerting configuration is not publicly readable, no documented remediation policy exists, and no gate blocks a change on a dependency finding. |
| `OSPS-VM-06.01` | A documented threshold for remediating SAST findings | **Partially Met** | The de facto threshold is zero: [codeql-analysis.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/codeql-analysis.yml) scans every push and pull request, and the `checks` job of [tests.yml](https://github.com/LFDT-Panurus/panurus/blob/main/.github/workflows/tests.yml) fails on any `golangci-lint`, `staticcheck`, `go vet` or `ineffassign` finding. That threshold is not written down anywhere, and CodeQL results are not gated. |
| `OSPS-VM-06.02` | All changes are automatically evaluated for security weaknesses and blocked on violation | **Partially Met** | Evaluation happens on every pull request (CodeQL, `golangci-lint`, `staticcheck`, nightly fuzzing). Blocking is by convention: the only required status check on `main` is `DCO`, and the ruleset that required `CodeQL` is disabled — see `OSPS-QA-03.01` in [Level 2](baseline_level_2.md). |

## Summary

| Status | Count |
|--------|------:|
| Met | 4 |
| Partially Met | 5 |
| Not Met | 12 |
| Unverified | 0 |
| **Total** | **21** |

Level 3 is not a near-term goal. With workflow `permissions:` now declared on every workflow
(`OSPS-AC-04.02`), the cheapest remaining wins that also help Level 2 are passing the `fsc-version`
and `fuzztime` inputs through `env:` variables (`OSPS-BR-01.04`) and writing down the SAST/SCA thresholds that CI already
enforces in practice. Release signing plus SBOM and VEX generation are the substantial items.
