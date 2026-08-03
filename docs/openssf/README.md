# OpenSSF Security Baseline

This section records how Panurus measures up against the
[OpenSSF Open Source Project Security Baseline](https://baseline.openssf.org) (OSPS Baseline), so that
contributors and consumers can see which security practices the project already follows, which ones
are still missing, and how to re-run the assessment.

## EU Cyber Resilience Act (CRA) context

Panurus is maintained under [LF Decentralized Trust](https://www.lfdecentralizedtrust.org) (LFDT).
Under the EU Cyber Resilience Act, LFDT — not the individual maintainers — is the *open-source
steward* (CRA Article 3.14) for the project, and the steward's obligations are set out in CRA
Article 24. The OpenSSF and Linux Foundation summarise what that means for a project in the
[CRA Stewards One-Pager](https://github.com/ossf/wg-globalcyberpolicy/blob/main/docs/CRA/stewards-one-pager.md).

This assessment is one of the concrete artifacts that guidance calls for:

- **Documented, verifiable security posture (Article 24.1).** The steward must "put in place and
  document" a cybersecurity policy that enables effective handling of vulnerabilities and covers
  secure-development practices. For Panurus that policy is [SECURITY.md](../../SECURITY.md)
  (coordinated disclosure, timeframes and contacts) together with this Baseline self-assessment (the
  secure-development and process controls, including the gaps that are still open). The one-pager
  explicitly names the OpenSSF Security Baseline as the due-diligence mechanism for this.
- **A single point of contact for reporting.** Vulnerabilities are reported through the channels in
  [SECURITY.md](../../SECURITY.md) — the LF Decentralized Trust security list and GitHub private
  vulnerability reporting — never through public issues.
- **Infrastructure incidents and EU reporting.** Reporting a *severe incident* or an actively
  exploited vulnerability to the EU — the ENISA Single Reporting Platform, market-surveillance
  authorities and national CSIRTs — is a steward-level responsibility that runs through the Linux
  Foundation security process (<security@linuxfoundation.org>), not through this repository.

This section is a readiness posture, not a compliance claim or legal advice; questions about CRA
obligations should go to LFDT and LF Legal.

## Two different OpenSSF programs

The two OpenSSF programs that apply to a project like Panurus are often confused. They are separate,
and only the first one is assessed here.

| Program | Identifiers | Levels | Used here |
|---------|-------------|--------|-----------|
| [OSPS Baseline](https://baseline.openssf.org) | `OSPS-<CATEGORY>-<NN>.<NN>` | Level 1, Level 2, Level 3 | Yes — the pages below |
| [Best Practices Badge](https://www.bestpractices.dev) | free-form criteria | passing, silver, gold | No — tracked separately in the project's [badge entry](https://www.bestpractices.dev/en/projects/7176) |

The OSPS Baseline levels are *not* named "passing", "silver" or "gold" — those are Best Practices
Badge tiers. Baseline levels are scoped by project size instead:

- **Level 1** — any code or non-code project, any number of maintainers or users.
- **Level 2** — a code project with at least two maintainers and a small, consistent user base.
- **Level 3** — a code project with a large, consistent user base.

Every Baseline control is a `MUST`; the Baseline deliberately contains no `SHOULD` entries.

## What Panurus targets

Panurus has several active maintainers (see [MAINTAINERS.md](../../MAINTAINERS.md)), tagged releases,
and downstream users, so **Level 2 is the level the project aims to satisfy in full**. Level 1 is
almost entirely satisfied today; Level 3 is documented as a longer-term target because it requires
release-signing, SBOM, VEX and threat-modeling work that has not started.

## Assessment

- **Assessed against:** OSPS Baseline **v2026.02.19** (the current release at the time of writing)
- **Assessment date:** 2026-08-19
- **Assessed release:** `v0.17.0`

| Level | Controls | Met | Partially met | Not met | Unverified |
|-------|---------:|----:|--------------:|--------:|-----------:|
| [Level 1](baseline_level_1.md) | 24 | 20 | 2 | 0 | 2 |
| [Level 2](baseline_level_2.md) | 19 | 13 | 3 | 2 | 1 |
| [Level 3](baseline_level_3.md) | 21 | 4 | 5 | 12 | 0 |

Status values used in the per-level tables:

| Status | Meaning |
|--------|---------|
| **Met** | Satisfied, with evidence in this repository or in a publicly verifiable GitHub setting. |
| **Partially Met** | Partly satisfied; the remaining gap is named in the notes. |
| **Not Met** | Not satisfied today. |
| **Unverified** | Cannot be confirmed from public repository state; needs confirmation by a maintainer or org administrator. |

Nothing is marked **Met** on the basis of "this is standard practice for the foundation". A control is
only **Met** when a file in this repository, a release artifact, or a publicly readable GitHub
setting shows it.

## Main gaps

The assessment converges on a small number of themes rather than 20 unrelated items:

1. **Release provenance.** Releases carry no signed manifest, no checksums and no SBOM, and the tags
   are lightweight rather than signed — so there is nothing for a consumer to verify, and no
   documented verification procedure (`OSPS-BR-06.01`, `OSPS-DO-03.01`, `OSPS-DO-03.02`,
   `OSPS-QA-02.02`).
2. **Support lifecycle.** No statement of how long a release is supported or when it stops receiving
   security fixes (`OSPS-DO-04.01`, `OSPS-DO-05.01`).
3. **Security analysis artifacts.** No published security assessment or threat model, and the
   project README states it has not been audited (`OSPS-SA-03.01`, `OSPS-SA-03.02`).
4. **Policy thresholds.** CodeQL, `golangci-lint` and Dependabot all run, but no document defines the
   severity threshold at which findings must be fixed, or what happens before a release
   (`OSPS-VM-05.01`, `OSPS-VM-05.02`, `OSPS-VM-06.01`).
5. **Enforcement vs. policy.** [DEVELOPMENT.md](../../DEVELOPMENT.md) requires one maintainer approval
   per PR, but the active branch ruleset requires zero approving reviews and only the DCO check, so
   the policy is honored by convention rather than enforced by the platform (`OSPS-QA-03.01`,
   `OSPS-QA-07.01`).
6. **Workflow permissions and unsanitized input.** Every workflow declares an explicit top-level
   `permissions:` block, so no job falls back to the repository default (`OSPS-AC-04.01` met).
   Write scopes are scoped to the jobs that need them: `md_links.yml` grants `pull-requests: write`
   workflow-wide (single-job workflow), `docs.yml` scopes `pages: write` and `id-token: write` to
   the `deploy` job only (`OSPS-AC-04.02` met). Two `workflow_dispatch` inputs — `fsc-version` in
   `tests.yml` and `fuzztime` in `nightly-fuzz.yml` — are still interpolated straight into shell
   steps rather than passed through `env:` variables (`OSPS-BR-01.04` not met).

## How to reassess

1. Check whether a newer Baseline release exists at
   [baseline.openssf.org](https://baseline.openssf.org). Only the version labeled *current* should
   be used for new compliance work, and control identifiers are occasionally retired between
   versions.
2. Pull the machine-readable checklist for that version (for example
   `https://baseline.openssf.org/versions/2026-02-19-checklist.md`) and diff its control list against
   the tables in [Level 1](baseline_level_1.md), [Level 2](baseline_level_2.md) and
   [Level 3](baseline_level_3.md).
3. Re-verify each row against the repository, not against memory. Controls about GitHub
   configuration can be checked with the API, for example:

   ```bash
   # branch rulesets: which checks and approvals are actually enforced on main
   gh api repos/LFDT-Panurus/panurus/rulesets
   gh api repos/LFDT-Panurus/panurus/rulesets/<id>

   # release assets, signatures and changelog
   gh release view v0.17.0 --json tagName,assets,body

   # private vulnerability reporting and published advisories
   gh api repos/LFDT-Panurus/panurus/private-vulnerability-reporting
   gh api repos/LFDT-Panurus/panurus/security-advisories
   ```

4. Update the header of each page (Baseline version, assessment date, assessed release) and the
   summary table above.
5. Open a GitHub issue for every control that moves to, or stays at, **Not Met**, and reference the
   control identifier in the issue so progress stays traceable.
6. If the project also wants credit on the Best Practices Badge, update
   [project 7176](https://www.bestpractices.dev/en/projects/7176) separately — the two programs do
   not share data.

## Related documentation

- [Security policy](../../SECURITY.md) — how to report a vulnerability
- [Contributing](../../CONTRIBUTING.md) and [Development guidelines](../development/general.md)
- [Selector resource limits](../security/selector_resource_limits.md) — a security-relevant design note
- [CRA Stewards One-Pager](https://github.com/ossf/wg-globalcyberpolicy/blob/main/docs/CRA/stewards-one-pager.md) — the OpenSSF/LF steward guidance the CRA context above aligns with
