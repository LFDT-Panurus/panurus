/*
Copyright IBM Corp. All Rights Reserved.

SPDX-License-Identifier: Apache-2.0
*/

package htlc_test

import (
	"context"
	"crypto"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/LFDT-Panurus/panurus/token/driver"
	"github.com/LFDT-Panurus/panurus/token/driver/mock"
	"github.com/LFDT-Panurus/panurus/token/services/identity/marshal"
	"github.com/LFDT-Panurus/panurus/token/services/interop/encoding"
	"github.com/LFDT-Panurus/panurus/token/services/interop/htlc"
	token3 "github.com/LFDT-Panurus/panurus/token/token"
	"github.com/stretchr/testify/require"
)

func TestHashInfoValidate(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		hi := &htlc.HashInfo{
			Hash:         []byte("somehash"),
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Base64,
		}
		require.NoError(t, hi.Validate())
	})

	t.Run("unavailable hash function", func(t *testing.T) {
		hi := &htlc.HashInfo{
			Hash:         []byte("somehash"),
			HashFunc:     crypto.Hash(999),
			HashEncoding: encoding.Base64,
		}
		require.EqualError(t, hi.Validate(), "hash function not available")
	})

	t.Run("unavailable encoding", func(t *testing.T) {
		hi := &htlc.HashInfo{
			Hash:         []byte("somehash"),
			HashFunc:     crypto.SHA256,
			HashEncoding: encoding.Encoding(999),
		}
		require.EqualError(t, hi.Validate(), "encoding function not available")
	})
}

func TestHashInfoImage(t *testing.T) {
	preImage := []byte("secret")

	t.Run("SHA256 with None encoding", func(t *testing.T) {
		hi := &htlc.HashInfo{HashFunc: crypto.SHA256, HashEncoding: encoding.None}
		image, err := hi.Image(preImage)
		require.NoError(t, err)

		h := crypto.SHA256.New()
		h.Write(preImage)
		require.Equal(t, h.Sum(nil), image)
	})

	t.Run("SHA256 with Base64 encoding", func(t *testing.T) {
		hi := &htlc.HashInfo{HashFunc: crypto.SHA256, HashEncoding: encoding.Base64}

		img1, err := hi.Image(preImage)
		require.NoError(t, err)
		require.NotEmpty(t, img1)

		// deterministic
		img2, err := hi.Image(preImage)
		require.NoError(t, err)
		require.Equal(t, img1, img2)

		// different input -> different output
		img3, err := hi.Image([]byte("other"))
		require.NoError(t, err)
		require.NotEqual(t, img1, img3)
	})

	t.Run("SHA256 with Hex encoding", func(t *testing.T) {
		hi := &htlc.HashInfo{HashFunc: crypto.SHA256, HashEncoding: encoding.Hex}
		image, err := hi.Image(preImage)
		require.NoError(t, err)
		require.NotEmpty(t, image)
	})

	t.Run("invalid hash info returns error", func(t *testing.T) {
		hi := &htlc.HashInfo{HashFunc: crypto.Hash(999), HashEncoding: encoding.Base64}
		_, err := hi.Image(preImage)
		require.Error(t, err)
		require.Contains(t, err.Error(), "hash info not valid")
	})
}

func TestHashInfoCompare(t *testing.T) {
	hi := &htlc.HashInfo{
		Hash:         []byte("expected"),
		HashFunc:     crypto.SHA256,
		HashEncoding: encoding.Base64,
	}
	require.NoError(t, hi.Compare([]byte("expected")))
	require.Error(t, hi.Compare([]byte("something else")))
}

func TestHashInfoImageThenCompare(t *testing.T) {
	hi := &htlc.HashInfo{HashFunc: crypto.SHA256, HashEncoding: encoding.Base64}

	image, err := hi.Image([]byte("secret"))
	require.NoError(t, err)

	hi.Hash = image
	require.NoError(t, hi.Compare(image))

	wrongImage, err := hi.Image([]byte("wrong"))
	require.NoError(t, err)
	require.Error(t, hi.Compare(wrongImage))
}

func validHashInfo() htlc.HashInfo {
	return htlc.HashInfo{
		Hash:         []byte("hash"),
		HashFunc:     crypto.SHA256,
		HashEncoding: encoding.Base64,
	}
}

func TestScriptValidate(t *testing.T) {
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	tests := []struct {
		name   string
		script htlc.Script
		errMsg string
	}{
		{
			name:   "valid script",
			script: htlc.Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: future, HashInfo: validHashInfo()},
		},
		{
			name:   "missing sender",
			script: htlc.Script{Recipient: []byte("r"), Deadline: future, HashInfo: validHashInfo()},
			errMsg: "sender not set",
		},
		{
			name:   "missing recipient",
			script: htlc.Script{Sender: []byte("s"), Deadline: future, HashInfo: validHashInfo()},
			errMsg: "recipient not set",
		},
		{
			name:   "expired deadline",
			script: htlc.Script{Sender: []byte("s"), Recipient: []byte("r"), Deadline: past, HashInfo: validHashInfo()},
			errMsg: "expiration date has already passed",
		},
		{
			name: "invalid hash info",
			script: htlc.Script{
				Sender: []byte("s"), Recipient: []byte("r"), Deadline: future,
				HashInfo: htlc.HashInfo{Hash: []byte("h"), HashFunc: crypto.Hash(999), HashEncoding: encoding.Base64},
			},
			errMsg: "hash function not available",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.script.Validate(time.Now())
			if tt.errMsg == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, tt.errMsg)
			}
		})
	}
}

func TestScriptFromBytes(t *testing.T) {
	original := htlc.Script{
		Sender:    []byte("sender"),
		Recipient: []byte("recipient"),
		Deadline:  time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
		HashInfo:  validHashInfo(),
	}
	raw, err := json.Marshal(original)
	require.NoError(t, err)

	var parsed htlc.Script
	require.NoError(t, parsed.FromBytes(raw))
	require.Equal(t, original.Sender, parsed.Sender)
	require.Equal(t, original.Recipient, parsed.Recipient)
	require.True(t, original.Deadline.Equal(parsed.Deadline))
	require.Equal(t, original.HashInfo.HashFunc, parsed.HashInfo.HashFunc)
	require.Equal(t, original.HashInfo.HashEncoding, parsed.HashInfo.HashEncoding)
}

func TestScriptFromBytesInvalidJSON(t *testing.T) {
	var s htlc.Script
	require.Error(t, s.FromBytes([]byte("not json")))
}

func TestScriptAuthAmIAnAuditor(t *testing.T) {
	sa := htlc.NewScriptAuth(nil)
	require.False(t, sa.AmIAnAuditor())
}

func TestScriptAuthIssued(t *testing.T) {
	sa := htlc.NewScriptAuth(nil)
	require.False(t, sa.Issued(t.Context(), nil, &token3.Token{}))
}

func TestScriptAuthOwnerType(t *testing.T) {
	scriptBytes, err := json.Marshal(htlc.Script{
		Sender:    []byte("sender"),
		Recipient: []byte("recipient"),
	})
	require.NoError(t, err)

	raw := marshal.EncodeIdentity(driver.HTLCScriptIdentityType, scriptBytes)

	sa := htlc.NewScriptAuth(nil)
	ownerType, data, err := sa.OwnerType(raw)
	require.NoError(t, err)
	require.Equal(t, driver.HTLCScriptIdentityType, ownerType)
	require.Equal(t, scriptBytes, data)
}

func TestScriptAuthOwnerTypeInvalidBytes(t *testing.T) {
	sa := htlc.NewScriptAuth(nil)
	_, _, err := sa.OwnerType([]byte("garbage"))
	require.Error(t, err)
}

// makeHTLCToken builds a token whose owner field is a serialized HTLC script
// with the given sender and recipient identities.
func makeHTLCToken(t *testing.T, sender, recipient []byte) *token3.Token {
	t.Helper()
	scriptBytes, err := json.Marshal(htlc.Script{Sender: sender, Recipient: recipient})
	require.NoError(t, err)

	return &token3.Token{
		Owner:    marshal.EncodeIdentity(driver.HTLCScriptIdentityType, scriptBytes),
		Type:     "USD",
		Quantity: "100",
	}
}

// stubWalletService returns a mock WalletService where OwnerWallet resolves
// the given sender/recipient identity strings to wallets with the given IDs.
// Pass "" to make a lookup fail (simulating "not my wallet").
func stubWalletService(senderID, recipientID string) *mock.WalletService {
	ws := &mock.WalletService{}
	ws.OwnerWalletStub = func(_ context.Context, id driver.WalletLookupID) (driver.OwnerWallet, error) {
		idBytes, ok := id.(driver.Identity)
		if !ok {
			return nil, errors.New("wallet not found")
		}
		switch string(idBytes) {
		case "sender":
			if senderID != "" {
				ow := &mock.OwnerWallet{}
				ow.IDReturns(senderID)

				return ow, nil
			}
		case "recipient":
			if recipientID != "" {
				ow := &mock.OwnerWallet{}
				ow.IDReturns(recipientID)

				return ow, nil
			}
		}

		return nil, errors.New("wallet not found")
	}

	return ws
}

func TestScriptAuthIsMine(t *testing.T) {
	tok := makeHTLCToken(t, []byte("sender"), []byte("recipient"))

	t.Run("sender owns token", func(t *testing.T) {
		sa := htlc.NewScriptAuth(stubWalletService("wallet1", ""))
		_, ids, mine := sa.IsMine(t.Context(), tok)
		require.True(t, mine)
		require.Contains(t, ids, "htlc.senderwallet1")
	})

	t.Run("recipient owns token", func(t *testing.T) {
		sa := htlc.NewScriptAuth(stubWalletService("", "wallet2"))
		_, ids, mine := sa.IsMine(t.Context(), tok)
		require.True(t, mine)
		require.Contains(t, ids, "htlc.recipientwallet2")
	})

	t.Run("both own token", func(t *testing.T) {
		sa := htlc.NewScriptAuth(stubWalletService("wallet1", "wallet2"))
		_, ids, mine := sa.IsMine(t.Context(), tok)
		require.True(t, mine)
		require.Len(t, ids, 2)
		require.Contains(t, ids, "htlc.senderwallet1")
		require.Contains(t, ids, "htlc.recipientwallet2")
	})

	t.Run("neither owns token", func(t *testing.T) {
		sa := htlc.NewScriptAuth(stubWalletService("", ""))
		_, ids, mine := sa.IsMine(t.Context(), tok)
		require.False(t, mine)
		require.Empty(t, ids)
	})
}

func TestScriptAuthIsMineEdgeCases(t *testing.T) {
	sa := htlc.NewScriptAuth(nil)

	t.Run("invalid owner bytes", func(t *testing.T) {
		tok := &token3.Token{Owner: []byte("garbage"), Type: "USD", Quantity: "100"}
		_, _, mine := sa.IsMine(t.Context(), tok)
		require.False(t, mine)
	})

	t.Run("wrong owner type", func(t *testing.T) {
		tok := &token3.Token{
			Owner:    marshal.EncodeIdentity(driver.X509IdentityType, []byte("id")),
			Type:     "USD",
			Quantity: "100",
		}
		_, _, mine := sa.IsMine(t.Context(), tok)
		require.False(t, mine)
	})

	t.Run("invalid script JSON", func(t *testing.T) {
		tok := &token3.Token{
			Owner:    marshal.EncodeIdentity(driver.HTLCScriptIdentityType, []byte("not-json")),
			Type:     "USD",
			Quantity: "100",
		}
		_, _, mine := sa.IsMine(t.Context(), tok)
		require.False(t, mine)
	})

	t.Run("nil sender and recipient", func(t *testing.T) {
		tok := makeHTLCToken(t, nil, nil)
		_, _, mine := sa.IsMine(t.Context(), tok)
		require.False(t, mine)
	})
}
