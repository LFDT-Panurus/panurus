# Token Transactions DB

The Token Transactions DB is a database of audited records. It is used to track the
history of audited events. In particular, it is used to track payments, holdings,
and transactions of any business party identified by a unique enrollment ID.

## Getting Started

Each Token Transactions DB is bound to a wallet to be uniquely identifiable. 
To get the instance of the Token Transactions DB bound to a given wallet, 
use the following:

```go
   ttxDB := ttxdb.Get(context, wallet)
```

## Append Token Requests

A Token Request describes a set of token operations in a backend agnostic language.
A Token Request can be assembled directly using the Token API or by using service packages like 
[`ttx`](https://github.com/LFDT-Panurus/panurus/tree/main/token/services/ttx).

Once a Token Request is assembled, it can be appended to the Token Transactions DB as follows:

```go
	if err := ttxDB.Append(ctx, tokenRequest); err != nil {
		return errors.WithMessagef(err, "failed appending audit records for tx [%s]", tx.ID())
	}
```

The above code will extract the movement records and the transaction records from the Token Request and append them to the Token Transactions DB.

It is also possible to append just the transaction records corresponding to a given Token Request as follows:

```go
	if err := ttxDB.AppendTransactionRecord(tokenRequest); err != nil {
		return errors.WithMessagef(err, "failed appending audit records for tx [%s]", tx.ID())
	}
```

## Payments

The following example shows how to retrieve the total amount of last 10 payments made by a given 
business party, identified by the corresponding enrollment ID, for a given token type.

```go
    filter := ttxDB.NewPaymentsFilter()
    filter, err = filter.ByEnrollmentId(eID).ByType(tokenType).Last(10).Execute()
    if err != nil {
        return errors.WithMessagef(err, "failed getting payments for enrollment id [%s] and token type [%s]", eID, tokenType)
    }
    sumLastPayments := filter.Sum()
```

## Holdings

The following example shows how to retrieve the current holding of a given token type for a given business party.
Recall that the current holding is equal to the difference between inbound and outbound transactions over
the entire history.

```go
    filter := ttxDB.NewHoldingsFilter()
    filter, err = filter.ByEnrollmentId(eID).ByType(tokenType).Execute()
    if err != nil {
        return errors.WithMessagef(err, "failed getting holdings for enrollment id [%s] and token type [%s]", eID, tokenType)
    }
    holding := filter.Sum()
```

## Transaction Records

The following example shows how to retrieve the total amount of transactions for a given business party,

```go
	it, err := qe.Transactions(ctx, ttxdb.QueryTransactionsParams{From: p.From, To: p.To})
	if err != nil {
		return errors.WithMessagef(err, "failed getting transactions for enrollment id [%s]", eID)
	}
	defer it.Close()

	for {
		tx, err := it.Next()
		if err != nil {
			return errors.WithMessagef(err, "failed getting transactions for enrollment id [%s]", eID)
        }
		if tx == nil {
			break
		}
		fmt.Printf("Transaction: %s\n", tx.ID())
	}
```

## Composite Owners

An output owned by a composite identity (multisig, boolpolicy) carries one
audit row per member, so identity consumers (`ByRecipient`,
`RevocationHandles`) see every member. Amount aggregations — payments,
holdings, transaction records — count each `(output index, enrollment ID)`
pair once (`OutputStream.UniquePerOutput`), so members sharing an enrollment
ID do not multiply the recorded amount. A spent input is expanded the same
way, one row per member carrying the token's full quantity; sent-amount
aggregation counts each `(token ID, enrollment ID)` pair once
(`InputStream.UniquePerInput`), falling back to
`(action index, input index, enrollment ID)` when the token ID has been
filtered out of the metadata. Positional deduplication applies only to inputs
built through `token.NewInput`, which marks the position as known; a
literal-built input without a token ID keeps the previous keep-all behavior.
