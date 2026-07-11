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

## Database Schema

The endorserdb uses a single, self-contained table:

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