# Tools: tokendiag

- [`tokendiag`](../../cmd/tokendiag/README.md) is a read-only diagnostic tool for
  inspecting token-selector state directly against an existing Panurus database
  (SQLite or PostgreSQL). It was added for
  [#2395](https://github.com/LFDT-Panurus/panurus/issues/2395), a CERT load test that
  showed a small number of hot tokens absorbing the vast majority of lock-contention
  errors.
- The `locks` subcommand lists every currently held row in the `token_locks` table,
  flags locks whose consuming transaction has already reached a terminal status
  (`Confirmed`, `Deleted`, or `Orphan`) as **leaked** — the row should have been
  released on settlement but was not, and will otherwise sit until the next
  lease-age sweep — and prints a summary suitable for scripting.
- Mirrors `skicleanup`'s configuration format (`driver`, `dataSource`, `tablePrefix`,
  `skipPrefix`, `tableNames`), so an existing `skicleanup` config file can usually be
  reused as-is; see the tool's own README for details and examples.
