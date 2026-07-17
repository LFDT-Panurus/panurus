# TTX Responder Threat Model

This document defines the responder-side threat model for the interactive protocols under `../../../token/services/ttx`. Every session envelope, transaction byte string, recipient structure, and spend request received from a remote initiator is hostile.

## Security Goals

A responder must:

- reject malformed input with an error rather than panic;
- release signatures or recipient data only after the protocol's authentication and consistency checks pass;
- avoid mutating identity, endpoint, wallet, or transaction state before validating the structures that authorize the mutation;
- bind acknowledgements to the transaction that the responder actually reviewed;
- reject ambiguous encodings rather than accepting multiple byte strings for the same logical message.

The application still decides whether a valid transaction satisfies its business rules. In particular, the multisig and policy spend flows deliberately return the assembled transaction so that application code can verify that it consumes the token named in the earlier `SpendRequest` before calling `EndorseView`.

## Trust Boundaries

| Boundary | Hostile input | Security-sensitive operation |
|----------|---------------|------------------------------|
| `ReceiveTransactionView` | Envelope body and ASN.1 transaction bytes | TMS lookup, request validation, persistence, later signatures |
| `EndorseView` | Signature request and final distributed transaction | Token-owner signature and node acknowledgement signature |
| Recipient responders | TMS ID, wallet ID, recipient data, nonce, composite follow-up | Recipient/audit-data release, signer registration, endpoint binding |
| Withdrawal and upgrade responders | Recipient data, token/proof material | Recipient registration and endpoint binding |
| Multisig/policy spend responders | Serialized `SpendRequest` and assembled transaction | Application approval followed by token-owner endorsement |
