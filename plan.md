# Plan — issue #2073 ✅ COMPLETE: identity assorted robustness defects

## Goal

Close the bundle of robustness defects reported in
[#2073](https://github.com/LFDT-Panurus/panurus/issues/2073) across the identity stack. The one
item with real correctness impact is the stale `localIdentitiesByIdentity` map after a second
`LocalMembership.Load`; everything else is latent fragility or defense-in-depth.

## Steps

1. `token/services/identity/membership/lm.go` — `Load` must reset `localIdentitiesByIdentity`
   alongside the other three `localIdentitiesBy*` maps, so `lookup`/`getLocalIdentity` cannot
   return pre-reload entries.
2. `token/services/identity/boolpolicy/sig.go` — `evalRef` bounds-checks `i` against `len(sigs)`
   but then indexes `v.Verifiers[i]`. Add an explicit `i >= len(v.Verifiers)` guard at the point of
   use so it no longer depends on `Verify`'s earlier length-equality check being kept in sync.
3. `token/services/identity/idemixnym/nym/audit.go` — `FromBytes` uses stdlib `encoding/json` while
   the same logical type is parsed with the project's strict `DisallowUnknownFields` wrapper
   everywhere else. Switch to the strict wrapper and add the nil-embedded-pointer guard that
   `crypto.DeserializeAuditInfo` has, so a payload carrying no `crypto.AuditInfo` fields is rejected
   in `FromBytes` rather than panicking in a promoted method.
4. `token/services/identity/interop/htlc/validator.go` and `info.go` — switch to the strict JSON
   wrapper already used by `deserializer.go` for the same wire types (`htlc.Script`, `ScriptInfo`).
5. `token/services/identity/interop/htlc/validator.go` — compare the HTLC preimage against the
   metadata entry in constant time.
6. `token/services/identity/role/cache.go` — `time.Since(start)` is evaluated eagerly even though
   `start` is only assigned under a debug-level check; move the formatting behind the same check and
   consolidate the four package-level `logger` calls onto the instance `c.Logger`.
7. `token/services/identity/idemix/schema/schema.go` — `EidNymAuditOpts`/`RhNymAuditOpts` index
   `attrs` at schema-dependent positions (up to `attrs[27]` for `w3c-v0.0.1`) with no bounds check,
   so an audit info that passes `crypto.AuditInfo.Validate`'s `len(Attributes) >= 4` check but
   declares the w3c schema panics. Bounds-check before indexing.
8. `token/services/identity/signer_router.go` — add teardown for `byConfID`
   (`Unregister`/`Close`) and unregister a `LocalMembership`'s own conf_ids from
   `LocalMembership.Close`, so released KeyManagers are not left routable.
   (`role.Registry.Wallets` already has the teardown hook the issue asks for: `Registry.Done`.)
9. Unit tests for each behavioural change; `docs/` update where user-visible.
10. `make checks` / `make lint-auto-fix` / `make unit-tests` clean.

## Implementation Progress

- [x] Done — 1. `Load` resets `localIdentitiesByIdentity` — reset alongside the other three maps in `Load`; regression test verified to fail without it
- [x] Done — 2. `boolpolicy` verifier bounds guard — `evalRef` now bounds-checks `i` against `sigs`, `memo` and `Verifiers`, and rejects a nil `Verifiers` entry
- [x] Done — 3. `nym.AuditInfo.FromBytes` strict JSON + nil guard — switched to `token/core/common/encoding/json`; nil embedded `crypto.AuditInfo` rejected in `FromBytes`
- [x] Done — 4. htlc validator/info strict JSON — `validator.go` and `info.go` now use the strict wrapper `deserializer.go` already used
- [x] Done — 5. htlc preimage constant-time compare — `subtle.ConstantTimeCompare` in `MetadataClaimKeyCheck`
- [x] Done — 6. `role/cache.go` debug-guard + single logger — `time.Since` reads moved behind the debug check; package-level `logger` removed, everything on `c.Logger`
- [x] Done — 7. idemix schema audit-opts bounds checks — `attributeAt` guards `Eid`/`RhNymAuditOpts`; schema names and indices are now named constants
- [x] Done — 8. `SignerRouter` teardown — `Unregister`/`Close`/`Len` added; `LocalMembership.Close` unregisters its own conf_ids after releasing their KeyManagers
- [x] Done — 9. Tests — unit tests per item, plus `FuzzVerifyOwnerNoPanic` and `FuzzMetadataClaimKeyCheckNoPanic` wired into `nightly-fuzz.yml`
- [x] Done — 10. Checks green — `make checks`, `make lint-auto-fix`, `make unit-tests` and `-race` clean; only pre-existing `TestTranslatePath` fails (asserts the checkout dir is named `panurus`)

## Notes & Decisions

- The issue also notes that `crypto.AuditInfo.Validate` never validates `Schema` against a known
  set. A whitelist inside `Validate` would hardcode `DefaultManager`'s two schemas into a type whose
  `SchemaManager` is an interface, breaking any custom manager that supports other schemas. The
  reachable consequence of an unvalidated `Schema` is the unchecked `attrs[...]` indexing in
  `DefaultManager`, so step 7 guards there instead — an unsupported schema already returns an error
  from those methods.

- `role.Registry.Wallets` already had the teardown hook the issue asks for (`Registry.Done`, which
  closes wallets implementing `Close` and drops the map), so only `SignerRouter.byConfID` needed one.
- Consolidating `role/cache.go` onto `c.Logger` left the package-level `logger` unused (every other
  reference in the package is a shadowing parameter), so it was removed rather than kept dead —
  which is also what "one logical component, one logger" means here.
- `docs/services/identity.md` updated: reload-replaces-not-merges under LocalMembership, the
  SignerRouter teardown API, the audit-info JSON strictness and schema/attribute-count coupling, the
  boolpolicy verification bounds guarantees, and the HTLC parsing/comparison notes.
