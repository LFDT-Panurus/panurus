/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package ttx

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token"
	jsession "github.com/LFDT-Panurus/panurus/token/services/utils/json/session"
	utilsession "github.com/LFDT-Panurus/panurus/token/services/utils/session"
	"github.com/LFDT-Panurus/panurus/token/services/utils/session/mock"
	token2 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/hyperledger-labs/fabric-smart-client/platform/view/view"
	"github.com/stretchr/testify/require"
)

func TestVersionedRecipientRequestRoundTrip(t *testing.T) {
	original := &RecipientRequest{
		TMSID:    token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"},
		WalletID: []byte("wallet"),
		MultiSig: true,
		Nonce:    []byte("test-nonce-32bytes-padding-xxxxx"),
	}
	roundTripTTXMessage(t, TypeRecipientRequest, original, &RecipientRequest{})
}

func TestVersionedRecipientResponseRoundTrip(t *testing.T) {
	original := &RecipientResponse{
		RecipientData: &RecipientData{
			Identity:  []byte("recipient"),
			AuditInfo: []byte("audit"),
		},
		Signature: []byte("sig-bytes"),
	}
	roundTripTTXMessage(t, TypeRecipientResponse, original, &RecipientResponse{})
}

func TestVersionedRecipientResponseAckRoundTrip(t *testing.T) {
	original := &RecipientResponse{
		Signature: []byte("sig-bytes"),
	}
	roundTripTTXMessage(t, TypeRecipientResponse, original, &RecipientResponse{})
}

func TestVersionedExchangeRecipientRoundTrip(t *testing.T) {
	original := &ExchangeRecipientRequest{
		TMSID:     token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"},
		WalletID:  []byte("wallet"),
		Nonce:     []byte("exchange-nonce-32bytes-pad-xxxxx"),
		Signature: []byte("initiator-signature"),
	}
	roundTripTTXMessage(t, TypeExchangeRecipientRequest, original, &ExchangeRecipientRequest{})
}

func TestVersionedExchangeRecipientResponseRoundTrip(t *testing.T) {
	original := &ExchangeRecipientResponse{
		RecipientData: &RecipientData{
			Identity:  []byte("responder"),
			AuditInfo: []byte("audit"),
		},
		Signature: []byte("exchange-sig"),
	}
	roundTripTTXMessage(t, TypeExchangeRecipientResp, original, &ExchangeRecipientResponse{})
}

func TestVersionedMultisigRecipientDataRoundTrip(t *testing.T) {
	original := &MultisigRecipientData{
		RecipientData: &token.RecipientData{Identity: []byte("ms")},
		Nodes:         []view.Identity{view.Identity("a")},
	}
	roundTripTTXMessage(t, TypeMultisigRecipientData, original, &MultisigRecipientData{})
}

func TestVersionedPolicyRecipientDataRoundTrip(t *testing.T) {
	original := &PolicyRecipientData{
		RecipientData: &token.RecipientData{Identity: []byte("pol")},
	}
	roundTripTTXMessage(t, TypePolicyRecipientData, original, &PolicyRecipientData{})
}

func TestVersionedWithdrawalRequestRoundTrip(t *testing.T) {
	original := &WithdrawalRequest{
		TMSID:     token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"},
		TokenType: "TOK",
		Amount:    42,
	}
	roundTripTTXMessage(t, TypeWithdrawalRequest, original, &WithdrawalRequest{})
}

func TestVersionedWithdrawalChallengeRoundTrip(t *testing.T) {
	original := &WithdrawalChallenge{
		Nonce: []byte("withdrawal-nonce-32bytes-pad-xxxx"),
	}
	roundTripTTXMessage(t, TypeWithdrawalChallenge, original, &WithdrawalChallenge{})
}

func TestVersionedWithdrawalResponseRoundTrip(t *testing.T) {
	original := &WithdrawalResponse{
		Signature: []byte("attestation-sig"),
	}
	roundTripTTXMessage(t, TypeWithdrawalResponse, original, &WithdrawalResponse{})
}

func TestVersionedUpgradeAgreementRoundTrip(t *testing.T) {
	original := &UpgradeTokensAgreement{
		Challenge: []byte("challenge"),
		TMSID:     token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"},
	}
	roundTripTTXMessage(t, TypeUpgradeAgreement, original, &UpgradeTokensAgreement{})
}

func TestVersionedSignatureRequestRoundTrip(t *testing.T) {
	original := &SignatureRequest{
		TX:     []byte("tx-bytes"),
		Signer: view.Identity("signer"),
	}
	roundTripTTXMessage(t, TypeSignatureRequest, original, &SignatureRequest{})
}

func TestVersionedSignatureRoundTrip(t *testing.T) {
	original := &SignaturePayload{Signature: []byte("sigma")}
	roundTripTTXMessage(t, TypeSignature, original, &SignaturePayload{})
}

func TestVersionedTransactionRoundTrip(t *testing.T) {
	original := &TransactionPayload{Raw: []byte("tx-bytes")}
	roundTripTTXMessage(t, TypeTransaction, original, &TransactionPayload{})
}

func TestVersionedUpgradeRequestRoundTrip(t *testing.T) {
	original := &UpgradeTokensRequest{
		TMSID: token.TMSID{Network: "net", Channel: "ch", Namespace: "ns"},
		Tokens: []token2.LedgerToken{
			{ID: token2.ID{TxId: "tx1"}},
		},
	}
	roundTripTTXMessage(t, TypeUpgradeRequest, original, &UpgradeTokensRequest{})
}

func roundTripTTXMessage(t *testing.T, msgType string, sent, received any) {
	t.Helper()

	var wire []byte
	mockSession := &mock.Session{}
	mockSession.SendWithContextStub = func(_ context.Context, payload []byte) error {
		wire = append([]byte(nil), payload...)

		return nil
	}

	s := utilsession.New(mockSession, t.Context(), jsession.JSONMarshaller{})
	require.NoError(t, jsession.SendTyped(s, t.Context(), sent, msgType))

	var env jsession.Envelope
	require.NoError(t, json.Unmarshal(wire, &env))
	require.Equal(t, jsession.CurrentVersion, env.Version)
	require.Equal(t, msgType, env.Type)

	ch := make(chan *view.Message, 1)
	ch <- &view.Message{Payload: wire, Status: int32(view.OK)}
	mockSession.ReceiveReturns(ch)

	require.NoError(t, jsession.ReceiveTypedWithTimeout(s, msgType, received, time.Second))

	sentBytes, err := json.Marshal(sent)
	require.NoError(t, err)
	recvBytes, err := json.Marshal(received)
	require.NoError(t, err)
	require.JSONEq(t, string(sentBytes), string(recvBytes))
}
