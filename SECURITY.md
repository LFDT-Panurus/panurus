[//]: # (SPDX-License-Identifier: CC-BY-4.0)

# Panurus, an LF Decentralized Trust Project Security Policy

## About this document

This is the vulnerability disclosure policy for the Panurus project. It conforms to the
[LF Decentralized Trust Security Vulnerability Disclosure Policy][lfdt-security] and is adapted
from the LFDT `SAMPLE-SECURITY.md` template. Where this document is silent, the LFDT policy governs.

## Security Team

The Panurus security team is responsible for receiving, triaging, and coordinating the response to
vulnerability reports. Each member subscribes to the LF Decentralized Trust security email list and
to LFDT-wide security infrastructure. Members are added to and removed from the team via approved
pull requests against this file.

| Name | Email ID | Discord ID | Area/Specialty |
|------|----------|------------|----------------|
| Angelo De Caro | <adc@zurich.ibm.com> | adecaro | Cryptography, zero-knowledge token protocols (`zkatdlog`) |
| Kaoutar Elkhiyaoui | <kao@zurich.ibm.com> | KElkhiyaoui | Cryptography, token protocol design and validation |
| Akram Bitar | <akram@il.ibm.com> | akrambitar | SDK, drivers, integration and CI |

Because Panurus contains security-sensitive cryptographic code — zero-knowledge proofs, range
proofs, and Idemix-based identity under `token/core/zkatdlog/` — the security team includes
maintainers with cryptography expertise, per the LFDT policy.

The security team accepts the following responsibilities:

1. Acknowledge receipt of a report to the reporter within **2 business days**.
2. Triage the report, and open a GitHub Security Advisory if it appears to be a vulnerability.
   Reports that are ordinary bugs are redirected to the normal issue process, and the reporter is told so.
3. Negotiate an embargo period with the reporter where needed. An embargo **must not exceed 90 days**.
4. Develop and review the patch privately, using GitHub's private vulnerability patching features.
5. Obtain a CVE identifier.
6. Agree on a disclosure date and notify embargo list members, if applicable.
7. Ship a release containing the fix.
8. Disclose publicly **within 48 hours after the release**, via a GitHub Security Advisory.
9. Credit the reporter in the advisory, unless they ask to remain anonymous.

## Discussion Forums

Vulnerability discussion happens in the private GitHub Security Advisory opened for the report.
A private channel on the [LF Decentralized Trust Discord][discord] may be created if broader
coordination is required.

**Do not** discuss a suspected vulnerability in a public issue, pull request, discussion, or
Discord channel before it has been disclosed.

## Report Intakes

Report a suspected vulnerability through **either** of these channels:

- **Email** the LF Decentralized Trust security email list at
  <security@lists.lfdecentralizedtrust.org>. Please include:
  - the repository name (`LFDT-Panurus/panurus`),
  - a description of the issue,
  - steps to reproduce,
  - affected versions,
  - any known mitigations.
- **GitHub private vulnerability reporting** — open a draft advisory from the
  [Security tab][security-tab] of the repository.

Reports are handled per the response outline above.

## CNA/CVE Reporting

GitHub acts as the CVE Numbering Authority (CNA) for Panurus. The security team requests CVE
identifiers through the GitHub Security Advisory workflow.

## Embargo List

Panurus does not maintain a project-specific embargo list. Where an embargo is warranted, the
security team coordinates through the LFDT security email list and the private GitHub advisory.
Requests to be included in a specific embargo should be sent to
<security@lists.lfdecentralizedtrust.org> with the project name and the rationale for a
need-to-know.

## Security Advisories

Panurus uses [GitHub Security Advisories][advisories] as its advisory mechanism. Published
advisories are the authoritative record of disclosed vulnerabilities for the project.

## Private Patch Deployment Infrastructure

Panurus uses GitHub's private vulnerability patching features, which allow the fix to be developed
and reviewed in a private fork associated with the advisory. Maintainers needing access or
assistance can contact <community-architects@lfdecentralizedtrust.org>.

---

This policy borrows heavily from the recommendations of the OpenSSF Vulnerability Disclosure
working group ([ossf/wg-vulnerability-disclosures][ossf-wg]), and the response outline derives from
the OpenSSF maintainers guide.

<a rel="license" href="http://creativecommons.org/licenses/by/4.0/"><img alt="Creative Commons License" style="border-width:0" src="https://i.creativecommons.org/l/by/4.0/88x31.png" /></a><br />This work is licensed under a <a rel="license" href="http://creativecommons.org/licenses/by/4.0/">Creative Commons Attribution 4.0 International License</a>.

[lfdt-security]: https://github.com/LF-Decentralized-Trust/governance/blob/main/tac/governing-documents/security.md
[discord]: https://discord.gg/hyperledger
[security-tab]: https://github.com/LFDT-Panurus/panurus/security
[advisories]: https://github.com/LFDT-Panurus/panurus/security/advisories
[ossf-wg]: https://github.com/ossf/wg-vulnerability-disclosures
