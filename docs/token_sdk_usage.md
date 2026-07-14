# Panurus API Usage Guide

This guide provides a comprehensive overview of how to use Panurus API. 
It is based on the integration tests found in `integration/token/fungible/views`.

## 1. Core Concepts

Panurus leverages the **View** model from the **Fabric Smart Client**. 
Views are used to model distributed business processes, allowing nodes to communicate and execute protocols (like token issuance or transfer) in a structured way.

Most token operations follow this pattern:

1.  **Preparation**:
    Identify the parties involved (sender, recipient, auditor). The **Identity Service** manages long-term identities (X.509, Idemix) and wallets.
    *   *Reference*: [Identity Service](./services.md) and [Wallet Manager](./tokenapi.md#wallet-manager).

2.  **Transaction Creation**:
    Initialize a **Token Transaction** using `ttx.NewTransaction` (for nominal transactions) or `ttx.NewAnonymousTransaction` (for anonymous transactions). This creates a blueprint for the operation.
    *   *Reference*: [Building a Token Transaction](./tokenapi.md#building-a-token-transaction).

3.  **Action**:
    Add specific token operations to the transaction, such as `Issue`, `Transfer`, or `Redeem`. These actions modify the ledger state.
    *   *Reference*: [Token API](./tokenapi.md).

4.  **Endorsement**:
    Collect signatures from the necessary parties (e.g., issuer, owner, auditor) to authorize the transaction. The **Validator** checks these endorsements against the public parameters.
    *   *Reference*: [Validator](./tokenapi.md#validator).

5.  **Finality**:
    Submit the transaction to the ordering service and wait for it to be committed to the ledger. 

> [!IMPORTANT]
> The code snippets below omit full error handling for brevity. In production code, you **must** handle `err` returned by these functions to ensure robustness.

---

## 2. Issuance (`issue.go`)

Issuance creates new tokens. It requires an issuer wallet and an auditor.

### Key Snippet
```go
// 1. Get Recipient Identity
recipient, err := ttx.RequestRecipientIdentity(context, recipientNode)
if err != nil {
    return nil, err
}

// 2. Create Transaction
// 'TxOpts' allows specifying the auditor.
tx, err := ttx.NewAnonymousTransaction(context, ttx.WithAuditor(auditorID))
if err != nil {
    return nil, err
}

// 3. Issue Tokens
// Use the issuer wallet to issue 'quantity' of 'tokenType' to 'recipient'.
wallet := ttx.GetIssuerWallet(context, issuerWalletID)
err = tx.Issue(wallet, recipient, tokenType, quantity)
if err != nil {
    return nil, err
}

// 4. Collect Endorsements
// Signs the transaction (Issuer + Auditor).
_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
if err != nil {
    return nil, err
}

// 5. Submit & Wait for Finality
_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
if err != nil {
    return nil, err
}
```

---

## 3. Transfer ([`transfer.go`](../integration/token/fungible/views/transfer.go))

Transfer moves tokens from a sender to a recipient.

### Key Snippet
```go
// 1. Get Recipient Identity
recipient, err := ttx.RequestRecipientIdentity(context, recipientNode)
if err != nil {
    return nil, err
}

// 2. Create Transaction
tx, err := ttx.NewAnonymousTransaction(context, ttx.WithAuditor(auditorID))
if err != nil {
    return nil, err
}

// 3. Add Transfer Action
// Sender wallet is used to select input tokens.
senderWallet := ttx.GetWallet(context, senderWalletID)
err = tx.Transfer(
    senderWallet,
    tokenType,
    []uint64{amount},
    []view.Identity{recipient},
    // Optional: Select specific token IDs
    token.WithTokenIDs(tokenIDs...), 
)
if err != nil {
    return nil, err
}

// 4. Collect Endorsements (Sender + Auditor)
// If the sender wallet is remote, use `ttx.WithExternalWalletSigner`.
_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
if err != nil {
    return nil, err
}

// 5. Submit & Wait
_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
if err != nil {
    return nil, err
}
```

### Advanced Transfer Options
*   **Manual Token Selection**: You can manually select tokens using `tx.Selector().Select(...)` before calling `tx.Transfer`.
*   **Atomic Swap**: See Section 5.

---

## 4. Redemption ([`redeem.go`](../integration/token/fungible/views/redeem.go))

Redemption retires tokens, effectively removing them from circulation. It requires approval from the **Issuer**.

### Key Snippet
```go
// 1. Create Transaction
tx, err := ttx.NewAnonymousTransaction(context, ttx.WithAuditor(auditorID))
if err != nil {
    return nil, err
}

// 2. Add Redeem Action
// If the issuer is not automatically resolvable, provide their FSC identity.
// If needed, also pin the issuer signing key expected by public parameters.
senderWallet := ttx.GetWallet(context, senderWalletID)
err = tx.Redeem(
    senderWallet,
    tokenType,
    amount,
    ttx.WithFSCIssuerIdentity(issuerIdentity),                     // Contact issuer for approval
    ttx.WithIssuerPublicParamsPublicKey(issuerPublicParamsPubKey), // Optional key pinning
)
if err != nil {
    return nil, err
}

// 3. Collect Endorsements (Sender + Auditor + Issuer)
_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
if err != nil {
    return nil, err
}

// 4. Submit & Wait
_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
if err != nil {
    return nil, err
}
```

Use `ttx.WithFSCIssuerIdentity(...)` when your app cannot resolve the issuer endpoint automatically.
Use `ttx.WithIssuerPublicParamsPublicKey(...)` when you want redeem authorization to be verified against a specific issuer key from public parameters.

---

## 5. Atomic Swap ([`swap.go`](../integration/token/fungible/views/swap.go))

Swaps allow two parties (Alice and Bob) to exchange tokens atomically.

### Initiator (Alice)
```go
// 1. Exchange Identities
me, other, err := ttx.ExchangeRecipientIdentities(context, aliceWallet, bobNode)
if err != nil {
    return nil, err
}

// 2. Create Transaction
tx, err := ttx.NewAnonymousTransaction(context, ttx.WithAuditor(auditorID))
if err != nil {
    return nil, err
}

// 3. Add Alice's Transfer
senderWallet := ttx.GetWallet(context, aliceWallet)
err = tx.Transfer(senderWallet, aliceTokenType, []uint64{aliceAmount}, []view.Identity{other})
if err != nil {
    return nil, err
}

// 4. Collect Bob's Action
// Alice asks Bob to add his transfer to the SAME transaction.
_, err = context.RunView(ttx.NewCollectActionsView(tx, &ttx.ActionTransfer{
    From:      other,
    Type:      bobTokenType,
    Amount:    bobAmount,
    Recipient: me,
}))
if err != nil {
    return nil, err
}

// 5. Endorse & Submit
_, err = context.RunView(ttx.NewCollectEndorsementsView(tx))
if err != nil {
    return nil, err
}
_, err = context.RunView(ttx.NewOrderingAndFinalityView(tx))
if err != nil {
    return nil, err
}
```

### Responder (Bob)
```go
// 1. Respond to Identity Exchange
me, _, err := ttx.RespondExchangeRecipientIdentities(context)
if err != nil {
    return nil, err
}

// 2. Receive Transaction & Action Request
tx, action, err := ttx.ReceiveAction(context)
if err != nil {
    return nil, err
}

// 3. Add Bob's Transfer
bobWallet := ttx.MyWalletFromTx(context, tx)
err = tx.Transfer(bobWallet, action.Type, []uint64{action.Amount}, []view.Identity{action.Recipient})
if err != nil {
    return nil, err
}

// 4. Send Back to Alice
_, err = context.RunView(ttx.NewCollectActionsResponderView(tx, action))
if err != nil {
    return nil, err
}

// 5. Endorse
_, err = context.RunView(ttx.NewEndorseView(tx))
if err != nil {
    return nil, err
}

// 6. Wait for Finality
_, err = context.RunView(ttx.NewFinalityView(tx))
if err != nil {
    return nil, err
}
```

---

## 6. Queries Types ([`balance.go`](../integration/token/fungible/views/balance.go), [`history.go`](../integration/token/fungible/views/history.go))

### Balance
```go
// Get Wallet
tms, err := token.GetManagementService(context, ServiceOpts(tmsID)...)
if err != nil {
    return nil, err
}
wallet := tms.WalletManager().OwnerWallet(context.Context(), walletID)

// List Unspent Tokens
iterator, err := wallet.ListUnspentTokensIterator(token.WithType(tokenType))
if err != nil {
    return nil, err
}

// Calculate Sum
sum, err := iterators.Reduce(iterator.UnspentTokensIterator, token.ToQuantitySum(precision))
if err != nil {
    return nil, err
}
```

### History (Auditor)
```go
// Get Auditor Wallet & Instance
w := ttx.MyAuditorWallet(context)
auditor, err := ttx.NewAuditor(context, w)
if err != nil {
    return nil, err
}

// Query Transactions
it, err := auditor.Transactions(context.Context(), ttxdb.QueryTransactionsParams{From: fromTime, To: toTime}, pagination.None())
if err != nil {
    return nil, err
}
```

### History (Owner)
```go
owner := ttx.NewOwner(context, tms)
it, err := owner.Transactions(context.Context(), ttxdb.QueryTransactionsParams{...}, pagination.None())
if err != nil {
    return nil, err
}
```

### Issuer Balance ([`history.go`](../integration/token/fungible/views/history.go))
An issuer wallet can report its gross issued balance, its gross redeemed balance (tokens redeemed against that issuer), and its net outstanding issued supply.
```go
wallet := ttx.GetIssuerWallet(context, issuerWalletID)

opts := &driver.IssuerBalanceOptions{TokenType: tokenType} // optionally add From/To to restrict by time range

issued, err := wallet.IssuedBalance(context.Context(), opts)
if err != nil {
    return nil, err
}
redeemed, err := wallet.RedeemedBalance(context.Context(), opts)
if err != nil {
    return nil, err
}
net, err := wallet.Balance(context.Context(), opts) // == issued - redeemed
if err != nil {
    return nil, err
}
```

---

## 7. Special Patterns

### Withdrawal ([`withdraw.go`](../integration/token/fungible/views/withdraw.go))
Uses an **Initiator-Responder Inversion** pattern. The user requests a withdrawal, and the Issuer (responder) becomes the initiator of the Token Transaction to issue the tokens.

### Multisig ([`multisig.go`](../integration/token/fungible/views/multisig.go))
*   **Lock**: `multisig.Wrap(tx).Lock(...)`
*   **Spend**: `multisig.Wrap(tx).Spend(...)`. Requires `multisig.Wallet` to list co-owned tokens.

---

## 8. References

For deeper dives into specific components, consult the following documentation:

*   **Concepts & Architecture**: [`tokensdk.md`](./tokensdk.md) - High-level overview of Panurus, terminology, and architecture.
*   **API Specification**: [`tokenapi.md`](./tokenapi.md) - Detailed explanation of the Token API, Token Requests, and Wallet Manager.
*   **Configuration**: [`core-token.md`](configuration.md) - Reference for compiling `core.yaml` and configuring Panurus.
*   **Services**: [`services.md`](./services.md) - Overview of the supporting services like Identity and Network services.
*   **Drivers**: [`driverapi.md`](./driverapi.md) - Information on implementing custom token drivers.
