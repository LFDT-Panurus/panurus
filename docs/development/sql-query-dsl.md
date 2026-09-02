# SQL Query DSL

The SQL stores under `token/services/storage/db/sql` do not write SQL by hand. They
build it with a small query-builder DSL that lives in
[`token/services/storage/db/sql/query`](../../token/services/storage/db/sql/query),
so that a single store implementation can serve both the SQLite and the
PostgreSQL driver.

This page documents the conventions the DSL relies on. It is aimed at developers
adding a store method or a new condition, not at application authors.

## Layout

| Package | Responsibility |
| :--- | :--- |
| `query` | Entry points: `Select()`, `Insert()`, `Update()`, `Delete()`, `Table()`. |
| `query/common` | The `Builder` that accumulates SQL text plus bound parameters, and the `Serializable` / `Condition` / `CondInterpreter` contracts. |
| `query/cond` | Condition constructors: `Eq`, `Cmp`, `In`, `InTuple`, `And`, `Or`, `Exists`, `BetweenTimestamps`, … |
| `query/select`, `query/insert`, `query/update`, `query/delete` | Per-statement builders. |
| `query/pagination` | Pagination strategies and the interpreter that turns them into `LIMIT`/`OFFSET`/`WHERE` clauses. |

Anything whose SQL text differs per dialect is deferred to a `CondInterpreter`,
implemented once per driver in
[`sqlite/conditions.go`](../../token/services/storage/db/sql/sqlite/conditions.go)
and
[`postgres/conditions.go`](../../token/services/storage/db/sql/postgres/conditions.go).

## Values are always bound parameters

Table and column names in the DSL come from Go compile-time literals; every
*value* is written through `Builder.WriteParam`, which emits a `$n` placeholder
and appends the value to the parameter list. No store interpolates a value into
SQL text. Keep it that way when adding conditions: reach for `CmpVal`/`WriteParam`,
never `WriteString` with caller data.

## Empty lists mean "no restriction"

`In`, `FieldIn` and `InTuple` all return the `AlwaysTrue` sentinel when handed an
empty list of values (or, for `InTuple`, an empty list of fields). This is a
deliberate convention, not an oversight:

- `And` and `Or` drop the `AlwaysTrue`/`AlwaysFalse` sentinels when composing, so
  a condition that is "not applied" disappears cleanly from the output.
- They can only do that for the sentinels themselves. A condition that rendered
  to the *empty string* would stay in the list and leave a dangling operator
  behind, e.g. `( AND status = $1)`.

So a new condition constructor that can legitimately have nothing to say must
return `AlwaysTrue`, and must never render to an empty fragment. Store-level
helpers follow the same rule — see `hasTokens` in
[`common/tcondition.go`](../../token/services/storage/db/sql/common/tcondition.go).

## Tuple membership

`cond.InTuple(fields, vals)` produces a membership test over a tuple of columns.
Both dialects render it with the SQL standard row-value form via the shared
`common.WriteInTuple` helper:

```sql
(tx_id, idx) IN (($1, $2), ($3, $4))
```

A single field compared against a single value collapses to plain equality
(`tx_id = $1`). Every tuple in `vals` must carry exactly one value per field; the
builder panics on ragged input before writing anything, rather than emitting
malformed SQL.

## Pagination

`query/pagination` offers four strategies, all satisfying the FSC
`driver.Pagination` interface, all selected by passing them to `.Paginated(p)`:

| Constructor | Generated clause | Use for |
| :--- | :--- | :--- |
| `None()` | none | returning the whole result set in one shot |
| `Empty()` | `LIMIT 0` | short-circuiting a query that must return nothing |
| `Offset(offset, pageSize)` | `LIMIT ? OFFSET ?` | random access into a bounded number of pages |
| `Keyset*(…)` | `WHERE id > ? ORDER BY id ASC LIMIT ?` | walking a large result set forward |

### `Offset` and deep pagination

`OFFSET n` makes the database scan and discard `n` rows on every page, so the cost
of fetching a page grows with its distance from the start. That is fine for the
first handful of pages and for jumping to an arbitrary page; it is the wrong tool
for iterating a large table end to end. It is also not stable under concurrent
writes: an insert before the current offset shifts every later page, which can
skip or duplicate rows.

### `Keyset` and its constraints

Keyset (cursor) pagination avoids both problems by remembering the id of the last
row of the previous page and restricting the next query with `id > cursor`. The
production stores (`ttxdb`/`auditdb` `QueryTransactions`) currently use
`offset`/`none`/`empty`; keyset is available but not yet wired into a production
query path.

Three constructors produce it, differing only in how the id is read back out of a
returned row:

- `KeysetWithField[I](offset, pageSize, sqlIdName, idFieldName)` — the id is an
  exported struct field, read by reflection.
- `KeysetWithId[I, V](offset, pageSize, sqlIdName)` — each row implements
  `Id() I`.
- `Keyset[I](offset, pageSize, sqlIdName, idGetter)` — the general form, taking
  an explicit `func(any) I`.

All three return the same type. Before adopting keyset for a query, note its
limitations:

- **Ascending, single column, unique.** The generated SQL always emits
  `ORDER BY <id> ASC` and walks forward with `>`. There is no descending variant
  and no secondary tiebreaker, so the id column must be unique — a non-unique
  column silently skips the rows that share the cursor value.
- **`int` and `string` ids only.** Other id types are rejected by the constructor,
  because the pagination interpreter dispatches on the concrete instantiation.
- **Forward one page at a time.** The cursor only locates the page immediately
  after the current one. Any other jump (`GoToPage`, `Prev`, skipping ahead) falls
  back to `OFFSET`, with the caveats above.

The "no cursor yet" state is represented by a nil pointer rather than a sentinel
value, so an id that is legitimately `-1` or `""` is not mistaken for "start from
the offset instead".

## Adding a condition

1. Add the constructor to `query/cond`, returning `Condition`. Guard the
   degenerate inputs by returning `AlwaysTrue`/`AlwaysFalse`.
2. If the SQL text is the same on every dialect, implement `WriteString` directly
   against `common.Builder`.
3. If it differs, add a method to `common.CondInterpreter` and implement it in
   both `sqlite` and `postgres`. Test doubles in
   `db/sql/common/*_test.go` implement the same interface and need updating too.
4. Add a case to the table in
   [`query/cond/condition_test.go`](../../token/services/storage/db/sql/query/cond/condition_test.go),
   including the empty-input case.
