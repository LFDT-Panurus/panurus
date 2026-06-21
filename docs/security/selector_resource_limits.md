# Selector Resource Limits

## Overview

The Token SDK selector service implements per-identity resource limits to prevent resource exhaustion attacks and ensure fair resource allocation. These limits are enforced at the service layer and apply regardless of the storage backend in use.

## Security Controls

### 1. Lock Quota

**Purpose**: Prevents any single identity (wallet) from monopolizing lock resources by imposing a hard upper bound on the number of active locks.

**Default**: 1000 locks per identity

**Behavior**:
- Each identity can hold a maximum number of simultaneous token locks
- Requests exceeding this quota are immediately rejected with `ErrQuotaExceeded`
- The quota is decremented when locks are released (via `UnlockIDs` or `UnlockByTxID`)
- Quota tracking is per-identity, ensuring isolation between different wallets

**Error Handling**:
- Selector does NOT retry when quota is exceeded
- Applications should handle `ErrQuotaExceeded` by either:
  - Waiting and retrying later
  - Releasing unused locks
  - Splitting operations across multiple identities

### 2. Rate Limiting

**Purpose**: Prevents burst flooding by limiting the rate at which an identity can create new locks.

**Default**: 10 requests/second with burst capacity of 20

**Algorithm**: Token bucket
- Allows burst traffic up to the burst capacity
- Refills at a steady rate (requests per second)
- Provides smooth rate limiting with predictable behavior

**Behavior**:
- Each identity has an independent rate limit bucket
- Requests exceeding the rate limit are immediately rejected with `ErrRateLimitExceeded`
- Rate limit state is maintained in memory and resets on service restart
- Empty identity strings bypass rate limiting (for backward compatibility)

**Error Handling**:
- Selector does NOT retry when rate limit is exceeded
- Applications should implement exponential backoff or request throttling

## Configuration

### YAML Configuration

Add to your configuration file under `token.selector`:

```yaml
token:
  selector:
    # Maximum locks any single identity can hold simultaneously
    # Set to 0 to disable quota enforcement
    maxLocksPerIdentity: 1000
    
    # Lock creation requests per second per identity
    # Set to 0 to disable rate limiting
    rateLimit: 10.0
    
    # Burst capacity for rate limiter
    # Should be >= rateLimit for smooth operation
    rateLimitBurst: 20.0
```

### Programmatic Configuration

```go
import (
    "github.com/hyperledger-labs/fabric-token-sdk/token/services/selector/simple/inmemory"
    "github.com/hyperledger-labs/fabric-token-sdk/token/sdk/network"
)

// Create custom locker configuration
lockerConfig := inmemory.LockerConfig{
    MaxLocksPerIdentity: 500,      // Lower quota for stricter control
    RateLimit:           5.0,       // 5 requests per second
    RateLimitBurst:      10.0,      // Allow bursts up to 10
}

// Create locker provider with custom config
lockerProvider := network.NewLockerProviderWithConfig(
    ttxStoreServiceManager,
    sleepTimeout,
    validTxEvictionTimeout,
    lockerConfig,
)
```

### Disabling Limits

To disable a specific limit, set its value to 0:

```yaml
token:
  selector:
    maxLocksPerIdentity: 0  # Unlimited locks
    rateLimit: 0            # No rate limiting
```

## Monitoring

### Metrics

The following behaviors can be monitored through application logs:

- **Quota Exceeded**: Log entries with "quota exceeded for identity"
- **Rate Limit Exceeded**: Log entries with "rate limit exceeded for identity"
- **Lock Count**: Current number of locks per identity (via debug logs)

### Recommended Alerts

1. **High Quota Usage**: Alert when an identity consistently approaches the quota limit
2. **Frequent Rate Limiting**: Alert when rate limit errors exceed a threshold
3. **Quota Exhaustion**: Alert when quota errors prevent legitimate operations

## Best Practices

### For Application Developers

1. **Handle Errors Gracefully**:
   ```go
   _, err := selector.Select(ctx, ownerFilter, amount, tokenType)
   if errors.HasType(err, inmemory.ErrQuotaExceeded) {
       // Quota exceeded - wait or release locks
       return handleQuotaExceeded(err)
   }
   if errors.HasType(err, inmemory.ErrRateLimitExceeded) {
       // Rate limited - implement backoff
       return handleRateLimitExceeded(err)
   }
   ```

2. **Release Locks Promptly**: Always unlock tokens when transactions fail or complete

3. **Batch Operations**: Group related operations to minimize lock requests

4. **Monitor Usage**: Track quota and rate limit errors in production

### For Operators

1. **Tune Limits**: Adjust based on observed usage patterns and system capacity

2. **Set Conservative Defaults**: Start with lower limits and increase as needed

3. **Monitor System Load**: Ensure limits don't cause legitimate operations to fail

4. **Plan for Growth**: Review limits as transaction volume increases

## Security Considerations

### Attack Scenarios Mitigated

1. **Resource Exhaustion**: Prevents a single malicious identity from locking all available tokens

2. **Denial of Service**: Rate limiting prevents rapid-fire lock requests that could overwhelm the system

3. **Lock Hoarding**: Quota limits prevent identities from accumulating excessive locks

### Limitations

1. **Sybil Attacks**: Limits are per-identity; attackers with multiple identities can still consume resources

2. **Memory Usage**: Rate limiter state is kept in memory; many unique identities increase memory usage

3. **Restart Behavior**: Rate limit state is lost on restart, allowing temporary burst after recovery

## Backward Compatibility

The implementation maintains backward compatibility:

- The original `Lock()` method still works without identity tracking
- Locks created without identity bypass quota and rate limiting
- Existing code continues to function without modification
- New code should use `LockWithIdentity()` for security benefits

## Error Types

### ErrQuotaExceeded

```go
var ErrQuotaExceeded = errors.New("lock quota exceeded for identity")
```

Returned when an identity attempts to acquire more locks than allowed by `maxLocksPerIdentity`.

### ErrRateLimitExceeded

```go
var ErrRateLimitExceeded = errors.New("rate limit exceeded")
```

Returned when an identity exceeds the configured rate limit for lock creation requests.

## Testing

### Unit Tests

Comprehensive unit tests are provided in:
- `token/services/selector/simple/inmemory/ratelimiter_test.go`
- `token/services/selector/simple/inmemory/locker_quota_test.go`

### Integration Testing

Test quota and rate limiting in integration tests:

```go
func TestQuotaEnforcement(t *testing.T) {
    // Create selector with low quota for testing
    config := inmemory.LockerConfig{
        MaxLocksPerIdentity: 5,
        RateLimit:           0,
    }
    
    // Attempt to exceed quota
    for i := 0; i < 10; i++ {
        _, err := selector.Select(ctx, ownerFilter, amount, tokenType)
        if i >= 5 {
            assert.ErrorIs(t, err, inmemory.ErrQuotaExceeded)
        }
    }
}
```

## References

- [Token Selector Service Documentation](../services/selector.md)
- [Configuration Guide](../configuration.md)