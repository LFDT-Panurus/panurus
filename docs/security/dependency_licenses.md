# Third-Party License Dispositions

A license scanner reports a *finding*, not a verdict. Most findings on Panurus are artefacts of how
the tool reads metadata — a package whose `License:` field embeds an entire license text, a Go
module whose license sits in a file the scanner does not look for, a genuinely dual-licensed
package reported as `UNKNOWN`. A few are real decisions. Nothing should change in the dependency set
just because a tool mislabelled it, and nothing should stay unexamined just because a tool was
quiet.

This page is the register. Per finding it records the **SPDX identifier read from the dependency's
own license file**, where that file is, and the decision taken. It also states the baseline the
decisions are made against, and the commands that re-derive every number here — so the next scanner
report can be triaged against measurements rather than memory.

## Baseline

The question a dependency's license raises depends entirely on whether Panurus *links* it or merely
*builds and documents itself with* it. Developer tooling is never redistributed as part of a
Panurus artifact; a module compiled into `cmd/node` is.

| Class | Licenses | In the linked build set | In build/docs/benchmark tooling |
|---|---|---|---|
| Permissive | Apache-2.0, MIT, BSD-2-Clause, BSD-3-Clause, ISC | Accepted | Accepted |
| Weak copyleft (file-level) | MPL-2.0 | Accepted where it is inherent to the dependency's purpose; avoided where a permissive equivalent is already in the module graph | Accepted |
| Strong copyleft | GPL / LGPL / AGPL family | Not accepted | Case by case; none present |

MPL-2.0 is **not** forbidden here. Its reciprocity is per-file: it obliges source availability for
the MPL-licensed files themselves, and leaves the rest of a combined work — including Panurus,
which is [Apache-2.0](../../LICENSE) — under its own terms. Panurus links MPL-2.0 code by design in
at least one place (the Vault KVS backend, which exists to talk to Vault). What the baseline rules
out is carrying a weak-copyleft module *gratuitously*, where a permissive module with the same API
is already present in the graph.

## The register

| Package | Version | Scanner said | SPDX (verified) | Upstream license file | Disposition |
|---|---|---|---|---|---|
| `pandas` | 3.0.5 | **FORBIDDEN** | `BSD-3-Clause` | [`LICENSE`](https://github.com/pandas-dev/pandas/blob/main/LICENSE) | **Tooling artefact — no change.** pandas is BSD-3-Clause. Its `License:` metadata field embeds the full license text and several copyright holders, and its repository carries a `LICENSES/` directory of notices for vendored third-party code; read as one blob, that resolves to a worst-case class. pandas is used only by the benchmark scripts in [`cmd/benchmarking`](../../cmd/benchmarking) and is not shipped, imported, or vendored by any Panurus module. |
| `certifi` | 2026.7.22 | CAUTION | `MPL-2.0` | [`LICENSE`](https://github.com/certifi/python-certifi/blob/master/LICENSE) | **Approved, unmodified — no change.** Docs toolchain only (`mkdocs-material` → `requests` → `certifi`), at its latest version as of that date. MPL-2.0 is file-level reciprocal and the package is used as-is, so the obligation is satisfied by upstream's own published source. |
| `pathspec` | 1.1.1 | CAUTION | `MPL-2.0` | [`LICENSE`](https://github.com/cpburnz/python-pathspec/blob/master/LICENSE) | **Approved, unmodified — no change.** As `certifi`: docs toolchain only (`mkdocs` → `pathspec`), latest as of that date, used as-is. |
| `python-dateutil` | 2.9.0.post0 | UNKNOWN (`"Dual License"`) | `Apache-2.0 OR BSD-3-Clause` | [`LICENSE`](https://github.com/dateutil/dateutil/blob/master/LICENSE) | **Dual license recorded — no change.** The literal string `Dual License` in its `License:` field is what the scanner could not parse; its classifiers declare both licenses. Panurus takes it under Apache-2.0. Transitive via `pandas` and `ghp-import`, latest as of that date. |
| `github.com/hashicorp/go-uuid` | v1.0.3 | *undetected* | `MPL-2.0` | [`LICENSE`](https://github.com/hashicorp/go-uuid/blob/master/LICENSE) | **Replaced.** See [below](#hashicorpgo-uuid--googleuuid). The only MPL-2.0 module the core library pulled in by its own choice, used for two lines of random-ID generation, with a permissive equivalent already in every affected module's graph. |
| ~60 Go modules | various | *license undetected* | Apache-2.0 / MIT / BSD / ISC | per module | **Detection gap recorded — no per-package work.** Measured rather than assumed; see [Go dependencies](#go-dependencies-measured-not-assumed) and [The detection gap](#the-detection-gap). |

One correction to the original report: `github.com/google/uuid` is **BSD-3-Clause**, not Apache-2.0.
Both are permissive, so the decision is unaffected.

## Go dependencies: measured, not assumed

"License undetected" across ~60 Go modules is a property of the scanner, not of the dependency set.
Rather than spot-check a sample by hand, the full set was classified from each module's own license
file in the module cache, per Go module, over the modules that **contribute packages to the build**
(`go list -deps`) — i.e. code that is actually compiled in, as opposed to the much larger module
*graph* (`go list -m all` reports 495 entries for the root module, most of which are never linked).

Measured on 2026-10-15, at the commit that introduced this page. The counts move with every
dependency bump and nothing in CI holds them to account — re-derive them with the recipe below
rather than trusting the numbers on sight.

| Go module | Linked modules | Apache-2.0 / MIT / BSD / ISC | MPL-2.0 | Strong copyleft |
|---|---|---|---|---|
| `.` (core library) | 108 | 108 | 0 | 0 |
| `cmd/tokengen` | 109 | 109 | 0 | 0 |
| `cmd/skicleanup` | 101 | 101 | 0 | 0 |
| `cmd/tokendiag` | 99 | 99 | 0 | 0 |
| `x/token/services/network/evm` | 92 | 92 | 0 | 0 |
| `cmd/node` | 193 | 191 | 2 | 0 |
| `cmd/token_validation_service` | 187 | 185 | 2 | 0 |
| `cmd/artifactgen` | 197 | 195 | 2 | 0 |
| `integration` | 217 | 215 | 2 | 0 |
| `token/services/storage/db/kvs/hashicorp` | 92 | 82 | 10 | 0 |
| `cmd/profiler` | 0 | — | 0 | 0 |

`cmd/profiler` depends on the standard library alone. No GPL-, LGPL- or AGPL-licensed
module is linked by any Panurus module. Every module not in a
permissive class is named in [MPL-2.0 in Panurus](#mpl-20-in-panurus) below.

To reproduce, per module: `go list -deps -json ./...`, take each distinct
`.Module{Path,Version,Dir}`, and classify the first of `LICENSE`, `LICENSE.txt`, `LICENSE.md`,
`LICENCE`, `COPYING` found in `Dir` by its text.

## The detection gap

Go modules carry no license metadata field — a scanner has to find and read a file. The
"undetected" class is almost entirely modules whose license is present but not where the tool
looked:

| Module | License file | Actual SPDX |
|---|---|---|
| `github.com/kr/pretty`, `github.com/kr/text` | `License` (no extension) | MIT |
| `github.com/go-errors/errors` | `LICENSE.MIT` | MIT |
| `github.com/multiformats/go-multicodec` | `LICENSE` + `LICENSE-APACHE` + `LICENSE-MIT` | `Apache-2.0 OR MIT` |
| `github.com/multiformats/go-base36` | `LICENSE.md` ("Permissive License Stack") | `Apache-2.0 OR MIT` |
| `filippo.io/keygen`, `github.com/decred/dcrd/dcrec/secp256k1/v4`, `github.com/libp2p/go-reuseport` | `LICENSE` (ISC text, no SPDX header) | ISC |

Every family named in the original report — `go.uber.org/*`, `golang.org/x/*`,
`google.golang.org/*`, FSC, `jackc/*`, OTel — is covered by the full sweep above, which read each
module's own license file: all came back Apache-2.0, MIT or BSD.

**Recorded as a detection gap.** No per-package action. A future scanner report should be read with
this in mind: an "undetected" Go module is a prompt to read its license file, not a finding.

## MPL-2.0 in Panurus

After the `go-uuid` replacement, every remaining MPL-2.0 module is one Panurus does not choose
directly:

| Module(s) | Reached via | Why it stays |
|---|---|---|
| `github.com/hashicorp/vault/api`, `github.com/hashicorp/hcl`, `github.com/hashicorp/errwrap`, `github.com/hashicorp/go-cleanhttp`, `github.com/hashicorp/go-multierror`, `github.com/hashicorp/go-retryablehttp`, `github.com/hashicorp/go-rootcerts`, `github.com/hashicorp/go-secure-stdlib/{parseutil,strutil}`, `github.com/hashicorp/go-sockaddr` | [`token/services/storage/db/kvs/hashicorp`](../../token/services/storage/db/kvs/hashicorp) | Inherent: this module's purpose is to talk to Vault, whose client is MPL-2.0. It is an optional backend in its own Go module, so a deployment that does not use Vault never links it. |
| `github.com/libp2p/go-yamux/v5`, `github.com/hashicorp/golang-lru` | libp2p, via Fabric Smart Client's P2P transport | Transitive through FSC; not selectable from Panurus. Affects the FSC-based binaries only (`cmd/node`, `cmd/token_validation_service`, `cmd/artifactgen`, `integration`). |
| `github.com/hashicorp/golang-lru/v2`, `pgregory.net/rapid` | *test* packages of transitive dependencies (`modernc.org/libc`, `github.com/moby/moby`) | In the module graph, never linked into any Panurus binary or test. |

## `hashicorp/go-uuid` → `google/uuid`

**Decision: replace.** `go-uuid` was used for exactly one thing — a random identifier string — at
two call sites, while `github.com/google/uuid` (BSD-3-Clause) was already present as an indirect
dependency of every module that carried `go-uuid`. Approving the MPL-2.0 dependency would have been
defensible; replacing it costs four lines, adds nothing to the module graph, and leaves the core
library's linked dependency set entirely permissive.

| Call site | Before | After |
|---|---|---|
| [`token/services/storage/db/sql/common/transactions.go`](../../token/services/storage/db/sql/common/transactions.go) (3×: endorse-ack, transaction and movement row IDs) | `uuid.GenerateUUID()` → `string` | `uuid.NewRandom()` → `uuid.UUID`, stored via `.String()` |
| [`token/services/storage/services/recovery/manager.go`](../../token/services/storage/services/recovery/manager.go) (instance-ID suffix) | `uuid.GenerateUUID()` → `string` | `uuid.NewRandom()` → `uuid.UUID`, appended via `.String()` |

Both APIs return an error on entropy failure, which the existing error handling already covers.
The stored form is unchanged in shape. The three columns are `id CHAR(36) NOT NULL PRIMARY KEY`
— a fixed-width column — and both the old and the new value are exactly 36 characters, a
dash-separated hex string, so the width is exactly satisfied either way. `uuid.NewRandom`
additionally sets the RFC 4122 version and variant bits that `GenerateUUID` left as raw random;
the values are only ever generated, stored and compared for equality, never parsed or
version-checked (no `uuid.Parse` call exists in the tree, and `id` appears in no `SELECT`), so
existing rows remain valid alongside new ones.

The source of randomness does change, in a way worth recording. Fabric Smart Client's
`pkg/utils` has an `init()` that calls `uuid.EnableRandPool()`, and that package is linked into
the same binary (`go list -deps`), so `uuid.NewRandom` draws from one process-wide 512-byte
buffer rather than reading `crypto/rand` per call. That buffer is mutex-guarded, so concurrent
inserts cannot collide, and it is refilled from `crypto/rand`, so the entropy source is
unchanged; the bytes become public row identifiers, so holding them in memory discloses nothing.
Note also that `google/uuid` exposes a process-global `uuid.SetRand`, which `go-uuid` did not:
nothing in the tree or its dependencies calls it, but code that did would make these primary keys
repeat.

`go-uuid` was then dropped from the nine `go.mod` files that declared it (one direct, eight
indirect) and `google/uuid` promoted to a direct requirement of the root module, via `make tidy`.

## Left unchanged, deliberately

- **`pandas` 3.0.5** stays pinned and in use. The FORBIDDEN flag is a metadata artefact, not a
  policy violation. (3.0.6 exists; dependency *upgrades* are tracked separately.)
- **`certifi`, `pathspec`, `python-dateutil`** stay as they are — all three already at their latest
  version, all three used unmodified, none of them shipped.
- **The MPL-2.0 modules above** stay. Each is either inherent to an optional backend's purpose or
  transitive through FSC, and none is replaceable from Panurus.
- **The ~60 "undetected" Go modules** get no per-package change. The class is recorded here and the
  classification is reproducible.
