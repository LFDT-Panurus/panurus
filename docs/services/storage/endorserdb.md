# Endorser Database (endorserdb)

The `endorserdb` package provides storage services for validation records in Panurus. It manages the persistence and querying of token transaction validation records that are created during the endorsement process.

## Overview

The endorser database stores validation records that track token requests validated by endorsers. Each validation record contains:
- Transaction ID
- Token request data
- Validation metadata
- Public parameters hash
- Status and status message
- Timestamp

It also keeps a separate, append-only audit log of **validation failures** — token requests the
endorser attempted but failed to validate. Unlike a validation record, a failure record is purely
forensic: it never gates whether the same request may be evaluated again. See
[Recording a Validation Failure](#recording-a-validation-failure-auditing).

## Architecture

The endorserdb follows the same architectural pattern as other storage services in Panurus:

```
endorserdb (Service Layer)
    ↓
db/driver (Interface Layer)
    ↓
db/sql/common (SQL Implementation)
    ↓
db/sql/{postgres,sqlite} (Database-Specific)
```

### Key Components

1. **Service Layer** (`store.go`):
   - `StoreService`: High-level API for validation record operations
   - `StoreServiceManager`: Manages store instances per TMS ID
   - `ValidationRecordsIterator`: Iterator for query results

2. **Driver Interface** (`db/driver/endorser.go`):
   - `EndorserStore`: Read operations interface
   - `EndorserStoreTransaction`: Write operations interface
   - `ValidationRecord`: Data structure for validation records
   - `QueryValidationRecordsParams`: Query parameters

3. **SQL Implementation** (`db/sql/common/endorser.go`):
   - Core SQL logic for validation record storage
   - Query building and execution
   - Transaction management

4. **Database-Specific** (`db/sql/{postgres,sqlite}/endorser.go`):
   - Database-specific wrappers
   - Condition and pagination interpreters

## Usage

### Getting a Store Service

```go
import "github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"

// Get store service manager
manager := endorserdb.NewStoreServiceManager(configService, drivers)

// Get store service for a specific TMS
store, err := manager.StoreServiceByTMSId(tmsID)
if err != nil {
    return err
}
```

### Appending Validation Records

```go
err := store.AppendValidationRecord(
    ctx,
    txID,
    tokenRequest,
    metadata,
    ppHash,
)
```

Unlike TTXDB and AuditDB, which are handed an already-deserialised `*token.Request`, this store
receives **raw token request bytes taken off the wire**. It therefore checks them before filing them
as a validated request: `txID`, `tokenRequest`, and `ppHash` must all be non-empty, and
`tokenRequest` must deserialise at a supported protocol version and carry **at least one action**. A
validation record asserting that a request was validated is worthless if the request it names carries
nothing to validate.

The check is not a substitute for validation. The caller
(`token/services/network/fabric/endorsement/fsc/responder.go`) must still run
`validator.UnmarshallAndVerifyWithMetadata` and take `ppHash` from its **own** TMS rather than from
the peer that sent the request. The store's checks narrow what a skipped validation can look like;
they cannot tell whether the actions are legal. Note also that this format is the bare
actions-and-signatures encoding, which carries no anchor, so — unlike TTXDB — a record here cannot be
bound to its transaction id by a structural check. See
[**Store Integrity Verification**](../../security/store_integrity_verification.md).

### Querying Validation Records

```go
// Query with time range
from := time.Now().Add(-24 * time.Hour)
to := time.Now()

it, err := store.ValidationRecords(ctx, endorserdb.QueryValidationRecordsParams{
    From: &from,
    To: &to,
})
if err != nil {
    return err
}
defer it.Close()

// Iterate through results
for {
    record, err := it.Next()
    if err != nil {
        break
    }
    // Process record
}
```

### Updating Status

```go
err := store.SetStatus(ctx, txID, driver.Confirmed, "Transaction confirmed")
```

### Getting Status

```go
status, message, err := store.GetStatus(ctx, txID)
```

### Checking Whether a Request Was Already Processed

`AlreadyProcessed` reports whether a validation record already exists for a given `txID`. A record is
written once an endorser has approved a request and just before it endorses it, keyed by the request
anchor (transaction id), so its presence means the request has already been processed:

```go
processed, err := store.AlreadyProcessed(ctx, txID)
```

It is built on `GetStatus` (which reports `Unknown` without error for a `txID` that has no record), so
any existing record — including one whose status is later set to a terminal value such as `Deleted` or
`Orphan` — counts as processed. The FSC token-request approval responder calls this before running the
(expensive) token-request verification and rejects an already-processed anchor early with
`ErrAlreadyProcessed`. This early check is an optimisation: the responder writes the record *before*
releasing the endorsement signature, so the `tx_id` primary key remains the atomic single-approval
backstop for a concurrent duplicate that races past the check — it fails at the insert, before a
second endorsement is produced.

### Undoing a Record for a Failed Endorsement

`DeleteValidationRecord` removes the validation record for a `txID` (deleting a `txID` that has no
record is not an error):

```go
err := store.DeleteValidationRecord(ctx, txID)
```

The responder writes the record before endorsing so the already-processed marker is durable before the
signature is released. If endorsement then fails, it calls `DeleteValidationRecord` to roll the record
back, leaving the request retryable instead of permanently rejected by `AlreadyProcessed`. Unlike a
status transition (which keeps the record and so keeps it counting as processed), this removes the row
entirely, so a subsequent `AlreadyProcessed` reports `false` again.

### Recording a Validation Failure (auditing)

When an endorser *fails* to validate a token request, it still records the attempt for auditing
purposes via `RecordValidationFailure`:

```go
err := store.RecordValidationFailure(ctx, txID, tokenRequest, reason)
```

This writes an **audit-only** record to a separate, append-only table (`VALIDATION_FAILURES`). It is
deliberately **not** a validation record, and the distinction is the whole point:

- **It never gates re-evaluation.** `AlreadyProcessed` reads the `VALIDATIONS` table only, so a
  recorded failure does not make a request look processed. Whether the same request may be evaluated
  again is governed entirely by the success path (a `VALIDATIONS` row), never by the presence of a
  failure record. A request that failed for a transient reason can simply be re-submitted and
  re-evaluated; each attempt is audited.
- **It applies no token-request well-formedness check.** A request often fails precisely because it
  was malformed, carried zero actions, or declared an unsupported version — exactly the inputs
  `AppendValidationRecord` refuses. The failure log must be able to capture them, so only `txID` and
  `tokenRequest` are required to be non-empty.
- **It is append-only.** The table is keyed by a generated `id`, not `tx_id`, so several failures may
  be recorded for the same anchor (e.g. successive failed retries) without a primary-key clash, and
  no earlier record is overwritten.

The FSC approval responder (`token/services/network/fabric/endorsement/fsc/responder.go`) calls this
when `behaviour.validate` returns an error, **except** for `ErrAlreadyProcessed` (a replay of a
request that was already evaluated and recorded — there is nothing new to audit). The write is
best-effort: it runs on a context detached from the request's (so a failure caused by the request's
context being cancelled is still audited) and a failure to record is logged rather than returned, so
it neither masks the original validation error nor affects retryability.

Recorded failures can be queried with `ValidationFailures`:

```go
it, err := store.ValidationFailures(ctx, endorserdb.QueryValidationFailureRecordsParams{
    From: &from,
    To:   &to,
})
```

## Database Schema

The endorserdb owns two self-contained tables:

### VALIDATIONS Table
Stores validation records created during endorsement:
- `tx_id`: Transaction identifier (primary key)
- `request`: Token request data (NOT NULL)
- `metadata`: Validation metadata (NOT NULL)
- `pp_hash`: Public parameters hash (NOT NULL)
- `status`: Validation status (NOT NULL)
- `status_message`: Status message (NOT NULL)
- `stored_at`: Timestamp (NOT NULL)

There is no foreign key to the Requests table — a validation record can be created and have its status tracked entirely independently of any TTXDB/AuditDB Requests row for the same `tx_id`.

### VALIDATION_FAILURES Table
Append-only audit log of token requests whose validation failed:
- `id`: Generated record identifier (primary key)
- `tx_id`: Transaction identifier (indexed, **not** unique — several failures may share a `tx_id`)
- `request`: Raw token request bytes that failed validation (NOT NULL)
- `reason`: Free-text description of the failure (NOT NULL)
- `stored_at`: Timestamp (NOT NULL, indexed)

This table is never consulted by `AlreadyProcessed`, so rows here do not affect the single-approval
guard or re-evaluation of a request.

## Relationship with TTXDB

The endorserdb was created by extracting validation-related functionality from the Token Transaction Database (ttxdb). Both services share the same physical database, but each owns its own table (ttxdb owns Requests/Transactions/Movements/Endorsements, endorserdb owns Validations) and interface:

- **ttxdb**: Manages token transactions, movements, and token requests
- **endorserdb**: Manages validation records and their status

This separation provides better modularity and clearer separation of concerns.

## Configuration

The endorserdb uses the same configuration as other storage services:

```yaml
token:
  tms:
    mytms:
      endorserdb:
        persistence:
          type: sql
          opts:
            driver: postgres
            dataSource: "host=localhost port=5432 user=postgres password=postgres dbname=tokendb sslmode=disable"
```

## Testing

### Unit Tests
```bash
# Run endorser-specific tests
go test ./token/services/storage/db/sql/postgres -run TestEndorser
go test ./token/services/storage/db/sql/sqlite -run TestEndorser
```

### Integration Tests
```bash
# Run endorser integration tests
make integration-tests-endorser
```

## Migration from TTXDB

If you're migrating code that previously used ttxdb for validation records:

1. **Replace imports**:
   ```go
   // Old
   import "github.com/LFDT-Panurus/panurus/token/services/storage/ttxdb"
   
   // New
   import "github.com/LFDT-Panurus/panurus/token/services/storage/endorserdb"
   ```

2. **Update method calls**:
   ```go
   // Old
   ttxStore.AppendValidationRecord(ctx, txID, request, metadata, ppHash)
   ttxStore.ValidationRecords(ctx, params)
   
   // New
   endorserStore.AppendValidationRecord(ctx, txID, request, metadata, ppHash)
   endorserStore.ValidationRecords(ctx, params)
   ```

3. **Update transaction creation**:
   ```go
   // Old
   tx, err := ttxStore.NewTransactionStoreTransaction()
   tx.AddValidationRecord(...)
   
   // New
   tx, err := endorserStore.NewEndorserStoreTransaction()
   tx.AddValidationRecord(...)
   ```

## See Also

- [Storage Services Overview](../storage.md)
- [TTXDB/AuditDB Documentation](ttxdb.md)
- [Store Integrity Verification](../../security/store_integrity_verification.md)