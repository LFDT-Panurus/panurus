/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package validator_test

import (
	"context"
	"crypto"
	"encoding/json"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/actions"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/setup"
	"github.com/LFDT-Panurus/panurus/token/core/fabtoken/v1/validator"
	validator2 "github.com/LFDT-Panurus/panurus/token/core/zkatdlog/nogh/v1/validator"
	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/driver/protos-go/v1/request"
	benchmark2 "github.com/LFDT-Panurus/panurus/token/services/benchmark"
	"github.com/LFDT-Panurus/panurus/token/services/identity"
	"github.com/LFDT-Panurus/panurus/token/services/identity/x509"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	"github.com/LFDT-Panurus/panurus/token/services/logging"
	"github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestActionDeserializer_DeserializeActions(t *testing.T) {
	ad := &validator.ActionDeserializer{}

	t.Run("Empty", func(t *testing.T) {
		tr := &driver.TokenRequest{}
		ia, ta, err := ad.DeserializeActions(tr)
		require.NoError(t, err)
		assert.Empty(t, ia)
		assert.Empty(t, ta)
	})

	t.Run("WithIssue", func(t *testing.T) {
		ia1 := &actions.IssueAction{Issuer: []byte("issuer1")}
		ia1Bytes, err := ia1.Serialize()
		require.NoError(t, err)

		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: ia1Bytes},
			},
		}
		ia, ta, err := ad.DeserializeActions(tr)
		require.NoError(t, err)
		assert.Len(t, ia, 1)
		assert.Empty(t, ta)
		assert.Equal(t, ia1.Issuer, ia[0].Issuer)
	})

	t.Run("WithTransfer", func(t *testing.T) {
		ta1 := &actions.TransferAction{Issuer: []byte("issuer1")}
		ta1Bytes, err := ta1.Serialize()
		require.NoError(t, err)

		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: ta1Bytes},
			},
		}
		ia, ta, err := ad.DeserializeActions(tr)
		require.NoError(t, err)
		assert.Empty(t, ia)
		assert.Len(t, ta, 1)
		assert.Equal(t, ta1.Issuer, ta[0].Issuer)
	})

	t.Run("IssueDeserializeError", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: []byte("invalid")},
			},
		}
		_, _, err := ad.DeserializeActions(tr)
		require.Error(t, err)
	})

	t.Run("TransferDeserializeError", func(t *testing.T) {
		tr := &driver.TokenRequest{
			Actions: []*driver.TypedAction{
				{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: []byte("invalid")},
			},
		}
		_, _, err := ad.DeserializeActions(tr)
		require.Error(t, err)
	})
}

func TestNewValidator(t *testing.T) {
	logger := logging.MustGetLogger("test")
	pp := &setup.PublicParams{}
	deserializer := &mock.Deserializer{}

	v := validator.NewValidator(logger, pp, deserializer, nil, nil, nil)
	assert.NotNil(t, v)
}

func TestIssueValidate(t *testing.T) {
	ctx := context.Background()
	pp := &setup.PublicParams{
		QuantityPrecision: 64,
		IssuerIDs:         []driver.Identity{[]byte("issuer1")},
	}
	deserializer := &mock.Deserializer{}
	sigProvider := &mock.SignatureProvider{}

	t.Run("EmptyOutputs", func(t *testing.T) {
		ia := &actions.IssueAction{
			Issuer: []byte("issuer1"),
		}
		c := &validator.Context{
			PP:          pp,
			IssueAction: ia,
		}
		err := validator.IssueValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "no outputs in issue action")
	})

	t.Run("ZeroQuantity", func(t *testing.T) {
		ia := &actions.IssueAction{
			Issuer: []byte("issuer1"),
			Outputs: []*actions.Output{
				{
					Quantity: "0",
					Type:     "ABC",
					Owner:    []byte("owner1"),
				},
			},
		}
		c := &validator.Context{
			PP:          pp,
			IssueAction: ia,
		}
		err := validator.IssueValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "quantity is zero")
	})

	t.Run("UnauthorizedIssuer", func(t *testing.T) {
		ia := &actions.IssueAction{
			Issuer: []byte("unauthorized"),
			Outputs: []*actions.Output{
				{
					Quantity: "100",
					Type:     "ABC",
					Owner:    []byte("owner1"),
				},
			},
		}
		c := &validator.Context{
			PP:          pp,
			IssueAction: ia,
		}
		err := validator.IssueValidate(ctx, c)
		require.Error(t, err)
		require.ErrorIs(t, err, validator2.ErrIssuerNotAuthorized)
		assert.Contains(t, err.Error(), validator2.ErrIssuerNotAuthorized.Error())
	})

	t.Run("VerifierError", func(t *testing.T) {
		ia := &actions.IssueAction{
			Issuer: []byte("issuer1"),
			Outputs: []*actions.Output{
				{
					Quantity: "100",
					Type:     "ABC",
					Owner:    []byte("owner1"),
				},
			},
		}
		deserializer.GetIssuerVerifierReturns(nil, assert.AnError)
		c := &validator.Context{
			PP:           pp,
			IssueAction:  ia,
			Deserializer: deserializer,
		}
		err := validator.IssueValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed getting verifier for issuer identity")
	})

	t.Run("SignatureVerificationError", func(t *testing.T) {
		ia := &actions.IssueAction{
			Issuer: []byte("issuer1"),
			Outputs: []*actions.Output{
				{
					Quantity: "100",
					Type:     "ABC",
					Owner:    []byte("owner1"),
				},
			},
		}
		deserializer.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns(nil, assert.AnError)
		c := &validator.Context{
			PP:                pp,
			IssueAction:       ia,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
		}
		err := validator.IssueValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed verifying signature")
	})

	t.Run("Success", func(t *testing.T) {
		ia := &actions.IssueAction{
			Issuer: []byte("issuer1"),
			Outputs: []*actions.Output{
				{
					Quantity: "100",
					Type:     "ABC",
					Owner:    []byte("owner1"),
				},
			},
		}
		deserializer.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns([]byte("signature"), nil)
		c := &validator.Context{
			PP:                pp,
			IssueAction:       ia,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
		}
		err := validator.IssueValidate(ctx, c)
		require.NoError(t, err)
	})

	t.Run("SuccessNoIssuersInPP", func(t *testing.T) {
		ppNoIssuers := &setup.PublicParams{
			QuantityPrecision: 64,
		}
		ia := &actions.IssueAction{
			Issuer: []byte("any-issuer"),
			Outputs: []*actions.Output{
				{
					Quantity: "100",
					Type:     "ABC",
					Owner:    []byte("owner1"),
				},
			},
		}
		deserializer.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns([]byte("signature"), nil)
		c := &validator.Context{
			PP:                ppNoIssuers,
			IssueAction:       ia,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
		}
		err := validator.IssueValidate(ctx, c)
		require.NoError(t, err)
	})
}

func TestTransferActionValidate(t *testing.T) {
	ctx := context.Background()
	ta := &actions.TransferAction{
		Inputs: []*actions.TransferActionInput{
			{
				ID: &token.ID{TxId: "tx1", Index: 0},
				Input: &actions.Output{
					Type:     "ABC",
					Quantity: "100",
					Owner:    []byte("owner1"),
				},
			},
		},
		Outputs: []*actions.Output{
			{
				Quantity: "100",
				Type:     "ABC",
				Owner:    []byte("owner1"),
			},
		},
	}
	c := &validator.Context{
		TransferAction: ta,
	}
	err := validator.TransferActionValidate(ctx, c)
	require.NoError(t, err)
}

func TestTransferSignatureValidate(t *testing.T) {
	ctx := context.Background()
	logger := logging.MustGetLogger("test")
	pp := &setup.PublicParams{
		IssuerIDs: []driver.Identity{[]byte("issuer1")},
	}

	t.Run("NoInputs", func(t *testing.T) {
		ta := &actions.TransferAction{}
		c := &validator.Context{
			TransferAction: ta,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expected at least 1")
	})

	t.Run("OwnerVerifierError", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		ta := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(nil, assert.AnError)
		c := &validator.Context{
			TransferAction: ta,
			Deserializer:   deserializer,
			Logger:         logger,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed deserializing owner")
	})

	t.Run("OwnerSignatureError", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns(nil, assert.AnError)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed signature verification")
	})

	t.Run("SuccessTransfer", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
			Outputs: []*actions.Output{
				{
					Owner: []byte("owner2"),
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns([]byte("sig1"), nil)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                pp,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.NoError(t, err)
		assert.Len(t, c.Signatures, 1)
		assert.Len(t, c.InputTokens, 1)
	})

	t.Run("RedeemWithoutIssuer", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
			Outputs: []*actions.Output{
				{
					Owner: nil, // redeem
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns([]byte("sig1"), nil)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                pp,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "must have at least one issuer")
	})

	t.Run("RedeemWithIssuer", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Issuer: []byte("issuer1"),
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
			Outputs: []*actions.Output{
				{
					Owner: nil, // redeem
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		deserializer.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns([]byte("sig"), nil)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                pp,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.NoError(t, err)
		assert.Len(t, c.Signatures, 2) // one for owner, one for issuer
	})

	t.Run("RedeemIssuerVerifierError", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Issuer: []byte("issuer1"),
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
			Outputs: []*actions.Output{
				{
					Owner: nil, // redeem
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		deserializer.GetIssuerVerifierReturns(nil, assert.AnError)
		sigProvider.HasBeenSignedByReturns([]byte("sig"), nil)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                pp,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed deserializing issuer")
	})

	t.Run("SameOwnerCachesVerifier", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Inputs: []*actions.TransferActionInput{
				{Input: &actions.Output{Owner: []byte("owner1")}},
				{Input: &actions.Output{Owner: []byte("owner1")}},
				{Input: &actions.Output{Owner: []byte("owner1")}},
			},
			Outputs: []*actions.Output{
				{Owner: []byte("owner2")},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		sigProvider.HasBeenSignedByReturns([]byte("sig"), nil)
		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                pp,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.NoError(t, err)
		assert.Equal(t, 1, deserializer.GetOwnerVerifierCallCount(), "GetOwnerVerifier should be called only once for repeated same owner")
	})

	t.Run("RedeemIssuerSignatureError", func(t *testing.T) {
		deserializer := &mock.Deserializer{}
		sigProvider := &mock.SignatureProvider{}
		ta := &actions.TransferAction{
			Issuer: []byte("issuer1"),
			Inputs: []*actions.TransferActionInput{
				{
					Input: &actions.Output{
						Owner: []byte("owner1"),
					},
				},
			},
			Outputs: []*actions.Output{
				{
					Owner: nil, // redeem
				},
			},
		}
		deserializer.GetOwnerVerifierReturns(&mock.Verifier{}, nil)
		deserializer.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
		// first call (for owner) returns success
		sigProvider.HasBeenSignedByReturnsOnCall(0, []byte("sig-owner"), nil)
		// second call (for issuer) returns error
		sigProvider.HasBeenSignedByReturnsOnCall(1, nil, assert.AnError)

		c := &validator.Context{
			TransferAction:    ta,
			Deserializer:      deserializer,
			SignatureProvider: sigProvider,
			Logger:            logger,
			PP:                pp,
		}
		err := validator.TransferSignatureValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed signature verification")
	})
}

func TestTransferBalanceValidate(t *testing.T) {
	ctx := context.Background()
	pp := &setup.PublicParams{
		QuantityPrecision: 64,
	}

	t.Run("NoOutputs", func(t *testing.T) {
		ta := &actions.TransferAction{}
		c := &validator.Context{
			TransferAction: ta,
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "there is no output")
	})

	t.Run("NoInputs", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    nil,
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "there is no input")
	})

	t.Run("NilFirstInput", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{nil},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "first input is nil")
	})

	t.Run("NilInput", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "100"}, nil},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "input 1 is nil")
	})

	t.Run("ParseQuantityInputError", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "invalid"}},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed parsing quantity")
	})

	t.Run("MismatchedInputType", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100", Type: "ABC"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "50", Type: "ABC"}, {Quantity: "50", Type: "XYZ"}},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match type")
	})

	t.Run("ParseQuantityOutputError", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "invalid"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "100"}},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed parsing quantity")
	})

	t.Run("MismatchedOutputType", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100", Type: "XYZ"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "100", Type: "ABC"}},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match type")
	})

	t.Run("Unbalanced", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "101", Type: "ABC"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "100", Type: "ABC"}},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "does not match output sum")
	})

	t.Run("Success", func(t *testing.T) {
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Quantity: "100", Type: "ABC"}, {Quantity: "50", Type: "ABC"}},
		}
		c := &validator.Context{
			TransferAction: ta,
			PP:             pp,
			InputTokens:    []*actions.Output{{Quantity: "150", Type: "ABC"}},
		}
		err := validator.TransferBalanceValidate(ctx, c)
		require.NoError(t, err)
	})
}

func TestTransferHTLCValidate(t *testing.T) {
	ctx := context.Background()

	t.Run("NoHTLC", func(t *testing.T) {
		owner1, _ := identity.WrapWithType(x509.IdentityType, []byte("owner1"))
		owner2, _ := identity.WrapWithType(x509.IdentityType, []byte("owner2"))
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Owner: owner1}},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: owner2}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.NoError(t, err)
	})

	t.Run("InputIsHTLC_Reclaim_Success", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		preimage := []byte("preimage")
		hash := crypto.SHA256.New()
		hash.Write(preimage)
		img := hash.Sum(nil)

		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(-1 * time.Hour), // expired
			HashInfo: htlc.HashInfo{
				Hash:         img,
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, err := json.Marshal(script)
		require.NoError(t, err)
		htlcOwner, err := identity.WrapWithType(htlc.ScriptType, scriptBytes)
		require.NoError(t, err)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: sender, Type: "ABC", Quantity: "100"},
			},
		}
		c := &validator.Context{
			TransferAction:  ta,
			InputTokens:     []*actions.Output{{Owner: htlcOwner, Type: "ABC", Quantity: "100"}},
			Signatures:      [][]byte{[]byte("sig")},
			MetadataCounter: make(map[string]int),
		}
		err = validator.TransferHTLCValidate(ctx, c)
		require.NoError(t, err)
	})

	t.Run("InputIsHTLC_Claim_Success", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		preimage := []byte("preimage")
		hash := crypto.SHA256.New()
		hash.Write(preimage)
		img := hash.Sum(nil)
		// encoded image for claim key
		imgEncoded := []byte(encoding.Base64.New().EncodeToString(img))

		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(1 * time.Hour), // not expired
			HashInfo: htlc.HashInfo{
				Hash:         imgEncoded,
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, err := json.Marshal(script)
		require.NoError(t, err)
		htlcOwner, err := identity.WrapWithType(htlc.ScriptType, scriptBytes)
		require.NoError(t, err)

		claimSig := &htlc.ClaimSignature{
			Preimage:           preimage,
			RecipientSignature: []byte("rec-sig"),
		}
		claimSigBytes, err := json.Marshal(claimSig)
		require.NoError(t, err)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: recipient, Type: "ABC", Quantity: "100"},
			},
			Metadata: map[string][]byte{
				htlc.ClaimKey(imgEncoded): preimage,
			},
		}
		c := &validator.Context{
			TransferAction:  ta,
			InputTokens:     []*actions.Output{{Owner: htlcOwner, Type: "ABC", Quantity: "100"}},
			Signatures:      [][]byte{claimSigBytes},
			MetadataCounter: make(map[string]int),
		}
		err = validator.TransferHTLCValidate(ctx, c)
		require.NoError(t, err)
		assert.Equal(t, 1, c.MetadataCounter[htlc.ClaimKey(imgEncoded)])
	})

	t.Run("OutputIsHTLC_Success", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		preimage := []byte("preimage")
		hash := crypto.SHA256.New()
		hash.Write(preimage)
		img := hash.Sum(nil)

		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(1 * time.Hour), // not expired
			HashInfo: htlc.HashInfo{
				Hash:         img,
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, err := json.Marshal(script)
		require.NoError(t, err)
		htlcOwner, err := identity.WrapWithType(htlc.ScriptType, scriptBytes)
		require.NoError(t, err)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: htlcOwner, Type: "ABC", Quantity: "100"},
			},
			Metadata: map[string][]byte{
				htlc.LockKey(img): htlc.LockValue(img),
			},
		}
		c := &validator.Context{
			TransferAction:  ta,
			InputTokens:     []*actions.Output{{Owner: sender, Type: "ABC", Quantity: "100"}},
			MetadataCounter: make(map[string]int),
		}
		err = validator.TransferHTLCValidate(ctx, c)
		require.NoError(t, err)
		assert.Equal(t, 1, c.MetadataCounter[htlc.LockKey(img)])
	})

	t.Run("InputIsHTLC_InvalidOwner", func(t *testing.T) {
		htlcOwner := []byte("invalid-typed-identity")
		ta := &actions.TransferAction{
			Outputs: []*actions.Output{{Owner: []byte("rec")}},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: htlcOwner}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unmarshal owner")
	})

	t.Run("InputIsHTLC_TwoOutputs_Error", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(1 * time.Hour),
			HashInfo: htlc.HashInfo{
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, _ := json.Marshal(script)
		htlcOwner, _ := identity.WrapWithType(htlc.ScriptType, scriptBytes)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: recipient, Type: "ABC", Quantity: "100"},
				{Owner: sender, Type: "ABC", Quantity: "50"},
			},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: htlcOwner, Type: "ABC", Quantity: "150"}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "an htlc script only transfers the ownership of a token")
	})

	t.Run("InputIsHTLC_TypeMismatch_Error", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(1 * time.Hour),
			HashInfo: htlc.HashInfo{
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, _ := json.Marshal(script)
		htlcOwner, _ := identity.WrapWithType(htlc.ScriptType, scriptBytes)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: recipient, Type: "XYZ", Quantity: "100"},
			},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: htlcOwner, Type: "ABC", Quantity: "100"}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "type of input does not match type of output")
	})

	t.Run("InputIsHTLC_QuantityMismatch_Error", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(1 * time.Hour),
			HashInfo: htlc.HashInfo{
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, _ := json.Marshal(script)
		htlcOwner, _ := identity.WrapWithType(htlc.ScriptType, scriptBytes)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: recipient, Type: "ABC", Quantity: "101"},
			},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: htlcOwner, Type: "ABC", Quantity: "100"}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "quantity of input does not match quantity of output")
	})

	t.Run("InputIsHTLC_Redeem_Error", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(1 * time.Hour),
			HashInfo: htlc.HashInfo{
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, _ := json.Marshal(script)
		htlcOwner, _ := identity.WrapWithType(htlc.ScriptType, scriptBytes)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: nil, Type: "ABC", Quantity: "100"},
			},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: htlcOwner, Type: "ABC", Quantity: "100"}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "should not be a redeem")
	})

	t.Run("OutputIsHTLC_Expired_Error", func(t *testing.T) {
		sender, _ := identity.WrapWithType(x509.IdentityType, []byte("sender"))
		recipient, _ := identity.WrapWithType(x509.IdentityType, []byte("recipient"))
		script := &htlc.Script{
			Sender:    sender,
			Recipient: recipient,
			Deadline:  time.Now().Add(-1 * time.Hour), // expired
			HashInfo: htlc.HashInfo{
				HashFunc:     crypto.SHA256,
				HashEncoding: encoding.Base64,
			},
		}
		scriptBytes, _ := json.Marshal(script)
		htlcOwner, _ := identity.WrapWithType(htlc.ScriptType, scriptBytes)

		ta := &actions.TransferAction{
			Outputs: []*actions.Output{
				{Owner: htlcOwner, Type: "ABC", Quantity: "100"},
			},
		}
		c := &validator.Context{
			TransferAction: ta,
			InputTokens:    []*actions.Output{{Owner: sender, Type: "ABC", Quantity: "100"}},
		}
		err := validator.TransferHTLCValidate(ctx, c)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "expiration date has already passed")
	})
}

// BenchmarkValidatorTransfer benchmarks the verification of a transfer token request.
func BenchmarkValidatorTransfer(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			n := int(benchmark2.SetupSamples()) // #nosec G115
			if n == 0 {
				n = min(b.N, 1000)
			}
			env, err := newBenchmarkValidatorEnv(n, tc.BenchmarkCase, false)
			require.NoError(b, err)

			b.ResetTimer()
			i := 0
			for b.Loop() {
				e := env.Envs[i%len(env.Envs)]
				_, _, err := e.v.VerifyTokenRequestFromRaw(
					b.Context(),
					nil,
					"an_anchor",
					e.raw,
				)
				require.NoError(b, err)
				i++
			}
		})
	}
}

// TestParallelBenchmarkValidatorTransfer runs the validator transfer benchmark in parallel.
func TestParallelBenchmarkValidatorTransfer(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkValidatorEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkValidatorEnv, error) {
			return newBenchmarkValidatorEnv(1, c, false)
		},
		func(ctx context.Context, env *benchmarkValidatorEnv) error {
			_, _, err := env.Envs[0].v.VerifyTokenRequestFromRaw(
				ctx,
				nil,
				"an_anchor",
				env.Envs[0].raw,
			)

			return err
		},
	)
}

// BenchmarkValidatorIssue benchmarks the verification of an issue token request.
func BenchmarkValidatorIssue(b *testing.B) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(b, err)

	for _, tc := range cases {
		b.Run(tc.Name, func(b *testing.B) {
			n := int(benchmark2.SetupSamples()) // #nosec G115
			if n == 0 {
				n = min(b.N, 1000)
			}
			env, err := newBenchmarkValidatorEnv(n, tc.BenchmarkCase, true)
			require.NoError(b, err)

			b.ResetTimer()
			i := 0
			for b.Loop() {
				e := env.Envs[i%len(env.Envs)]
				_, _, err := e.v.VerifyTokenRequestFromRaw(
					b.Context(),
					nil,
					"an_anchor",
					e.raw,
				)
				require.NoError(b, err)
				i++
			}
		})
	}
}

// TestParallelBenchmarkValidatorIssue runs the validator issue benchmark in parallel.
func TestParallelBenchmarkValidatorIssue(t *testing.T) {
	_, _, cases, err := benchmark2.GenerateCasesWithDefaults()
	require.NoError(t, err)

	test := benchmark2.NewTest[*benchmarkValidatorEnv](cases)
	test.RunBenchmark(t,
		func(c *benchmark2.Case) (*benchmarkValidatorEnv, error) {
			return newBenchmarkValidatorEnv(1, c, true)
		},
		func(ctx context.Context, env *benchmarkValidatorEnv) error {
			_, _, err := env.Envs[0].v.VerifyTokenRequestFromRaw(
				ctx,
				nil,
				"an_anchor",
				env.Envs[0].raw,
			)

			return err
		},
	)
}

type validatorEnv struct {
	v   *validator.Validator
	raw []byte
}

func newBenchmarkValidatorEnv(n int, benchmarkCase *benchmark2.Case, isIssue bool) (*benchmarkValidatorEnv, error) {
	envs := make([]*validatorEnv, n)
	for i := range n {
		env, err := newValidatorEnv(benchmarkCase, isIssue)
		if err != nil {
			return nil, err
		}
		envs[i] = env
	}

	return &benchmarkValidatorEnv{Envs: envs}, nil
}

type benchmarkValidatorEnv struct {
	Envs []*validatorEnv
}

func newValidatorEnv(benchmarkCase *benchmark2.Case, isIssue bool) (*validatorEnv, error) {
	logger := logging.MustGetLogger("test")
	pp, err := setup.Setup(64)
	if err != nil {
		return nil, err
	}
	des := &mock.Deserializer{}
	v := validator.NewValidator(logger, pp, des, nil, nil, nil)

	id, _ := identity.WrapWithType(x509.IdentityType, []byte("owner"))
	issuer, _ := identity.WrapWithType(x509.IdentityType, []byte("issuer"))

	tr := &driver.TokenRequest{}
	if isIssue {
		ia := &actions.IssueAction{
			Issuer: issuer,
		}
		for range benchmarkCase.NumOutputs {
			ia.Outputs = append(ia.Outputs, &actions.Output{
				Quantity: token.NewQuantityFromUInt64(100).Hex(),
				Type:     "ABC",
				Owner:    id,
			})
		}
		rawIA, err := ia.Serialize()
		if err != nil {
			return nil, err
		}
		tr.Actions = []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_ISSUE, Raw: rawIA},
		}
		tr.Signatures = []*driver.RequestSignature{
			{Action: &driver.ActionSignature{Signature: []byte("signature")}},
		}
	} else {
		ta := &actions.TransferAction{
			Issuer: issuer,
		}
		for range benchmarkCase.NumInputs {
			ta.Inputs = append(ta.Inputs, &actions.TransferActionInput{
				ID: &token.ID{TxId: "tx1", Index: 0},
				Input: &actions.Output{
					Quantity: token.NewQuantityFromUInt64(100).Hex(),
					Type:     "ABC",
					Owner:    id,
				},
			})
		}
		for range benchmarkCase.NumOutputs {
			ta.Outputs = append(ta.Outputs, &actions.Output{
				Quantity: token.NewQuantityFromUInt64(100).Hex(),
				Type:     "ABC",
				Owner:    id,
			})
		}
		rawTA, err := ta.Serialize()
		if err != nil {
			return nil, err
		}
		tr.Actions = []*driver.TypedAction{
			{Type: request.ActionType_ACTION_TYPE_TRANSFER, Raw: rawTA},
		}
		for range benchmarkCase.NumInputs {
			tr.Signatures = append(tr.Signatures, &driver.RequestSignature{
				Action: &driver.ActionSignature{Signature: []byte("signature")},
			})
		}
	}

	raw, err := tr.Bytes()
	if err != nil {
		return nil, err
	}

	des.GetIssuerVerifierReturns(&mock.Verifier{}, nil)
	des.GetOwnerVerifierReturns(&mock.Verifier{}, nil)

	return &validatorEnv{
		v:   v,
		raw: raw,
	}, nil
}
