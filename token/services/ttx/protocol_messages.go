/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

// Message-type discriminators stamped on the versioned envelope for every
// interactive ttx protocol message. These live with the service that uses
// them rather than in the generic session package.
const (
	// recipients.go
	TypeRecipientRequest         = "recipient_req"
	TypeRecipientResponse        = "recipient_resp"
	TypeExchangeRecipientRequest = "exchange_req"
	TypeExchangeRecipientResp    = "exchange_resp"
	TypeMultisigRecipientData    = "multisig_data"
	TypePolicyRecipientData      = "policy_data"

	// withdrawal.go
	TypeWithdrawalRequest   = "withdrawal_req"
	TypeWithdrawalChallenge = "withdrawal_challenge"
	TypeWithdrawalResponse  = "withdrawal_resp"

	// upgrade.go
	TypeUpgradeAgreement = "upgrade_agree"
	TypeUpgradeRequest   = "upgrade_req"

	// multisig/spend.go and boolpolicy/spend.go
	TypeSpendRequest  = "spend_req"
	TypeSpendResponse = "spend_resp"

	// collectendorsements.go, endorse.go, accept.go, auditor.go, receivetx.go
	TypeSignatureRequest    = "sig_req"
	TypeSignature           = "signature"
	TypeTransaction         = "transaction"
	TypeTransactionResponse = "tx_resp"

	// collectactions.go
	TypeActions        = "actions"
	TypeActionTransfer = "action_transfer"
)

// TransactionPayload carries a serialized transaction on the wire.
type TransactionPayload struct {
	Raw []byte `json:"raw"`
}

// SignaturePayload carries a signature on the wire.
type SignaturePayload struct {
	Signature []byte `json:"signature"`
}
